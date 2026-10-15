package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
)

// fakeCatalogue answers for what is on offer with what it is set to, as slowly as it is told, and counts how often it was asked.
type fakeCatalogue struct {
	offered map[string]string
	err     error
	// hangs is a catalogue that never answers: it waits for whoever asked to give up
	hangs bool

	mu    sync.Mutex
	asked int
}

func (f *fakeCatalogue) Offered(ctx context.Context) (map[string]string, error) {
	f.mu.Lock()
	f.asked++
	f.mu.Unlock()

	if f.hangs {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	return f.offered, f.err
}

// Emby's catalogue as it writes it (seen on 4.10): an id with dashes, each version with how settled it is and the oldest server that runs it.
const embyCatalogue = `[
	{"name":"Backup & Restore","guid":"E711475E-EFAD-431B-8527-033BA9873A34","type":"UserInstalled","versions":[
		{"versionStr":"1.8.6.0","classification":"Release","requiredVersionStr":"4.8.0.80"},
		{"versionStr":"1.8.7.0","classification":"Release","requiredVersionStr":"4.8.0.80"},
		{"versionStr":"1.9.0.0","classification":"Beta","requiredVersionStr":"4.8.0.80"},
		{"versionStr":"2.0.0.0","classification":"Release","requiredVersionStr":"4.12.0.0"},
		{"versionStr":"3.0.0.0","classification":"Nightly","requiredVersionStr":""}]},
	{"name":"OMDb","guid":"c25b3c85-1880-4827-9c72-0fa74314f428","type":"UserInstalled","versions":[
		{"versionStr":"1.0.23","classification":"Release","requiredVersionStr":"4.8.11"}]},
	{"name":"Odd","guid":"00000000-0000-0000-0000-000000000001","type":"UserInstalled","versions":[
		{"versionStr":"next","classification":"Release","requiredVersionStr":""}]}
]`

// Jellyfin's catalogue as it writes it (seen on 12.2): each version with the server it was built for.
const jellyfinCatalogue = `[
	{"name":"Playback Reporting","guid":"5c534381-91a3-43cb-907a-35aa02eb9d2c","versions":[
		{"version":"20.0.0.0","VersionNumber":"20.0.0.0","targetAbi":"13.0.0.0"},
		{"version":"19.0.0.0","VersionNumber":"19.0.0.0","targetAbi":"12.0.0.0"},
		{"version":"18.0.0.0","VersionNumber":"18.0.0.0","targetAbi":"10.11.0.0"}]}
]`

// One version is newer than another by its numbers, the first that differs deciding, and a number left off the end is a 0: Emby writes what is
// installed as 1.0.23.0 and the same version on offer as 1.0.23. A version that is not numbers is newer than nothing, and nothing than it.
func TestNewer(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		version, than string
		want          bool
	}{
		{"1.0.29", "1.0.28.0", true},
		{"1.0.28.0", "1.0.29", false},
		{"1.0.23", "1.0.23.0", false},
		{"1.0.23.0", "1.0.23", false},
		{"1.10", "1.9", true},
		{"2", "1.9.9.9", true},
		{"4.8.0.80", "4.10.1.0", false},
		{"13.0.0.0", "12.2.0", true},
		{"12.0.0.0", "12.2.0", false},
		{"", "1.0", false},
		{"1.0", "", false},
		{"next", "1.0", false},
		{"1.0", "1.x", false},
		{"1.-2", "1.0", false},
	} {
		if got := newer(c.version, c.than); got != c.want {
			t.Errorf("%q newer than %q: %t, want %t", c.version, c.than, got, c.want)
		}
	}
}

// The newest version of a plugin is the newest its server would itself take. An Emby set to settled versions leaves a beta out and one set to betas
// takes it, neither takes a version that needs a newer server or one of a kind it has no word for, and a Jellyfin leaves out a version built for a
// newer server. A plugin with no version that can be read as numbers is not in the answer, and an id is written one way whoever wrote it.
func TestNewest(t *testing.T) {
	t.Parallel()

	const backup, omdb, playback = "e711475eefad431b8527033ba9873a34", "c25b3c85188048279c720fa74314f428", "5c53438191a343cb907a35aa02eb9d2c"

	for _, c := range []struct {
		name, catalogue, server, level string
		want                           map[string]string
	}{
		{"an Emby taking settled versions", embyCatalogue, "4.10.1.0", "Release", map[string]string{backup: "1.8.7.0", omdb: "1.0.23"}},
		{"an Emby taking betas", embyCatalogue, "4.11.0.7", "Beta", map[string]string{backup: "1.9.0.0", omdb: "1.0.23"}},
		{"an Emby new enough for the next", embyCatalogue, "4.12.0.1", "Release", map[string]string{backup: "2.0.0.0", omdb: "1.0.23"}},
		{"an Emby too old for any", embyCatalogue, "4.7.0.0", "", map[string]string{}},
		{"an Emby that says how settled in a word not known here", embyCatalogue, "4.10.1.0", "Canary", map[string]string{backup: "1.8.7.0", omdb: "1.0.23"}},
		{"a Jellyfin", jellyfinCatalogue, "12.2.0", "", map[string]string{playback: "19.0.0.0"}},
		{"a Jellyfin too old for any", jellyfinCatalogue, "10.10.7", "", map[string]string{}},
		{"an empty catalogue", `[]`, "12.2.0", "", map[string]string{}},
	} {
		var offers []offer
		if err := json.Unmarshal([]byte(c.catalogue), &offers); err != nil {
			t.Fatal(err)
		}

		if got := newest(offers, c.server, c.level); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: newest = %v, want %v", c.name, got, c.want)
		}
	}
}

// What a server has installed is listed with the newer version on offer beside each plugin that has one. A plugin whose newest is the one
// installed, written with a number fewer, has none, nor has one the catalogue does not hold. When the catalogue fails, is empty or is not there
// to ask, the plugins are still listed and which are behind is not known; when the plugins themselves cannot be read there is no answer.
func TestPlugins(t *testing.T) {
	t.Parallel()

	installed := []embyfin.Plugin{
		{ID: "e711475e-efad-431b-8527-033ba9873a34", Name: "Backup & Restore", Version: "1.8.6.0"},
		{ID: "c25b3c85-1880-4827-9c72-0fa74314f428", Name: "OMDb", Version: "1.0.23.0"},
		{ID: "11111111-2222-3333-4444-555555555555", Name: "Auto Box Sets", Version: "1.2.7.0"},
	}
	offered := map[string]string{"e711475eefad431b8527033ba9873a34": "1.8.7.0", "c25b3c85188048279c720fa74314f428": "1.0.23"}
	plain := []poll.Plugin{{Name: "Backup & Restore", Version: "1.8.6.0"}, {Name: "OMDb", Version: "1.0.23.0"}, {Name: "Auto Box Sets", Version: "1.2.7.0"}}

	s := &Server{client: &fake{plugins: installed}, catalogue: &fakeCatalogue{offered: offered}}
	got, err := s.Plugins(t.Context())
	if want := (poll.Plugins{Checked: true, Installed: []poll.Plugin{{Name: "Backup & Restore", Version: "1.8.6.0", Latest: "1.8.7.0"}, plain[1], plain[2]}}); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("plugins = %+v and %v, want %+v", got, err, want)
	}

	for name, s := range map[string]*Server{
		"a catalogue that fails":      {client: &fake{plugins: installed}, catalogue: &fakeCatalogue{err: errDown}},
		"a catalogue with nothing in": {client: &fake{plugins: installed}, catalogue: &fakeCatalogue{offered: map[string]string{}}},
		"no catalogue to ask":         {client: &fake{plugins: installed}},
	} {
		got, err := s.Plugins(t.Context())
		if want := (poll.Plugins{Installed: plain}); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: plugins = %+v and %v, want them listed and none said to be behind or current", name, got, err)
		}
	}

	asked := &fakeCatalogue{offered: offered}
	if got, err := (&Server{client: &fake{err: errDown}, catalogue: asked}).Plugins(t.Context()); err == nil || len(got.Installed) != 0 || asked.asked != 0 {
		t.Errorf("plugins that cannot be read = %+v and %v, with the catalogue asked %d times; want an error and the catalogue left alone", got, err, asked.asked)
	}
}

// Jellyfin lists more than is running: a plugin taken out, an older version a newer one replaced, and a new version fetched and waiting for a
// restart beside the one still running. The first two are not plugins the server has. With the new version already fetched nothing is behind,
// whichever line is asked about, and with a still newer one on offer it is the newest line that is behind.
func TestPluginsJellyfinListsTwice(t *testing.T) {
	t.Parallel()

	const id = "5c53438191a343cb907a35aa02eb9d2c"

	installed := []embyfin.Plugin{
		{ID: id, Name: "Playback Reporting", Version: "17.0.0.0", Status: "Superseded"},
		{ID: id, Name: "Playback Reporting", Version: "18.0.0.0", Status: "Active"},
		{ID: id, Name: "Playback Reporting", Version: "19.0.0.0", Status: "Restart"},
		{ID: "aaaa", Name: "Reports", Version: "19.0.0.0", Status: "Deleted"},
		{ID: "bbbb", Name: "Trakt", Version: "25.0.0.0", Status: "Disabled"},
	}

	for latest, behind := range map[string]string{"19.0.0.0": "", "20.0.0.0": "20.0.0.0"} {
		s := &Server{client: &fake{backend: embyfin.Jellyfin, plugins: installed}, catalogue: &fakeCatalogue{offered: map[string]string{id: latest, "bbbb": "25.0.0.0"}}}

		got, err := s.Plugins(t.Context())
		want := poll.Plugins{Checked: true, Installed: []poll.Plugin{{Name: "Playback Reporting", Version: "18.0.0.0"}, {Name: "Playback Reporting", Version: "19.0.0.0", Latest: behind}, {Name: "Trakt", Version: "25.0.0.0"}}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("with %s on offer: plugins = %+v and %v, want %+v", latest, got, err, want)
		}
	}
}

// A catalogue is asked once and what it had is kept for an hour, however often the plugins are asked after: it changes rarely and Emby's is over
// a megabyte. One that did not answer is asked again the next time, since there is nothing of its to keep.
func TestTheCatalogueIsAskedOnceAnHour(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		kept := &fakeCatalogue{offered: map[string]string{"aaaa": "2.0"}}
		s := &Server{client: &fake{plugins: []embyfin.Plugin{{ID: "aaaa", Name: "Reports", Version: "1.0"}}}, catalogue: kept}

		for range 3 {
			if got, err := s.Plugins(t.Context()); err != nil || !got.Checked || got.Installed[0].Latest != "2.0" {
				t.Fatalf("plugins = %+v and %v, want Reports behind 2.0", got, err)
			}
			time.Sleep(5 * time.Minute)
		}
		if kept.asked != 1 {
			t.Errorf("the catalogue was asked %d times in a quarter of an hour, want once", kept.asked)
		}

		time.Sleep(catalogueKept)
		kept.offered = map[string]string{"aaaa": "3.0"}
		if got, _ := s.Plugins(t.Context()); kept.asked != 2 || got.Installed[0].Latest != "3.0" {
			t.Errorf("after the hour the catalogue had been asked %d times and Reports is behind %q, want twice and 3.0", kept.asked, got.Installed[0].Latest)
		}

		failing := &fakeCatalogue{err: errDown}
		s = &Server{client: &fake{}, catalogue: failing}
		for range 3 {
			_, _ = s.Plugins(t.Context())
		}
		if failing.asked != 3 {
			t.Errorf("a catalogue that did not answer was asked %d times in three, want every time", failing.asked)
		}
	})
}

// A server that cannot reach its catalogue may never answer for it. It is given so long and no longer, and the plugins are then listed without it:
// the libraries are asked in the same round and are not to wait.
func TestACatalogueThatNeverAnswers(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		s := &Server{client: &fake{plugins: []embyfin.Plugin{{ID: "aaaa", Name: "Reports", Version: "1.0"}}}, catalogue: &fakeCatalogue{hangs: true}}

		began := time.Now()
		got, err := s.Plugins(t.Context())
		if err != nil || got.Checked || len(got.Installed) != 1 || time.Since(began) != catalogueWait {
			t.Errorf("plugins = %+v and %v after %s, want Reports listed, nothing known of what is behind, after %s", got, err, time.Since(began), catalogueWait)
		}
	})
}

// The catalogue is read with two questions, each a GET with the key in a header: what the server is, to tell which versions it could run, and
// what is on offer. A server with no way to its catalogue says so its own way, Emby by failing the question and Jellyfin with an empty list
// (seen on 4.10 and 12.2), and neither is taken for a catalogue with nothing newer in it.
func TestTheCatalogueIsReadOverHTTP(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		kind, info, packages string
		status               int
		want                 map[string]string
		fails                bool
	}{
		{"emby", `{"Version":"4.11.0.7","SystemUpdateLevel":"Beta"}`, embyCatalogue, http.StatusOK, map[string]string{"e711475eefad431b8527033ba9873a34": "1.9.0.0", "c25b3c85188048279c720fa74314f428": "1.0.23"}, false},
		{"jellyfin", `{"Version":"12.2.0"}`, jellyfinCatalogue, http.StatusOK, map[string]string{"5c53438191a343cb907a35aa02eb9d2c": "19.0.0.0"}, false},
		{"emby", `{"Version":"4.10.1.0","SystemUpdateLevel":"Release"}`, `Cancelling connection to https://catalogue.example/packages.json due to a previous timeout.`, http.StatusInternalServerError, nil, true},
		{"jellyfin", `{"Version":"12.2.0"}`, `[]`, http.StatusOK, map[string]string{}, false},
	} {
		var mu sync.Mutex
		var sent []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			sent = append(sent, r.Method+" "+r.URL.String()+" keyed:"+map[bool]string{true: "yes", false: "no"}[strings.Contains(strings.Join(r.Header.Values("X-Emby-Token"), "")+strings.Join(r.Header.Values("Authorization"), ""), testKey)])
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/System/Info" {
				_, _ = w.Write([]byte(c.info))
				return
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.packages))
		}))

		s, err := Connect(c.kind, server.URL, testKey, Detail{})
		if err != nil || s.catalogue == nil {
			t.Fatalf("%s: connected with %v and a catalogue to ask: %t", c.kind, err, s != nil && s.catalogue != nil)
		}

		got, err := s.catalogue.Offered(t.Context())
		if (err != nil) != c.fails || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s answering %d: on offer = %v and %v, want %v and a failure: %t", c.kind, c.status, got, err, c.want, c.fails)
		}
		if err != nil && strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: the failure repeats the key: %v", c.kind, err)
		}

		mu.Lock()
		if want := []string{"GET /System/Info keyed:yes", "GET /Packages keyed:yes"}; !reflect.DeepEqual(sent, want) {
			t.Errorf("%s: sent %v, want %v", c.kind, sent, want)
		}
		mu.Unlock()

		server.Close()
	}
}
