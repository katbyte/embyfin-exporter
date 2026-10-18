package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/mediaserver"
	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-exporter/lib/poll/polltest"
	"github.com/katbyte/go-kt/prometheus/poller"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// viper is global, so none of these can run in parallel and each resets it.
const (
	testUse      = "embyfin-exporter"
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

// With nothing set the page is served where the docs say, and the servers are asked as often as they say: these are what a dashboard's
// scrape interval and an example compose file are written against.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestDefaults(t *testing.T) {
	alone(t)

	f := must(t)
	every, err := poller.ParseIntervals(f.Intervals, poll.Every)
	if f.Listen != ":9096" || err != nil || every["server"] != 15*time.Second || every["sessions"] != 15*time.Second || every["libraries"] != 5*time.Minute || every["played"] != 30*time.Second || len(every) != 8 {
		t.Errorf("defaults = %s, intervals %v (%v); want :9096, server and sessions every 15s, libraries 5m and played 30s among the 8", f.Listen, every, err)
	}
	if f.Metrics != "" || f.Skip != "" {
		t.Errorf("metrics %q and skip %q with nothing given, want every group on", f.Metrics, f.Skip)
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

	t.Setenv("EMBYFIN_EXPORTER_EMBY_SERVER", testEmby)
	t.Setenv("EMBYFIN_EXPORTER_EMBY_TOKEN", "emby-key")
	t.Setenv("EMBYFIN_EXPORTER_JELLYFIN_SERVER", testJellyfin)
	t.Setenv("EMBYFIN_EXPORTER_JELLYFIN_TOKEN", "jellyfin-key")
	t.Setenv("EMBYFIN_EXPORTER_LISTEN", "127.0.0.1:9000")
	t.Setenv("EMBYFIN_EXPORTER_LABELS", "hostname=nas")
	t.Setenv("EMBYFIN_EXPORTER_DETAIL", "users,titles")
	t.Setenv("EMBYFIN_EXPORTER_INTERVALS", "sessions=45s,libraries=10m,played=2m")
	t.Setenv("EMBYFIN_EXPORTER_METRICS", "sessions,plugins,played")
	t.Setenv("EMBYFIN_EXPORTER_SKIP", "plugins,played")

	want := FlagData{
		EmbyServer: testEmby, EmbyToken: "emby-key", JellyfinServer: testJellyfin, JellyfinToken: "jellyfin-key",
		Listen: "127.0.0.1:9000", Labels: "hostname=nas", Detail: "users,titles", Intervals: "sessions=45s,libraries=10m,played=2m", Metrics: "sessions,plugins,played", Skip: "plugins,played",
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
	write(t, home, ".embyfin-exporter", "EMBY_SERVER=http://from-file\nEMBY_TOKEN=from-file\nINTERVALS=sessions=45s\nLISTEN=:9000\n")

	f := must(t)
	if f.EmbyServer != "http://from-file" || f.EmbyToken != "from-file" || f.Intervals != "sessions=45s" || f.Listen != ":9000" {
		t.Errorf("from the file = %+v, want every setting it holds, the ones of two words included", f)
	}

	viper.Reset()
	t.Setenv("EMBYFIN_EXPORTER_EMBY_SERVER", "http://from-env")
	t.Setenv("EMBYFIN_EXPORTER_INTERVALS", "sessions=20s")
	f = must(t)
	if f.EmbyServer != "http://from-env" || f.Intervals != "sessions=20s" || f.EmbyToken != "from-file" {
		t.Errorf("with the environment = %+v, want it over the file, and the file for the rest", f)
	}

	viper.Reset()
	f = must(t, "--emby-server", "http://from-flag", "--intervals", "sessions=5s")
	if f.EmbyServer != "http://from-flag" || f.Intervals != "sessions=5s" {
		t.Errorf("with flags = %+v, want them over the environment", f)
	}
}

// A .embyfin-exporter in the working directory is found before the one in $HOME, and neither being there is not an error.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestSettingsFilePrecedence(t *testing.T) {
	home, wd := alone(t)

	if f := must(t); f.EmbyServer != "" {
		t.Errorf("server = %q with no settings file, want none", f.EmbyServer)
	}

	viper.Reset()
	write(t, home, ".embyfin-exporter", "EMBY_SERVER=http://from-home\n")
	if f := must(t); f.EmbyServer != "http://from-home" {
		t.Errorf("server = %q, want the one in $HOME", f.EmbyServer)
	}

	viper.Reset()
	write(t, wd, ".embyfin-exporter", "EMBY_SERVER=http://from-here\n")
	if f := must(t); f.EmbyServer != "http://from-here" {
		t.Errorf("server = %q, want the working directory's over $HOME's", f.EmbyServer)
	}
}

// A settings file that is named is the only one read, and one that cannot be read is an error: a run told where its settings are must not come
// up with someone else's, or with none.
func TestANamedSettingsFile(t *testing.T) {
	home, _ := alone(t)
	write(t, home, ".embyfin-exporter", "EMBY_SERVER=http://from-home\n")
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
		"neither":              {err: "EMBYFIN_EXPORTER_EMBY_SERVER"},
		"a server with no key": {flags: FlagData{EmbyServer: testEmby}, err: "EMBYFIN_EXPORTER_EMBY_TOKEN"},
		"a key with no server": {flags: FlagData{EmbyServer: testEmby, EmbyToken: testToken, JellyfinToken: testToken}, err: "EMBYFIN_EXPORTER_JELLYFIN_SERVER"},
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

// Each server is asked with a client of its own, made with its own address and key, and a client that cannot be made is said with the server it was
// for.
func TestPoller(t *testing.T) {
	t.Parallel()

	f := FlagData{EmbyServer: testEmby, EmbyToken: "emby-key", JellyfinServer: testJellyfin, JellyfinToken: "jellyfin-key", Detail: "users", Intervals: "sessions=1s,libraries=1m,played=1s"}

	var made []string
	p, err := f.poller(func(kind, server, token string, detail mediaserver.Detail) (poll.Server, error) {
		made = append(made, kind+" "+server+" "+token)
		if detail != (mediaserver.Detail{Users: true}) {
			t.Errorf("%s: made to name %+v, want accounts alone as the settings ask", kind, detail)
		}

		return &polltest.Server{Named: kind}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if every := p.Round(kindEmby, "sessions"); every.Ran || len(made) != 2 {
		t.Errorf("the poller asked before it was started (%+v), or made %d clients, want 2", every, len(made))
	}
	if want := "emby " + testEmby + " emby-key,jellyfin " + testJellyfin + " jellyfin-key"; strings.Join(made, ",") != want {
		t.Errorf("clients made for %q, want %q", strings.Join(made, ","), want)
	}

	_, err = f.poller(func(string, string, string, mediaserver.Detail) (poll.Server, error) {
		return nil, errors.New("no such host")
	})
	if err == nil || !strings.Contains(err.Error(), "emby: no such host") {
		t.Errorf("a client that cannot be made = %v, want the error with the server it was for", err)
	}

	anything := func(kind, _, _ string, _ mediaserver.Detail) (poll.Server, error) {
		return &polltest.Server{Named: kind}, nil
	}

	f.Intervals = "sessions=0s"
	if _, err := f.poller(anything); err == nil || !strings.Contains(err.Error(), "EMBYFIN_EXPORTER_INTERVALS") {
		t.Errorf("an interval of nothing = %v, want it refused, naming the setting", err)
	}

	f.Intervals, f.Skip = "", "plugins,everything"
	if _, err := f.poller(anything); err == nil || !strings.Contains(err.Error(), "EMBYFIN_EXPORTER_SKIP") || !strings.Contains(err.Error(), `"everything"`) {
		t.Errorf("--skip plugins,everything = %v, want it refused, naming the word and the setting", err)
	}

	f.Skip = "plugins,played"
	skipped, err := f.poller(anything)
	if err != nil {
		t.Fatal(err)
	}
	if got := skipped.Skipped().String(); got != "plugins,played" {
		t.Errorf("--skip plugins,played skips %q, want plugins and played", got)
	}

	// metrics names the groups on the page, and skip takes some of them off again
	f.Metrics, f.Skip = "sessions,plugins,played", "played"
	named, err := f.poller(anything)
	if err != nil {
		t.Fatal(err)
	}
	if got := named.Skipped().String(); got != "poll,reasons,streaming,tasks,libraries,accounts,sign-ins,played" {
		t.Errorf("--metrics sessions,plugins,played --skip played skips %q, want all but sessions and plugins", got)
	}

	f.Metrics = "sessions,everything"
	if _, err := f.poller(anything); err == nil || !strings.Contains(err.Error(), "EMBYFIN_EXPORTER_METRICS") || !strings.Contains(err.Error(), `"everything"`) {
		t.Errorf("--metrics sessions,everything = %v, want it refused, naming the word and the setting", err)
	}
	f.Metrics, f.Skip = "", ""

	// a word --detail does not know stops the program before any client is made, and the error says which word and which setting
	f.Intervals, f.Detail = "", "users,everyone"
	made = nil
	if _, err := f.poller(func(kind, _, _ string, _ mediaserver.Detail) (poll.Server, error) {
		made = append(made, kind)
		return &polltest.Server{Named: kind}, nil
	}); err == nil || !strings.Contains(err.Error(), `"everyone"`) || !strings.Contains(err.Error(), "EMBYFIN_EXPORTER_DETAIL") || len(made) != 0 {
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
