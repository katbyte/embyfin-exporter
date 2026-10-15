package mediaserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

// everything is a server asked to name all it can: accounts, titles and devices.
var everything = Detail{Users: true, Titles: true, Devices: true}

// fakeReports answers in the plugin's place with the rows it is set to, and remembers the question and how often it was put.
type fakeReports struct {
	rows    [][]string
	missing bool
	err     error
	// locked is how many times it says its record is being written to before it answers
	locked int

	sql   string
	names bool
	asked int
}

func (f *fakeReports) Query(_ context.Context, sql string, names bool) (rows [][]string, installed bool, err error) {
	f.sql, f.names = sql, names
	f.asked++

	if f.asked <= f.locked {
		return nil, false, errBusy
	}

	return f.rows, !f.missing, f.err
}

// The weeks the plays below began in, as the plugin's database names them: each by its Monday.
const (
	thisWeek   = "2026-10-12"
	weekBefore = "2026-10-05"
)

// onJellyfin and onEmby are a server of each kind whose plugin is reports, asked to name what detail says. The two plugins are asked differently:
// Emby's also for why a stream was made, and for which device when devices are to be named.
func onJellyfin(reports reporter, detail Detail) *Server {
	return &Server{client: &fake{backend: embyfin.Jellyfin}, reports: reports, detail: detail}
}

func onEmby(reports reporter, detail Detail) *Server {
	return &Server{client: &fake{backend: embyfin.Emby}, reports: reports, detail: detail}
}

// The question names an account, a title or a device only when the settings ask for one, so what was not asked for never leaves the server. It
// asks for the plays of this week and the last, each under the Monday of the week it began in, and of each whether it ended within a minute. Only
// Emby's plugin is asked why a stream was made and for which device: Jellyfin's keeps neither.
func TestHistoryQuery(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		asking  asking
		columns string
	}{
		"jellyfin, nobody named": {asking{}, "ItemType, PlaybackMethod, " + shortOnJellyfin},
		"jellyfin, accounts":     {asking{detail: Detail{Users: true}}, "ItemType, PlaybackMethod, UserId, " + shortOnJellyfin},
		"jellyfin, titles":       {asking{detail: Detail{Titles: true}}, "ItemType, PlaybackMethod, ItemName, " + shortOnJellyfin},
		"jellyfin, devices":      {asking{detail: Detail{Devices: true}}, "ItemType, PlaybackMethod, " + shortOnJellyfin},
		"jellyfin, everything":   {asking{detail: everything}, "ItemType, PlaybackMethod, UserId, ItemName, " + shortOnJellyfin},
		"emby, nobody named":     {asking{emby: true}, "ItemType, PlaybackMethod, TranscodeReasons, " + shortOnEmby},
		"emby, accounts":         {asking{emby: true, detail: Detail{Users: true}}, "ItemType, PlaybackMethod, UserId, TranscodeReasons, " + shortOnEmby},
		"emby, devices":          {asking{emby: true, detail: Detail{Devices: true}}, "ItemType, PlaybackMethod, TranscodeReasons, DeviceName, ClientName, " + shortOnEmby},
		"emby, everything":       {asking{emby: true, detail: everything}, "ItemType, PlaybackMethod, UserId, ItemName, TranscodeReasons, DeviceName, ClientName, " + shortOnEmby},
	} {
		columns := "date(DateCreated, 'weekday 0', '-6 days'), " + c.columns
		want := "SELECT " + columns + ", COUNT(*), SUM(PlayDuration) FROM PlaybackActivity WHERE DateCreated >= date('now', 'localtime', 'weekday 0', '-13 days') GROUP BY " + columns
		if got := c.asking.query(); got != want {
			t.Errorf("%s: the question is\n%s\nwant\n%s", name, got, want)
		}
	}

	// a play is short when it ran under a minute and has not been added to for five; only Emby's plugin keeps how long one was paused
	if want := "(PlayDuration < 60 AND (julianday('now', 'localtime') - julianday(DateCreated)) * 86400 > PlayDuration + IFNULL(PauseDuration, 0) + 300)"; shortOnEmby != want || shortOnJellyfin != strings.Replace(want, " + IFNULL(PauseDuration, 0)", "", 1) {
		t.Errorf("a short play is asked for as\n%s\nand\n%s", shortOnEmby, shortOnJellyfin)
	}
}

// What was played, from rows as a real plugin gives them: every value as text, a method with what was done to each stream after it, an episode
// named with its show first, the id of an account that has since been deleted left where its name would be, and a 1 for the plays that ended
// within a minute. The same show played in two weeks is two sums, one under each. Jellyfin's plugin says nothing of why a stream was made.
func TestHistory(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{rows: [][]string{
		{thisWeek, "Episode", "DirectPlay", "ada", "Dark - s01e01 - Secrets", "0", "2", "3000"},
		{thisWeek, "Episode", "DirectPlay", "ada", "Dark - s01e01 - Secrets", "1", "2", "30"},
		{thisWeek, "Episode", "DirectPlay", "ada", "Dark - s01e02 - Lies", "0", "1", "2700"},
		{thisWeek, "Episode", "Transcode (v:h264 a:direct)", "grace", "Dark - s01e01 - Secrets", "0", "1", "1500"},
		{thisWeek, "Episode", "Transcode (v:direct a:direct)", "grace", "Severance - s02e10 - Cold Harbor", "0", "1", "4000"},
		{thisWeek, "Movie", "DirectStream", "ada", "Heat", "0", "1", "9000"},
		{thisWeek, "Movie", "Transcode", "0123456789abcdef0123456789abcdef", "Heat", "0", "1", "600"},
		{thisWeek, "Audio", "DirectPlay", "grace", "Not Known - A Song (Not Known)", "0", "5", "900"},
		{thisWeek, "Trailer", "DirectStream", "grace", "Heat - Trailer", "0", "1", "120"},
		{thisWeek, "Episode", "DirectPlay", "", "A Show With No Numbers", "0", "1", ""},
		{weekBefore, "Episode", "DirectPlay", "ada", "Dark - s01e01 - Secrets", "0", "1", "50"},
	}}

	got, err := onJellyfin(reports, everything).History(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := poll.History{
		Installed: true,
		Plays: []poll.Played{
			{Week: weekBefore, Kind: poll.Episode, Method: poll.DirectPlay, Plays: 1, Seconds: 50},
			{Week: thisWeek, Kind: poll.Episode, Method: poll.DirectPlay, Plays: 6, Seconds: 5730, Short: 2},
			{Week: thisWeek, Kind: poll.Episode, Method: poll.DirectStream, Plays: 1, Seconds: 4000},
			{Week: thisWeek, Kind: poll.Episode, Method: poll.Transcode, Plays: 1, Seconds: 1500},
			{Week: thisWeek, Kind: poll.Movie, Method: poll.DirectStream, Plays: 1, Seconds: 9000},
			{Week: thisWeek, Kind: poll.Movie, Method: poll.Transcode, Plays: 1, Seconds: 600},
			{Week: thisWeek, Kind: poll.OtherKind, Method: poll.DirectStream, Plays: 1, Seconds: 120},
			{Week: thisWeek, Kind: poll.Track, Method: poll.DirectPlay, Plays: 5, Seconds: 900},
		},
		ByUser: []poll.Played{
			{Week: weekBefore, User: "ada", Kind: poll.Episode, Plays: 1, Seconds: 50},
			{Week: thisWeek, User: "ada", Kind: poll.Episode, Plays: 5, Seconds: 5730, Short: 2},
			{Week: thisWeek, User: "ada", Kind: poll.Movie, Plays: 1, Seconds: 9000},
			{Week: thisWeek, User: "grace", Kind: poll.Episode, Plays: 2, Seconds: 5500},
			{Week: thisWeek, User: "grace", Kind: poll.OtherKind, Plays: 1, Seconds: 120},
			{Week: thisWeek, User: "grace", Kind: poll.Track, Plays: 5, Seconds: 900},
			{Week: thisWeek, User: "unknown", Kind: poll.Episode, Plays: 1},
			{Week: thisWeek, User: "unknown", Kind: poll.Movie, Plays: 1, Seconds: 600},
		},
		ByTitle: []poll.Played{
			{Week: weekBefore, Title: "Dark", Kind: poll.Episode, Plays: 1, Seconds: 50},
			{Week: thisWeek, Title: "A Show With No Numbers", Kind: poll.Episode, Plays: 1},
			{Week: thisWeek, Title: "Dark", Kind: poll.Episode, Plays: 6, Seconds: 7230, Short: 2},
			{Week: thisWeek, Title: "Heat", Kind: poll.Movie, Plays: 2, Seconds: 9600},
			{Week: thisWeek, Title: "Severance", Kind: poll.Episode, Plays: 1, Seconds: 4000},
		},
		ByReason: []poll.Played{},
		ByClient: []poll.Played{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("history =\n%+v\nwant\n%+v", got, want)
	}
	if !reports.names || !strings.Contains(reports.sql, "UserId") || !strings.Contains(reports.sql, "ItemName") {
		t.Errorf("asked to name accounts and titles, the plugin was asked for names: %t, with\n%s", reports.names, reports.sql)
	}
}

// Emby's plugin also says why the server made a stream of a play, as the server's own words one after another, and for which device and app. A
// play with two reasons is under both, one said twice is under it once, and a file sent as it is has no reason and is under none. The device and
// its app are handed on only when devices are to be named.
func TestHistoryOnEmbySaysWhyStreamsWereMade(t *testing.T) {
	t.Parallel()

	const television, phone = "the television in the den", "the phone in the hall"
	reports := &fakeReports{rows: [][]string{
		{thisWeek, "Movie", "Transcode (v:h264 a:aac)", "ContainerBitrateExceedsLimit", television, "Den Player", "0", "2", "7000"},
		{thisWeek, "Episode", "Transcode (v:direct a:ac3)", "DirectPlayError ContainerNotSupported DirectPlayError", phone, "Pocket Player", "0", "1", "1500"},
		{thisWeek, "Episode", "Transcode (v:direct a:ac3)", "ContainerNotSupported DirectPlayError", phone, "Pocket Player", "1", "1", "12"},
		{thisWeek, "Movie", "DirectPlay", "", television, "Den Player", "0", "1", "3000"},
		{weekBefore, "Movie", "Transcode", "ContainerBitrateExceedsLimit", television, "Den Player", "0", "1", "100"},
	}}

	got, err := onEmby(reports, Detail{Devices: true}).History(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := poll.History{
		Installed: true,
		Plays: []poll.Played{
			{Week: weekBefore, Kind: poll.Movie, Method: poll.Transcode, Plays: 1, Seconds: 100},
			{Week: thisWeek, Kind: poll.Episode, Method: poll.Transcode, Plays: 2, Seconds: 1512, Short: 1},
			{Week: thisWeek, Kind: poll.Movie, Method: poll.DirectPlay, Plays: 1, Seconds: 3000},
			{Week: thisWeek, Kind: poll.Movie, Method: poll.Transcode, Plays: 2, Seconds: 7000},
		},
		ByUser:  []poll.Played{},
		ByTitle: []poll.Played{},
		ByReason: []poll.Played{
			{Week: weekBefore, Reason: "container_bitrate_exceeds_limit", Plays: 1, Seconds: 100},
			{Week: thisWeek, Reason: "container_bitrate_exceeds_limit", Plays: 2, Seconds: 7000},
			{Week: thisWeek, Reason: "container_not_supported", Plays: 2, Seconds: 1512, Short: 1},
			{Week: thisWeek, Reason: "direct_play_error", Plays: 2, Seconds: 1512, Short: 1},
		},
		ByClient: []poll.Played{
			{Week: weekBefore, Reason: "container_bitrate_exceeds_limit", Device: television, Client: "Den Player", Plays: 1, Seconds: 100},
			{Week: thisWeek, Reason: "container_not_supported", Device: phone, Client: "Pocket Player", Plays: 2, Seconds: 1512, Short: 1},
			{Week: thisWeek, Reason: "direct_play_error", Device: phone, Client: "Pocket Player", Plays: 2, Seconds: 1512, Short: 1},
			{Week: thisWeek, Reason: "container_bitrate_exceeds_limit", Device: television, Client: "Den Player", Plays: 2, Seconds: 7000},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("history =\n%+v\nwant\n%+v", got, want)
	}

	// not asked to name devices, the plugin is not asked for them, and the reasons are there without them
	plain := &fakeReports{rows: [][]string{{thisWeek, "Movie", "Transcode", "ContainerBitrateExceedsLimit", "0", "2", "7000"}}}
	got, err = onEmby(plain, Detail{}).History(t.Context())
	if err != nil || len(got.ByReason) != 1 || got.ByReason[0].Plays != 2 || len(got.ByClient) != 0 {
		t.Errorf("not asked to name devices: history = %+v and %v, want the reason and no device", got, err)
	}
	if strings.Contains(plain.sql, "DeviceName") || strings.Contains(plain.sql, "ClientName") || !strings.Contains(plain.sql, "TranscodeReasons") {
		t.Errorf("not asked to name devices, the plugin was asked\n%s", plain.sql)
	}
}

// A server not asked to name anyone asks the plugin for neither accounts nor titles, and what it hands on has neither.
func TestHistoryNamesNobodyUnlessAsked(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{rows: [][]string{{thisWeek, "Episode", "DirectPlay", "0", "3", "5700"}, {thisWeek, "Episode", "Transcode", "0", "1", "1500"}, {thisWeek, "Movie", "DirectPlay", "0", "2", "9000"}}}

	got, err := onJellyfin(reports, Detail{}).History(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := poll.History{Installed: true, Plays: []poll.Played{
		{Week: thisWeek, Kind: poll.Episode, Method: poll.DirectPlay, Plays: 3, Seconds: 5700},
		{Week: thisWeek, Kind: poll.Episode, Method: poll.Transcode, Plays: 1, Seconds: 1500},
		{Week: thisWeek, Kind: poll.Movie, Method: poll.DirectPlay, Plays: 2, Seconds: 9000},
	}, ByUser: []poll.Played{}, ByTitle: []poll.Played{}, ByReason: []poll.Played{}, ByClient: []poll.Played{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("history =\n%+v\nwant\n%+v", got, want)
	}
	if reports.names || strings.Contains(reports.sql, "UserId") || strings.Contains(reports.sql, "ItemName") {
		t.Errorf("not asked to name anyone, the plugin was still asked for names: %t, with\n%s", reports.names, reports.sql)
	}
}

// Only one of the settings on names only what it asks for.
func TestHistoryNamesOnlyWhatIsAskedFor(t *testing.T) {
	t.Parallel()

	got, err := onJellyfin(&fakeReports{rows: [][]string{{thisWeek, "Movie", "DirectPlay", "ada", "0", "1", "60"}}}, Detail{Users: true}).History(t.Context())
	if err != nil || len(got.ByUser) != 1 || got.ByUser[0].User != "ada" || len(got.ByTitle) != 0 || len(got.ByClient) != 0 {
		t.Errorf("accounts alone: history = %+v and %v, want ada's plays and no titles or devices", got, err)
	}

	got, err = onJellyfin(&fakeReports{rows: [][]string{{thisWeek, "Movie", "DirectPlay", "Heat", "0", "1", "60"}}}, Detail{Titles: true}).History(t.Context())
	if err != nil || len(got.ByTitle) != 1 || got.ByTitle[0].Title != "Heat" || len(got.ByUser) != 0 || len(got.ByClient) != 0 {
		t.Errorf("titles alone: history = %+v and %v, want Heat's plays and no accounts or devices", got, err)
	}

	got, err = onEmby(&fakeReports{rows: [][]string{{thisWeek, "Movie", "Transcode", "ContainerNotSupported", "a television", "an app", "0", "1", "60"}}}, Detail{Devices: true}).History(t.Context())
	if err != nil || len(got.ByClient) != 1 || got.ByClient[0].Device != "a television" || got.ByClient[0].Client != "an app" || len(got.ByUser) != 0 || len(got.ByTitle) != 0 {
		t.Errorf("devices alone: history = %+v and %v, want the television's plays and no accounts or titles", got, err)
	}
}

// A server without the plugin has no history, which is an answer: the round that asked has not failed.
func TestHistoryOfAServerWithoutThePlugin(t *testing.T) {
	t.Parallel()

	got, err := onEmby(&fakeReports{missing: true}, everything).History(t.Context())
	if err != nil || !reflect.DeepEqual(got, poll.History{}) {
		t.Errorf("history = %+v and %v, want none and no error", got, err)
	}
}

// An answer that cannot be read is an error and no history, never a half of one: a counter that dropped for a round would read as a week's
// playing all over again.
func TestHistoryThatCannotBeRead(t *testing.T) {
	t.Parallel()

	for name, reports := range map[string]*fakeReports{
		"the plugin fails":      {err: errDown},
		"a row too short":       {rows: [][]string{{thisWeek, "Movie", "DirectPlay", "0", "1", "60"}, {thisWeek, "Movie", "DirectPlay", "1"}}},
		"a row too long":        {rows: [][]string{{thisWeek, "Movie", "DirectPlay", "ada", "Heat", "0", "1", "60"}}},
		"a count not a number":  {rows: [][]string{{thisWeek, "Movie", "DirectPlay", "0", "one", "60"}}},
		"a length not a number": {rows: [][]string{{thisWeek, "Movie", "DirectPlay", "0", "1", "a minute"}}},
		"a week not a day":      {rows: [][]string{{"", "Movie", "DirectPlay", "0", "1", "60"}}},
		"a week not written so": {rows: [][]string{{"12/10/2026", "Movie", "DirectPlay", "0", "1", "60"}}},
	} {
		got, err := onJellyfin(reports, Detail{}).History(t.Context())
		if err == nil || !reflect.DeepEqual(got, poll.History{}) {
			t.Errorf("%s: history = %+v and %v, want an error and nothing", name, got, err)
		}
	}
}

// A plugin whose record is being written to as the question arrives is asked again a moment later, and the answer it then gives is the answer. One
// that stays locked is a failure after four tries, and a program told to stop while it waits stops.
func TestAPluginWhoseRecordIsBeingWrittenToIsAskedAgain(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		reports := &fakeReports{locked: 2, rows: [][]string{{thisWeek, "Movie", "DirectPlay", "0", "1", "60"}}}
		s := onJellyfin(reports, Detail{})

		start := time.Now()
		got, err := s.History(t.Context())
		if err != nil || len(got.Plays) != 1 || reports.asked != 3 || time.Since(start) != 2*busyWait {
			t.Errorf("locked twice: history = %+v and %v after %d tries and %s, want the plays at the third try, half a second on", got, err, reports.asked, time.Since(start))
		}

		stuck := &fakeReports{locked: 100}
		s = onJellyfin(stuck, Detail{})
		if got, err := s.History(t.Context()); !errors.Is(err, errBusy) || stuck.asked != busyTries || !reflect.DeepEqual(got, poll.History{}) {
			t.Errorf("locked for good: history = %+v and %v after %d tries, want the plugin's own failure after %d", got, err, stuck.asked, busyTries)
		}

		stopping, stop := context.WithCancel(t.Context())
		stop()
		stuck = &fakeReports{locked: 100}
		s = onJellyfin(stuck, Detail{})
		if _, err := s.History(stopping); !errors.Is(err, context.Canceled) || stuck.asked != 1 {
			t.Errorf("told to stop: error = %v after %d tries, want the stop and no second try", err, stuck.asked)
		}
	})
}

// The two ways a real Jellyfin's plugin fails a question that lands as it writes to its record: a success that says the database is locked, and a
// bare HTTP 500. Either is one to try again and not to give up on, and the trying again is left to whoever put the question.
func TestTheWaysAPluginSaysItIsBusy(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		status int
		body   string
	}{
		"says the database is locked": {http.StatusOK, `{"colums":[],"results":[],"message":"Error Running Query</br>database is locked<pre>Error: SQLitePCL.pretty.SQLiteException: database is locked\n   at SQLitePCL.pretty.SQLiteException.CheckOk()</pre>"}`},
		"fails outright":              {http.StatusInternalServerError, "Error processing request."},
	} {
		server, seen := canned(t, c.status, c.body)

		p, err := connectDirect(embyfin.Jellyfin, server.URL, testKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := p.Query(t.Context(), asking{}.query(), false); !errors.Is(err, errBusy) || strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: error = %v, want the one that says the record was being written to, without the key in it", name, err)
		}
		if len(seen()) != 1 {
			t.Errorf("%s: the plugin was asked %d times by one question, want once", name, len(seen()))
		}
	}
}

// asked is one request the canned server below was sent.
type asked struct {
	method, path, body string
	keyed              bool
}

// canned is a server that answers the plugin's address with body and status, and remembers what it was sent.
func canned(t *testing.T, status int, body string) (server *httptest.Server, seen func() []asked) {
	t.Helper()

	var mu sync.Mutex
	var sent []asked
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		question, _ := io.ReadAll(r.Body)

		mu.Lock()
		sent = append(sent, asked{r.Method, r.URL.String(), string(question), strings.Contains(strings.Join(r.Header.Values("X-Emby-Token"), "")+strings.Join(r.Header.Values("Authorization"), ""), testKey)})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server, func() []asked {
		mu.Lock()
		defer mu.Unlock()

		return sent
	}
}

// The plugin of either server is asked the same way: one POST to its address with the question and whether to name accounts, and the server's key
// in a header. The answer is as each really writes one: Emby leaves an empty value out as null, Jellyfin writes it as empty text, and Emby's has
// the one more column for why a stream was made.
func TestThePluginIsAskedOverHTTP(t *testing.T) {
	t.Parallel()

	detail := Detail{Users: true, Titles: true}

	for kind, body := range map[string]string{
		"emby":     `{"colums":["week","ItemType","PlaybackMethod","UserName","ItemName","TranscodeReasons","short","COUNT(*)","SUM(PlayDuration)"],"results":[["2026-10-12","Episode","DirectPlay","ada","Dark - s01e01 - Secrets",null,"0","2","3000"],["2026-10-12","Movie","Transcode (v:h264 a:aac)","grace","Heat","ContainerNotSupported","1",1,null],["2026-10-12","Movie","DirectPlay","grace","Heat",null,"0","1",""]],"message":""}`,
		"jellyfin": `{"colums":["week","ItemType","PlaybackMethod","UserName","ItemName","short","COUNT(*)","SUM(PlayDuration)"],"results":[["2026-10-12","Episode","DirectPlay","ada","Dark - s01e01 - Secrets","0","2","3000"],["2026-10-12","Movie","Transcode (v:h264 a:aac)","grace","Heat","1",1,null],["2026-10-12","Movie","DirectPlay","grace","Heat","0","1",""]],"message":""}`,
	} {
		server, seen := canned(t, http.StatusOK, body)

		s, err := Connect(kind, server.URL, testKey, detail)
		if err != nil {
			t.Fatalf("%s: connect: %v", kind, err)
		}

		got, err := s.History(t.Context())
		if err != nil {
			t.Fatalf("%s: history: %v", kind, err)
		}
		if !got.Installed || len(got.Plays) != 3 || len(got.ByUser) != 2 || len(got.ByTitle) != 2 {
			t.Errorf("%s: history = %+v", kind, got)
		}
		if want := (poll.Played{Week: thisWeek, User: "grace", Kind: poll.Movie, Plays: 2, Short: 1}); got.ByUser[1] != want {
			t.Errorf("%s: grace's films = %+v, want %+v", kind, got.ByUser[1], want)
		}
		if reasons := len(got.ByReason); (kind == "emby") != (reasons == 1) {
			t.Errorf("%s: the reasons for a stream = %+v", kind, got.ByReason)
		}

		request := seen()[0]
		var question struct {
			Query string `json:"CustomQueryString"`
			Names bool   `json:"ReplaceUserId"`
		}
		if err := json.Unmarshal([]byte(request.body), &question); err != nil {
			t.Fatalf("%s: the question sent is not JSON: %v: %s", kind, err, request.body)
		}
		if request.method != http.MethodPost || request.path != "/user_usage_stats/submit_custom_query" || !request.keyed {
			t.Errorf("%s: sent %s %s, keyed in a header: %t", kind, request.method, request.path, request.keyed)
		}
		if want := (asking{emby: kind == "emby", detail: detail}).query(); question.Query != want || !question.Names {
			t.Errorf("%s: asked %+v, want %s", kind, question, want)
		}
	}
}

// A server with nothing at the plugin's address has no plugin, and that is not a failure. Anything else it refuses with is.
func TestAServerWithoutThePluginOverHTTP(t *testing.T) {
	t.Parallel()

	missing, _ := canned(t, http.StatusNotFound, `"Not Found"`)
	refusing, _ := canned(t, http.StatusUnauthorized, "no such key: "+testKey)

	for _, kind := range []string{"emby", "jellyfin"} {
		s, err := Connect(kind, missing.URL, testKey, Detail{})
		if err != nil {
			t.Fatalf("%s: connect: %v", kind, err)
		}
		if got, err := s.History(t.Context()); err != nil || got.Installed {
			t.Errorf("%s: with no plugin, history = %+v and %v, want none and no error", kind, got, err)
		}

		s, err = Connect(kind, refusing.URL, testKey, Detail{})
		if err != nil {
			t.Fatalf("%s: connect: %v", kind, err)
		}
		if _, err := s.History(t.Context()); err == nil || strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: with the key refused, error = %v, want one without the key in it", kind, err)
		}
	}
}

// A question the plugin cannot answer comes back as a success with a message, which is an error here, said in its first line and without the page
// of the plugin's own workings that follows.
func TestAQuestionThePluginCannotAnswer(t *testing.T) {
	t.Parallel()

	server, _ := canned(t, http.StatusOK, `{"colums":[],"results":[],"message":"Error Running Query</br>near \"nothing\": syntax error<pre>Error: SQLitePCL.pretty.SQLiteException: near \"nothing\": syntax error\n   at SQLitePCL.pretty.SQLiteException.Throw()</pre>"}`)

	s, err := Connect("jellyfin", server.URL, testKey, Detail{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.History(t.Context())
	if err == nil || !strings.Contains(err.Error(), `Error Running Query: near "nothing": syntax error`) || strings.Contains(err.Error(), "SQLitePCL") {
		t.Errorf("error = %v, want the plugin's first line and no more", err)
	}
}

// A server on which nothing has been played in the two weeks has the plugin and no plays, which is not a failure. Emby's plugin answers with no
// rows; Jellyfin's with no columns either and a message saying nothing came back, as seen on a real one.
func TestNothingPlayedIsAnAnswer(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"emby":     `{"colums":["date(DateCreated, 'weekday 0', '-6 days')","ItemType","PlaybackMethod","COUNT(*)","SUM(PlayDuration)"],"results":[],"message":""}`,
		"jellyfin": `{"colums":[],"results":[],"message":"Query executed, no data returned.</br>Number of rows effected : 0"}`,
	} {
		server, _ := canned(t, http.StatusOK, body)

		s, err := Connect(name, server.URL, testKey, everything)
		if err != nil {
			t.Fatal(err)
		}

		got, err := s.History(t.Context())
		if err != nil || !got.Installed || len(got.Plays) != 0 || len(got.ByUser) != 0 || len(got.ByTitle) != 0 {
			t.Errorf("%s: history = %+v and %v, want the plugin there, nothing played and no error", name, got, err)
		}
	}
}

// What the files being played come to, read from the sessions as each server really writes them: Emby gives a file a bitrate that is its picture's
// alone and each stream its own, Jellyfin gives the file none, and either way it is the streams added together. A session with nothing playing has
// none, and a file whose streams say nothing goes by its own.
func TestFileRates(t *testing.T) {
	t.Parallel()

	server, seen := canned(t, http.StatusOK, `[
		{"Id":"emby","NowPlayingItem":{"Name":"Alien","Bitrate":785759,"MediaStreams":[{"Type":"Video","BitRate":785759},{"Type":"Audio","BitRate":192000},{"Type":"Subtitle"}]}},
		{"Id":"jellyfin","NowPlayingItem":{"Name":"Alien (1979)","MediaStreams":[{"Type":"Video","BitRate":593759},{"Type":"Audio","BitRate":192000}]}},
		{"Id":"bare","NowPlayingItem":{"Name":"A Clip","Bitrate":500000,"MediaStreams":[{"Type":"Video"}]}},
		{"Id":"silent","NowPlayingItem":{"Name":"A Photo"}},
		{"Id":"idle"}
	]`)

	for _, backend := range []embyfin.Backend{embyfin.Emby, embyfin.Jellyfin} {
		d, err := connectDirect(backend, server.URL, testKey)
		if err != nil {
			t.Fatal(err)
		}

		got, err := d.FileRates(t.Context())
		if want := map[string]int{"emby": 977759, "jellyfin": 785759, "bare": 500000}; err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: file rates = %v and %v, want %v", backend, got, err, want)
		}
	}

	for _, request := range seen() {
		if request.method != http.MethodGet || request.path != "/Sessions" || !request.keyed {
			t.Errorf("sent %s %s, keyed in a header: %t; want a keyed GET of /Sessions", request.method, request.path, request.keyed)
		}
	}
}

// How a play reached the device, from the plugin's words for it, each seen on a real Emby. A stream the server made with nothing re-encoded is a
// direct stream, as it is for a session.
func TestReported(t *testing.T) {
	t.Parallel()

	for word, want := range map[string]poll.Method{
		"DirectPlay":                    poll.DirectPlay,
		"DirectStream":                  poll.DirectStream,
		"Transcode":                     poll.Transcode,
		"Transcode (v:h264 a:direct)":   poll.Transcode,
		"Transcode (v:direct a:ac3)":    poll.Transcode,
		"Transcode (v:h264 a:aac)":      poll.Transcode,
		"Transcode (v:direct a:direct)": poll.DirectStream,
		"":                              poll.UnknownMethod,
		"Remux":                         poll.UnknownMethod,
	} {
		if got := reported(word); got != want {
			t.Errorf("reported(%q) = %q, want %q", word, got, want)
		}
	}
}

// Whose a play was: the name the plugin gives, and "unknown" for the id it leaves of an account that is gone or for no name at all.
func TestAccount(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"ada":                              "ada",
		"Ada Lovelace":                     "Ada Lovelace",
		"0123456789abcdef0123456789abcdef": "unknown",
		"0123456789ABCDEF0123456789ABCDEF": "unknown",
		"":                                 "unknown",
		// an id is 32 letters and digits, and a name that merely looks a little like one is a name
		"0123456789abcdef": "0123456789abcdef",
	} {
		if got := account(name); got != want {
			t.Errorf("account(%q) = %q, want %q", name, got, want)
		}
	}
}

// A show's name is what comes before the season and episode in the plugin's name for an episode, a film's is its own, and nothing else has one.
func TestTitle(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		kind       poll.Kind
		name, want string
	}{
		{poll.Episode, "Dark - s01e02 - Lies", "Dark"},
		{poll.Episode, "11.22.63 - s01e01 - The Rabbit Hole", "11.22.63"},
		{poll.Episode, "Star Trek - Lower Decks - s04e10 - Old Friends, New Planets", "Star Trek - Lower Decks"},
		{poll.Episode, "A Long Show - s2026e101 - Episode 101", "A Long Show"},
		{poll.Episode, "Not Known - s00e00 - A Special", "Not Known"},
		{poll.Episode, "A Show With No Numbers", "A Show With No Numbers"},
		{poll.Movie, "Heat", "Heat"},
		{poll.Movie, "A Film - s01e01 - Named Like An Episode", "A Film - s01e01 - Named Like An Episode"},
		{poll.Track, "Not Known - A Song (Not Known)", ""},
		{poll.OtherKind, "Heat - Trailer", ""},
		{poll.Video, "A Clip", ""},
	} {
		if got := title(c.kind, c.name); got != c.want {
			t.Errorf("title(%s, %q) = %q, want %q", c.kind, c.name, got, c.want)
		}
	}
}
