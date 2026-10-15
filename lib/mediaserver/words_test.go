package mediaserver

import (
	"slices"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
)

// collectionTypes is every type of library either server has, and two that neither does.
var collectionTypes = []string{"movies", "tvshows", "music", "musicvideos", "boxsets", "playlists", "books", "audiobooks", "homevideos", "photos", "trailers", "livetv", "folders", "games", "", "mixed"}

func labels(kinds []kind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.label)
	}

	return out
}

// Each kind counted in a library is one more question to the server every round, so a library is only asked for what its type holds, and one of
// a type not known here for what a library of anything holds.
func TestKindsIn(t *testing.T) {
	t.Parallel()

	for collectionType, want := range map[string][]string{
		"movies":      {"movie"},
		"tvshows":     {"series", "season", "episode"},
		"TvShows":     {"series", "season", "episode"},
		"musicvideos": {"music_video"},
		"boxsets":     {"collection"},
		"playlists":   {"playlist"},
		"homevideos":  {"video", "photo"},
		"photos":      {"video", "photo"},
		"":            {"movie", "series", "season", "episode", "video"},
		"mixed":       {"movie", "series", "season", "episode", "video"},
		"games":       {"movie", "series", "season", "episode", "video"},
	} {
		for _, backend := range []embyfin.Backend{embyfin.Emby, embyfin.Jellyfin} {
			if got := labels(kindsIn(backend, collectionType)); !slices.Equal(got, want) {
				t.Errorf("a %q library on %s is counted for %v, want %v", collectionType, backend, got, want)
			}
		}
	}
}

// Where the two servers part: Emby has no audiobook kind and keeps one as an album of tracks, and only Emby counts a library's artists.
func TestKindsInWhereTheServersDiffer(t *testing.T) {
	t.Parallel()

	for collectionType, want := range map[string]map[embyfin.Backend][]string{
		"music":      {embyfin.Emby: {"artist", "album", "track"}, embyfin.Jellyfin: {"album", "track"}},
		"books":      {embyfin.Emby: {"book"}, embyfin.Jellyfin: {"book", "audiobook"}},
		"audiobooks": {embyfin.Emby: {"album", "track"}, embyfin.Jellyfin: {"audiobook"}},
	} {
		for backend, kinds := range want {
			if got := labels(kindsIn(backend, collectionType)); !slices.Equal(got, kinds) {
				t.Errorf("a %q library on %s is counted for %v, want %v", collectionType, backend, got, kinds)
			}
		}
	}
}

// A server asked for a kind it cannot answer for by library gives the count of something else: Emby everything it holds for a kind it does not
// know, Jellyfin every artist on the server whichever library is named. Neither is ever asked.
func TestAServerIsNeverAskedForAKindItCannotCount(t *testing.T) {
	t.Parallel()

	for _, collectionType := range collectionTypes {
		if got := kindsIn(embyfin.Emby, collectionType); slices.Contains(got, audiobook) {
			t.Errorf("a %q library on emby is asked for its audiobooks, a kind emby does not know", collectionType)
		}
		if got := kindsIn(embyfin.Jellyfin, collectionType); slices.Contains(got, artist) {
			t.Errorf("a %q library on jellyfin is asked for its artists, which jellyfin does not count by library", collectionType)
		}
	}
}

// Every kind has a word of its own on the page and a type of its own on the server, or two counts would be written under one label.
func TestKindsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []kind{movie, series, season, episode, artist, album, track, musicVideo, collection, playlist, book, audiobook, video, photo}
	seenLabel, seenServer := map[string]bool{}, map[string]bool{}
	for _, k := range all {
		if k.label == "" || k.server == "" || seenLabel[k.label] || seenServer[k.server] {
			t.Errorf("%+v is empty or shares a name with another kind", k)
		}
		seenLabel[k.label], seenServer[k.server] = true, true
	}
}

// A session with nothing playing is idle even if its pause flag is still set from the last thing it played.
func TestSessionState(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		playing, paused bool
		want            poll.SessionState
	}{{true, false, poll.Playing}, {true, true, poll.Paused}, {false, false, poll.Idle}, {false, true, poll.Idle}} {
		if got := sessionState(c.playing, c.paused); got != c.want {
			t.Errorf("playing %t and paused %t = %s, want %s", c.playing, c.paused, got, c.want)
		}
	}
}

// The kind of thing a session plays, by the type its server gives what is playing, whatever the capitals. What the page has no word for is other,
// and every word it does have is one the page lists.
func TestPlaying(t *testing.T) {
	t.Parallel()

	for itemType, want := range map[string]poll.Kind{
		"Movie": poll.Movie, "Episode": poll.Episode, "Audio": poll.Track, "audio": poll.Track, "MusicVideo": poll.MusicVideo, "Video": poll.Video, "AudioBook": poll.Audiobook,
		"TvChannel": poll.OtherKind, "Trailer": poll.OtherKind, "Photo": poll.OtherKind, "Series": poll.OtherKind, "": poll.OtherKind,
	} {
		if got := playing(itemType); got != want {
			t.Errorf("%q = %s, want %s", itemType, got, want)
		}
	}

	for _, k := range played {
		if !slices.Contains(poll.Kinds, poll.Kind(k.label)) {
			t.Errorf("the %s kind can be playing and is not one the page lists", k.label)
		}
	}
}

// Why a server is making a stream, in the page's way of writing: each reason once, in order, and nothing for a word with no letters in it. The
// first three are reasons seen on a real Emby.
func TestReasons(t *testing.T) {
	t.Parallel()

	for word, want := range map[string]string{
		"ContainerNotSupported": "container_not_supported", "DirectPlayError": "direct_play_error", "ContainerBitrateExceedsLimit": "container_bitrate_exceeds_limit",
		"HDRNotSupported": "hdr_not_supported", "VideoH264Level": "video_h264_level", "Audio5Point1": "audio5_point1", "already_so": "already_so", "Two Words": "two_words", "lower": "lower", "X": "x", "--": "", "": "",
	} {
		if got := snake(word); got != want {
			t.Errorf("%q = %q, want %q", word, got, want)
		}
	}

	got := reasons([]string{"SubtitleCodecNotSupported", "", "ContainerNotSupported", "SubtitleCodecNotSupported", "--"})
	if want := []string{"container_not_supported", "subtitle_codec_not_supported"}; !slices.Equal(got, want) {
		t.Errorf("reasons = %v, want %v", got, want)
	}
	if got := reasons(nil); got != nil {
		t.Errorf("no reasons = %v, want none", got)
	}
}

// How far a task has got is the server's percentage as a part of 1, and never less than nothing or more than all of it.
func TestProgress(t *testing.T) {
	t.Parallel()

	for percent, want := range map[float64]float64{0: 0, 37.5: 0.375, 100: 1, 250: 1, -3: 0} {
		if got := progress(percent); got != want {
			t.Errorf("%v%% = %v, want %v", percent, got, want)
		}
	}
}

// The servers' words for how a session plays, and anything else as unknown rather than guessed at.
func TestMethod(t *testing.T) {
	t.Parallel()

	for word, want := range map[string]poll.Method{
		"DirectPlay": poll.DirectPlay, "DirectStream": poll.DirectStream, "Transcode": poll.Transcode, "transcode": poll.Transcode,
		"": poll.UnknownMethod, "Remux": poll.UnknownMethod,
	} {
		if got := method(word); got != want {
			t.Errorf("%q = %s, want %s", word, got, want)
		}
	}
}

// A stream is re-encoded when the server is not passing it on as it is and there is one: a song has no picture to re-encode.
func TestReencoded(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		direct bool
		codec  string
		want   bool
	}{{false, "h264", true}, {true, "h264", false}, {false, "", false}, {true, "", false}} {
		if got := reencoded(c.direct, c.codec); got != c.want {
			t.Errorf("direct %t with codec %q: re-encoded = %t, want %t", c.direct, c.codec, got, c.want)
		}
	}
}

// Emby says of the encoder whether it is hardware, and its word is taken over anything else; Jellyfin names the acceleration, with "none" for
// software. Neither saying anything is software.
func TestHardware(t *testing.T) {
	t.Parallel()

	yes, no := true, false
	for name, c := range map[string]struct {
		encoder      *bool
		acceleration string
		want         bool
	}{
		"emby, a hardware encoder":       {&yes, "", true},
		"emby, a software encoder":       {&no, "", false},
		"emby says no, whatever else":    {&no, "qsv", false},
		"jellyfin, quick sync":           {nil, "qsv", true},
		"jellyfin, software":             {nil, "none", false},
		"jellyfin, software in capitals": {nil, "None", false},
		"nothing said":                   {nil, "", false},
	} {
		if got := hardware(c.encoder, c.acceleration); got != c.want {
			t.Errorf("%s: hardware = %t, want %t", name, got, c.want)
		}
	}
}

// A task being cancelled is not idle yet, and a state neither server is known to give is.
func TestTaskState(t *testing.T) {
	t.Parallel()

	for word, want := range map[string]poll.TaskState{"Idle": poll.TaskIdle, "Running": poll.TaskRunning, "Cancelling": poll.TaskCancelling, "": poll.TaskIdle, "Queued": poll.TaskIdle} {
		if got := taskState(word); got != want {
			t.Errorf("%q = %s, want %s", word, got, want)
		}
	}
}

// Only a run that completed went as it should, and only one that failed went wrong: a run that was cancelled, or cut short by the server
// stopping, is neither.
func TestRanOKAndFailed(t *testing.T) {
	t.Parallel()

	for status, want := range map[string]struct{ ok, failed bool }{"Completed": {true, false}, "Failed": {false, true}, "failed": {false, true}, "Cancelled": {}, "Aborted": {}, "": {}} {
		if ranOK(status) != want.ok || failed(status) != want.failed {
			t.Errorf("%q: ran ok = %t and failed = %t, want %t and %t", status, ranOK(status), failed(status), want.ok, want.failed)
		}
	}
}

// The servers' times: seven places of a second, with a zone or, from Emby at times, without one. One that cannot be read is no time rather than
// a wrong one.
func TestWhen(t *testing.T) {
	t.Parallel()

	want := time.Date(2024, 12, 24, 22, 8, 54, 123456700, time.UTC)
	for _, s := range []string{"2024-12-24T22:08:54.1234567Z", "2024-12-24T22:08:54.1234567+00:00", "2024-12-24T14:08:54.1234567-08:00", "2024-12-24T22:08:54.1234567"} {
		if got := when(s); !got.Equal(want) {
			t.Errorf("%q = %s, want %s", s, got, want)
		}
	}

	// the last two are how a server writes "no date": year 1, with a zone and without
	for _, s := range []string{"", "yesterday", "0001-01-01", "0001-01-01T00:00:00.0000000Z", "0001-01-01T00:00:00.0000000"} {
		if got := when(s); !got.IsZero() {
			t.Errorf("%q = %s, want no time", s, got)
		}
	}
}

// How long a run took needs both its ends, the right way round.
func TestBetween(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 2, 12, 2, 7, 7, 0, time.UTC)
	end := start.Add(18 * time.Minute)

	if got := between(start, end); got != 18*time.Minute {
		t.Errorf("between = %s, want 18m", got)
	}
	for name, got := range map[string]time.Duration{"no start": between(time.Time{}, end), "no end": between(start, time.Time{}), "the wrong way round": between(end, start)} {
		if got != 0 {
			t.Errorf("%s: between = %s, want nothing", name, got)
		}
	}
}

// Where a device is, from the address a server gives for it, in each way a server has been seen to write one: at home on a private network or
// the server's own machine, remote on any other, and unknown when there is no address to read.
func TestNetwork(t *testing.T) {
	t.Parallel()

	for address, want := range map[string]poll.Network{
		"192.168.1.20":        poll.Home,
		"10.1.2.3":            poll.Home,
		"172.17.0.1":          poll.Home,
		"127.0.0.1":           poll.Home,
		"192.168.1.20:51234":  poll.Home,
		"::1":                 poll.Home,
		"::ffff:192.168.1.20": poll.Home,
		"fd12:3456::1":        poll.Home,
		"fe80::1%eth0":        poll.Home,
		"[fe80::1]:8096":      poll.Home,
		"169.254.10.10":       poll.Home,
		"203.0.113.7":         poll.Remote,
		"203.0.113.7:443":     poll.Remote,
		"2001:db8::1":         poll.Remote,
		"::ffff:203.0.113.7":  poll.Remote,
		"[2001:db8::1]:443":   poll.Remote,
		// an address a carrier or a mesh network shares out is not one of the house's own
		"100.64.0.5":           poll.Remote,
		"":                     poll.UnknownNetwork,
		"0.0.0.0":              poll.UnknownNetwork,
		"the-phone-in-the-den": poll.UnknownNetwork,
	} {
		if got := network(address); got != want {
			t.Errorf("network(%q) = %q, want %q", address, got, want)
		}
	}
}

// What a session is playing, written to be read: an episode with its show and number, a film with its year, a song with who it is by.
func TestNowPlaying(t *testing.T) {
	t.Parallel()

	one, two := 1, 2

	for want, item := range map[string]embyfin.Item{
		"Dark - S01E02 - Lies":         {Name: "Lies", Type: "Episode", SeriesName: "Dark", ParentIndexNumber: &one, IndexNumber: &two},
		"Dark - A Special":             {Name: "A Special", Type: "Episode", SeriesName: "Dark"},
		"An Episode Of Nothing":        {Name: "An Episode Of Nothing", Type: "Episode"},
		"Heat (1995)":                  {Name: "Heat", Type: "Movie", ProductionYear: 1995},
		"Alien (1979)":                 {Name: "Alien (1979)", Type: "Movie", ProductionYear: 1979},
		"A Film Of No Year":            {Name: "A Film Of No Year", Type: "Movie"},
		"Pink Floyd - Breathe":         {Name: "Breathe", Type: "Audio", Artists: []string{"Pink Floyd", "Someone Else"}, ProductionYear: 1973},
		"A Song By Nobody":             {Name: "A Song By Nobody", Type: "Audio"},
		"A Clip":                       {Name: "A Clip", Type: "Video", ProductionYear: 2020},
		"Channel 4":                    {Name: "Channel 4", Type: "TvChannel"},
		"Gorillaz - Clint Eastwood":    {Name: "Clint Eastwood", Type: "MusicVideo", Artists: []string{"Gorillaz"}},
		"Frank Herbert - Arrakis":      {Name: "Arrakis", Type: "AudioBook", Artists: []string{"Frank Herbert"}},
		"A Thing With An Empty Artist": {Name: "A Thing With An Empty Artist", Type: "Audio", Artists: []string{""}},
	} {
		if got := nowPlaying(&item); got != want {
			t.Errorf("nowPlaying(%+v) = %q, want %q", item, got, want)
		}
	}
}

// The servers count time in ten-millionths of a second, and a time below nothing is none.
func TestTicks(t *testing.T) {
	t.Parallel()

	for n, want := range map[int64]time.Duration{0: 0, 10_000_000: time.Second, 26_302_510_000: 2630251 * time.Millisecond, -5: 0} {
		if got := ticks(n); got != want {
			t.Errorf("ticks(%d) = %s, want %s", n, got, want)
		}
	}
}
