//go:build acceptance

package acceptance_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-metrics/cli"
	"github.com/katbyte/go-kt/pmetrics"
)

// deployed gives the program the server under test the way a deployment does, in its environment, where no settings file or variable of the
// developer's own can reach it.
func deployed(t *testing.T) {
	t.Helper()

	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, kind := range []string{"EMBY", "JELLYFIN"} {
		t.Setenv("EMBYFIN_METRICS_"+kind+"_SERVER", "")
		t.Setenv("EMBYFIN_METRICS_"+kind+"_TOKEN", "")
	}
	t.Setenv("EMBYFIN_METRICS_DETAIL", "")
	t.Setenv("EMBYFIN_METRICS_"+strings.ToUpper(backend)+"_SERVER", address)
	t.Setenv("EMBYFIN_METRICS_"+strings.ToUpper(backend)+"_TOKEN", token)
}

// serving starts the program serving on a port of its own, asking the server that often, and gives back where it is. What was played is asked
// after as often as sessions are, and settings are any more of the program's settings, as its command line takes them. It is told to stop when the
// test ends, and the test fails if it does not stop cleanly.
func serving(t *testing.T, sessions, library time.Duration, settings ...string) string {
	t.Helper()

	deployed(t)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	at := listener.Addr().String()
	_ = listener.Close()

	cmd, err := cli.Make()
	if err != nil {
		t.Fatalf("making the command: %v", err)
	}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{"serve", "--listen", at, "--sessions-interval", sessions.String(), "--library-interval", library.String(), "--history-interval", sessions.String()}, settings...))

	ctx, stop := context.WithCancel(context.WithoutCancel(t.Context()))
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.ExecuteContext(ctx) }()

	t.Cleanup(func() {
		stop()

		select {
		case err := <-stopped:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("serve stopped with %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("serve did not stop when told to")
		}
	})

	return "http://" + at
}

// get reads one path from a program that is serving: the status it answers with and what it says. A program still starting is tried for a few
// seconds, and one that never answers is given back as status 0.
func get(t *testing.T, at string) (status int, body string) {
	t.Helper()

	for range 50 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, at, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		return resp.StatusCode, string(raw)
	}

	return 0, ""
}

// served reads the page from a program that is serving until it satisfies check or patience runs out, and gives back the last page read and
// whether it did.
func served(t *testing.T, base string, check func(text string) bool) (string, bool) {
	t.Helper()

	deadline := time.Now().Add(patience)
	for {
		_, text := get(t, base+pmetrics.Path)
		if check(text) {
			return text, true
		}
		if time.Now().After(deadline) {
			return text, false
		}

		time.Sleep(250 * time.Millisecond)
	}
}

// The program itself, given the server as a deployment gives it, prints a page with the server up and its films counted, and the key nowhere on
// it. Run where no settings file or variable of the developer's own can reach it.
func TestTheProgramPrintsThePage(t *testing.T) {
	live(t)
	deployed(t)

	cmd, err := cli.Make()
	if err != nil {
		t.Fatalf("making the command: %v", err)
	}

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"print"})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("print: %v", err)
	}

	text := out.String()
	expect(t, text, map[string]float64{
		reading("embyfin_up"): 1,
		reading("embyfin_library_items", "library", filmLibrary, "type", "movie"): films,
	})

	if strings.Contains(text, token) {
		t.Error("the key is on the page")
	}

	other := jellyfin
	if backend == other {
		other = emby
	}
	if strings.Contains(text, `server="`+other+`"`) {
		t.Errorf("the page reports on a %s nobody named:\n%s", other, text)
	}
}

// The program serving, as it runs in a deployment: it answers its health check at once, and a scrape over HTTP reads the server up and its films
// counted, with the key nowhere on the page. It goes on asking on its own schedule, so a later scrape reads a later answer.
func TestTheProgramServesThePage(t *testing.T) {
	live(t)

	base := serving(t, time.Second, 2*time.Second)

	if status, body := get(t, base+pmetrics.HealthPath); status != http.StatusOK || strings.TrimSpace(body) != "ok" {
		t.Fatalf("%s answered HTTP %d and %q, want 200 and ok", pmetrics.HealthPath, status, body)
	}

	asked := func(text string) float64 {
		at, _ := value(text, reading("embyfin_poll_last_success_timestamp_seconds", "poll", "sessions"))

		return at
	}

	text, ok := served(t, base, func(text string) bool {
		up, _ := value(text, reading("embyfin_up"))
		counted, _ := value(text, reading("embyfin_library_items", "library", filmLibrary, "type", "movie"))

		return up == 1 && counted == films
	})
	if !ok {
		t.Fatalf("the page served never showed the server up with its films counted:\n%s", text)
	}
	if strings.Contains(text, token) {
		t.Error("the key is on the page")
	}
	// nobody asked for anyone to be named
	if found := named.FindAllString(text, -1); len(found) != 0 {
		t.Errorf("the page names %v when no setting asked for anyone to be named:\n%s", found, text)
	}

	if later, ok := served(t, base, func(later string) bool { return asked(later) > asked(text) }); !ok {
		t.Errorf("the server was last asked at %v and the page never showed a later answer than %v", asked(later), asked(text))
	}
}

// The program names accounts when its settings ask it to: an account that has just signed in is then on the page by name, with when it was last
// seen. Asked for accounts alone, it names no title.
func TestTheProgramNamesAccountsWhenAskedTo(t *testing.T) {
	live(t)

	signIn(t, speaker, speakerApp)

	base := serving(t, time.Second, time.Second, "--detail", "users")

	seen := reading("embyfin_user_last_seen_timestamp_seconds", "user", user)
	text, ok := served(t, base, func(text string) bool {
		at, found := value(text, seen)

		return found && time.Since(time.Unix(int64(at), 0)) < time.Hour
	})
	if !ok {
		t.Fatalf("told to name accounts, the page never showed when %s was last seen:\n%s", user, text)
	}
	if found := regexp.MustCompile(`(title|device|client)="[^"]`).FindAllString(text, -1); len(found) != 0 {
		t.Errorf("told to name accounts alone, the page names %v:\n%s", found, text)
	}
}
