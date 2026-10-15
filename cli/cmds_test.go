package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/mediaserver"
	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-exporter/lib/poll/polltest"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/spf13/cobra"
)

// servers is a connector that hands out these servers by kind, and one with nothing on it for a kind not among them.
func servers(known ...*polltest.Server) connector {
	return func(kind, _, _ string, _ mediaserver.Detail) (poll.Server, error) {
		for _, s := range known {
			if s.Kind() == kind {
				return s, nil
			}
		}

		return &polltest.Server{Named: kind}, nil
	}
}

// program is the program given these arguments and these servers, not yet run, and where what it prints will go.
func program(t *testing.T, connect connector, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	root, err := makeWith(connect)
	if err != nil {
		t.Fatalf("makeWith: %v", err)
	}

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(args)

	return root, &out
}

// run executes the program with these arguments against these servers, and gives back what it printed.
func run(t *testing.T, connect connector, args ...string) (string, error) {
	t.Helper()

	root, out := program(t, connect, args...)
	err := root.ExecuteContext(t.Context())

	return out.String(), err
}

//nolint:paralleltest // viper is global state; Make mutates it
func TestMakeBuildsTheCommands(t *testing.T) {
	alone(t)

	root, err := Make()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"version", "print", "serve"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("no %s command: %v", name, err)
		}
	}
	// a setting is a flag on every command, so it can be given after whichever one is run
	for _, name := range []string{"emby-server", "jellyfin-token", "listen", "detail", "intervals", "config"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("no --%s flag", name)
		}
	}
}

//nolint:paralleltest // viper is global state; Make mutates it
func TestVersionCommand(t *testing.T) {
	alone(t)

	out, err := run(t, servers(), "version")
	if err != nil || !strings.HasPrefix(out, "embyfin-exporter ") {
		t.Errorf("version printed %q (%v), want the name and a version", out, err)
	}
}

// With no command the program says where the help is rather than serving or failing.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestNoCommandPointsAtHelp(t *testing.T) {
	alone(t)

	out, err := run(t, servers())
	if err != nil || !strings.Contains(out, "embyfin-exporter help") {
		t.Errorf("no command printed %q (%v), want a pointer to help", out, err)
	}
}

// print asks every question once and writes the page a scrape would read, both servers on it.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintWritesThePage(t *testing.T) {
	alone(t)

	emby := &polltest.Server{Named: kindEmby, DevicesAnswer: make([]poll.Device, 3), SessionsAnswer: []poll.Session{{State: poll.Playing, Method: poll.Transcode}}}
	jellyfin := &polltest.Server{Named: kindJellyfin, DevicesAnswer: make([]poll.Device, 5)}

	out, err := run(t, servers(emby, jellyfin), "print", "--emby-server", testEmby, "--emby-token", testToken, "--jellyfin-server", testJellyfin, "--jellyfin-token", testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`embyfin_up{server="emby"} 1`,
		`embyfin_up{server="jellyfin"} 1`,
		`embyfin_devices{server="emby"} 3`,
		`embyfin_devices{server="jellyfin"} 5`,
		`embyfin_streams{method="transcode",server="emby"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, testToken) {
		t.Errorf("the page holds a key:\n%s", out)
	}
	// once each, not on a schedule
	if emby.Asked(polltest.Sessions) != 1 || emby.Asked(polltest.Libraries) != 1 || emby.Asked(polltest.History) != 1 {
		t.Errorf("print asked for sessions %d times, libraries %d and what was played %d, want once each", emby.Asked(polltest.Sessions), emby.Asked(polltest.Libraries), emby.Asked(polltest.History))
	}
}

// Labels asked for are on every reading, the one that says which build wrote the page included, beside the labels a reading has of its own.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestLabelsGoOnEveryReading(t *testing.T) {
	alone(t)

	emby := &polltest.Server{Named: kindEmby, DevicesAnswer: make([]poll.Device, 3)}

	out, err := run(t, servers(emby), "print", "--emby-server", testEmby, "--emby-token", testToken, "--labels", "hostname=nas, site=home")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`embyfin_up{hostname="nas",server="emby",site="home"} 1`, `embyfin_devices{hostname="nas",server="emby",site="home"} 3`, `embyfin_exporter_build_info{commit="",goversion="`, `hostname="nas",site="home",version="`} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s:\n%s", want, out)
		}
	}
	for line := range strings.Lines(out) {
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" && !strings.Contains(line, `hostname="nas"`) {
			t.Errorf("a reading has no hostname label: %s", line)
		}
	}
}

// A label that cannot be used stops both commands before a server is asked anything, and the error says which label and which setting: one not
// written name=value, one a store would not take, and one named as a reading's own label is.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestALabelThatCannotBeUsed(t *testing.T) {
	for labels, want := range map[string]string{"hostname": "hostname", "9lives=cat": "9lives", "server=nas": "server", "hostname=": "hostname"} {
		for _, command := range []string{"print", "serve"} {
			alone(t)

			emby := &polltest.Server{Named: kindEmby}

			_, err := run(t, servers(emby), command, "--emby-server", testEmby, "--emby-token", testToken, "--listen", "127.0.0.1:0", "--labels", labels)
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "EMBYFIN_EXPORTER_LABELS") {
				t.Errorf("%s --labels %s = %v, want an error naming %q and the setting", command, labels, err, want)
			}
			if asked := emby.Asked(polltest.Sessions) + emby.Asked(polltest.Libraries); asked != 0 {
				t.Errorf("%s --labels %s asked the server %d questions before refusing", command, labels, asked)
			}
		}
	}
}

// print is also the check that an address and a key work, so a server that does not answer makes it fail, with the page still printed to show
// what that looks like, and the server that did answer still on it.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestPrintFailsWhenAServerDoesNotAnswer(t *testing.T) {
	alone(t)

	emby := &polltest.Server{Named: kindEmby}
	jellyfin := &polltest.Server{Named: kindJellyfin}
	jellyfin.Fail("", errors.New("connection refused"))

	out, err := run(t, servers(emby, jellyfin), "print", "--emby-server", testEmby, "--emby-token", testToken, "--jellyfin-server", testJellyfin, "--jellyfin-token", testToken)
	if err == nil || !strings.Contains(err.Error(), kindJellyfin) {
		t.Errorf("print with Jellyfin down = %v, want an error naming it", err)
	}
	if !strings.Contains(out, `embyfin_up{server="emby"} 1`) || !strings.Contains(out, `embyfin_up{server="jellyfin"} 0`) {
		t.Errorf("the page should show Emby up and Jellyfin down:\n%s", out)
	}
}

// With no server named there is nothing to report on, and both commands say which variables name one rather than serve an empty page.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestACommandNeedsAServer(t *testing.T) {
	for _, command := range []string{"print", "serve"} {
		alone(t)

		_, err := run(t, servers(), command)
		if err == nil || !strings.Contains(err.Error(), "EMBYFIN_EXPORTER_EMBY_SERVER") || !strings.Contains(err.Error(), "EMBYFIN_EXPORTER_JELLYFIN_SERVER") {
			t.Errorf("%s with no server = %v, want an error naming the variables to set", command, err)
		}
	}
}

// freePort is an address on this machine nothing else is listening on: taken, noted and given back.
func freePort(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	return ln.Addr().String()
}

// fetch reads a path from the program once it is serving, trying for a second while it starts.
func fetch(t *testing.T, addr, path string) string {
	t.Helper()

	for range 50 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}

		return string(body)
	}
	t.Fatalf("nothing answered at %s%s", addr, path)

	return ""
}

// serve is the whole program: it polls each server, serves what they said, keeps one server's trouble from the other's readings, and stops
// cleanly when told to.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestServe(t *testing.T) {
	alone(t)

	emby := &polltest.Server{Named: kindEmby, DevicesAnswer: make([]poll.Device, 3)}
	jellyfin := &polltest.Server{Named: kindJellyfin}
	jellyfin.Fail("", errors.New("connection refused"))
	addr := freePort(t)

	root, _ := program(t, servers(emby, jellyfin), "serve", "--listen", addr, "--emby-server", testEmby, "--emby-token", testToken, "--jellyfin-server", testJellyfin, "--jellyfin-token", testToken)
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- root.ExecuteContext(ctx) }()

	if got := strings.TrimSpace(fetch(t, addr, metrics.HealthPath)); got != "ok" {
		t.Errorf("%s = %q, want ok", metrics.HealthPath, got)
	}

	// the first round is asked as the program starts, so it is on the page within moments
	var body string
	for range 50 {
		body = fetch(t, addr, metrics.Path)
		if strings.Contains(body, `embyfin_devices{server="emby"} 3`) && strings.Contains(body, `embyfin_up{server="jellyfin"} 0`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, want := range []string{`embyfin_up{server="emby"} 1`, `embyfin_devices{server="emby"} 3`, `embyfin_up{server="jellyfin"} 0`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, testToken) {
		t.Errorf("the page holds a key:\n%s", body)
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("serve stopped with %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop when told to")
	}
}

// A port that is already taken stops the program with the address in the error, and the polling it had started stops with it.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestServeSaysWhenThePortIsTaken(t *testing.T) {
	alone(t)

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()

	root, _ := program(t, servers(), "serve", "--listen", addr, "--emby-server", testEmby, "--emby-token", testToken)
	stopped := make(chan error, 1)
	go func() { stopped <- root.ExecuteContext(t.Context()) }()

	select {
	case err := <-stopped:
		if err == nil || !strings.Contains(err.Error(), addr) {
			t.Errorf("serve on a taken port = %v, want an error naming %s", err, addr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not give up on a port that was taken")
	}
}
