//go:build acceptance

package acceptance_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/cli"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/go-kt/prometheus/metricstest"
)

// deployed gives the program the server under test the way a deployment does, in its environment, where no settings file or variable of the
// developer's own can reach it.
func deployed(t *testing.T) {
	t.Helper()

	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, kind := range []string{"EMBY", "JELLYFIN"} {
		t.Setenv("EMBYFIN_EXPORTER_"+kind+"_SERVER", "")
		t.Setenv("EMBYFIN_EXPORTER_"+kind+"_TOKEN", "")
	}
	t.Setenv("EMBYFIN_EXPORTER_DETAIL", "")
	t.Setenv("EMBYFIN_EXPORTER_"+strings.ToUpper(backend)+"_SERVER", address)
	t.Setenv("EMBYFIN_EXPORTER_"+strings.ToUpper(backend)+"_TOKEN", token)
}

// serving starts the program serving on a port of its own, asking for sessions and what was played every second and for the library as often as
// library says, and gives back where it is. Settings are any more of the program's settings, as its command line takes them. It is told to stop when
// the test ends, and the test fails if it does not stop cleanly.
func serving(t *testing.T, library time.Duration, settings ...string) string {
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
	cmd.SetArgs(append([]string{"serve", "--listen", at, "--intervals", "server=1s,sessions=1s,tasks=1s,played=1s,libraries=" + library.String() + ",accounts=" + library.String() + ",sign-ins=" + library.String() + ",plugins=" + library.String()}, settings...))

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

// served reads the page from a program that is serving until it satisfies check or patience runs out, and gives back the last page read and
// whether it did.
func served(t *testing.T, base string, check func(text string) bool) (string, bool) {
	t.Helper()

	return metricstest.Until(t, base+metrics.Path, patience, check)
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

	base := serving(t, 2*time.Second)

	if body := metricstest.Get(t, base+metrics.HealthPath); strings.TrimSpace(body) != "ok" {
		t.Fatalf("%s answered %q, want ok", metrics.HealthPath, body)
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

	base := serving(t, time.Second, "--detail", "users")

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

// The program told to skip plugins, sign-ins and what was played leaves them off its page, and never runs the round that would ask the plugin for
// what was played, while the server is up with its films counted and its sessions on the page as ever.
func TestTheProgramLeavesOffWhatItIsToldTo(t *testing.T) {
	live(t)

	base := serving(t, 2*time.Second, "--skip", "plugins,sign-ins,played")

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

	// a few rounds on, a skipped one has had every chance to run: what was played is asked as often as sessions are
	text, ok = served(t, base, func(later string) bool { return asked(later) >= asked(text)+2 })
	if !ok {
		t.Fatalf("the server was not asked again after %v:\n%s", asked(text), text)
	}

	for _, left := range []string{"embyfin_plugin", "embyfin_logins_total", "embyfin_user_logins_total", "embyfin_playback_", "embyfin_plays_total", `poll="plugins"`, `poll="sign-ins"`, `poll="played"`} {
		if strings.Contains(text, left) {
			t.Errorf("told to skip plugins, sign-ins and what was played, the page still has %s:\n%s", left, text)
		}
	}
	if len(every(text, "embyfin_sessions")) == 0 || len(every(text, "embyfin_users")) == 0 {
		t.Errorf("the page lost sessions or accounts, which were not skipped:\n%s", text)
	}
}

// The program told to ask only for sessions, libraries and how the asking goes puts those and the server's own readings on its page and nothing else,
// and runs only the rounds that ask for them. Told the same without poll, its page says nothing of how the asking goes.
func TestTheProgramAsksOnlyForWhatItIsToldTo(t *testing.T) {
	live(t)

	base := serving(t, 2*time.Second, "--metrics", "sessions,libraries,poll")

	text, ok := served(t, base, func(text string) bool {
		up, _ := value(text, reading("embyfin_up"))
		counted, _ := value(text, reading("embyfin_library_items", "library", filmLibrary, "type", "movie"))

		return up == 1 && counted == films && len(every(text, "embyfin_sessions")) > 0
	})
	if !ok {
		t.Fatalf("the page served never showed the server up with its sessions and its films counted:\n%s", text)
	}

	for _, left := range []string{"embyfin_scheduled_task", "embyfin_library_scan_", "embyfin_users", "embyfin_devices", "embyfin_plugin", "embyfin_logins_total", "embyfin_playback_", "embyfin_session_info", "embyfin_transcode_reasons"} {
		if strings.Contains(text, left) {
			t.Errorf("told to ask only for sessions and libraries, the page has %s:\n%s", left, text)
		}
	}

	ran := every(text, "embyfin_poll_interval_seconds")
	rounds := make([]string, 0, len(ran))
	for _, line := range ran {
		rounds = append(rounds, label(line, "poll"))
	}
	slices.Sort(rounds)
	if want := []string{"libraries", "server", "sessions"}; !slices.Equal(rounds, want) {
		t.Errorf("the rounds run are %v, want %v", rounds, want)
	}

	quiet := serving(t, 2*time.Second, "--metrics", "sessions,libraries")
	text, ok = served(t, quiet, func(text string) bool {
		up, _ := value(text, reading("embyfin_up"))
		counted, _ := value(text, reading("embyfin_library_items", "library", filmLibrary, "type", "movie"))

		return up == 1 && counted == films
	})
	if !ok || strings.Contains(text, "embyfin_poll_") {
		t.Errorf("told to ask for sessions and libraries alone, the page should have them and nothing of how the asking goes:\n%s", text)
	}
}
