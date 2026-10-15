package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/go-kt/pointer"
)

// testKey is a key no server has; a test checks it is never repeated.
const testKey = "0123456789abcdef0123456789abcdef"

var errDown = errors.New("the server is down")

// fake answers in a server's place with what it is set to. A count is looked up under the library's id and the kind asked after, and every count
// asked for is remembered.
type fake struct {
	backend  embyfin.Backend
	info     embyfin.SystemInfo
	sessions []embyfin.Session
	tasks    []embyfin.Task
	folders  []embyfin.VirtualFolder
	counts   map[string]int
	users    []embyfin.User
	devices  []embyfin.Device
	plugins  []embyfin.Plugin
	// err fails every question, and countErr only the counts
	err      error
	countErr error

	mu    sync.Mutex
	asked []string
}

func (f *fake) Backend() embyfin.Backend {
	if f.backend == "" {
		return embyfin.Emby
	}

	return f.backend
}

func (*fake) APIVersion() string { return "1.2.3" }

func (f *fake) SystemInfo(context.Context) (*embyfin.SystemInfo, error) { return &f.info, f.err }

func (f *fake) Sessions(context.Context) ([]embyfin.Session, error) { return f.sessions, f.err }

func (f *fake) Tasks(context.Context) ([]embyfin.Task, error) { return f.tasks, f.err }

func (f *fake) VirtualFolders(context.Context) ([]embyfin.VirtualFolder, error) {
	return f.folders, f.err
}

func (f *fake) Count(_ context.Context, opts embyfin.SearchOptions) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	question := opts.ParentID + "/" + opts.IncludeItemTypes
	f.asked = append(f.asked, question)

	return f.counts[question], errors.Join(f.err, f.countErr)
}

func (f *fake) Users(context.Context) ([]embyfin.User, error) { return f.users, f.err }

func (f *fake) Plugins(context.Context) ([]embyfin.Plugin, error) { return f.plugins, f.err }

func (f *fake) Devices(context.Context) ([]embyfin.Device, error) { return f.devices, f.err }

func TestInfo(t *testing.T) {
	t.Parallel()

	for _, backend := range []embyfin.Backend{embyfin.Emby, embyfin.Jellyfin} {
		s := &Server{client: &fake{backend: backend, info: embyfin.SystemInfo{ServerName: "den", Version: "4.9.1.0", HasPendingRestart: true, HasUpdateAvailable: true}}}

		got, err := s.Info(t.Context())
		if err != nil {
			t.Fatalf("%s: info: %v", backend, err)
		}
		if want := (poll.Info{Name: "den", Version: "4.9.1.0", APIVersion: "1.2.3", RestartPending: true, UpdateAvailable: true}); got != want {
			t.Errorf("%s: info = %+v, want %+v", backend, got, want)
		}
		if s.Kind() != string(backend) {
			t.Errorf("kind = %q, want %q", s.Kind(), backend)
		}
	}
}

// What a session is doing and how, as each server says it. The first transcode is one seen on a real Emby, the picture passed on and the sound
// re-encoded, and so are the two with reasons and a bitrate.
func TestSessions(t *testing.T) {
	t.Parallel()

	yes, no := true, false
	playing, song := &embyfin.Item{Name: "A Film", Type: "Movie"}, &embyfin.Item{Name: "A Song", Type: "Audio"}

	for name, c := range map[string]struct {
		session embyfin.Session
		want    poll.Session
	}{
		"nothing playing":                                               {embyfin.Session{}, poll.Session{State: poll.Idle}},
		"nothing playing, the pause flag left set":                      {embyfin.Session{PlayState: embyfin.PlayState{IsPaused: true, PlayMethod: "Transcode"}}, poll.Session{State: poll.Idle}},
		"nothing playing, a stream not yet cleared up":                  {embyfin.Session{Transcoding: &embyfin.Transcoding{VideoCodec: "h264"}}, poll.Session{State: poll.Idle}},
		"the file as it is":                                             {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "DirectPlay"}}, poll.Session{State: poll.Playing, Method: poll.DirectPlay, Kind: poll.Movie}},
		"paused":                                                        {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{IsPaused: true, PlayMethod: "DirectPlay"}}, poll.Session{State: poll.Paused, Method: poll.DirectPlay, Kind: poll.Movie}},
		"a direct stream by the device's word":                          {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "DirectStream"}}, poll.Session{State: poll.Playing, Method: poll.DirectStream, Kind: poll.Movie}},
		"a transcode by the device's word alone":                        {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie}},
		"the device does not say":                                       {embyfin.Session{NowPlayingItem: playing}, poll.Session{State: poll.Playing, Method: poll.UnknownMethod, Kind: poll.Movie}},
		"the picture passed on, the sound re-encoded":                   {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "ac3", VideoDirect: true, DecoderHardware: &no, EncoderHardware: &no}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie}},
		"repacked, nothing re-encoded, called a transcode":              {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "aac", VideoDirect: true, AudioDirect: true}}, poll.Session{State: poll.Playing, Method: poll.DirectStream, Kind: poll.Movie}},
		"the sound re-encoded, called a direct stream":                  {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "DirectStream"}, Transcoding: &embyfin.Transcoding{VideoCodec: "hevc", AudioCodec: "aac", VideoDirect: true}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie}},
		"emby, the picture re-encoded by a GPU":                         {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "aac", AudioDirect: true, DecoderHardware: &no, EncoderHardware: &yes}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie, VideoTranscode: true, Hardware: true}},
		"emby, the picture decoded by a GPU and re-encoded without one": {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "aac", DecoderHardware: &yes, EncoderHardware: &no}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie, VideoTranscode: true}},
		"jellyfin, the picture re-encoded by a GPU":                     {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{IsPaused: true, PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "aac", HardwareAcceleration: "vaapi"}}, poll.Session{State: poll.Paused, Method: poll.Transcode, Kind: poll.Movie, VideoTranscode: true, Hardware: true}},
		"jellyfin, the picture re-encoded without one":                  {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "aac", HardwareAcceleration: "none"}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie, VideoTranscode: true}},
		"a song re-encoded has no picture to re-encode":                 {embyfin.Session{NowPlayingItem: song, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{AudioCodec: "mp3", HardwareAcceleration: "vaapi"}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Track}},
		"emby, a stream made for its subtitles":                         {embyfin.Session{NowPlayingItem: playing, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "mp3", Bitrate: 3808002, Reasons: []string{"SubtitleCodecNotSupported"}, EncoderHardware: &no}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie, VideoTranscode: true, Bitrate: 3808002, Reasons: []string{"subtitle_codec_not_supported"}}},
		"emby, an episode with two reasons, one of them said twice":     {embyfin.Session{NowPlayingItem: &embyfin.Item{Type: "Episode"}, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "eac3", AudioDirect: true, Bitrate: 22371303, Reasons: []string{"DirectPlayError", "ContainerNotSupported", "DirectPlayError"}, EncoderHardware: &yes}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Episode, VideoTranscode: true, Hardware: true, Bitrate: 22371303, Reasons: []string{"container_not_supported", "direct_play_error"}}},
		"live television is none of the kinds the page tells apart":     {embyfin.Session{NowPlayingItem: &embyfin.Item{Type: "TvChannel"}, PlayState: embyfin.PlayState{PlayMethod: "DirectPlay"}}, poll.Session{State: poll.Playing, Method: poll.DirectPlay, Kind: poll.OtherKind}},
		"a bitrate below nothing is none":                               {embyfin.Session{NowPlayingItem: song, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{AudioCodec: "mp3", Bitrate: -1}}, poll.Session{State: poll.Playing, Method: poll.Transcode, Kind: poll.Track}},
		"a song repacked":                                               {embyfin.Session{NowPlayingItem: song, PlayState: embyfin.PlayState{PlayMethod: "Transcode"}, Transcoding: &embyfin.Transcoding{AudioCodec: "flac", AudioDirect: true}}, poll.Session{State: poll.Playing, Method: poll.DirectStream, Kind: poll.Track}},
	} {
		s := &Server{client: &fake{sessions: []embyfin.Session{c.session}}}

		// none of these sessions says where it connects from, and what leaves the server for one it makes a stream for is that stream
		if c.want.State != poll.Idle {
			c.want.Network, c.want.Outgoing = poll.UnknownNetwork, c.want.Bitrate
		}

		got, err := s.Sessions(t.Context())
		if err != nil {
			t.Fatalf("%s: sessions: %v", name, err)
		}
		if len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
			t.Errorf("%s: session = %+v, want %+v", name, got, c.want)
		}
	}
}

// fakeFiles answers for what the files being played come to, and counts how often it was asked.
type fakeFiles struct {
	rates map[string]int
	err   error
	asked int
}

func (f *fakeFiles) FileRates(context.Context) (map[string]int, error) {
	f.asked++

	return f.rates, f.err
}

// streaming is three sessions as a server gives them: an episode sent as the file is to a television at home, a film the server re-encodes for
// a phone outside, and a tablet with nothing playing.
func streaming() []embyfin.Session {
	one, two := 1, 2

	return []embyfin.Session{
		{
			ID: "aaa", UserName: "ada", DeviceName: "Ada's television", Client: "Den Player", RemoteEndPoint: "192.168.1.20",
			NowPlayingItem: &embyfin.Item{Name: "Lies", Type: "Episode", SeriesName: "Dark", ParentIndexNumber: &one, IndexNumber: &two, RunTimeTicks: 30_000_000_000},
			PlayState:      embyfin.PlayState{PositionTicks: 6_000_000_000, PlayMethod: "DirectPlay"},
		},
		{
			ID: "bbb", UserName: "grace", DeviceName: "Grace's phone", Client: "Pocket Player", RemoteEndPoint: "203.0.113.7",
			NowPlayingItem: &embyfin.Item{Name: "Heat", Type: "Movie", ProductionYear: 1995, RunTimeTicks: 102_000_000_000},
			PlayState:      embyfin.PlayState{PositionTicks: 0, IsPaused: true, PlayMethod: "Transcode"},
			Transcoding:    &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "aac", Bitrate: 4_000_000, Reasons: []string{"ContainerBitrateExceedsLimit"}},
		},
		{ID: "ccc", UserName: "ada", DeviceName: "Ada's tablet", Client: "Den Player", RemoteEndPoint: "192.168.1.21"},
	}
}

// Who is streaming what, on what and from where: asked to name all of it, a session has whose it is, what it plays in a form to be read, the
// device and its app, whether the device is at home or outside, how far through it is, and what leaves the server for it: the file's own bitrate
// for a file sent as it is, and the stream's for one the server makes. A session with nothing playing has none of it.
func TestWhoIsStreaming(t *testing.T) {
	t.Parallel()

	files := &fakeFiles{rates: map[string]int{"aaa": 9_000_000, "bbb": 20_000_000}}
	s := &Server{client: &fake{sessions: streaming()}, files: files, detail: everything}

	got, err := s.Sessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := []poll.Session{
		{ID: "aaa", State: poll.Playing, Method: poll.DirectPlay, Kind: poll.Episode, Network: poll.Home, Outgoing: 9_000_000, Position: 10 * time.Minute, Runtime: 50 * time.Minute, User: "ada", Title: "Dark - S01E02 - Lies", Device: "Ada's television", Client: "Den Player"},
		{ID: "bbb", State: poll.Paused, Method: poll.Transcode, Kind: poll.Movie, Network: poll.Remote, VideoTranscode: true, Bitrate: 4_000_000, Reasons: []string{"container_bitrate_exceeds_limit"}, Outgoing: 4_000_000, Runtime: 170 * time.Minute, User: "grace", Title: "Heat (1995)", Device: "Grace's phone", Client: "Pocket Player"},
		{State: poll.Idle},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sessions =\n%+v\nwant\n%+v", got, want)
	}
	if files.asked != 1 {
		t.Errorf("what the files come to was asked %d times, want once", files.asked)
	}
}

// Each word of the settings names its own part of a session and no other, and with none of them a session has nobody's name, no title and no
// device. Where it connects from is home or remote either way, and the address itself is in nothing that is handed on.
func TestASessionNamesOnlyWhatIsAskedFor(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		detail                      Detail
		user, title, device, client string
	}{
		"nothing":  {Detail{}, "", "", "", ""},
		"accounts": {Detail{Users: true}, "ada", "", "", ""},
		"titles":   {Detail{Titles: true}, "", "Dark - S01E02 - Lies", "", ""},
		"devices":  {Detail{Devices: true}, "", "", "Ada's television", "Den Player"},
	} {
		s := &Server{client: &fake{sessions: streaming()}, files: &fakeFiles{}, detail: c.detail}

		got, err := s.Sessions(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got[0].User != c.user || got[0].Title != c.title || got[0].Device != c.device || got[0].Client != c.client {
			t.Errorf("asked for %s: session = %+v", name, got[0])
		}
		if got[0].ID != "aaa" || got[0].Network != poll.Home || got[1].Network != poll.Remote {
			t.Errorf("asked for %s: the sessions' ids and networks = %q %q and %q, want them whatever is asked", name, got[0].ID, got[0].Network, got[1].Network)
		}
		if said := fmt.Sprintf("%+v", got); strings.Contains(said, "192.168.1.20") || strings.Contains(said, "203.0.113.7") {
			t.Errorf("asked for %s: an address is handed on: %s", name, said)
		}
	}
}

// What the files come to is a second question to the server, so it is put only when some session is being sent a file as it is, and a server
// that fails it still has its sessions, less what the files come to.
func TestTheFilesAreAskedAfterOnlyWhenNeeded(t *testing.T) {
	t.Parallel()

	all := streaming()

	for name, c := range map[string]struct {
		sessions []embyfin.Session
		asked    int
	}{
		"nobody connected":                 {nil, 0},
		"nothing playing":                  {all[2:], 0},
		"only a stream the server makes":   {all[1:], 0},
		"a file sent as it is among them":  {all, 1},
		"a file sent as it is and no more": {all[:1], 1},
	} {
		files := &fakeFiles{rates: map[string]int{"aaa": 9_000_000}}
		s := &Server{client: &fake{sessions: c.sessions}, files: files}

		if _, err := s.Sessions(t.Context()); err != nil || files.asked != c.asked {
			t.Errorf("%s: what the files come to was asked %d times (%v), want %d", name, files.asked, err, c.asked)
		}
	}

	failing := &Server{client: &fake{sessions: all}, files: &fakeFiles{err: errDown}}
	got, err := failing.Sessions(t.Context())
	if err != nil || len(got) != 3 || got[0].State != poll.Playing || got[0].Outgoing != 0 || got[1].Outgoing != 4_000_000 {
		t.Errorf("with the second question failing, sessions = %+v and %v, want all three and only the stream's bitrate", got, err)
	}
}

// The page is counts and states: whose a session or an account is, the device, where it connects from and what it plays are in the server's answers
// and must not be in what is handed on.
func TestNothingSaysWhoseItIs(t *testing.T) {
	t.Parallel()

	private := []string{"alice", "Alice's iPad", "203.0.113.7", "A Film Nobody Should Know Of", "Infuse"}
	s := &Server{client: &fake{
		sessions: []embyfin.Session{{
			ID: "one", UserName: private[0], DeviceName: private[1], RemoteEndPoint: private[2], Client: private[4],
			NowPlayingItem: &embyfin.Item{Name: private[3], Type: "Movie", Path: "/films/" + private[3] + ".mkv"},
			PlayState:      embyfin.PlayState{PlayMethod: "Transcode"},
			Transcoding:    &embyfin.Transcoding{VideoCodec: "h264", AudioCodec: "aac", Bitrate: 4000000, Reasons: []string{"ContainerNotSupported"}},
		}},
		users:   []embyfin.User{{ID: "one", Name: private[0], LastActivityDate: "2026-10-08T18:05:38.3907119Z"}},
		devices: []embyfin.Device{{Name: private[1], AppName: private[4], LastUserName: private[0], DateLastActivity: "2026-10-08T18:05:38.3907119Z"}},
	}}

	sessions, err1 := s.Sessions(t.Context())
	users, err2 := s.Users(t.Context())
	devices, err3 := s.Devices(t.Context())
	if err := errors.Join(err1, err2, err3); err != nil {
		t.Fatal(err)
	}

	said := fmt.Sprintf("%+v %+v %+v", sessions, users, devices)
	for _, word := range private {
		if strings.Contains(said, word) {
			t.Errorf("%q is handed on: %s", word, said)
		}
	}
}

// A task's name, its state, how far it has got, whether it is the scan of every library, and how its last run went. A task goes by its name and by
// its key when it has none, only a running one has got anywhere, a run that was cancelled neither finished nor failed, and Emby's times with no zone
// are read as well as those with one.
func TestTasks(t *testing.T) {
	t.Parallel()

	ended := time.Date(2026, 2, 12, 2, 25, 39, 408882600, time.UTC)
	s := &Server{client: &fake{tasks: []embyfin.Task{
		{Key: "RefreshLibrary", State: "Running", Progress: 37.5, LastExecutionResult: &embyfin.TaskResult{Status: "Completed", StartTimeUtc: "2026-02-12T02:07:07.2329092Z", EndTimeUtc: "2026-02-12T02:25:39.4088826Z"}},
		{Key: "RefreshLibrary", Name: "Scan media library", State: "Idle", Progress: 80},
		{Key: "RefreshGuide", State: "Idle", LastExecutionResult: &embyfin.TaskResult{Status: "Failed", StartTimeUtc: "2026-02-12T02:07:07.2329092", EndTimeUtc: "2026-02-12T02:25:39.4088826"}},
		{Key: "CleanCache", Name: "Cache file cleanup", State: "Cancelling", Progress: 250, LastExecutionResult: &embyfin.TaskResult{Status: "Cancelled", EndTimeUtc: "2026-02-12T02:25:39.4088826Z"}},
		{Key: "Odd", State: "Idle", LastExecutionResult: &embyfin.TaskResult{Status: "Aborted", StartTimeUtc: "soon", EndTimeUtc: "later"}},
	}}}

	got, err := s.Tasks(t.Context())
	if err != nil {
		t.Fatalf("tasks: %v", err)
	}

	took := 18*time.Minute + 32*time.Second + 175973400*time.Nanosecond
	want := []poll.Task{
		{Name: "RefreshLibrary", State: poll.TaskRunning, Progress: 0.375, LibraryScan: true, LastRun: &poll.Run{OK: true, Ended: ended, Took: took}},
		{Name: "Scan media library", State: poll.TaskIdle, LibraryScan: true},
		{Name: "RefreshGuide", State: poll.TaskIdle, LastRun: &poll.Run{Failed: true, Ended: ended, Took: took}},
		{Name: "Cache file cleanup", State: poll.TaskCancelling, Progress: 1, LastRun: &poll.Run{Ended: ended}},
		{Name: "Odd", State: poll.TaskIdle, LastRun: &poll.Run{}},
	}
	if len(got) != len(want) {
		t.Fatalf("%d tasks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].State != want[i].State || got[i].Progress != want[i].Progress || got[i].LibraryScan != want[i].LibraryScan || (got[i].LastRun == nil) != (want[i].LastRun == nil) {
			t.Errorf("task %d = %+v, want %+v", i, got[i], want[i])
			continue
		}
		if run, wanted := got[i].LastRun, want[i].LastRun; run != nil && (run.OK != wanted.OK || run.Failed != wanted.Failed || !run.Ended.Equal(wanted.Ended) || run.Took != wanted.Took) {
			t.Errorf("task %d last ran %+v, want %+v", i, pointer.From(run), pointer.From(wanted))
		}
	}
}

// Each library is asked for the kinds its type holds and no others, by its own id. One with no id is listed and not asked: a count with no library
// named would be of the whole server.
func TestLibraries(t *testing.T) {
	t.Parallel()

	client := &fake{
		folders: []embyfin.VirtualFolder{
			{Name: "Films", CollectionType: "movies", ItemID: "10"},
			{Name: "TV", CollectionType: "tvshows", ItemID: "20"},
			{Name: "Music", CollectionType: "music", ItemID: "30"},
			{Name: "Collections", CollectionType: "boxsets", ItemID: "40"},
			{Name: "Anything", ItemID: "50"},
			{Name: "Lost", CollectionType: "movies"},
		},
		counts: map[string]int{"10/Movie": 1200, "20/Series": 80, "20/Season": 400, "20/Episode": 9000, "30/MusicArtist": 30, "30/Audio": 500, "40/BoxSet": 12, "50/Movie": 3, "50/Video": 9},
	}

	got, err := (&Server{client: client}).Libraries(t.Context())
	if err != nil {
		t.Fatalf("libraries: %v", err)
	}

	want := []poll.Library{
		{Name: "Films", Items: map[string]int{"movie": 1200}},
		{Name: "TV", Items: map[string]int{"series": 80, "season": 400, "episode": 9000}},
		{Name: "Music", Items: map[string]int{"artist": 30, "album": 0, "track": 500}},
		{Name: "Collections", Items: map[string]int{"collection": 12}},
		{Name: "Anything", Items: map[string]int{"movie": 3, "series": 0, "season": 0, "episode": 0, "video": 9}},
		{Name: "Lost", Items: map[string]int{}},
	}
	if len(got) != len(want) {
		t.Fatalf("%d libraries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || fmt.Sprint(got[i].Items) != fmt.Sprint(want[i].Items) {
			t.Errorf("library %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	asked := []string{"10/Movie", "20/Series", "20/Season", "20/Episode", "30/MusicArtist", "30/MusicAlbum", "30/Audio", "40/BoxSet", "50/Movie", "50/Series", "50/Season", "50/Episode", "50/Video"}
	if !slices.Equal(client.asked, asked) {
		t.Errorf("asked for %v, want %v", client.asked, asked)
	}
}

// Jellyfin gives every artist it knows whichever library is named, so a music library there is not asked for its artists.
func TestLibrariesOnJellyfin(t *testing.T) {
	t.Parallel()

	client := &fake{backend: embyfin.Jellyfin, folders: []embyfin.VirtualFolder{{Name: "music", CollectionType: "music", ItemID: "abc"}}, counts: map[string]int{"abc/MusicArtist": 1775, "abc/MusicAlbum": 40, "abc/Audio": 500}}

	got, err := (&Server{client: client}).Libraries(t.Context())
	if err != nil {
		t.Fatalf("libraries: %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0].Items) != fmt.Sprint(map[string]int{"album": 40, "track": 500}) {
		t.Errorf("libraries = %+v, want the one with 40 albums and 500 tracks", got)
	}
}

// Half a set of counts is worse than the last full one, so one count failing fails the answer, and the error says which.
func TestLibrariesFailWhole(t *testing.T) {
	t.Parallel()

	client := &fake{folders: []embyfin.VirtualFolder{{Name: "Films", CollectionType: "movies", ItemID: "10"}}, countErr: errDown}

	got, err := (&Server{client: client}).Libraries(t.Context())
	if !errors.Is(err, errDown) || got != nil {
		t.Fatalf("libraries = %+v and %v, want none and the server's error", got, err)
	}
	if !strings.Contains(err.Error(), "movie") || !strings.Contains(err.Error(), "Films") {
		t.Errorf("the error does not say which count failed: %v", err)
	}
}

// An account is whether it is disabled and when it was last seen, and a device when it was last seen. One never seen has no time.
func TestUsersAndDevices(t *testing.T) {
	t.Parallel()

	s := &Server{client: &fake{
		users: []embyfin.User{
			{Name: "one", LastActivityDate: "2026-10-08T18:05:38.3907119Z"},
			{Name: "two", Policy: embyfin.UserPolicy{IsDisabled: true}},
		},
		devices: []embyfin.Device{{Name: "a", DateLastActivity: "2026-10-12T21:10:32.0000000Z"}, {Name: "b"}, {Name: "c", DateLastActivity: "0001-01-01T00:00:00.0000000Z"}},
	}}

	users, err := s.Users(t.Context())
	if err != nil {
		t.Fatalf("users: %v", err)
	}
	if len(users) != 2 || users[0].Disabled || !users[0].LastActive.Equal(time.Date(2026, 10, 8, 18, 5, 38, 390711900, time.UTC)) || !users[1].Disabled || !users[1].LastActive.IsZero() {
		t.Errorf("users = %+v", users)
	}
	// whose an account is stays with the server unless the settings ask for accounts to be named
	if users[0].Name != "" || users[1].Name != "" {
		t.Errorf("users = %+v, want none of them named", users)
	}

	s.detail = Detail{Users: true}
	if users, err = s.Users(t.Context()); err != nil || len(users) != 2 || users[0].Name != "one" || users[1].Name != "two" {
		t.Errorf("asked to name accounts, users = %+v and %v", users, err)
	}
	s.detail = Detail{Titles: true}
	if users, err = s.Users(t.Context()); err != nil || users[0].Name != "" {
		t.Errorf("asked to name titles alone, users = %+v and %v, want none of them named", users, err)
	}

	devices, err := s.Devices(t.Context())
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if len(devices) != 3 || !devices[0].LastActive.Equal(time.Date(2026, 10, 12, 21, 10, 32, 0, time.UTC)) || !devices[1].LastActive.IsZero() || !devices[2].LastActive.IsZero() {
		t.Errorf("devices = %+v", devices)
	}
}

// A question the server does not answer is an error and no answer, never an empty one that would read as nothing playing or no libraries.
func TestAFailureIsPassedOn(t *testing.T) {
	t.Parallel()

	s := &Server{client: &fake{err: errDown, folders: []embyfin.VirtualFolder{{Name: "Films", ItemID: "10"}}}}

	_, infoErr := s.Info(t.Context())
	sessions, sessionsErr := s.Sessions(t.Context())
	tasks, tasksErr := s.Tasks(t.Context())
	libraries, librariesErr := s.Libraries(t.Context())
	users, usersErr := s.Users(t.Context())
	devices, devicesErr := s.Devices(t.Context())

	for question, err := range map[string]error{"info": infoErr, "sessions": sessionsErr, "tasks": tasksErr, "libraries": librariesErr, "users": usersErr, "devices": devicesErr} {
		if !errors.Is(err, errDown) {
			t.Errorf("%s: error = %v, want the server's", question, err)
		}
	}
	if sessions != nil || tasks != nil || libraries != nil || users != nil || devices != nil {
		t.Errorf("a failed question still gave an answer: %v %v %v %v %v", sessions, tasks, libraries, users, devices)
	}
}

// Connect makes a client that reaches a real server of either kind over HTTP, with the key in a header and not in the address.
func TestConnect(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var addresses []string
	var keyed int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		addresses = append(addresses, r.URL.String())
		if strings.Contains(fmt.Sprint(r.Header), testKey) {
			keyed++
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ServerName":"den","Version":"9.9.9"}`))
	}))
	t.Cleanup(server.Close)

	for _, kind := range []string{"emby", "jellyfin"} {
		s, err := Connect(kind, server.URL, testKey, Detail{})
		if err != nil {
			t.Fatalf("%s: connect: %v", kind, err)
		}

		info, err := s.Info(t.Context())
		if err != nil {
			t.Fatalf("%s: info: %v", kind, err)
		}
		if s.Kind() != kind || info.Name != "den" || info.Version != "9.9.9" || info.APIVersion == "" {
			t.Errorf("%s: kind %q and info %+v", kind, s.Kind(), info)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(addresses) != 2 || keyed != 2 {
		t.Errorf("%d requests of which %d carried the key in a header, want 2 and 2", len(addresses), keyed)
	}
	if strings.Contains(strings.Join(addresses, " "), testKey) {
		t.Errorf("the key is in an address: %v", addresses)
	}
}

// The key is never repeated in an error, which is what gets logged: not when the server refuses it and quotes it back, and not when there is no
// server there at all.
func TestTheKeyIsNotInAnError(t *testing.T) {
	t.Parallel()

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such key: "+testKey, http.StatusUnauthorized)
	}))
	t.Cleanup(refusing.Close)

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	for name, address := range map[string]string{"refused": refusing.URL, "nothing there": gone.URL} {
		for _, kind := range []string{"emby", "jellyfin"} {
			s, err := Connect(kind, address, testKey, Detail{})
			if err != nil {
				t.Fatalf("%s, %s: connect: %v", name, kind, err)
			}

			_, err = s.Info(t.Context())
			if err == nil || strings.Contains(err.Error(), testKey) {
				t.Errorf("%s, %s: error = %v, want one without the key in it", name, kind, err)
			}
		}
	}
}

// A kind that is neither server, or a server with no address or key, is refused before anything is sent.
func TestConnectRefusesWhatItCannotUse(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct{ kind, address, key string }{
		"a kind that is neither": {"plex", "http://nas:32400", testKey},
		"no address":             {"emby", "", testKey},
		"no key":                 {"jellyfin", "http://nas:8097", ""},
	} {
		if s, err := Connect(c.kind, c.address, c.key, Detail{}); err == nil || s != nil {
			t.Errorf("%s: connect = %v and %v, want an error", name, s, err)
		}
	}
}
