//go:build acceptance

package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/mediaserver"
	"github.com/katbyte/embyfin-metrics/lib/poll"
)

// byHand marks the plays a test lays down itself in the plugin's record, as the app they were played with: a name no real app has, so they can be
// found again and taken out.
const byHand = "laid down by hand"

// plugin puts one question, in SQL, to the test server's Playback Reporting plugin, and gives back its rows. The plugin runs whatever it is sent,
// so this is also how a test lays down plays of its own in a week of its choosing, which the program under test never does: it only asks for sums.
// A question that lands as the plugin is writing to its record fails, with an HTTP 500 or with word that the database is locked, and is put
// again, as the program puts its own.
func plugin(t *testing.T, sql string) [][]string {
	t.Helper()

	raw, err := json.Marshal(map[string]any{"CustomQueryString": sql, "ReplaceUserId": false})
	if err != nil {
		t.Fatal(err)
	}

	for try := 1; ; try++ {
		// without the test's cancellation, so a cleanup can still take out what the test laid down
		req, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodPost, address+"/user_usage_stats/submit_custom_query", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Emby-Token", token)
		req.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		var answer struct {
			Results [][]string `json:"results"`
			Message string     `json:"message"`
		}
		err = json.NewDecoder(resp.Body).Decode(&answer)
		_ = resp.Body.Close()

		if (resp.StatusCode == http.StatusInternalServerError || strings.Contains(answer.Message, "database is locked")) && try < 10 {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK || err != nil || strings.HasPrefix(answer.Message, "Error") {
			t.Fatalf("asking the plugin %q: HTTP %d, %v, %s", sql, resp.StatusCode, err, answer.Message)
		}

		return answer.Results
	}
}

// lay puts one play in the plugin's record by hand: whose it was, by the account's id, what kind of thing and what the plugin would call it, how
// it reached the device, how long it ran, and how many days ago it began.
func lay(t *testing.T, account, kind, name, method string, seconds, daysAgo int) {
	t.Helper()

	plugin(t, "INSERT INTO PlaybackActivity (DateCreated, UserId, ItemId, ItemType, ItemName, PlaybackMethod, ClientName, DeviceName, PlayDuration) VALUES (datetime('now', 'localtime', '-"+strconv.Itoa(daysAgo)+" days'), '"+account+"', '1', '"+kind+"', '"+name+"', '"+method+"', '"+byHand+"', 'no device', "+strconv.Itoa(seconds)+")")
}

// summed adds up every reading of a name whose labels include the ones given, as name, value pairs: the same account's or title's plays in each
// week, say.
func summed(text, name string, labels ...string) float64 {
	var sum float64
	for _, line := range every(text, name) {
		matches := true
		for i := 0; i+1 < len(labels); i += 2 {
			matches = matches && strings.Contains(line, labels[i]+`="`+labels[i+1]+`"`)
		}
		if !matches {
			continue
		}

		if v, err := strconv.ParseFloat(line[strings.LastIndex(line, " ")+1:], 64); err == nil {
			sum += v
		}
	}

	return sum
}

// A film played on a real server is time on the page, under the account that played it and the film's title for a program told to name them, and
// under neither for one that was not. The time goes up while the film is still playing: it is not all credited when the film ends, which is what
// lets a dashboard say when in the day people watch.
func TestTimePlayedIsOnThePageWhileItPlays(t *testing.T) {
	live(t)

	if installed, found := value(scrape(t, poll.RoundHistory), reading("embyfin_playback_reporting_installed")); !found || installed != 1 {
		t.Fatalf("the page says the server has the Playback Reporting plugin: %v (said at all: %t), want 1: .scripts/testenv.sh gives it one", installed, found)
	}

	device := signIn(t, tablet, tabletApp)
	id, title := film(t)
	playSession, _ := device.howToPlay(t, id, map[string]any{})

	played := func(text string) float64 {
		return summed(text, "embyfin_user_playback_seconds_total", "server", backend, "user", user, "type", "movie")
	}
	before := scrapeNaming(t, poll.RoundHistory)

	device.report(t, "/Sessions/Playing", id, playSession, "DirectPlay", false)
	// a stop the test already sent is answered either way
	t.Cleanup(func() { device.report(t, "/Sessions/Playing/Stopped", id, playSession, "DirectPlay", false) })

	// the device says how the play is going every two seconds, as an app does, until the page has shown the time going up twice
	var first, second float64
	var text string
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline) && second == 0; time.Sleep(2 * time.Second) {
		device.report(t, "/Sessions/Playing/Progress", id, playSession, "DirectPlay", false)

		text = scrapeNaming(t, poll.RoundHistory)
		switch now := played(text); {
		case first == 0 && now > played(before):
			first = now
		case first != 0 && now > first:
			second = now
		}
	}
	if first == 0 || second == 0 {
		t.Fatalf("with the film playing, %s's time went from %v to %v and then %v; want it to go up, and up again while still playing:\n%s", user, played(before), first, second, strings.Join(every(text, "embyfin_user_playback_seconds_total"), "\n"))
	}

	device.report(t, "/Sessions/Playing/Stopped", id, playSession, "DirectPlay", false)

	for name, labels := range map[string][]string{
		"embyfin_user_plays_total":             {"user", user, "type", "movie"},
		"embyfin_title_playback_seconds_total": {"title", title, "type", "movie"},
		"embyfin_title_plays_total":            {"title", title, "type", "movie"},
		"embyfin_playback_seconds_total":       {"type", "movie", "method", "direct_play"},
		"embyfin_plays_total":                  {"type", "movie", "method", "direct_play"},
	} {
		if now, was := summed(text, name, labels...), summed(before, name, labels...); now <= was {
			t.Errorf("%s%v went from %v to %v with the film played, want it to go up", name, labels, was, now)
		}
	}

	// the week the play is under is this one, named by its Monday as the server's own clock has it
	monday := plugin(t, "SELECT date('now', 'localtime', 'weekday 0', '-6 days')")[0][0]
	if got, found := value(text, reading("embyfin_user_playback_seconds_total", "user", user, "type", "movie", "week", monday)); !found || got <= 0 {
		t.Errorf("%s's films in the week of %s = %v (on the page: %t), want the time just played", user, monday, got, found)
	}

	// a program not told to name anyone has the same time on its page, and nobody's name nor the film's
	plain := scrape(t, poll.RoundHistory, poll.RoundLibrary)
	if got := summed(plain, "embyfin_playback_seconds_total", "type", "movie", "method", "direct_play"); got < second-played(before) {
		t.Errorf("the page that names nobody has %v seconds of films played as the file is, want at least the %v just played", got, second-played(before))
	}
	for _, private := range append([]string{user, admin, title, tablet, tabletApp}, named.FindAllString(plain, -1)...) {
		if strings.Contains(plain, private) {
			t.Errorf("the page of a program not told to name anyone has %q on it:\n%s", private, plain)
		}
	}
}

// Plays are kept under the week they began in, and the page carries this week and the last. Laid down by hand in a real server's plugin: a play
// from last week is under last week, one from three weeks ago is not on the page, an account that is gone is "unknown", an episode is under its
// show, and a stream the server made with nothing re-encoded is a direct stream. A play that began last week and is still running goes on adding
// to last week, and a program started afresh reads the same as the one before it.
func TestPlaysAreKeptUnderTheWeekTheyBeganIn(t *testing.T) {
	live(t)

	sweep := func() { plugin(t, "DELETE FROM PlaybackActivity WHERE ClientName = '"+byHand+"'") }
	sweep()
	t.Cleanup(sweep)

	weeks := plugin(t, "SELECT date('now', 'localtime', 'weekday 0', '-6 days'), date('now', 'localtime', 'weekday 0', '-13 days')")
	thisWeek, lastWeek := weeks[0][0], weeks[0][1]

	someone := signIn(t, speaker, speakerApp)
	const (
		gone = "0123456789abcdef0123456789abcdef"
		show = "A Show Nobody Made"
		film = "A Film Nobody Made"
	)

	lay(t, someone.userID, "Episode", show+" - s01e01 - One", "DirectPlay", 1000, 7)
	lay(t, someone.userID, "Episode", show+" - s01e02 - Two", "Transcode (v:h264 a:aac)", 200, 7)
	lay(t, gone, "Movie", film, "Transcode (v:direct a:direct)", 500, 7)
	lay(t, someone.userID, "Movie", film, "DirectPlay", 9999, 21)
	lay(t, gone, "Episode", show+" - s01e03 - Three", "DirectStream", 700, 0)

	text := scrapeNaming(t, poll.RoundHistory)
	want := map[string]float64{
		reading("embyfin_user_playback_seconds_total", "user", user, "type", "episode", "week", lastWeek):       1200,
		reading("embyfin_user_plays_total", "user", user, "type", "episode", "week", lastWeek):                  2,
		reading("embyfin_user_playback_seconds_total", "user", "unknown", "type", "movie", "week", lastWeek):    500,
		reading("embyfin_user_playback_seconds_total", "user", "unknown", "type", "episode", "week", thisWeek):  700,
		reading("embyfin_title_playback_seconds_total", "title", show, "type", "episode", "week", lastWeek):     1200,
		reading("embyfin_title_playback_seconds_total", "title", show, "type", "episode", "week", thisWeek):     700,
		reading("embyfin_title_plays_total", "title", show, "type", "episode", "week", lastWeek):                2,
		reading("embyfin_title_playback_seconds_total", "title", film, "type", "movie", "week", lastWeek):       500,
		reading("embyfin_playback_seconds_total", "type", "episode", "method", "direct_play", "week", lastWeek): 1000,
		reading("embyfin_playback_seconds_total", "type", "episode", "method", "transcode", "week", lastWeek):   200,
		reading("embyfin_playback_seconds_total", "type", "movie", "method", "direct_stream", "week", lastWeek): 500,
		reading("embyfin_plays_total", "type", "movie", "method", "direct_stream", "week", lastWeek):            1,
	}
	expect(t, text, want)

	// three weeks ago is off the page, and nothing on it is under any week but these two
	if strings.Contains(text, "9999") {
		t.Errorf("a play from three weeks ago is on the page:\n%s", strings.Join(every(text, "embyfin_user_playback_seconds_total"), "\n"))
	}
	for _, name := range []string{"embyfin_playback_seconds_total", "embyfin_plays_total", "embyfin_user_playback_seconds_total", "embyfin_title_playback_seconds_total"} {
		for _, line := range every(text, name) {
			if !strings.Contains(line, `week="`+thisWeek+`"`) && !strings.Contains(line, `week="`+lastWeek+`"`) {
				t.Errorf("a reading is under a week that is neither this one nor the last: %s", line)
			}
		}
	}

	// the play that began last week runs on for ten more minutes: it is last week's reading that goes up
	plugin(t, "UPDATE PlaybackActivity SET PlayDuration = 1600 WHERE ClientName = '"+byHand+"' AND ItemName = '"+show+" - s01e01 - One'")

	// read by a program started afresh, which knows nothing of the page before
	afresh, err := mediaserver.Connect(backend, address, token, mediaserver.Detail{Users: true, Titles: true, Devices: true})
	if err != nil {
		t.Fatal(err)
	}

	want[reading("embyfin_user_playback_seconds_total", "user", user, "type", "episode", "week", lastWeek)] = 1800
	want[reading("embyfin_title_playback_seconds_total", "title", show, "type", "episode", "week", lastWeek)] = 1800
	want[reading("embyfin_playback_seconds_total", "type", "episode", "method", "direct_play", "week", lastWeek)] = 1600
	expect(t, page(t, afresh, poll.RoundHistory), want)

	// told to name nobody, the same plays are on the page by kind and method alone
	plain := scrape(t, poll.RoundHistory)
	expect(t, plain, map[string]float64{
		reading("embyfin_playback_seconds_total", "type", "episode", "method", "direct_play", "week", lastWeek): 1600,
		reading("embyfin_playback_seconds_total", "type", "movie", "method", "direct_stream", "week", lastWeek): 500,
	})
	for _, private := range append([]string{user, show, film}, named.FindAllString(plain, -1)...) {
		if strings.Contains(plain, private) {
			t.Errorf("the page of a program not told to name anyone has %q on it:\n%s", private, plain)
		}
	}
}
