//go:build acceptance

package acceptance_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/go-kt/pmetrics"
)

// image is the program's own image, as make docker builds it, for the test that runs it. make testacc builds it and names it here.
var image = os.Getenv("EMBYFIN_METRICS_TEST_PROGRAM_IMAGE")

// home is the developer's own home, read before any test gives the program another: docker keeps where its daemon is there.
var home = os.Getenv("HOME")

// docker runs one docker command and gives back what it printed, with env added to its environment: a variable named there can be handed on to
// a container by name alone, so its value is on no command line. It is not stopped when the test ends, so a cleanup can still put things back.
func docker(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), "docker", args...) //nolint:gosec // every argument is one a test here wrote
	cmd.Env = append(append(os.Environ(), "HOME="+home), env...)
	out, err := cmd.CombinedOutput()

	return strings.TrimSpace(string(out)), err
}

// A server that stops answering is reported as down while the program carries on serving, its last readings are left off the page once they are
// three polls old rather than shown as current, and it is reported on again as soon as it answers. The server is stopped by freezing its container,
// which is how one that hangs looks from outside: it takes the connection and says nothing.
func TestAServerThatStopsAnswering(t *testing.T) {
	live(t)

	if container == "" {
		t.Skip("no container named: EMBYFIN_METRICS_TEST_CONTAINER is what .scripts/testenv.sh up prints")
	}

	base := serving(t, time.Second, 2*time.Second)

	up := func(text string) float64 {
		is, _ := value(text, reading("embyfin_up"))

		return is
	}
	known := func(text string) bool {
		return len(every(text, "embyfin_sessions")) > 0 && len(every(text, "embyfin_libraries")) > 0 && len(every(text, "embyfin_server_info")) > 0
	}

	if text, ok := served(t, base, func(text string) bool { return up(text) == 1 && known(text) }); !ok {
		t.Fatalf("the page served never showed the server up with its sessions and libraries:\n%s", text)
	}

	if out, err := docker(t, nil, "pause", container); err != nil {
		t.Fatalf("freezing the server: %v: %s", err, out)
	}
	// thawing one that is not frozen is refused, which is as it should be after the test thawed it itself
	t.Cleanup(func() { _, _ = docker(t, nil, "unpause", container) })

	failures := reading("embyfin_poll_failures_total", "poll", "sessions")
	down, ok := served(t, base, func(text string) bool {
		failed, _ := value(text, failures)

		return up(text) == 0 && failed >= 1
	})
	if !ok {
		t.Fatalf("the server stopped answering and the page never showed it down:\n%s", down)
	}

	// three polls on, what it last said of itself is gone, and how the asking is going is still there
	stale, ok := served(t, base, func(text string) bool {
		return len(every(text, "embyfin_sessions"))+len(every(text, "embyfin_streams"))+len(every(text, "embyfin_server_info"))+len(every(text, "embyfin_libraries"))+len(every(text, "embyfin_library_items")) == 0
	})
	if !ok {
		t.Errorf("the server has not answered for three polls and the page still shows its last readings:\n%s", stale)
	}
	if up(stale) != 0 || len(every(stale, "embyfin_poll_failures_total")) == 0 {
		t.Errorf("with its readings gone the page should still say the server is down and how the asking is going:\n%s", stale)
	}
	if status, _ := get(t, base+pmetrics.HealthPath); status != http.StatusOK {
		t.Errorf("the program's own health check answered HTTP %d while the server was down, want 200: it is the server that is down", status)
	}

	if out, err := docker(t, nil, "unpause", container); err != nil {
		t.Fatalf("thawing the server: %v: %s", err, out)
	}

	back, ok := served(t, base, func(text string) bool {
		counted, _ := value(text, reading("embyfin_library_items", "library", filmLibrary, "type", "movie"))

		return up(text) == 1 && known(text) && counted == films
	})
	if !ok {
		t.Fatalf("the server is answering again and the page never showed it up with its readings back:\n%s", back)
	}
	if after, _ := value(back, failures); after < 1 {
		t.Errorf("the page counts %v failed polls after the server was down, want the ones that failed", after)
	}
}

// The image a deployment runs, started beside the server the way a compose stack starts it: it reaches the server, serves the page with the server
// up and its films counted, passes its own health check, runs as someone other than root, and writes the key nowhere in its log.
func TestTheImage(t *testing.T) {
	live(t)

	if image == "" || container == "" {
		t.Skip("no image to run: make testacc builds one and names it in EMBYFIN_METRICS_TEST_PROGRAM_IMAGE, beside the server named in EMBYFIN_METRICS_TEST_CONTAINER")
	}

	// the server as another container on the same network reaches it, which is not where the tests do
	inside, err := docker(t, nil, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", container)
	if err != nil || inside == "" {
		t.Fatalf("finding the server's address among the containers: %v: %s", err, inside)
	}

	name := container + "-program"
	server, key := "EMBYFIN_METRICS_"+strings.ToUpper(backend)+"_SERVER", "EMBYFIN_METRICS_"+strings.ToUpper(backend)+"_TOKEN"

	_, _ = docker(t, nil, "rm", "--force", name)
	t.Cleanup(func() { _, _ = docker(t, nil, "rm", "--force", name) })

	// the health check is asked every two seconds where the image says thirty, so the test need not wait half a minute for the first
	if out, err := docker(t, []string{key + "=" + token}, "run", "--detach", "--name", name, "--publish", "127.0.0.1::9096", "--health-interval", "2s", "--env", server+"=http://"+inside+":8096", "--env", key, "--env", "EMBYFIN_METRICS_SESSIONS_INTERVAL=1s", image); err != nil {
		t.Fatalf("starting the image %s: %v: %s", image, err, out)
	}

	published, err := docker(t, nil, "port", name, "9096/tcp")
	if err != nil || published == "" {
		t.Fatalf("finding where the image's port is published: %v: %s", err, published)
	}
	base := "http://" + strings.TrimSpace(strings.Split(published, "\n")[0])

	text, ok := served(t, base, func(text string) bool {
		up, _ := value(text, reading("embyfin_up"))
		counted, _ := value(text, reading("embyfin_library_items", "library", filmLibrary, "type", "movie"))

		return up == 1 && counted == films
	})
	if !ok {
		logs, _ := docker(t, nil, "logs", name)
		t.Fatalf("the image never served a page with the server up and its films counted:\n%s\nits log:\n%s", text, logs)
	}
	if strings.Contains(text, token) {
		t.Error("the key is on the page")
	}

	if status, body := get(t, base+pmetrics.HealthPath); status != http.StatusOK || strings.TrimSpace(body) != "ok" {
		t.Errorf("%s answered HTTP %d and %q, want 200 and ok", pmetrics.HealthPath, status, body)
	}

	if who, err := docker(t, nil, "exec", name, "id", "-u"); err != nil || who == "" || who == "0" {
		t.Errorf("the program runs as user %q (%v), want someone other than root", who, err)
	}

	deadline := time.Now().Add(patience)
	for {
		health, err := docker(t, nil, "inspect", "--format", "{{.State.Health.Status}}", name)
		if health == "healthy" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("the image's own health check says %q (%v), want healthy", health, err)
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if logs, err := docker(t, nil, "logs", name); err != nil || strings.Contains(logs, token) {
		t.Errorf("reading the image's log: %v, and the key is in it: %t", err, strings.Contains(logs, token))
	}
}
