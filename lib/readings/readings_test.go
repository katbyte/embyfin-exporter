package readings_test

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-exporter/lib/readings"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/katbyte/go-kt/prometheus/metricstest"
)

// saved is where the page is kept as it was last agreed.
const saved = "testdata/page.txt"

const day = 24 * time.Hour

// The two weeks the saved page's plays began in, each named by its Monday: the page is written on the first day of 2000, a Saturday.
const (
	thisWeek = "1999-12-27"
	lastWeek = "1999-12-20"
)

// fixed is a server whose state is whatever the test says.
type fixed poll.State

// answered is an answer given now.
func answered[T any](value T) poll.Answer[T] {
	return poll.Answer[T]{Value: value, At: time.Now()}
}

// emby is a server that has answered everything: five sessions of which three are playing or paused and two are streams the server is making,
// one of them for a device outside the house, a scan a quarter of the way through, a task that failed and one that was cancelled, three libraries,
// four accounts last seen an hour, three days and twenty days ago and never, the last of them disabled, seven devices of which four were seen
// within the year, three plugins of which two have a newer version on offer, and two weeks' plays. It was asked to name all it can, so the sessions say whose they are, what they play and on what, two of
// the accounts have names, and the plays are also by account and by title.
func emby() *fixed {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	return &fixed{
		Kind: "emby",
		Info: answered(poll.Info{Name: "nas", Version: "4.10.0.4", APIVersion: "4.10.0.40", RestartPending: true}),
		Sessions: answered([]poll.Session{
			{ID: "0a", State: poll.Playing, Method: poll.Transcode, Kind: poll.Episode, Network: poll.Remote, VideoTranscode: true, Hardware: true, Bitrate: 8000000, Reasons: []string{"container_bitrate_exceeds_limit"}, Outgoing: 8000000, Position: 10 * time.Minute, Runtime: 50 * time.Minute, User: "ada", Title: "Dark - S01E02 - Lies", Device: "the television in the den", Client: "Den Player"},
			{ID: "0b", State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie, Network: poll.Home, Bitrate: 4500000, Reasons: []string{"audio_codec_not_supported", "container_bitrate_exceeds_limit"}, Outgoing: 4500000, Runtime: 170 * time.Minute, User: "grace", Title: "Heat (1995)", Device: "the phone in the hall", Client: "Pocket Player"},
			{ID: "0c", State: poll.Paused, Method: poll.DirectPlay, Kind: poll.Track, Network: poll.Home, Outgoing: 320000, Position: 65 * time.Second, Runtime: 4 * time.Minute, User: "ada", Title: "Pink Floyd - Breathe", Device: "the speaker on the shelf", Client: "Shelf Player"},
			{State: poll.Idle},
			{State: poll.Idle},
		}),
		Tasks: answered([]poll.Task{
			{Name: "Scan media library", State: poll.TaskRunning, Progress: 0.25, LibraryScan: true, LastRun: &poll.Run{OK: true, Ended: ago(time.Hour), Took: 90 * time.Second}},
			{Name: "Export Library to Trakt", State: poll.TaskIdle, LastRun: &poll.Run{Failed: true, Ended: ago(2 * time.Hour)}},
			{Name: "Refresh Guide", State: poll.TaskIdle, LastRun: &poll.Run{Ended: ago(3 * time.Hour)}},
			{Name: "Vacuum Database", State: poll.TaskIdle},
		}),
		Libraries: answered([]poll.Library{
			{Name: "Movies", Items: map[string]int{"movie": 1200}, Added: map[string]int{"1d": 0, "7d": 3, "30d": 12}},
			{Name: "Kids", Items: map[string]int{"movie": 300}, Added: map[string]int{"1d": 0, "7d": 0, "30d": 0}},
			{Name: "TV", Items: map[string]int{"series": 80, "season": 400, "episode": 9000}, Added: map[string]int{"1d": 6, "7d": 40, "30d": 150}},
		}),
		Users:   answered([]poll.User{{Name: "ada", LastActive: ago(time.Hour)}, {Name: "grace", LastActive: ago(3 * day)}, {LastActive: ago(20 * day)}, {Disabled: true}}),
		Devices: answered([]poll.Device{{LastActive: ago(time.Hour)}, {LastActive: ago(2 * day)}, {LastActive: ago(10 * day)}, {LastActive: ago(40 * day)}, {LastActive: ago(400 * day)}, {}, {}}),
		Logins: answered([]poll.Login{
			{Week: thisWeek, Network: poll.Home, User: "ada", Count: 4},
			{Week: thisWeek, Network: poll.Remote, User: "ada", Count: 1},
			{Week: thisWeek, Network: poll.Remote, User: "grace", Count: 2},
			{Week: thisWeek, Failed: true, Network: poll.Remote, User: "ada", Count: 3},
			{Week: thisWeek, Failed: true, Network: poll.Remote, User: "unknown", Count: 40},
			{Week: lastWeek, Failed: true, Network: poll.Home, User: "grace", Count: 1},
		}),
		Plugins: answered(poll.Plugins{Checked: true, Installed: []poll.Plugin{
			{Name: "Backup & Restore", Version: "1.8.6.0", Latest: "1.8.7.0"},
			{Name: "Playback Reporting", Version: "2.1.0.7"},
			{Name: "Webhooks", Version: "1.0.38.0", Latest: "1.0.39.0"},
		}}),
		History: answered(poll.History{
			Installed: true,
			Plays: []poll.Played{
				{Week: lastWeek, Kind: poll.Episode, Method: poll.DirectPlay, Plays: 9, Seconds: 13500},
				{Week: thisWeek, Kind: poll.Episode, Method: poll.DirectPlay, Plays: 40, Seconds: 60000, Short: 3},
				{Week: thisWeek, Kind: poll.Episode, Method: poll.Transcode, Plays: 12, Seconds: 18000},
				{Week: thisWeek, Kind: poll.Movie, Method: poll.DirectStream, Plays: 3, Seconds: 21600},
			},
			ByUser: []poll.Played{
				{Week: lastWeek, User: "ada", Kind: poll.Episode, Plays: 9, Seconds: 13500},
				{Week: thisWeek, User: "ada", Kind: poll.Episode, Plays: 30, Seconds: 48000},
				{Week: thisWeek, User: "ada", Kind: poll.Movie, Plays: 3, Seconds: 21600},
				{Week: thisWeek, User: "grace", Kind: poll.Episode, Plays: 20, Seconds: 27000},
				{Week: thisWeek, User: "unknown", Kind: poll.Episode, Plays: 2, Seconds: 3000},
			},
			ByTitle: []poll.Played{
				{Week: lastWeek, Title: "Dark", Kind: poll.Episode, Plays: 9, Seconds: 13500},
				{Week: thisWeek, Title: "Dark", Kind: poll.Episode, Plays: 50, Seconds: 75000},
				{Week: thisWeek, Title: "Severance", Kind: poll.Episode, Plays: 2, Seconds: 3000},
				{Week: thisWeek, Title: "Heat", Kind: poll.Movie, Plays: 3, Seconds: 21600},
			},
			ByReason: []poll.Played{
				{Week: thisWeek, Reason: "container_bitrate_exceeds_limit", Plays: 10, Seconds: 15000},
				{Week: thisWeek, Reason: "direct_play_error", Plays: 2, Seconds: 3000},
			},
			ByClient: []poll.Played{
				{Week: thisWeek, Reason: "container_bitrate_exceeds_limit", Device: "the television in the den", Client: "Den Player", Plays: 10, Seconds: 15000},
				{Week: thisWeek, Reason: "direct_play_error", Device: "the phone in the hall", Client: "Pocket Player", Plays: 2, Seconds: 3000},
			},
		}),

		Rounds: map[poll.Round]poll.RoundState{
			poll.RoundServer:    {Ran: true, OK: true, Took: 50 * time.Millisecond, LastSuccess: now},
			poll.RoundSessions:  {Ran: true, OK: true, Took: 250 * time.Millisecond, LastSuccess: now},
			poll.RoundLibraries: {Ran: true, OK: true, Took: 3 * time.Second, LastSuccess: now.Add(-time.Minute), Failures: 2},
		},
	}
}

// down is a server that has been asked once and did not answer.
func down() *fixed {
	return &fixed{Kind: "jellyfin", Rounds: map[poll.Round]poll.RoundState{poll.RoundServer: {Ran: true, Took: 15 * time.Second, Failures: 1}}}
}

// write is the page of these servers' readings as a scrape reads it: the readings, how the asking went, and the one line the page adds to say which
// build wrote it. Each server's state is given to a poller that has asked nothing, as though it had been asked.
func write(t *testing.T, servers ...*fixed) string {
	t.Helper()

	return writeSkipping(t, nil, servers...)
}

// writeSkipping is write for a page that leaves off the groups skip names.
func writeSkipping(t *testing.T, skip poll.Skip, servers ...*fixed) string {
	t.Helper()

	p, err := poll.New(poll.Every, skip)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		p.Set((*poll.State)(s))
	}

	page, err := metrics.New(metrics.Options{Name: "embyfin-exporter"}, readings.Page(p)...)
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := page.Write(&out); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

// lines is the page's readings without the comments above each, and without the one line that changes with the Go it was built by.
func lines(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "embyfin_exporter_build_info") {
			out = append(out, line)
		}
	}

	return out
}

// has says whether the page holds a reading that starts as given: its name, or its name and labels.
func has(text, start string) bool {
	for _, line := range lines(text) {
		if strings.HasPrefix(line, start) {
			return true
		}
	}

	return false
}

// The page is what dashboards and alerts are written against, so the whole of it is kept in testdata/page.txt and any change to a name, a label
// or a help text shows up as a difference there. It is written at the start of the year 2000, where the test's clock begins.
func TestThePageIsWhatWasSaved(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		metricstest.Saved(t, write(t, emby(), down()), saved)
	})
}

// The names follow the conventions Prometheus checks for: a unit at the end, _total on what only goes up, help on everything. The one exception
// is a bitrate, which the check would have in bytes: it is in bits a second here, as both servers and every player give one.
func TestThePageFollowsTheNamingConventions(t *testing.T) {
	t.Parallel()

	metricstest.Lint(t, write(t, emby(), down()), metricstest.Allowed{Reading: "_bits_per_second", Problem: `instead of "bits"`})
}

// A server that has never been asked has no readings at all, and one that was asked and did not answer has only how the asking went: a reading
// of zero sessions would say something nobody knows.
func TestAServerThatNeverAnsweredHasNoReadingsOfItsState(t *testing.T) {
	t.Parallel()

	if got := lines(write(t, &fixed{Kind: "emby"})); len(got) != 0 {
		t.Errorf("a server never asked has readings: %v", got)
	}

	got := write(t, down())
	want := []string{
		`embyfin_poll_duration_seconds{poll="server",server="jellyfin"} 15`,
		`embyfin_poll_failures_total{poll="server",server="jellyfin"} 1`,
		`embyfin_poll_interval_seconds{poll="server",server="jellyfin"} 15`,
		`embyfin_up{server="jellyfin"} 0`,
	}
	if strings.Join(lines(got), "\n") != strings.Join(want, "\n") {
		t.Errorf("a server that did not answer reads\n%s\nwant\n%s", strings.Join(lines(got), "\n"), strings.Join(want, "\n"))
	}
}

// Every state, method and kind is on the page at 0 when no session has it, so a graph of transcodes shows nothing playing rather than nothing known.
// A reason is the exception: the servers' reasons are their own, and one is on the page only while a stream has it.
func TestEveryStateIsWrittenWhenNothingIsPlaying(t *testing.T) {
	t.Parallel()

	got := write(t, &fixed{Kind: "emby", Sessions: answered([]poll.Session{}), Tasks: answered([]poll.Task{})})
	for _, want := range []string{
		`embyfin_sessions{server="emby",state="playing"} 0`,
		`embyfin_sessions{server="emby",state="paused"} 0`,
		`embyfin_sessions{server="emby",state="idle"} 0`,
		`embyfin_streams{method="direct_play",server="emby"} 0`,
		`embyfin_streams{method="direct_stream",server="emby"} 0`,
		`embyfin_streams{method="transcode",server="emby"} 0`,
		`embyfin_streams{method="unknown",server="emby"} 0`,
		`embyfin_video_transcodes{encoder="hardware",server="emby"} 0`,
		`embyfin_video_transcodes{encoder="software",server="emby"} 0`,
		`embyfin_streams_by_type{server="emby",type="movie"} 0`,
		`embyfin_streams_by_type{server="emby",type="episode"} 0`,
		`embyfin_streams_by_type{server="emby",type="track"} 0`,
		`embyfin_streams_by_type{server="emby",type="music_video"} 0`,
		`embyfin_streams_by_type{server="emby",type="video"} 0`,
		`embyfin_streams_by_type{server="emby",type="audiobook"} 0`,
		`embyfin_streams_by_type{server="emby",type="other"} 0`,
		`embyfin_transcode_bits_per_second{server="emby"} 0`,
		`embyfin_streams_by_network{network="home",server="emby"} 0`,
		`embyfin_streams_by_network{network="remote",server="emby"} 0`,
		`embyfin_streams_by_network{network="unknown",server="emby"} 0`,
		`embyfin_streams_bits_per_second{network="remote",server="emby"} 0`,
		`embyfin_scheduled_tasks{server="emby",state="running"} 0`,
		`embyfin_scheduled_tasks_failed{server="emby"} 0`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s", want)
		}
	}

	// with no scan among the tasks there is nothing to say of one, nor of a task with none listed, a reason with no stream or a session with nothing playing
	for _, absent := range []string{"embyfin_library_scan", "embyfin_scheduled_task_last_run", "embyfin_transcode_reasons", "embyfin_client_transcode_reasons", "embyfin_session_"} {
		if has(got, absent) {
			t.Errorf("the page has a reading of %s with nothing to report:\n%s", absent, got)
		}
	}
}

// A session whose server does not say how it plays, or says something new, is counted as unknown rather than left out of the streams, and a state
// that is neither playing nor paused is idle: the counts always add up to the sessions there are.
func TestWhatTheServerDoesNotSayIsStillCounted(t *testing.T) {
	t.Parallel()

	got := write(t, &fixed{
		Kind:     "emby",
		Sessions: answered([]poll.Session{{State: poll.Playing}, {State: poll.Paused, Method: "remux"}, {State: "buffering"}, {State: poll.Playing, Method: poll.DirectStream, VideoTranscode: true}}),
		Tasks:    answered([]poll.Task{{State: "queued"}, {State: poll.TaskCancelling, LibraryScan: true}}),
	})
	for _, want := range []string{
		`embyfin_sessions{server="emby",state="playing"} 2`,
		`embyfin_sessions{server="emby",state="paused"} 1`,
		`embyfin_sessions{server="emby",state="idle"} 1`,
		`embyfin_streams{method="unknown",server="emby"} 2`,
		`embyfin_streams{method="direct_stream",server="emby"} 1`,
		`embyfin_video_transcodes{encoder="software",server="emby"} 1`,
		// a kind the page has no word for, or none given, is other
		`embyfin_streams_by_type{server="emby",type="other"} 3`,
		`embyfin_scheduled_tasks{server="emby",state="idle"} 1`,
		`embyfin_scheduled_tasks{server="emby",state="cancelling"} 1`,
		// a scan being cancelled is still running until it stops, and one that has never run has no last run to report
		`embyfin_library_scan_running{server="emby"} 1`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if has(got, "embyfin_library_scan_last_run") {
		t.Errorf("the page reports the last run of a scan that has never run:\n%s", got)
	}
}

// The streams the server is making: how much they come to between them, in the bits a second the servers give a bitrate in, and why it is making
// them. A stream with two reasons is under both, so the reasons add up to more than the streams.
func TestTheStreamsTheServerIsMaking(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_streams_by_type{server="emby",type="episode"} 1`,
		`embyfin_streams_by_type{server="emby",type="movie"} 1`,
		`embyfin_streams_by_type{server="emby",type="track"} 1`,
		`embyfin_streams_by_type{server="emby",type="other"} 0`,
		`embyfin_transcode_bits_per_second{server="emby"} 1.25e+07`,
		`embyfin_transcode_reasons{reason="container_bitrate_exceeds_limit",server="emby"} 2`,
		`embyfin_transcode_reasons{reason="audio_codec_not_supported",server="emby"} 1`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
}

// Each task that has run is on the page by name with whether its last run failed, so the count of failed tasks can be traced to the one. A task
// that has never run has no last run to report, one with no name cannot be told from the rest, and two of one name are one on the page.
func TestEachTaskThatHasRun(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		got := write(t, &fixed{Kind: "emby", Tasks: answered([]poll.Task{
			{Name: "Scan media library", LibraryScan: true, LastRun: &poll.Run{OK: true, Ended: now.Add(-time.Hour)}},
			{Name: "Export Library to Trakt", LastRun: &poll.Run{Failed: true, Ended: now.Add(-2 * time.Hour)}},
			{Name: "Refresh Guide", LastRun: &poll.Run{}},
			{Name: "Vacuum Database"},
			{LastRun: &poll.Run{Failed: true, Ended: now}},
			{Name: "Download Theme Videos", LastRun: &poll.Run{OK: true, Ended: now.Add(-3 * time.Hour)}},
			{Name: "Download Theme Videos", LastRun: &poll.Run{Failed: true, Ended: now.Add(-4 * time.Hour)}},
		})})

		var runs []string
		for _, line := range lines(got) {
			if strings.HasPrefix(line, "embyfin_scheduled_task_last_run") {
				runs = append(runs, line)
			}
		}

		want := []string{
			`embyfin_scheduled_task_last_run_failed{server="emby",task="Download Theme Videos"} 1`,
			`embyfin_scheduled_task_last_run_failed{server="emby",task="Export Library to Trakt"} 1`,
			`embyfin_scheduled_task_last_run_failed{server="emby",task="Refresh Guide"} 0`,
			`embyfin_scheduled_task_last_run_failed{server="emby",task="Scan media library"} 0`,
			`embyfin_scheduled_task_last_run_timestamp_seconds{server="emby",task="Download Theme Videos"} 9.46674e+08`,
			`embyfin_scheduled_task_last_run_timestamp_seconds{server="emby",task="Export Library to Trakt"} 9.466776e+08`,
			`embyfin_scheduled_task_last_run_timestamp_seconds{server="emby",task="Scan media library"} 9.466812e+08`,
		}
		if strings.Join(runs, "\n") != strings.Join(want, "\n") {
			t.Errorf("the last runs read\n%s\nwant\n%s", strings.Join(runs, "\n"), strings.Join(want, "\n"))
		}
	})
}

// How far a scan has got is on the page while it runs and gone once it stops: a scan that is not running has got nowhere, which is not 0.
func TestScanProgressIsOnlyThereWhileItRuns(t *testing.T) {
	t.Parallel()

	for state, want := range map[poll.TaskState]bool{poll.TaskRunning: true, poll.TaskCancelling: true, poll.TaskIdle: false} {
		got := write(t, &fixed{Kind: "emby", Tasks: answered([]poll.Task{{Name: "Scan media library", State: state, Progress: 0.4, LibraryScan: true}})})
		if has(got, `embyfin_library_scan_progress_ratio{server="emby"} 0.4`) != want {
			t.Errorf("a scan that is %s: progress on the page is %t, want %t:\n%s", state, !want, want, got)
		}
	}
}

// A server keeps every device it has ever seen, so the page also says how many were seen lately. A device with no date is in the count and in no
// window.
func TestDevicesSeenLately(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_devices{server="emby"} 7`,
		`embyfin_devices_active{server="emby",window="1d"} 1`,
		`embyfin_devices_active{server="emby",window="7d"} 2`,
		`embyfin_devices_active{server="emby",window="30d"} 3`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
}

// The count for the whole server is its libraries added together, and a run that was cancelled is not one that failed.
func TestTheWholeServerIsItsLibrariesAddedTogether(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_libraries{server="emby"} 3`,
		`embyfin_library_items{library="Movies",server="emby",type="movie"} 1200`,
		`embyfin_library_items{library="Kids",server="emby",type="movie"} 300`,
		`embyfin_items{server="emby",type="movie"} 1500`,
		`embyfin_items{server="emby",type="episode"} 9000`,
		`embyfin_scheduled_tasks{server="emby",state="idle"} 3`,
		`embyfin_scheduled_tasks_failed{server="emby"} 1`,
		`embyfin_users{server="emby"} 4`,
		`embyfin_users_disabled{server="emby"} 1`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
}

// Two readings with the same labels would lose the whole page, so two libraries of one name are added together, in what they hold and in what is
// new in them.
func TestTwoLibrariesOfOneNameAreAddedTogether(t *testing.T) {
	t.Parallel()

	got := write(t, &fixed{Kind: "emby", Libraries: answered([]poll.Library{
		{Name: "Films", Items: map[string]int{"movie": 10}, Added: map[string]int{"1d": 1, "7d": 2, "30d": 3}},
		{Name: "Films", Items: map[string]int{"movie": 5, "video": 1}, Added: map[string]int{"1d": 0, "7d": 4, "30d": 4}},
	})})
	for _, want := range []string{
		`embyfin_libraries{server="emby"} 2`,
		`embyfin_library_items{library="Films",server="emby",type="movie"} 15`,
		`embyfin_library_items{library="Films",server="emby",type="video"} 1`,
		`embyfin_items{server="emby",type="movie"} 15`,
		`embyfin_library_items_added{library="Films",server="emby",window="1d"} 1`,
		`embyfin_library_items_added{library="Films",server="emby",window="7d"} 6`,
		`embyfin_library_items_added{library="Films",server="emby",window="30d"} 7`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
}

// What is new in each library within a day, a week and a month is on the page for a server that can count it, at 0 where nothing is. A server
// that cannot count it has no such reading, which is not the same as nothing new.
func TestWhatIsNewInEachLibrary(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_library_items_added{library="TV",server="emby",window="1d"} 6`,
		`embyfin_library_items_added{library="TV",server="emby",window="7d"} 40`,
		`embyfin_library_items_added{library="TV",server="emby",window="30d"} 150`,
		`embyfin_library_items_added{library="Movies",server="emby",window="7d"} 3`,
		`embyfin_library_items_added{library="Kids",server="emby",window="30d"} 0`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}

	quiet := write(t, &fixed{Kind: "jellyfin", Libraries: answered([]poll.Library{{Name: "Films", Items: map[string]int{"movie": 10}}})})
	if has(quiet, "embyfin_library_items_added") || !has(quiet, `embyfin_library_items{library="Films",server="jellyfin",type="movie"} 10`) {
		t.Errorf("a server that cannot count what is new should have its libraries on the page and nothing of what is new:\n%s", quiet)
	}
}

// What was played in this week and the last, as the server's Playback Reporting plugin has it: by kind and method always, and by account and by
// title for a server that was asked to name them. Each is a counter under the week its plays began in, for a query to take the increase of over a
// day or any other stretch.
func TestWhatWasPlayed(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_playback_reporting_installed{server="emby"} 1`,
		`embyfin_playback_seconds_total{method="direct_play",server="emby",type="episode",week="1999-12-27"} 60000`,
		`embyfin_playback_seconds_total{method="direct_play",server="emby",type="episode",week="1999-12-20"} 13500`,
		`embyfin_playback_seconds_total{method="transcode",server="emby",type="episode",week="1999-12-27"} 18000`,
		`embyfin_playback_seconds_total{method="direct_stream",server="emby",type="movie",week="1999-12-27"} 21600`,
		`embyfin_plays_total{method="direct_play",server="emby",type="episode",week="1999-12-27"} 40`,
		`embyfin_user_playback_seconds_total{server="emby",type="episode",user="ada",week="1999-12-27"} 48000`,
		`embyfin_user_playback_seconds_total{server="emby",type="episode",user="ada",week="1999-12-20"} 13500`,
		`embyfin_user_playback_seconds_total{server="emby",type="movie",user="ada",week="1999-12-27"} 21600`,
		`embyfin_user_playback_seconds_total{server="emby",type="episode",user="unknown",week="1999-12-27"} 3000`,
		`embyfin_user_plays_total{server="emby",type="episode",user="grace",week="1999-12-27"} 20`,
		`embyfin_title_playback_seconds_total{server="emby",title="Dark",type="episode",week="1999-12-27"} 75000`,
		`embyfin_title_playback_seconds_total{server="emby",title="Heat",type="movie",week="1999-12-27"} 21600`,
		`embyfin_title_plays_total{server="emby",title="Severance",type="episode",week="1999-12-27"} 2`,
		// the plays that ended within a minute, at 0 where there were none
		`embyfin_short_plays_total{method="direct_play",server="emby",type="episode",week="1999-12-27"} 3`,
		`embyfin_short_plays_total{method="transcode",server="emby",type="episode",week="1999-12-27"} 0`,
		// why the server made streams, and for which device and app
		`embyfin_transcode_reason_seconds_total{reason="container_bitrate_exceeds_limit",server="emby",week="1999-12-27"} 15000`,
		`embyfin_transcode_reason_plays_total{reason="direct_play_error",server="emby",week="1999-12-27"} 2`,
		`embyfin_client_transcode_reason_seconds_total{client="Den Player",device="the television in the den",reason="container_bitrate_exceeds_limit",server="emby",week="1999-12-27"} 15000`,
		`embyfin_client_transcode_reason_plays_total{client="Pocket Player",device="the phone in the hall",reason="direct_play_error",server="emby",week="1999-12-27"} 2`,
		`embyfin_user_last_seen_timestamp_seconds{server="emby",user="ada"}`,
		`embyfin_user_last_seen_timestamp_seconds{server="emby",user="grace"}`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}

	if !strings.Contains(got, "# TYPE embyfin_user_playback_seconds_total counter") || !strings.Contains(got, "# TYPE embyfin_plays_total counter") {
		t.Errorf("what was played is not written as counters:\n%s", got)
	}
}

// named finds a label on the page that names someone or something: an account, a title, a device or its app, with anything in it.
var named = regexp.MustCompile(`(user|title|device|client)="[^"]`)

// A server that was not asked to name anyone has no name anywhere on its page: no account's, no device's, and no title of what is playing or was
// played. Its sessions and its plays are still there, by kind, method and where the device is, which say nothing of whose they are.
func TestNobodyIsNamedUnlessAskedFor(t *testing.T) {
	t.Parallel()

	state := emby()
	sessions := slices.Clone(state.Sessions.Value)
	for i := range sessions {
		sessions[i].User, sessions[i].Title, sessions[i].Device, sessions[i].Client = "", "", "", ""
	}
	state.Sessions = answered(sessions)
	logins := slices.Clone(state.Logins.Value)
	for i := range logins {
		logins[i].User = ""
	}
	state.Logins = answered(logins)
	state.Users = answered([]poll.User{{LastActive: time.Now()}, {Disabled: true}})
	history := state.History.Value
	history.ByUser, history.ByTitle, history.ByClient = nil, nil, nil
	state.History = answered(history)

	got := write(t, state)
	if found := named.FindAllString(got, -1); len(found) != 0 {
		t.Errorf("the page names %v when nobody was to be named:\n%s", found, got)
	}
	for _, absent := range []string{"embyfin_user_playback_seconds_total", "embyfin_user_plays_total", "embyfin_user_last_seen", "embyfin_user_logins_total", "embyfin_title_", "embyfin_client_transcode_reason"} {
		if has(got, absent) {
			t.Errorf("the page has a reading of %s when nobody was to be named", absent)
		}
	}
	for _, want := range []string{
		`embyfin_playback_seconds_total{method="direct_play",server="emby",type="episode",week="1999-12-27"} 60000`,
		`embyfin_users{server="emby"} 2`,
		`embyfin_session_info{client="",device="",method="transcode",network="remote",reasons="container_bitrate_exceeds_limit",server="emby",session="0a",state="playing",title="",type="episode",user=""} 1`,
		`embyfin_session_bits_per_second{server="emby",session="0a"} 8e+06`,
		`embyfin_streams_by_network{network="remote",server="emby"} 1`,
		`embyfin_transcode_reason_plays_total{reason="direct_play_error",server="emby",week="1999-12-27"} 2`,
		`embyfin_logins_total{network="remote",result="failed",server="emby",week="1999-12-27"} 43`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s, which names nobody:\n%s", want, got)
		}
	}
}

// How the signing in went: every sign-in and every try that was refused, by the week it was made in and where it came from, and by account for a
// server asked to name accounts. The tries from outside that were refused are the ones to watch on a server the internet can reach, and those on
// names the server has no account of are together under unknown, however many names were tried.
func TestSignIns(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_logins_total{network="home",result="ok",server="emby",week="1999-12-27"} 4`,
		`embyfin_logins_total{network="remote",result="ok",server="emby",week="1999-12-27"} 3`,
		`embyfin_logins_total{network="remote",result="failed",server="emby",week="1999-12-27"} 43`,
		`embyfin_logins_total{network="home",result="failed",server="emby",week="1999-12-20"} 1`,
		`embyfin_user_logins_total{result="ok",server="emby",user="ada",week="1999-12-27"} 5`,
		`embyfin_user_logins_total{result="ok",server="emby",user="grace",week="1999-12-27"} 2`,
		`embyfin_user_logins_total{result="failed",server="emby",user="ada",week="1999-12-27"} 3`,
		`embyfin_user_logins_total{result="failed",server="emby",user="unknown",week="1999-12-27"} 40`,
		`embyfin_user_logins_total{result="failed",server="emby",user="grace",week="1999-12-20"} 1`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "# TYPE embyfin_logins_total counter") {
		t.Errorf("the sign-ins are not written as a counter:\n%s", got)
	}

	// where a sign-in came from in a word not known here is unknown, and a server never asked has no sign-ins on its page, which is not none
	odd := write(t, &fixed{Kind: "emby", Logins: answered([]poll.Login{{Week: thisWeek, Network: "the moon", Count: 2}, {Week: thisWeek, Count: 1}})})
	if !has(odd, `embyfin_logins_total{network="unknown",result="ok",server="emby",week="1999-12-27"} 3`) || has(odd, "embyfin_user_logins_total") {
		t.Errorf("sign-ins from nowhere known read:\n%s", odd)
	}
	if never := write(t, &fixed{Kind: "emby", Sessions: answered([]poll.Session{})}); has(never, "embyfin_logins_total") {
		t.Errorf("a server never asked for its sign-ins has some on its page:\n%s", never)
	}
}

// The plugins a server has: how many, each with the version installed, and each a newer version is on offer for with that version beside it. A
// server whose catalogue did not answer still lists its plugins and does not say how many are behind, which is not the same as none; one with none
// behind says 0. A plugin a server lists twice has one line, since two the same would lose the whole page.
func TestPlugins(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_plugins{server="emby"} 3`,
		`embyfin_plugins_update_available{server="emby"} 2`,
		`embyfin_plugin_info{plugin="Backup & Restore",server="emby",version="1.8.6.0"} 1`,
		`embyfin_plugin_info{plugin="Playback Reporting",server="emby",version="2.1.0.7"} 1`,
		`embyfin_plugin_update_available{latest="1.8.7.0",plugin="Backup & Restore",server="emby",version="1.8.6.0"} 1`,
		`embyfin_plugin_update_available{latest="1.0.39.0",plugin="Webhooks",server="emby",version="1.0.38.0"} 1`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
	if has(got, `embyfin_plugin_update_available{latest="",`) || has(got, `embyfin_plugin_update_available{latest="2.1.0.7"`) {
		t.Errorf("a plugin with nothing newer on offer has a line saying one is:\n%s", got)
	}

	twice := []poll.Plugin{{Name: "Reports", Version: "19.0.0.0"}, {Name: "Reports", Version: "19.0.0.0"}, {Name: "Reports", Version: "18.0.0.0"}}

	unasked := write(t, &fixed{Kind: "jellyfin", Plugins: answered(poll.Plugins{Installed: twice})})
	if !has(unasked, `embyfin_plugins{server="jellyfin"} 2`) || !has(unasked, `embyfin_plugin_info{plugin="Reports",server="jellyfin",version="18.0.0.0"} 1`) || has(unasked, "embyfin_plugins_update_available") || has(unasked, "embyfin_plugin_update_available") {
		t.Errorf("a server whose catalogue did not answer, listing one plugin twice, reads:\n%s", unasked)
	}

	current := write(t, &fixed{Kind: "jellyfin", Plugins: answered(poll.Plugins{Checked: true, Installed: twice})})
	if !has(current, `embyfin_plugins_update_available{server="jellyfin"} 0`) || has(current, "embyfin_plugin_update_available") {
		t.Errorf("a server with every plugin current reads:\n%s", current)
	}

	if never := write(t, &fixed{Kind: "emby", Sessions: answered([]poll.Session{})}); has(never, "embyfin_plugin") {
		t.Errorf("a server never asked for its plugins has some on its page:\n%s", never)
	}
}

// Who is streaming now: each session playing or paused is on the page under the server's id for it, with whose it is, what it plays and on what
// for a server asked to name them, how fast it is being sent, how far through it is and how long the whole is. Beside them, how many streams are
// at home and how many remote with what each lot comes to, and which device and app each reason for a stream belongs to.
func TestWhoIsStreamingNow(t *testing.T) {
	t.Parallel()

	got := write(t, emby())
	for _, want := range []string{
		`embyfin_session_info{client="Den Player",device="the television in the den",method="transcode",network="remote",reasons="container_bitrate_exceeds_limit",server="emby",session="0a",state="playing",title="Dark - S01E02 - Lies",type="episode",user="ada"} 1`,
		`embyfin_session_info{client="Pocket Player",device="the phone in the hall",method="transcode",network="home",reasons="audio_codec_not_supported,container_bitrate_exceeds_limit",server="emby",session="0b",state="playing",title="Heat (1995)",type="movie",user="grace"} 1`,
		`embyfin_session_info{client="Shelf Player",device="the speaker on the shelf",method="direct_play",network="home",reasons="",server="emby",session="0c",state="paused",title="Pink Floyd - Breathe",type="track",user="ada"} 1`,
		`embyfin_session_bits_per_second{server="emby",session="0a"} 8e+06`,
		`embyfin_session_bits_per_second{server="emby",session="0c"} 320000`,
		`embyfin_session_position_seconds{server="emby",session="0a"} 600`,
		`embyfin_session_position_seconds{server="emby",session="0b"} 0`,
		`embyfin_session_runtime_seconds{server="emby",session="0a"} 3000`,
		`embyfin_session_runtime_seconds{server="emby",session="0c"} 240`,
		`embyfin_streams_by_network{network="home",server="emby"} 2`,
		`embyfin_streams_by_network{network="remote",server="emby"} 1`,
		`embyfin_streams_by_network{network="unknown",server="emby"} 0`,
		`embyfin_streams_bits_per_second{network="home",server="emby"} 4.82e+06`,
		`embyfin_streams_bits_per_second{network="remote",server="emby"} 8e+06`,
		`embyfin_client_transcode_reasons{client="Den Player",device="the television in the den",reason="container_bitrate_exceeds_limit",server="emby"} 1`,
		`embyfin_client_transcode_reasons{client="Pocket Player",device="the phone in the hall",reason="audio_codec_not_supported",server="emby"} 1`,
		`embyfin_client_transcode_reasons{client="Pocket Player",device="the phone in the hall",reason="container_bitrate_exceeds_limit",server="emby"} 1`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}

	// the two with nothing playing are counted and no more than that
	if n := len(scraped(got, "embyfin_session_info")); n != 3 {
		t.Errorf("the page has %d sessions on it one by one, want the three playing or paused", n)
	}
}

// A session is on the page one by one only when it can be told from the rest: one with no id is still counted and has no reading of its own, and
// of two with one id only the first has, since two readings with the same labels would lose the whole page. What a server does not say of a
// session, how fast it is sent or how long the thing is, is left off and not written as 0.
func TestASessionThatCannotBeToldApart(t *testing.T) {
	t.Parallel()

	got := write(t, &fixed{Kind: "emby", Sessions: answered([]poll.Session{
		{State: poll.Playing, Method: poll.DirectPlay, Kind: poll.Movie, Network: poll.Home},
		{ID: "twin", State: poll.Playing, Method: poll.DirectPlay, Kind: poll.Movie, Network: poll.Home, Position: time.Minute},
		{ID: "twin", State: poll.Paused, Method: poll.Transcode, Kind: poll.Episode, Network: poll.Remote, Outgoing: 5, Runtime: time.Hour},
		{ID: "odd", State: poll.Playing, Method: "remux", Kind: "game", Network: "the moon"},
	})})

	for _, want := range []string{
		`embyfin_sessions{server="emby",state="playing"} 3`,
		`embyfin_streams_by_network{network="home",server="emby"} 2`,
		`embyfin_streams_by_network{network="unknown",server="emby"} 1`,
		`embyfin_session_info{client="",device="",method="direct_play",network="home",reasons="",server="emby",session="twin",state="playing",title="",type="movie",user=""} 1`,
		`embyfin_session_position_seconds{server="emby",session="twin"} 60`,
		// what the server says in words not known here is unknown and other one by one too, as it is in the counts
		`embyfin_session_info{client="",device="",method="unknown",network="unknown",reasons="",server="emby",session="odd",state="playing",title="",type="other",user=""} 1`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
	if n := len(scraped(got, "embyfin_session_info")); n != 2 {
		t.Errorf("the page has %d sessions on it one by one, want the two that can be told apart:\n%s", n, got)
	}
	for _, absent := range []string{"embyfin_session_bits_per_second", "embyfin_session_runtime_seconds"} {
		if has(got, absent) {
			t.Errorf("the page has a reading of %s that no server gave:\n%s", absent, got)
		}
	}
}

// A server without the Playback Reporting plugin says so and has no readings of what was played, which is not the same as nothing played. One
// that has not been asked yet says nothing of the plugin either way.
func TestAServerWithoutThePluginSaysSo(t *testing.T) {
	t.Parallel()

	got := write(t, &fixed{Kind: "jellyfin", History: answered(poll.History{})})
	if !has(got, `embyfin_playback_reporting_installed{server="jellyfin"} 0`) {
		t.Errorf("the page does not say the server lacks the plugin:\n%s", got)
	}
	for _, absent := range []string{"embyfin_playback_seconds_total", "embyfin_plays_total", "embyfin_short_plays_total", "embyfin_user_", "embyfin_title_", "embyfin_transcode_reason_", "embyfin_client_transcode_reason_"} {
		if has(got, absent) {
			t.Errorf("the page has a reading of %s from a server with no record of what was played", absent)
		}
	}

	if got := write(t, &fixed{Kind: "jellyfin", Sessions: answered([]poll.Session{})}); has(got, "embyfin_playback_reporting_installed") {
		t.Errorf("the page says whether a server never asked has the plugin:\n%s", got)
	}
}

// Two accounts of one name are one on the page, last seen when the later of them was, and an account never seen has no time to give.
func TestWhenEachAccountWasLastSeen(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		got := write(t, &fixed{Kind: "emby", Users: answered([]poll.User{
			{Name: "ada", LastActive: now.Add(-3 * time.Hour)},
			{Name: "ada", LastActive: now.Add(-time.Hour)},
			{Name: "grace"},
			{LastActive: now},
		})})

		var seen []string
		for _, line := range lines(got) {
			if strings.HasPrefix(line, "embyfin_user_last_seen") {
				seen = append(seen, line)
			}
		}
		if want := []string{`embyfin_user_last_seen_timestamp_seconds{server="emby",user="ada"} 9.466812e+08`}; strings.Join(seen, "\n") != strings.Join(want, "\n") {
			t.Errorf("the accounts were last seen\n%s\nwant\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
		}
	})
}

// Whether an account counts as active is measured when the page is written, not when the server was asked: an account last seen 23 hours ago
// stops being active within the day two hours later, with no poll in between.
func TestActiveIsMeasuredWhenThePageIsWritten(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		p := &fixed{Kind: "emby", Users: answered([]poll.User{{LastActive: time.Now().Add(-23 * time.Hour)}})}

		if got := write(t, p); !has(got, `embyfin_users_active{server="emby",window="1d"} 1`) {
			t.Errorf("an account seen 23 hours ago is not active within the day:\n%s", got)
		}

		// the account is asked after again in the meantime, and the server says the same of it
		time.Sleep(2 * time.Hour)
		p.Users = answered(p.Users.Value)
		got := write(t, p)
		if !has(got, `embyfin_users_active{server="emby",window="1d"} 0`) || !has(got, `embyfin_users_active{server="emby",window="7d"} 1`) {
			t.Errorf("two hours on, the account should be active within 7 days and no longer within one:\n%s", got)
		}
	})
}

// play is one play in the plugin's record, for the model below: whose it was, when it began on the server's own clock and how long it ran.
type play struct {
	user  string
	began time.Time
	ran   time.Duration
}

// monday is the first moment of the week t is in.
func monday(t time.Time) time.Time {
	year, month, date := t.Date()

	return time.Date(year, month, date-(int(t.Weekday())+6)%7, 0, 0, 0, 0, t.Location())
}

// asked is what a server whose plugin holds these plays answers at now, as the question put to the plugin has it: the plays begun in this week and
// the last, as far as each has got, added up under the Monday of the week it began in.
func asked(plays []play, now time.Time) poll.History {
	type key struct{ week, user string }

	sums := map[key]int{}
	for _, p := range plays {
		if p.began.After(now) || p.began.Before(monday(now).AddDate(0, 0, -7)) {
			continue
		}
		sums[key{monday(p.began).Format(time.DateOnly), p.user}] += int(min(now.Sub(p.began), p.ran).Seconds())
	}

	h := poll.History{Installed: true}
	for k, seconds := range sums {
		h.ByUser = append(h.ByUser, poll.Played{Week: k.week, User: k.user, Kind: poll.Episode, Plays: 1, Seconds: seconds})
	}

	return h
}

// scraped is each reading of one name on a page, by its labels.
func scraped(text, name string) map[string]float64 {
	out := map[string]float64{}
	for _, line := range lines(text) {
		// the value comes after the last space: a label may have spaces of its own
		at := strings.LastIndex(line, " ")
		if !strings.HasPrefix(line, name+"{") || at < 0 {
			continue
		}

		n, err := strconv.ParseFloat(line[at+1:], 64)
		if err != nil {
			panic(err)
		}
		out[strings.TrimPrefix(line[:at], name)] = n
	}

	return out
}

// A query for the time played over any stretch has to come out right whatever falls inside the stretch: the end of a week, the end of a month,
// a play running through both, the program being restarted. So the readings are scraped every half minute across the night of Sunday 31 May
// 2026, which ends a week and a month, and added up the way a query does: each reading's rises, every reading added together.
//
// Nothing may be counted twice and nothing dropped. The one thing a query can miss is what a reading that is new stood at when it was first read:
// 20 seconds into its first play here, and 3 minutes 20 for the play that began as the program stopped. A store that takes a new counter to have
// started from nothing does not miss that either. No reading ever goes down, so there is no start-again for a query to allow for.
func TestTheTimePlayedAddsUpAcrossAWeekEndAndARestart(t *testing.T) {
	t.Parallel()

	at := func(date, hour, minute int) time.Time {
		return time.Date(2026, time.May, date, hour, minute, 0, 0, time.UTC)
	}
	plays := []play{
		// the evening before, which is what ada's reading for the week stands at when the scraping starts
		{"ada", at(30, 20, 0), time.Hour},
		// through midnight, from the old week and month into the new
		{"ada", at(31, 22, 0), 3 * time.Hour},
		{"grace", at(31, 23, 30), time.Hour},
		// begun in the new week
		{"ada", at(32, 1, 30), time.Hour},
		{"grace", at(32, 0, 45), 90 * time.Minute},
	}
	const played = (3+1+1)*3600 + 90*60

	// the program is stopped at a quarter to one and back three minutes later, while three of the plays are running
	stopped, back := at(32, 0, 45), at(32, 0, 48)

	// scraped 20 seconds past each half minute, since nothing scrapes on the stroke of a play beginning
	start := at(31, 21, 0).Add(20 * time.Second)

	last, first := map[string]float64{}, map[string]float64{}
	var rises, begun float64
	for now := start; !now.After(at(32, 4, 0)); now = now.Add(30 * time.Second) {
		if now.After(stopped) && now.Before(back) {
			continue
		}

		// a page written by a program that knows nothing of the pages before it, as one just started does
		for labels, value := range scraped(write(t, &fixed{Kind: "emby", History: answered(asked(plays, now))}), "embyfin_user_playback_seconds_total") {
			before, seen := last[labels]
			switch {
			case now.Equal(start):
				// where a reading stood before the stretch began is no part of the stretch
			case !seen:
				first[labels] = value
				begun += value
			case value < before:
				t.Fatalf("at %s the reading %s went down from %v to %v", now.Format(time.DateTime), labels, before, value)
			default:
				rises += value - before
			}
			last[labels] = value
		}
	}

	// ada's reading for the old week was there from the start; grace's for it and both for the new week began part way through
	if len(last) != 4 || len(first) != 3 {
		t.Fatalf("the readings were %v, of which new part way through %v; want ada's and grace's for each of the two weeks, three of them new", last, first)
	}
	if rises+begun != played {
		t.Errorf("the rises come to %v seconds and what the new readings began at to %v, %v in all; want the %v played, no more and no less", rises, begun, rises+begun, played)
	}
	if begun != 20+20+200 {
		t.Errorf("the new readings began at %v seconds between them, want 20 for two of them and 200 for the one whose play began as the program stopped", begun)
	}

	// a week on, the week before last is off the page and last week's readings stand where they stopped
	later := scraped(write(t, &fixed{Kind: "emby", History: answered(asked(plays, at(39, 1, 0)))}), "embyfin_user_playback_seconds_total")
	if len(later) != 2 {
		t.Errorf("a week on the readings are %v, want only those of the week that began on 1 June", later)
	}
	for labels, value := range later {
		if value != last[labels] {
			t.Errorf("a week on %s reads %v, want the %v it stopped at", labels, value, last[labels])
		}
	}
}

// names is every reading's name on the page.
func names(text string) map[string]bool {
	out := map[string]bool{}
	for _, line := range lines(text) {
		name, _, _ := strings.Cut(line, "{")
		name, _, _ = strings.Cut(name, " ")
		out[name] = true
	}

	return out
}

// Skipping a group leaves its readings off the page, every one of them and nothing else, whether the server answered for them or not. The three
// groups that come from the sessions answer go each on its own.
func TestSkippedGroupsAreLeftOff(t *testing.T) {
	t.Parallel()

	groups := map[poll.Group][]string{
		poll.GroupPoll:      {"embyfin_poll_interval_seconds", "embyfin_poll_duration_seconds", "embyfin_poll_last_success_timestamp_seconds", "embyfin_poll_failures_total"},
		poll.GroupSessions:  {"embyfin_sessions", "embyfin_streams", "embyfin_streams_by_type", "embyfin_video_transcodes", "embyfin_streams_by_network", "embyfin_streams_bits_per_second"},
		poll.GroupReasons:   {"embyfin_transcode_reasons", "embyfin_transcode_bits_per_second", "embyfin_client_transcode_reasons"},
		poll.GroupStreaming: {"embyfin_session_info", "embyfin_session_bits_per_second", "embyfin_session_position_seconds", "embyfin_session_runtime_seconds"},
		poll.GroupTasks:     {"embyfin_scheduled_tasks", "embyfin_scheduled_tasks_failed", "embyfin_scheduled_task_last_run_failed", "embyfin_scheduled_task_last_run_timestamp_seconds", "embyfin_library_scan_running", "embyfin_library_scan_progress_ratio", "embyfin_library_scan_last_run_ok", "embyfin_library_scan_last_run_timestamp_seconds", "embyfin_library_scan_last_run_duration_seconds"},
		poll.GroupPlugins:   {"embyfin_plugins", "embyfin_plugin_info", "embyfin_plugins_update_available", "embyfin_plugin_update_available"},
		poll.GroupLibraries: {"embyfin_libraries", "embyfin_library_items", "embyfin_items", "embyfin_library_items_added"},
		poll.GroupAccounts:  {"embyfin_users", "embyfin_users_disabled", "embyfin_users_active", "embyfin_user_last_seen_timestamp_seconds", "embyfin_devices", "embyfin_devices_active"},
		poll.GroupSignIns:   {"embyfin_logins_total", "embyfin_user_logins_total"},
		poll.GroupPlayed:    {"embyfin_playback_reporting_installed", "embyfin_playback_seconds_total", "embyfin_plays_total", "embyfin_user_playback_seconds_total", "embyfin_user_plays_total", "embyfin_title_playback_seconds_total", "embyfin_title_plays_total", "embyfin_short_plays_total", "embyfin_transcode_reason_seconds_total", "embyfin_transcode_reason_plays_total", "embyfin_client_transcode_reason_seconds_total", "embyfin_client_transcode_reason_plays_total"},
	}
	if len(groups) != len(poll.Groups) {
		t.Fatalf("%d groups checked, want all %d", len(groups), len(poll.Groups))
	}

	all := names(write(t, emby()))
	for group, listed := range groups {
		left := names(writeSkipping(t, poll.Skip{group: true}, emby()))

		var gone []string
		for name := range all {
			if !left[name] {
				gone = append(gone, name)
			}
		}
		slices.Sort(gone)

		want := slices.Sorted(slices.Values(listed))
		if !slices.Equal(gone, want) {
			t.Errorf("skipping %s left off %v, want %v", group, gone, want)
		}
	}
}
