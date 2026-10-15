//go:build acceptance

package acceptance_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
)

// What the test devices call themselves and their apps: words on no reading, so finding one on the page means it was put there.
const (
	phone         = "the-phone-in-the-hall"
	phoneApp      = "Hallway Player"
	television    = "the-television-upstairs"
	televisionApp = "Upstairs Player"
	tablet        = "the-tablet-in-the-kitchen"
	tabletApp     = "Kitchen Player"
	speaker       = "the-speaker-on-the-shelf"
	speakerApp    = "Shelf Player"
)

// film is the id of the first film of the film library, and its title.
func film(t *testing.T) (id, title string) {
	t.Helper()

	return first(t, filmLibrary, "Movie")
}

// sessions is how many sessions the page counts in a state, and streams how many it counts playing by a method.
func sessions(text, state string) float64 {
	n, _ := value(text, reading("embyfin_sessions", "state", state))

	return n
}

func streams(text, method string) float64 {
	n, _ := value(text, reading("embyfin_streams", "method", method))

	return n
}

// A device that signs in is a session with nothing playing; playing a film as the file is makes it a session playing and a direct play; pausing
// makes it paused and still a stream; stopping makes it idle again. Through all of it the page names nobody and nothing.
func TestASessionPlayingPausedAndStopped(t *testing.T) {
	live(t)

	before := scrape(t, poll.RoundSessions)
	if sessions(before, "playing") != 0 || sessions(before, "paused") != 0 {
		t.Fatalf("something is playing before the test starts:\n%s", before)
	}

	device := signIn(t, phone, phoneApp)
	if text, ok := eventually(t, func(text string) bool { return sessions(text, "idle") == sessions(before, "idle")+1 }, poll.RoundSessions); !ok {
		t.Fatalf("signing in made %v idle sessions of %v, want one more", sessions(text, "idle"), sessions(before, "idle"))
	}

	id, title := film(t)
	playSession, _ := device.howToPlay(t, id, map[string]any{})

	report := func(at string, paused bool) {
		t.Helper()

		device.report(t, at, id, playSession, "DirectPlay", paused)
	}

	report("/Sessions/Playing", false)
	// a stop the test already sent is answered either way
	t.Cleanup(func() { report("/Sessions/Playing/Stopped", false) })

	playing, ok := eventually(t, func(text string) bool { return sessions(text, "playing") == 1 }, poll.RoundSessions)
	if !ok {
		t.Fatalf("the film playing never showed as a session playing:\n%s", playing)
	}
	expect(t, playing, map[string]float64{
		reading("embyfin_sessions", "state", "playing"):            1,
		reading("embyfin_sessions", "state", "paused"):             0,
		reading("embyfin_sessions", "state", "idle"):               sessions(before, "idle"),
		reading("embyfin_streams", "method", "direct_play"):        1,
		reading("embyfin_streams", "method", "direct_stream"):      0,
		reading("embyfin_streams", "method", "transcode"):          0,
		reading("embyfin_streams", "method", "unknown"):            0,
		reading("embyfin_video_transcodes", "encoder", "hardware"): 0,
		reading("embyfin_video_transcodes", "encoder", "software"): 0,
		reading("embyfin_streams_by_type", "type", "movie"):        1,
		reading("embyfin_streams_by_type", "type", "episode"):      0,
		reading("embyfin_streams_by_type", "type", "track"):        0,
		reading("embyfin_streams_by_type", "type", "other"):        0,
		reading("embyfin_transcode_bits_per_second"):               0,
	})

	// the file goes out as it is, so the server has no reason for making a stream of it
	if reasons := every(playing, "embyfin_transcode_reasons"); len(reasons) != 0 {
		t.Errorf("the page gives reasons for a stream the server is not making: %v", reasons)
	}

	// who is playing, on what, and what: all of it in the server's answer, none of it on the page
	for _, private := range []string{user, admin, phone, phoneApp, title} {
		if strings.Contains(playing, private) {
			t.Errorf("the page names %q:\n%s", private, playing)
		}
	}

	report("/Sessions/Playing/Progress", true)
	if text, ok := eventually(t, func(text string) bool { return sessions(text, "paused") == 1 }, poll.RoundSessions); !ok || sessions(text, "playing") != 0 || streams(text, "direct_play") != 1 {
		t.Errorf("paused, the page counts %v playing, %v paused and %v direct plays, want 0, 1 and 1", sessions(text, "playing"), sessions(text, "paused"), streams(text, "direct_play"))
	}

	report("/Sessions/Playing/Stopped", false)
	if text, ok := eventually(t, func(text string) bool { return sessions(text, "playing")+sessions(text, "paused") == 0 }, poll.RoundSessions); !ok || streams(text, "direct_play") != 0 {
		t.Errorf("stopped, the page counts %v playing, %v paused and %v direct plays, want none of any", sessions(text, "playing"), sessions(text, "paused"), streams(text, "direct_play"))
	}

	// the account was seen today and the device is one the server knows of
	after := scrape(t, poll.RoundLibrary)
	if active, _ := value(after, reading("embyfin_users_active", "window", "1d")); active < 1 {
		t.Errorf("accounts seen within a day = %v, want the one that just played", active)
	}
	if known, _ := value(after, reading("embyfin_devices")); known < 1 {
		t.Errorf("devices the server knows of = %v, want the one that just played", known)
	}
	for _, window := range []string{"1d", "7d", "30d"} {
		if seen, _ := value(after, reading("embyfin_devices_active", "window", window)); seen < 1 {
			t.Errorf("devices seen within %s = %v, want the one that just played", window, seen)
		}
	}
}

// A device that cannot play a film as the file is has the server make a stream for it, re-encoding the picture and the sound. That is a transcode,
// and one whose picture is re-encoded without a GPU: the servers under test have none. The page says how much the stream comes to and why the server
// is making it, in the server's own reasons. Stopped, it is none of that.
func TestASessionTheServerTranscodesFor(t *testing.T) {
	live(t)

	device := signIn(t, television, televisionApp)
	id, _ := film(t)
	playSession := device.streamFor(t, id, lowBitrate)

	device.report(t, "/Sessions/Playing", id, playSession, "Transcode", false)
	t.Cleanup(func() { device.report(t, "/Sessions/Playing/Stopped", id, playSession, "Transcode", false) })

	text, ok := eventually(t, func(text string) bool {
		software, _ := value(text, reading("embyfin_video_transcodes", "encoder", "software"))

		return streams(text, "transcode") == 1 && software == 1
	}, poll.RoundSessions)
	if !ok {
		t.Fatalf("the stream the server is making never showed as a transcode:\n%s", text)
	}
	expect(t, text, map[string]float64{
		reading("embyfin_sessions", "state", "playing"):            1,
		reading("embyfin_streams", "method", "transcode"):          1,
		reading("embyfin_streams", "method", "direct_play"):        0,
		reading("embyfin_streams", "method", "direct_stream"):      0,
		reading("embyfin_streams", "method", "unknown"):            0,
		reading("embyfin_video_transcodes", "encoder", "software"): 1,
		reading("embyfin_video_transcodes", "encoder", "hardware"): 0,
		reading("embyfin_streams_by_type", "type", "movie"):        1,
	})

	// the device asked for no more than 300 kilobits a second, and the servers come in a little over what they are asked for
	if rate, _ := value(text, reading("embyfin_transcode_bits_per_second")); rate <= 0 || rate > 800000 {
		t.Errorf("the stream comes to %v bits a second, want more than nothing and no more than 800000", rate)
	}

	// which reasons is the server's to say; that it gives one, as words with nothing run together, is what is checked
	reasons := every(text, "embyfin_transcode_reasons")
	if len(reasons) == 0 {
		t.Errorf("the page gives no reason for the stream the server is making:\n%s", text)
	}
	for _, line := range reasons {
		if !strings.HasSuffix(line, " 1") || line != strings.ToLower(line) {
			t.Errorf("a reason reads %q, want it in small letters and counted once", line)
		}
	}

	// told to name devices, the page says whose television and which app each of those reasons belongs to, and what leaves the server for
	// the session is the stream it is making
	told := scrapeNaming(t, poll.RoundSessions)
	theirs := every(told, "embyfin_client_transcode_reasons")
	if len(theirs) != len(reasons) {
		t.Errorf("the page gives %d reasons by device and %d in all, want the same: %v", len(theirs), len(reasons), theirs)
	}
	for _, line := range theirs {
		if label(line, "device") != television || label(line, "client") != televisionApp || !strings.HasSuffix(line, " 1") {
			t.Errorf("a reason by device reads %q, want it under %s and %s, counted once", line, television, televisionApp)
		}
	}
	if lines := every(told, "embyfin_session_info"); len(lines) != 1 || label(lines[0], "method") != "transcode" || label(lines[0], "reasons") == "" {
		t.Errorf("the session one by one reads %v, want one transcode with its reasons", lines)
	} else if rate, _ := value(told, reading("embyfin_session_bits_per_second", "session", label(lines[0], "session"))); rate <= 0 || rate > 800000 {
		t.Errorf("the session's stream comes to %v bits a second, want more than nothing and no more than 800000", rate)
	}
	if len(every(text, "embyfin_client_transcode_reasons")) != 0 {
		t.Errorf("a page not told to name devices gives reasons by device:\n%s", text)
	}

	device.report(t, "/Sessions/Playing/Stopped", id, playSession, "Transcode", false)
	if text, ok := eventually(t, func(text string) bool { return sessions(text, "playing") == 0 && streams(text, "transcode") == 0 }, poll.RoundSessions); !ok {
		t.Errorf("stopped, the page still counts %v playing and %v transcodes", sessions(text, "playing"), streams(text, "transcode"))
	}

	stopped := scrape(t, poll.RoundSessions)
	expect(t, stopped, map[string]float64{
		reading("embyfin_video_transcodes", "encoder", "software"): 0,
		reading("embyfin_streams_by_type", "type", "movie"):        0,
		reading("embyfin_transcode_bits_per_second"):               0,
	})
	if reasons := every(stopped, "embyfin_transcode_reasons"); len(reasons) != 0 {
		t.Errorf("stopped, the page still gives reasons for a stream: %v", reasons)
	}
}

// A device that can play what the film is made of, only not in the file it comes in, has the server repack it: the picture and the sound are passed
// on as they are. That is a direct stream and not a transcode, and nothing is re-encoded, though the device itself calls it a transcode and the
// server still gives its reasons and what the stream comes to.
func TestASessionTheServerRepacksFor(t *testing.T) {
	live(t)

	device := signIn(t, tablet, tabletApp)
	id, _ := film(t)
	playSession := device.streamFor(t, id, highBitrate)

	device.report(t, "/Sessions/Playing", id, playSession, "Transcode", false)
	t.Cleanup(func() { device.report(t, "/Sessions/Playing/Stopped", id, playSession, "Transcode", false) })

	text, ok := eventually(t, func(text string) bool { return streams(text, "direct_stream") == 1 }, poll.RoundSessions)
	if !ok {
		t.Fatalf("the stream the server is repacking never showed as a direct stream:\n%s", text)
	}
	expect(t, text, map[string]float64{
		reading("embyfin_sessions", "state", "playing"):            1,
		reading("embyfin_streams", "method", "direct_stream"):      1,
		reading("embyfin_streams", "method", "transcode"):          0,
		reading("embyfin_streams", "method", "direct_play"):        0,
		reading("embyfin_streams", "method", "unknown"):            0,
		reading("embyfin_video_transcodes", "encoder", "software"): 0,
		reading("embyfin_video_transcodes", "encoder", "hardware"): 0,
		reading("embyfin_streams_by_type", "type", "movie"):        1,
	})

	if rate, _ := value(text, reading("embyfin_transcode_bits_per_second")); rate <= 0 {
		t.Errorf("the stream comes to %v bits a second, want more than nothing", rate)
	}
	if reasons := every(text, "embyfin_transcode_reasons"); len(reasons) == 0 {
		t.Errorf("the page gives no reason for the stream the server is making:\n%s", text)
	}

	device.report(t, "/Sessions/Playing/Stopped", id, playSession, "Transcode", false)
	if text, ok := eventually(t, func(text string) bool { return sessions(text, "playing") == 0 && streams(text, "direct_stream") == 0 }, poll.RoundSessions); !ok {
		t.Errorf("stopped, the page still counts %v playing and %v direct streams", sessions(text, "playing"), streams(text, "direct_stream"))
	}
}

// What is playing is counted by its kind, in the words the item counts use, whichever library it is from. An audiobook's chapter is a track on
// Emby, which keeps an audiobook as an album, and an audiobook on Jellyfin.
func TestWhatIsPlayingIsCountedByKind(t *testing.T) {
	live(t)

	listened := struct{ server, page string }{"Audio", "track"}
	if backend == jellyfin {
		listened = struct{ server, page string }{"AudioBook", "audiobook"}
	}

	device := signIn(t, speaker, speakerApp)
	for _, c := range []struct{ library, kind, want string }{
		{filmLibrary, "Movie", "movie"},
		{showLibrary, "Episode", "episode"},
		{musicLibrary, "Audio", "track"},
		{clipLibrary, "MusicVideo", "music_video"},
		{homeLibrary, "Video", "video"},
		{audiobookLibrary, listened.server, listened.page},
		{mixedLibrary, "Episode", "episode"},
	} {
		id, _ := first(t, c.library, c.kind)
		playSession, _ := device.howToPlay(t, id, map[string]any{})

		device.report(t, "/Sessions/Playing", id, playSession, "DirectPlay", false)

		text, ok := eventually(t, func(text string) bool { return sessions(text, "playing") == 1 }, poll.RoundSessions)
		if !ok {
			t.Fatalf("the %s from %s playing never showed as a session playing:\n%s", c.kind, c.library, text)
		}

		want := map[string]float64{}
		for _, kind := range poll.Kinds {
			want[reading("embyfin_streams_by_type", "type", string(kind))] = 0
		}
		want[reading("embyfin_streams_by_type", "type", c.want)] = 1
		expect(t, text, want)

		device.report(t, "/Sessions/Playing/Stopped", id, playSession, "DirectPlay", false)
		if text, ok := eventually(t, func(text string) bool { return sessions(text, "playing") == 0 }, poll.RoundSessions); !ok {
			t.Fatalf("the %s from %s stopped and the page still counts a session playing:\n%s", c.kind, c.library, text)
		}
	}
}

// label is what one label of a reading's line holds.
func label(line, name string) string {
	_, rest, found := strings.Cut(line, name+`="`)
	if !found {
		return ""
	}
	held, _, _ := strings.Cut(rest, `"`)

	return held
}

// Who is streaming now. A film playing on a real server is a line of its own on the page of a program told to name everything: whose session it
// is, the film in a form to be read, the device and its app, that it is the file as it is, playing, and on the server's own network, with what
// the file comes to, how far through it is and how long it is. Paused it says so, and stopped it is gone. The page of a program told to name
// nobody has the same line with none of the names.
func TestWhoIsStreamingIsOnThePage(t *testing.T) {
	live(t)

	device := signIn(t, speaker, speakerApp)
	id, title := film(t)
	playSession, _ := device.howToPlay(t, id, map[string]any{})

	// a second in, and then a second and a half
	say := func(at string, ticks int, paused bool) {
		t.Helper()

		if status, raw := device.send(t, http.MethodPost, at, map[string]any{"ItemId": id, "PlaySessionId": playSession, "PositionTicks": ticks, "IsPaused": paused, "CanSeek": true, "PlayMethod": "DirectPlay"}); status/100 != 2 {
			t.Fatalf("%s: HTTP %d: %.200s", at, status, raw)
		}
	}
	say("/Sessions/Playing", 10_000_000, false)
	// a stop the test already sent is answered either way
	t.Cleanup(func() { say("/Sessions/Playing/Stopped", 15_000_000, false) })

	mine := func(text string) []string {
		var lines []string
		for _, line := range every(text, "embyfin_session_info") {
			if label(line, "device") == speaker {
				lines = append(lines, line)
			}
		}

		return lines
	}

	var text string
	deadline := time.Now().Add(patience)
	for text = scrapeNaming(t, poll.RoundSessions); len(mine(text)) != 1 && time.Now().Before(deadline); text = scrapeNaming(t, poll.RoundSessions) {
		time.Sleep(500 * time.Millisecond)
	}
	if len(mine(text)) != 1 {
		t.Fatalf("the film playing never showed as a session of its own:\n%s", strings.Join(every(text, "embyfin_session_info"), "\n"))
	}

	line := mine(text)[0]
	session := label(line, "session")
	for name, want := range map[string]string{"user": user, "device": speaker, "client": speakerApp, "type": "movie", "method": "direct_play", "state": "playing", "network": "home", "reasons": ""} {
		if got := label(line, name); got != want {
			t.Errorf("the session's %s = %q, want %q: %s", name, got, want, line)
		}
	}
	// each server names the film in its own way, and either way the year it was made is there to tell it from another of the name
	if got := label(line, "title"); !strings.HasPrefix(got, title) || !strings.HasSuffix(got, ")") {
		t.Errorf("the session's title = %q, want %q with its year", got, title)
	}
	if session == "" || !strings.HasSuffix(line, " 1") {
		t.Errorf("the session reads %q, want an id and the value 1", line)
	}

	// the fixture's film is two seconds of picture and sound at under a megabit a second
	if rate, found := value(text, reading("embyfin_session_bits_per_second", "session", session)); !found || rate < 100_000 || rate > 2_000_000 {
		t.Errorf("what the file comes to = %v bits a second (on the page: %t), want what a small clip does", rate, found)
	}
	if at, found := value(text, reading("embyfin_session_position_seconds", "session", session)); !found || at != 1 {
		t.Errorf("how far through = %v seconds (on the page: %t), want the 1 the device said", at, found)
	}
	if long, found := value(text, reading("embyfin_session_runtime_seconds", "session", session)); !found || long < 1.5 || long > 3 {
		t.Errorf("how long the film is = %v seconds (on the page: %t), want about 2", long, found)
	}
	expect(t, text, map[string]float64{
		reading("embyfin_streams_by_network", "network", "home"):    1,
		reading("embyfin_streams_by_network", "network", "remote"):  0,
		reading("embyfin_streams_by_network", "network", "unknown"): 0,
	})
	if home, _ := value(text, reading("embyfin_streams_bits_per_second", "network", "home")); home < 100_000 {
		t.Errorf("what leaves the server for devices at home = %v bits a second, want the film's", home)
	}

	// a program told to name nobody has the session and where it is, and no name, title, device or address
	plain := scrape(t, poll.RoundSessions)
	if lines := every(plain, "embyfin_session_info"); len(lines) != 1 || label(lines[0], "session") != session || label(lines[0], "network") != "home" || label(lines[0], "method") != "direct_play" {
		t.Errorf("the page that names nobody reads %v, want the one session, at home, as the file is", lines)
	}
	for _, private := range append([]string{user, admin, title, speaker, speakerApp, "172.17.", "127.0.0.1"}, named.FindAllString(plain, -1)...) {
		if strings.Contains(plain, private) {
			t.Errorf("the page of a program not told to name anyone has %q on it:\n%s", private, plain)
		}
	}
	if strings.Contains(text, "172.17.") || strings.Contains(text, "127.0.0.1") {
		t.Errorf("an address is on the page of a program told to name everything:\n%s", text)
	}

	say("/Sessions/Playing/Progress", 15_000_000, true)
	deadline = time.Now().Add(patience)
	for text = scrapeNaming(t, poll.RoundSessions); time.Now().Before(deadline); text = scrapeNaming(t, poll.RoundSessions) {
		if lines := mine(text); len(lines) == 1 && label(lines[0], "state") == "paused" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lines := mine(text); len(lines) != 1 || label(lines[0], "state") != "paused" || label(lines[0], "session") != session {
		t.Errorf("paused, the session reads %v, want the same one, paused", lines)
	}
	if at, _ := value(text, reading("embyfin_session_position_seconds", "session", session)); at != 1.5 {
		t.Errorf("paused a second and a half in, the page says %v seconds", at)
	}

	say("/Sessions/Playing/Stopped", 15_000_000, false)
	deadline = time.Now().Add(patience)
	for text = scrapeNaming(t, poll.RoundSessions); len(mine(text)) != 0 && time.Now().Before(deadline); text = scrapeNaming(t, poll.RoundSessions) {
		time.Sleep(500 * time.Millisecond)
	}
	if len(mine(text)) != 0 || len(every(text, "embyfin_session_position_seconds")) != 0 || len(every(text, "embyfin_session_bits_per_second")) != 0 {
		t.Errorf("stopped, the session is still on the page:\n%s", strings.Join(append(every(text, "embyfin_session_info"), every(text, "embyfin_session_position_seconds")...), "\n"))
	}
}
