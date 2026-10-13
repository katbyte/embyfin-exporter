package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/mediaserver"
	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/katbyte/embyfin-metrics/lib/poll/polltest"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// viper is global, so none of these can run in parallel and each resets it.
const (
	testUse      = "embyfin-metrics"
	testEmby     = "http://nas:8096"
	testJellyfin = "http://nas:8097"
	testToken    = "s3cret"
)

// alone cuts a test off from the settings of whoever runs it: viper as it starts, an empty home and working directory, and none of the program's
// variables set.
func alone(t *testing.T) (home, wd string) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)

	home, wd = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(wd)
	t.Setenv(configEnv, "")
	for _, env := range envNames {
		t.Setenv(env, "")
	}

	return home, wd
}

// load drives the real flag wiring with these command-line arguments, after whatever the test put in the environment and the settings files.
func load(t *testing.T, args ...string) (*FlagData, error) {
	t.Helper()

	root := &cobra.Command{Use: testUse}
	if err := configureFlags(root); err != nil {
		t.Fatalf("configureFlags: %v", err)
	}
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := loadConfig(); err != nil {
		return nil, err
	}

	return GetFlags(), nil
}

// must is load for a test that expects the settings to be read.
func must(t *testing.T, args ...string) *FlagData {
	t.Helper()

	f, err := load(t, args...)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	return f
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// With nothing set the page is served where the README says, and the servers are asked as often as it says: these are what a dashboard's
// scrape interval and an example compose file are written against.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestDefaults(t *testing.T) {
	alone(t)

	f := must(t)
	if f.Listen != ":9096" || f.SessionsInterval != 15*time.Second || f.LibraryInterval != 5*time.Minute || f.HistoryInterval != 30*time.Second {
		t.Errorf("defaults = %s, sessions every %s, libraries every %s, what was played every %s; want :9096, 15s, 5m and 30s", f.Listen, f.SessionsInterval, f.LibraryInterval, f.HistoryInterval)
	}
	if f.EmbyServer != "" || f.EmbyToken != "" || f.JellyfinServer != "" || f.JellyfinToken != "" || f.Labels != "" {
		t.Errorf("a server or a label is set with nothing given: %+v", f)
	}
	// nobody is named unless someone asks
	if d, err := f.detail(); err != nil || f.Detail != "" || d != (mediaserver.Detail{}) {
		t.Errorf("with nothing given the page may name %+v (%q, %v), want nobody and nothing", d, f.Detail, err)
	}
}

// Every setting has an environment variable, which is how a container is configured.
func TestEveryFlagHasAnEnvironmentVariable(t *testing.T) {
	alone(t)

	t.Setenv("EMBYFIN_METRICS_EMBY_SERVER", testEmby)
	t.Setenv("EMBYFIN_METRICS_EMBY_TOKEN", "emby-key")
	t.Setenv("EMBYFIN_METRICS_JELLYFIN_SERVER", testJellyfin)
	t.Setenv("EMBYFIN_METRICS_JELLYFIN_TOKEN", "jellyfin-key")
	t.Setenv("EMBYFIN_METRICS_LISTEN", "127.0.0.1:9000")
	t.Setenv("EMBYFIN_METRICS_LABELS", "hostname=nas")
	t.Setenv("EMBYFIN_METRICS_DETAIL", "users,titles")
	t.Setenv("EMBYFIN_METRICS_SESSIONS_INTERVAL", "45s")
	t.Setenv("EMBYFIN_METRICS_LIBRARY_INTERVAL", "10m")
	t.Setenv("EMBYFIN_METRICS_HISTORY_INTERVAL", "2m")

	want := FlagData{
		EmbyServer: testEmby, EmbyToken: "emby-key", JellyfinServer: testJellyfin, JellyfinToken: "jellyfin-key",
		Listen: "127.0.0.1:9000", Labels: "hostname=nas", Detail: "users,titles", SessionsInterval: 45 * time.Second, LibraryInterval: 10 * time.Minute, HistoryInterval: 2 * time.Minute,
	}
	if got := *must(t); got != want {
		t.Errorf("from the environment = %+v, want %+v", got, want)
	}

	root := &cobra.Command{Use: testUse}
	if err := configureFlags(root); err != nil {
		t.Fatal(err)
	}
	root.PersistentFlags().VisitAll(func(fl *pflag.Flag) {
		if _, ok := envNames[fl.Name]; !ok && fl.Name != "config" {
			t.Errorf("--%s has no environment variable", fl.Name)
		}
	})
}

// A flag beats the environment, which beats a settings file: a container overrides what an image holds, and a command line overrides both.
func TestFlagThenEnvironmentThenFile(t *testing.T) {
	home, _ := alone(t)
	write(t, home, ".embyfin-metrics", "EMBY_SERVER=http://from-file\nEMBY_TOKEN=from-file\nSESSIONS_INTERVAL=45s\nLISTEN=:9000\n")

	f := must(t)
	if f.EmbyServer != "http://from-file" || f.EmbyToken != "from-file" || f.SessionsInterval != 45*time.Second || f.Listen != ":9000" {
		t.Errorf("from the file = %+v, want every setting it holds, the ones of two words included", f)
	}

	viper.Reset()
	t.Setenv("EMBYFIN_METRICS_EMBY_SERVER", "http://from-env")
	t.Setenv("EMBYFIN_METRICS_SESSIONS_INTERVAL", "20s")
	f = must(t)
	if f.EmbyServer != "http://from-env" || f.SessionsInterval != 20*time.Second || f.EmbyToken != "from-file" {
		t.Errorf("with the environment = %+v, want it over the file, and the file for the rest", f)
	}

	viper.Reset()
	f = must(t, "--emby-server", "http://from-flag", "--sessions-interval", "5s")
	if f.EmbyServer != "http://from-flag" || f.SessionsInterval != 5*time.Second {
		t.Errorf("with flags = %+v, want them over the environment", f)
	}
}

// A .embyfin-metrics in the working directory is found before the one in $HOME, and neither being there is not an error.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestSettingsFilePrecedence(t *testing.T) {
	home, wd := alone(t)

	if f := must(t); f.EmbyServer != "" {
		t.Errorf("server = %q with no settings file, want none", f.EmbyServer)
	}

	viper.Reset()
	write(t, home, ".embyfin-metrics", "EMBY_SERVER=http://from-home\n")
	if f := must(t); f.EmbyServer != "http://from-home" {
		t.Errorf("server = %q, want the one in $HOME", f.EmbyServer)
	}

	viper.Reset()
	write(t, wd, ".embyfin-metrics", "EMBY_SERVER=http://from-here\n")
	if f := must(t); f.EmbyServer != "http://from-here" {
		t.Errorf("server = %q, want the working directory's over $HOME's", f.EmbyServer)
	}
}

// A settings file that is named is the only one read, and one that cannot be read is an error: a run told where its settings are must not come
// up with someone else's, or with none.
func TestANamedSettingsFile(t *testing.T) {
	home, _ := alone(t)
	write(t, home, ".embyfin-metrics", "EMBY_SERVER=http://from-home\n")
	named := write(t, home, "other.env", "JELLYFIN_SERVER=http://from-named\n")

	f := must(t, "--config", named)
	if f.JellyfinServer != "http://from-named" || f.EmbyServer != "" {
		t.Errorf("with --config = %+v, want the named file and nothing of $HOME's", f)
	}

	viper.Reset()
	t.Setenv(configEnv, "~/other.env")
	if f := must(t); f.JellyfinServer != "http://from-named" {
		t.Errorf("with %s=~/other.env the server = %q, want the named file's", configEnv, f.JellyfinServer)
	}

	viper.Reset()
	if _, err := load(t, "--config", filepath.Join(home, "missing.env")); err == nil || !strings.Contains(err.Error(), "missing.env") {
		t.Errorf("a named file that is not there = %v, want an error naming it", err)
	}
}

// Either server may be left out. A server with no key, or a key with no server, is a mistake that is said, naming the variable to set.
func TestTargets(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		flags FlagData
		kinds []string
		err   string
	}{
		"both":                 {flags: FlagData{EmbyServer: testEmby, EmbyToken: testToken, JellyfinServer: testJellyfin, JellyfinToken: testToken}, kinds: []string{kindEmby, kindJellyfin}},
		"emby alone":           {flags: FlagData{EmbyServer: testEmby, EmbyToken: testToken}, kinds: []string{kindEmby}},
		"jellyfin alone":       {flags: FlagData{JellyfinServer: testJellyfin, JellyfinToken: testToken}, kinds: []string{kindJellyfin}},
		"neither":              {err: "EMBYFIN_METRICS_EMBY_SERVER"},
		"a server with no key": {flags: FlagData{EmbyServer: testEmby}, err: "EMBYFIN_METRICS_EMBY_TOKEN"},
		"a key with no server": {flags: FlagData{EmbyServer: testEmby, EmbyToken: testToken, JellyfinToken: testToken}, err: "EMBYFIN_METRICS_JELLYFIN_SERVER"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			targets, err := c.flags.targets()
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("targets = %v, want an error naming %s", err, c.err)
				}
				// the mistake is named and the key is not
				if strings.Contains(err.Error(), testToken) {
					t.Errorf("the error holds the key: %v", err)
				}

				return
			}
			if err != nil {
				t.Fatal(err)
			}

			var kinds []string
			for _, target := range targets {
				kinds = append(kinds, target.kind)
			}
			if strings.Join(kinds, ",") != strings.Join(c.kinds, ",") {
				t.Errorf("targets = %v, want %v", kinds, c.kinds)
			}
		})
	}
}

// Each server gets a poller of its own, made with its own address and key, and a client that cannot be made is said with the server it was for.
func TestPollers(t *testing.T) {
	t.Parallel()

	f := FlagData{EmbyServer: testEmby, EmbyToken: "emby-key", JellyfinServer: testJellyfin, JellyfinToken: "jellyfin-key", Detail: "users", SessionsInterval: time.Second, LibraryInterval: time.Minute, HistoryInterval: time.Second}

	var made []string
	pollers, err := f.pollers(func(kind, server, token string, detail mediaserver.Detail) (poll.Server, error) {
		made = append(made, kind+" "+server+" "+token)
		if detail != (mediaserver.Detail{Users: true}) {
			t.Errorf("%s: made to name %+v, want accounts alone as the settings ask", kind, detail)
		}

		return &polltest.Server{Named: kind}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pollers) != 2 || pollers[0].Kind() != kindEmby || pollers[1].Kind() != kindJellyfin {
		t.Errorf("pollers = %d, want one for Emby and one for Jellyfin", len(pollers))
	}
	if want := "emby " + testEmby + " emby-key,jellyfin " + testJellyfin + " jellyfin-key"; strings.Join(made, ",") != want {
		t.Errorf("clients made for %q, want %q", strings.Join(made, ","), want)
	}

	_, err = f.pollers(func(string, string, string, mediaserver.Detail) (poll.Server, error) {
		return nil, errors.New("no such host")
	})
	if err == nil || !strings.Contains(err.Error(), "emby: no such host") {
		t.Errorf("a client that cannot be made = %v, want the error with the server it was for", err)
	}

	anything := func(kind, _, _ string, _ mediaserver.Detail) (poll.Server, error) {
		return &polltest.Server{Named: kind}, nil
	}

	f.SessionsInterval = 0
	if _, err := f.pollers(anything); err == nil {
		t.Error("an interval of nothing was accepted")
	}

	// a word --detail does not know stops the program before any client is made, and the error says which word and which setting
	f.SessionsInterval, f.Detail = time.Second, "users,everyone"
	made = nil
	if _, err := f.pollers(func(kind, _, _ string, _ mediaserver.Detail) (poll.Server, error) {
		made = append(made, kind)
		return &polltest.Server{Named: kind}, nil
	}); err == nil || !strings.Contains(err.Error(), `"everyone"`) || !strings.Contains(err.Error(), "EMBYFIN_METRICS_DETAIL") || len(made) != 0 {
		t.Errorf("--detail users,everyone = %v with %d clients made, want an error naming the word and the setting and no client", err, len(made))
	}
}

// The first line the program logs says what its page will name, so a page with people on it is not a surprise.
func TestNamed(t *testing.T) {
	t.Parallel()

	for want, d := range map[string]mediaserver.Detail{
		"naming nobody":                    {},
		"naming accounts":                  {Users: true},
		"naming titles, devices":           {Titles: true, Devices: true},
		"naming accounts, titles, devices": {Users: true, Titles: true, Devices: true},
	} {
		if got := named(d); got != want {
			t.Errorf("named(%+v) = %q, want %q", d, got, want)
		}
	}
}

// Who and what the page may name is nobody and nothing unless the settings say, a word at a time, in any case and with spaces or not.
func TestDetail(t *testing.T) {
	t.Parallel()

	for setting, want := range map[string]mediaserver.Detail{
		"":               {},
		" ":              {},
		"users":          {Users: true},
		"titles":         {Titles: true},
		"devices":        {Devices: true},
		"users,titles":   {Users: true, Titles: true},
		"Titles, USERS ": {Users: true, Titles: true},
		"users,,users":   {Users: true},
		"devices,titles": {Titles: true, Devices: true},
		"all":            {Users: true, Titles: true, Devices: true},
		"all,users":      {Users: true, Titles: true, Devices: true},
	} {
		f := FlagData{Detail: setting}
		if got, err := f.detail(); err != nil || got != want {
			t.Errorf("--detail %q = %+v and %v, want %+v", setting, got, err, want)
		}
	}

	for _, setting := range []string{"everyone", "users titles", "users;titles", "user", "addresses"} {
		f := FlagData{Detail: setting}
		if got, err := f.detail(); err == nil || got != (mediaserver.Detail{}) {
			t.Errorf("--detail %q = %+v and %v, want an error and nobody named", setting, got, err)
		}
	}
}

// The connector the program runs with makes a client for either kind without sending anything, and refuses a kind that is neither.
func TestConnect(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{kindEmby, kindJellyfin} {
		s, err := connect(kind, testEmby, testToken, mediaserver.Detail{})
		if err != nil {
			t.Fatalf("%s: connect: %v", kind, err)
		}
		if s.Kind() != kind {
			t.Errorf("connected to a %q, want a %q", s.Kind(), kind)
		}
	}

	if s, err := connect("plex", testEmby, testToken, mediaserver.Detail{}); err == nil || s != nil {
		t.Errorf("connect to a kind that is neither = %v and %v, want an error", s, err)
	}
}
