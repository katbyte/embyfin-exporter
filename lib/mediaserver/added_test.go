package mediaserver

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
)

// fakeNewcomers answers for how many items are new with a number for each window, by how long ago the window began, and remembers what it was
// asked.
type fakeNewcomers struct {
	err error

	mu    sync.Mutex
	asked []string
}

func (f *fakeNewcomers) Added(_ context.Context, library, kinds string, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	days := int(time.Since(since).Round(time.Hour).Hours() / 24)
	f.asked = append(f.asked, library+" "+kinds)

	return days * 10, f.err
}

// What is new in a library is asked of a server that can count it, for the kinds whose arrival is an item added: a film library's films, a show
// library's episodes and not its shows and seasons, a music library's tracks. A server that cannot count it is not asked and says nothing, a
// library with no id is not asked after, and one count failing is the libraries failing, as with a count of what one holds.
func TestLibrariesSayWhatIsNew(t *testing.T) {
	t.Parallel()

	folders := []embyfin.VirtualFolder{
		{Name: "Films", ItemID: "10", CollectionType: "movies"},
		{Name: "Shows", ItemID: "20", CollectionType: "tvshows"},
		{Name: "Music", ItemID: "30", CollectionType: "music"},
		{Name: "Sets", ItemID: "40", CollectionType: "boxsets"},
		{Name: "Nowhere"},
	}

	asked := &fakeNewcomers{}
	s := &Server{client: &fake{folders: folders}, arrivals: asked}

	got, err := s.Libraries(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	window := map[string]int{"1d": 10, "7d": 70, "30d": 300}
	for i, name := range []string{"Films", "Shows", "Music", "Sets"} {
		if got[i].Name != name || !reflect.DeepEqual(got[i].Added, window) {
			t.Errorf("%s: what is new = %v, want %v", name, got[i].Added, window)
		}
	}
	if got[4].Added != nil {
		t.Errorf("a library with no id says what is new in it: %v", got[4].Added)
	}

	slices.Sort(asked.asked)
	want := []string{"10 Movie", "10 Movie", "10 Movie", "20 Episode", "20 Episode", "20 Episode", "30 Audio", "30 Audio", "30 Audio", "40 BoxSet", "40 BoxSet", "40 BoxSet"}
	if !slices.Equal(asked.asked, want) {
		t.Errorf("asked after %v, want each library's own arrivals once a window: %v", asked.asked, want)
	}

	// a server that cannot count what is new says nothing of it, which is not the same as nothing new
	quiet := &Server{client: &fake{backend: embyfin.Jellyfin, folders: folders}}
	got, err = quiet.Libraries(t.Context())
	if err != nil || len(got) != 5 || got[0].Added != nil || got[0].Items == nil {
		t.Errorf("a server that cannot count what is new: libraries = %+v and %v, want them with nothing said of it", got, err)
	}

	failing := &Server{client: &fake{folders: folders}, arrivals: &fakeNewcomers{err: errDown}}
	if got, err := failing.Libraries(t.Context()); err == nil || got != nil || !strings.Contains(err.Error(), "Films") {
		t.Errorf("with the count failing: libraries = %v and %v, want an error naming the library and none", got, err)
	}
}

// The kinds whose arrival is an item added, for each type of library: what it is counted for, less the kinds that only hold others.
func TestArrivals(t *testing.T) {
	t.Parallel()

	for collectionType, want := range map[string]string{
		"movies":      "Movie",
		"tvshows":     "Episode",
		"music":       "Audio",
		"musicvideos": "MusicVideo",
		"boxsets":     "BoxSet",
		"playlists":   "Playlist",
		"books":       "Book",
		"audiobooks":  "Audio",
		"homevideos":  "Video,Photo",
		"":            "Movie,Episode,Video",
	} {
		if got := arrivals(kindsIn(embyfin.Emby, collectionType)); got != want {
			t.Errorf("arrivals in a %q library = %q, want %q", collectionType, got, want)
		}
	}
}

// Emby is asked to count, not to list: one request for no items at all, of one library's kinds, added since a moment, and the answer is the
// server's own total. Only Emby is ever asked, since Jellyfin would answer with everything it holds.
func TestWhatIsNewIsAskedOverHTTP(t *testing.T) {
	t.Parallel()

	server, seen := canned(t, http.StatusOK, `{"Items":[],"TotalRecordCount":47}`)

	d, err := connectDirect(embyfin.Emby, server.URL, testKey)
	if err != nil {
		t.Fatal(err)
	}

	got, err := d.Added(t.Context(), "20", "Episode", time.Date(2026, 10, 12, 4, 30, 0, 0, time.FixedZone("PDT", -7*3600)))
	if err != nil || got != 47 {
		t.Errorf("added = %d and %v, want 47", got, err)
	}

	request := seen()[0]
	if want := "/Items?IncludeItemTypes=Episode&Limit=0&MinDateCreated=2026-10-12T11%3A30%3A00Z&ParentId=20&Recursive=true"; request.method != http.MethodGet || request.path != want || !request.keyed {
		t.Errorf("sent %s %s, keyed in a header: %t; want a keyed GET of %s", request.method, request.path, request.keyed, want)
	}

	for kind, counts := range map[string]bool{"emby": true, "jellyfin": false} {
		s, err := Connect(kind, server.URL, testKey, Detail{})
		if err != nil {
			t.Fatal(err)
		}
		if (s.arrivals != nil) != counts {
			t.Errorf("%s: asked what is new: %t, want %t", kind, s.arrivals != nil, counts)
		}
	}
}
