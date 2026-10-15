//go:build acceptance

package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/mediaserver"
	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-exporter/lib/readings"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/go-kt/pmetrics"
)

// What .scripts/testenv.sh up prints: where the server is, a key for it, the two accounts it made, and the container it runs in.
var (
	backend   = os.Getenv("EMBYFIN_EXPORTER_TEST_BACKEND")
	address   = os.Getenv("EMBYFIN_EXPORTER_TEST_SERVER")
	token     = os.Getenv("EMBYFIN_EXPORTER_TEST_TOKEN")
	admin     = os.Getenv("EMBYFIN_EXPORTER_TEST_ADMIN")
	user      = os.Getenv("EMBYFIN_EXPORTER_TEST_USER")
	password  = os.Getenv("EMBYFIN_EXPORTER_TEST_PASSWORD")
	container = os.Getenv("EMBYFIN_EXPORTER_TEST_CONTAINER")
)

// Which server is under test, as the server label has it.
const (
	emby     = "emby"
	jellyfin = "jellyfin"
)

// The catalogue .scripts/testenv.sh lays out, as counts: change one and the other must agree.
const (
	films       = 4
	series      = 2
	seasons     = 3
	episodes    = 5
	artists     = 2
	albums      = 3
	tracks      = 6
	musicVideos = 2
	homeVideos  = 1
	photos      = 2
	books       = 2
	// one audiobook, in two chapters
	audiobooks = 1
	chapters   = 2
	// the folder that mixes kinds: one film, and one series of one season and one episode
	mixedFilms    = 1
	mixedSeries   = 1
	mixedSeasons  = 1
	mixedEpisodes = 1
)

// The libraries the suite makes over the catalogue, and the collection and the playlist it makes of two of the films. Both servers then list a
// library of collections, and Emby one of playlists as well.
const (
	filmLibrary       = "Films"
	showLibrary       = "Shows"
	musicLibrary      = "Music"
	clipLibrary       = "Clips"
	homeLibrary       = "Home"
	bookLibrary       = "Books"
	audiobookLibrary  = "Audiobooks"
	mixedLibrary      = "Mixed"
	collectionLibrary = "Collections"
	playlistLibrary   = "Playlists"
	collection        = "Ridley Scott"
	playlist          = "Tonight"
)

// library is one library the suite makes: its name, its type, where its files are in the container, and how many of each kind the server should
// find there, by the server's own name for the kind.
type library struct {
	name, collectionType, folder string
	holds                        map[string]int
}

var libraries = catalogue()

// catalogue is the libraries the suite makes on the server under test. The two servers differ over two of them. An audiobook is an album of tracks
// in a library of audiobooks on Emby, and an audiobook in a library of books on Jellyfin, which has no library of audiobooks. And a library of
// mixed kinds is one given no type on Emby, where one given the type "mixed" reads no file at all (seen on 4.10.1); Jellyfin's is asked for by
// that word.
func catalogue() []library {
	listened := library{audiobookLibrary, "audiobooks", "/media/audiobooks", map[string]int{"MusicAlbum": audiobooks, "Audio": chapters}}
	mixed := library{mixedLibrary, "", "/media/mixed", map[string]int{"Movie": mixedFilms, "Series": mixedSeries, "Season": mixedSeasons, "Episode": mixedEpisodes}}
	if backend == jellyfin {
		listened = library{audiobookLibrary, "books", "/media/audiobooks", map[string]int{"AudioBook": chapters}}
		mixed.collectionType = "mixed"
	}

	return []library{
		{filmLibrary, "movies", "/media/films", map[string]int{"Movie": films}},
		{showLibrary, "tvshows", "/media/shows", map[string]int{"Series": series, "Season": seasons, "Episode": episodes}},
		{musicLibrary, "music", "/media/music", map[string]int{"MusicAlbum": albums, "Audio": tracks}},
		{clipLibrary, "musicvideos", "/media/musicvideos", map[string]int{"MusicVideo": musicVideos}},
		{homeLibrary, "homevideos", "/media/home", map[string]int{"Video": homeVideos, "Photo": photos}},
		{bookLibrary, "books", "/media/books", map[string]int{"Book": books}},
		listened,
		mixed,
	}
}

const (
	// scanPatience is how long the server is given to read the catalogue: seconds on a laptop, longer on a shared CI runner.
	scanPatience = 5 * time.Minute
	// patience is how long the page is given to show a change just made on the server.
	patience = 45 * time.Second
)

// client sets the server up and plays the part of its users. The program under test never uses it: it makes its own.
var client *embyfin.Client

// asker is the program's own way of asking the server, made once and used by every scrape. One made for each scrape would leave its connections
// open behind it, and a test that scrapes in a loop then holds hundreds open at once: enough to bring down the link to a Docker that runs in a
// virtual machine, as it did on a Mac. It names nobody, as the program does unless told to; naming is the same told to name all it can: accounts,
// titles and devices.
var asker, naming *mediaserver.Server

// named finds a label on a page that names someone or something: an account, a title, a device or its app, with anything in it. A page that
// names nobody still has the labels, empty.
var named = regexp.MustCompile(`(user|title|device|client)="[^"]`)

// runSuite sets the server up, runs the tests, and returns the exit code. With no server named every test skips.
func runSuite(m *testing.M) int {
	if backend == "" || address == "" || token == "" {
		return m.Run()
	}

	var err error
	if client, err = embyfin.New(embyfin.Backend(backend), address, token); err != nil {
		fmt.Fprintln(os.Stderr, "making a client for the test server:", err)

		return 1
	}
	if asker, err = mediaserver.Connect(backend, address, token, mediaserver.Detail{}); err != nil {
		fmt.Fprintln(os.Stderr, "connecting to the test server as the program does:", err)

		return 1
	}
	if naming, err = mediaserver.Connect(backend, address, token, mediaserver.Detail{Users: true, Titles: true, Devices: true}); err != nil {
		fmt.Fprintln(os.Stderr, "connecting to the test server as the program does when told to name accounts, titles and devices:", err)

		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*scanPatience)
	defer cancel()

	if err := setUp(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "setting the test server up:", err)

		return 1
	}

	return m.Run()
}

// live skips a test when no server is up.
func live(t *testing.T) {
	t.Helper()

	if client == nil {
		t.Skip(`no server; run: eval "$(EMBYFIN_EXPORTER_TEST_BACKEND=jellyfin .scripts/testenv.sh up)"`)
	}
}

// setUp makes the libraries, waits for the server to have read them, makes the collection and the playlist, and has the scan of every library run
// to its end so there is a finished one to report. Each step is skipped when a run before this one did it, so the suite can be run again on the
// same server.
func setUp(ctx context.Context) error {
	folders, err := client.VirtualFolders(ctx)
	if err != nil {
		return fmt.Errorf("listing the libraries: %w", err)
	}

	for _, l := range libraries {
		if slices.ContainsFunc(folders, func(f embyfin.VirtualFolder) bool { return f.Name == l.name }) {
			continue
		}
		if err := client.CreateLibrary(ctx, embyfin.LibrarySpec{Name: l.name, CollectionType: l.collectionType, Paths: []string{l.folder}, Refresh: true}); err != nil {
			return fmt.Errorf("creating the library %s: %w", l.name, err)
		}
	}

	if err := settle(ctx); err != nil {
		return err
	}

	if err := curate(ctx); err != nil {
		return err
	}

	return scanOnce(ctx)
}

// settle waits for every library to hold what the catalogue has with no scan running, and asks for a scan again whenever the server goes idle
// short of that: one ask is not always enough (embyfin-mcp's suites saw Emby 4.10.1 cancel one library's scan as the next library was made).
func settle(ctx context.Context) error {
	deadline := time.Now().Add(scanPatience)
	for {
		short := unread(ctx)
		if short == "" && idle(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the scans did not settle in %s: %s", scanPatience, short)
		}

		if short != "" && idle(ctx) {
			if err := client.RefreshLibrary(ctx); err != nil {
				fmt.Fprintln(os.Stderr, "asking for a scan again:", err)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// unread says what a library still lacks of the catalogue, and nothing once every one of them holds all of it.
func unread(ctx context.Context) string {
	for _, l := range libraries {
		folder, err := client.FindLibrary(ctx, l.name)
		// a library just made is listed before it has an id, and a count with no id would be of every library
		if err != nil || folder == nil || folder.ItemID == "" {
			return fmt.Sprintf("the library %s is not listed yet (%v)", l.name, err)
		}

		for kind, want := range l.holds {
			if got, err := client.Count(ctx, embyfin.SearchOptions{ParentID: folder.ItemID, IncludeItemTypes: kind}); err != nil || got != want {
				return fmt.Sprintf("the library %s holds %d of kind %s, want %d (%v)", l.name, got, kind, want, err)
			}
		}
	}

	return ""
}

// idle says that no scan is running, as far as the server says.
func idle(ctx context.Context) bool {
	running, err := client.ScansRunning(ctx)

	return err == nil && len(running) == 0
}

// curate makes a collection and a playlist of the first two films, unless they are there already.
func curate(ctx context.Context) error {
	folder, err := client.FindLibrary(ctx, filmLibrary)
	if err != nil {
		return err
	}

	found, _, err := client.Search(ctx, embyfin.SearchOptions{ParentID: folder.ItemID, IncludeItemTypes: "Movie", SortBy: "SortName"})
	if err != nil || len(found) < 2 {
		return fmt.Errorf("listing the films: %d of them, %w", len(found), err)
	}
	pair := []string{found[0].ID, found[1].ID}

	// a collection made under a name that exists is that collection again
	if _, err := client.CreateCollection(ctx, collection, pair); err != nil {
		return fmt.Errorf("making the collection: %w", err)
	}

	owner, err := client.ResolveUser(ctx, admin)
	if err != nil {
		return err
	}

	playlists, _, err := client.Search(ctx, embyfin.SearchOptions{IncludeItemTypes: "Playlist", UserID: owner.ID})
	if err != nil {
		return fmt.Errorf("listing the playlists: %w", err)
	}
	if len(playlists) > 0 {
		return nil
	}

	if _, err := client.CreatePlaylist(ctx, playlist, pair, "Video", owner.ID); err != nil {
		return fmt.Errorf("making the playlist: %w", err)
	}

	return nil
}

// scanOnce has the scan of every library run to its end, unless its last run already did, so there is a finished scan to report. Making a library
// scans that library and not all of them, and Emby cuts a scan short when the next library is made (seen on 4.10.1: the scan's last run reads
// Cancelled once the libraries are up), so it is asked for again until one completes.
func scanOnce(ctx context.Context) error {
	deadline := time.Now().Add(scanPatience)
	for {
		scan, err := theScan(ctx)
		if err != nil {
			return err
		}

		last := scan.LastExecutionResult
		if !scan.Running() && last != nil && strings.EqualFold(last.Status, "Completed") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the scan of every library did not complete in %s: it is %s and last ended %+v", scanPatience, scan.State, last)
		}

		if !scan.Running() {
			if err := client.RefreshLibrary(ctx); err != nil {
				return fmt.Errorf("asking for a scan of every library: %w", err)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// theScan is the scheduled task that scans every library.
func theScan(ctx context.Context) (*embyfin.Task, error) {
	tasks, err := client.Tasks(ctx)
	if err != nil {
		return nil, err
	}

	for i := range tasks {
		if tasks[i].IsLibraryScan() {
			return &tasks[i], nil
		}
	}

	return nil, errors.New("the server lists no scan of every library among its scheduled tasks")
}

// first is the id and the name of the first item of a kind in a library, by the server's own name for the kind.
func first(t *testing.T, library, kind string) (id, name string) {
	t.Helper()

	folder, err := client.FindLibrary(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}

	found, _, err := client.Search(t.Context(), embyfin.SearchOptions{ParentID: folder.ItemID, IncludeItemTypes: kind, SortBy: "SortName"})
	if err != nil || len(found) == 0 {
		t.Fatalf("listing what the library %s holds of kind %s: %d of them, %v", library, kind, len(found), err)
	}

	return found[0].ID, found[0].Name
}

// scrape asks the server everything once, as the program does when it starts, and gives back the page a scrape would then read. Only is the rounds
// to ask, every one when none is named. The page names nobody, as the program's does unless it is told to.
func scrape(t *testing.T, only ...poll.Round) string {
	t.Helper()

	return page(t, asker, only...)
}

// scrapeNaming is scrape for a program told to name accounts, titles and devices.
func scrapeNaming(t *testing.T, only ...poll.Round) string {
	t.Helper()

	return page(t, naming, only...)
}

// page is the page a scrape reads once server has been asked those rounds.
func page(t *testing.T, server *mediaserver.Server, only ...poll.Round) string {
	t.Helper()

	p, err := poll.New(server, poll.Intervals{Sessions: time.Minute, Library: 5 * time.Minute, History: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	if len(only) == 0 {
		only = poll.Rounds
	}
	for _, round := range only {
		p.Poll(t.Context(), round)
	}

	made, err := pmetrics.New(pmetrics.Options{Name: "embyfin-exporter", Namespace: readings.Namespace}, readings.New(p))
	if err != nil {
		t.Fatalf("making the page: %v", err)
	}

	var out strings.Builder
	if err := made.Write(&out); err != nil {
		t.Fatalf("writing the page: %v", err)
	}

	return out.String()
}

// reading names one reading of the server under test, as the page writes it: its name, then its labels beside the server's as name, value pairs.
func reading(name string, labels ...string) string {
	pairs := []string{`server="` + backend + `"`}
	for i := 0; i+1 < len(labels); i += 2 {
		pairs = append(pairs, labels[i]+`="`+labels[i+1]+`"`)
	}
	// the page lists a reading's labels by name
	slices.Sort(pairs)

	return name + "{" + strings.Join(pairs, ",") + "}"
}

// value is what the page says for one reading, and whether it says anything.
func value(text, reading string) (float64, bool) {
	for line := range strings.SplitSeq(text, "\n") {
		if rest, found := strings.CutPrefix(line, reading+" "); found {
			v, err := strconv.ParseFloat(rest, 64)

			return v, err == nil
		}
	}

	return 0, false
}

// every is each line of the page that is a reading of that name, whatever its labels.
func every(text, name string) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, name+"{") {
			out = append(out, line)
		}
	}

	return out
}

// expect fails the test for each reading the page does not give the value wanted.
func expect(t *testing.T, text string, want map[string]float64) {
	t.Helper()

	for _, name := range slices.Sorted(maps.Keys(want)) {
		if got, found := value(text, name); !found || got != want[name] {
			t.Errorf("%s = %v (on the page: %t), want %v", name, got, found, want[name])
		}
	}
}

// eventually scrapes until the page satisfies check or patience runs out, and gives back the last page read and whether it did: a change made on
// the server takes a moment to show in what it answers.
func eventually(t *testing.T, check func(text string) bool, only ...poll.Round) (string, bool) {
	t.Helper()

	deadline := time.Now().Add(patience)
	for {
		text := scrape(t, only...)
		if check(text) {
			return text, true
		}
		if time.Now().After(deadline) {
			return text, false
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// player is a device signed in as the ordinary account, the way an app on someone's phone is: what the device and its app are called, and the
// token and account the server gave it.
type player struct {
	device, app, token, userID string
}

// signIn signs a device in as the ordinary account and signs it out when the test ends.
func signIn(t *testing.T, device, app string) player {
	t.Helper()

	p := player{device: device, app: app}
	status, raw := p.send(t, http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": user, "Pw": password})

	var login struct {
		AccessToken string
		User        struct {
			ID string `json:"Id"`
		}
	}
	if err := json.Unmarshal(raw, &login); status != http.StatusOK || err != nil || login.AccessToken == "" {
		t.Fatalf("signing %s in as %s: HTTP %d: %.200s", device, user, status, raw)
	}
	p.token, p.userID = login.AccessToken, login.User.ID

	t.Cleanup(func() { p.send(t, http.MethodPost, "/Sessions/Logout", nil) })

	return p
}

// send is one request from the device, signed with its own token once it has one, so the server keeps what it says on that device's session.
func (p player) send(t *testing.T, method, at string, body any) (status int, answer []byte) {
	t.Helper()

	auth := fmt.Sprintf(`MediaBrowser Client=%q, Device=%q, DeviceId=%q, Version="0"`, p.app, p.device, p.device)
	if p.token != "" {
		auth += fmt.Sprintf(`, Token=%q`, p.token)
	}

	var r io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}

	// without the test's cancellation, so a cleanup can still put things back
	req, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()), method, address+at, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	req.Header.Set("X-Emby-Authorization", auth)
	if p.token != "" {
		req.Header.Set("X-Emby-Token", p.token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	answer, _ = io.ReadAll(resp.Body)

	return resp.StatusCode, answer
}

// report tells the server how a play stands: begun, going on or stopped, by where it is sent, and by which method the device says it plays.
func (p player) report(t *testing.T, at, item, playSession, method string, paused bool) {
	t.Helper()

	if status, raw := p.send(t, http.MethodPost, at, map[string]any{"ItemId": item, "PlaySessionId": playSession, "PositionTicks": 0, "IsPaused": paused, "CanSeek": true, "PlayMethod": method}); status/100 != 2 {
		t.Fatalf("%s: HTTP %d: %.200s", at, status, raw)
	}
}

// lowBitrate is below what the fixture's film comes to, so a device held to it has the server re-encode the film, and highBitrate is far above
// it, so a device given that much has the server pass the picture and the sound on as they are.
const (
	lowBitrate  = 300000
	highBitrate = 100000000
)

// profile is what a device says of itself that can play nothing as the file is: no direct play, and one way of being streamed to, in the codecs the
// fixture's film is already in, at no more than a bitrate. A server asked how such a device should play a film answers with a stream it will make:
// re-encoded when the bitrate is below the film's, and only repacked when it is not.
func profile(bitrate int) map[string]any {
	return map[string]any{
		"MaxStreamingBitrate": bitrate,
		"DirectPlayProfiles":  []any{},
		"TranscodingProfiles": []any{map[string]any{"Container": "ts", "Type": "Video", "VideoCodec": "h264", "AudioCodec": "aac", "Protocol": "hls", "Context": "Streaming", "MaxAudioChannels": "2"}},
		"CodecProfiles":       []any{},
		"SubtitleProfiles":    []any{},
	}
}

// streamFor has the server start making a stream of an item for the device, held to a bitrate, and gives back the play's id. The list of
// renditions names the list of pieces, which names the first piece, and asking for that is what starts the server on the work.
func (p player) streamFor(t *testing.T, item string, bitrate int) (playSession string) {
	t.Helper()

	playSession, stream := p.howToPlay(t, item, map[string]any{"UserId": p.userID, "MaxStreamingBitrate": bitrate, "EnableDirectPlay": false, "EnableDirectStream": true, "EnableTranscoding": true, "DeviceProfile": profile(bitrate)})
	if stream == "" {
		t.Fatal("the server offered no stream of its own making to a device that can play nothing as it is")
	}

	p.fetch(t, p.fetch(t, p.fetch(t, stream)))

	return playSession
}

// howToPlay asks the server how the device should play an item, and gives back the play's id and, when the server means to make a stream for it,
// where that stream is.
func (p player) howToPlay(t *testing.T, item string, body map[string]any) (playSession, stream string) {
	t.Helper()

	status, raw := p.send(t, http.MethodPost, "/Items/"+item+"/PlaybackInfo?UserId="+p.userID, body)

	var info struct {
		PlaySessionID string `json:"PlaySessionId"`
		MediaSources  []struct {
			TranscodingURL string `json:"TranscodingUrl"`
		}
	}
	if err := json.Unmarshal(raw, &info); status != http.StatusOK || err != nil || info.PlaySessionID == "" {
		t.Fatalf("asking how to play the item: HTTP %d: %.200s", status, raw)
	}

	if len(info.MediaSources) > 0 {
		stream = info.MediaSources[0].TranscodingURL
	}

	return info.PlaySessionID, stream
}

// fetch reads one part of a stream the server makes. A stream is a list of lists: the first names its one rendition, that one names the pieces,
// and it is asking for a piece that sets the server to making them. What comes back is what the part names first, as a path on the server, and
// nothing for a piece, which names nothing.
func (p player) fetch(t *testing.T, at string) string {
	t.Helper()

	status, body := p.send(t, http.MethodGet, at, nil)
	if status != http.StatusOK || len(body) == 0 {
		t.Fatalf("reading the stream at %.80s: HTTP %d and %d bytes", at, status, len(body))
	}

	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "/") {
			return line
		}

		folder, _, _ := strings.Cut(at, "?")

		return path.Dir(folder) + "/" + line
	}

	return ""
}
