//go:build acceptance

package acceptance_test

import (
	"strings"
	"testing"

	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/embyfin-metrics/lib/poll"
)

// What the test devices call themselves and their apps: words on no reading, so finding one on the page means it was put there.
const (
	phone         = "the-phone-in-the-hall"
	phoneApp      = "Hallway Player"
	television    = "the-television-upstairs"
	televisionApp = "Upstairs Player"
)

// film is the id of the first film of the film library, and its title.
func film(t *testing.T) (id, title string) {
	t.Helper()

	folder, err := client.FindLibrary(t.Context(), filmLibrary)
	if err != nil {
		t.Fatal(err)
	}

	found, _, err := client.Search(t.Context(), embyfin.SearchOptions{ParentID: folder.ItemID, IncludeItemTypes: "Movie", SortBy: "SortName"})
	if err != nil || len(found) == 0 {
		t.Fatalf("listing the films: %d of them, %v", len(found), err)
	}

	return found[0].ID, found[0].Name
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
		reading("embyfin_transcode_bytes_per_second"):              0,
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

	playSession, stream := device.howToPlay(t, id, map[string]any{"UserId": device.userID, "MaxStreamingBitrate": 300000, "EnableDirectPlay": false, "EnableDirectStream": false, "EnableTranscoding": true, "DeviceProfile": cannotPlay})
	if stream == "" {
		t.Fatal("the server offered no stream of its own making to a device that can play nothing as it is")
	}

	// the list of renditions names the list of pieces, which names the first piece, and asking for that is what starts the server on the work
	device.fetch(t, device.fetch(t, device.fetch(t, stream)))

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
	if rate, _ := value(text, reading("embyfin_transcode_bytes_per_second")); rate <= 0 || rate > 100000 {
		t.Errorf("the stream comes to %v bytes a second, want more than nothing and no more than 100000", rate)
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

	device.report(t, "/Sessions/Playing/Stopped", id, playSession, "Transcode", false)
	if text, ok := eventually(t, func(text string) bool { return sessions(text, "playing") == 0 && streams(text, "transcode") == 0 }, poll.RoundSessions); !ok {
		t.Errorf("stopped, the page still counts %v playing and %v transcodes", sessions(text, "playing"), streams(text, "transcode"))
	}

	stopped := scrape(t, poll.RoundSessions)
	expect(t, stopped, map[string]float64{
		reading("embyfin_video_transcodes", "encoder", "software"): 0,
		reading("embyfin_streams_by_type", "type", "movie"):        0,
		reading("embyfin_transcode_bytes_per_second"):              0,
	})
	if reasons := every(stopped, "embyfin_transcode_reasons"); len(reasons) != 0 {
		t.Errorf("stopped, the page still gives reasons for a stream: %v", reasons)
	}
}
