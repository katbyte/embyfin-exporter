package readings_test

import (
	"flag"
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/katbyte/embyfin-metrics/lib/readings"
	"github.com/katbyte/go-kt/pmetrics"
	"github.com/katbyte/go-kt/pointer"
	"github.com/prometheus/client_golang/prometheus/testutil/promlint"
)

// saved is where the page is kept as it was last agreed.
const saved = "testdata/page.txt"

const day = 24 * time.Hour

// update rewrites the saved page from what the code writes now: go test ./lib/readings -update. Read the difference before keeping it, since a
// reading that is renamed is one somebody's dashboard no longer finds.
var update = flag.Bool("update", false, "rewrite testdata/page.txt from the page as it is written now")

// fixed is a server whose state is whatever the test says.
type fixed poll.State

func (f fixed) State() poll.State { return poll.State(f) }

// answered is an answer given now.
func answered[T any](value T) poll.Answer[T] {
	return poll.Answer[T]{Value: value, At: time.Now()}
}

// emby is a server that has answered everything: five sessions of which three are playing or paused and two are streams the server is making, a
// scan a quarter of the way through, a task that failed and one that was cancelled, three libraries, four accounts last seen an hour, three days and
// twenty days ago and never, the last of them disabled, and seven devices of which four were seen within the year.
func emby() fixed {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	return fixed{
		Kind: "emby",
		Info: answered(poll.Info{Name: "nas", Version: "4.10.0.4", APIVersion: "4.10.0.40", RestartPending: true}),
		Sessions: answered([]poll.Session{
			{State: poll.Playing, Method: poll.Transcode, Kind: poll.Episode, VideoTranscode: true, Hardware: true, Bitrate: 8000000, Reasons: []string{"container_bitrate_exceeds_limit"}},
			{State: poll.Playing, Method: poll.Transcode, Kind: poll.Movie, Bitrate: 4500000, Reasons: []string{"audio_codec_not_supported", "container_bitrate_exceeds_limit"}},
			{State: poll.Paused, Method: poll.DirectPlay, Kind: poll.Track},
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
			{Name: "Movies", Items: map[string]int{"movie": 1200}},
			{Name: "Kids", Items: map[string]int{"movie": 300}},
			{Name: "TV", Items: map[string]int{"series": 80, "season": 400, "episode": 9000}},
		}),
		Users:   answered([]poll.User{{LastActive: ago(time.Hour)}, {LastActive: ago(3 * day)}, {LastActive: ago(20 * day)}, {Disabled: true}}),
		Devices: answered([]poll.Device{{LastActive: ago(time.Hour)}, {LastActive: ago(2 * day)}, {LastActive: ago(10 * day)}, {LastActive: ago(40 * day)}, {LastActive: ago(400 * day)}, {}, {}}),

		SessionsRound: poll.RoundState{Ran: true, OK: true, Took: 250 * time.Millisecond, LastSuccess: now},
		LibraryRound:  poll.RoundState{Ran: true, OK: true, Took: 3 * time.Second, LastSuccess: now.Add(-time.Minute), Failures: 2},
	}
}

// down is a server that has been asked once and did not answer.
func down() fixed {
	return fixed{Kind: "jellyfin", SessionsRound: poll.RoundState{Ran: true, Took: 15 * time.Second, Failures: 1}}
}

// write is the page of these servers' readings as a scrape reads it: the readings, and the one line the page adds to say which build wrote it.
func write(t *testing.T, sources ...readings.Source) string {
	t.Helper()

	p, err := pmetrics.New(pmetrics.Options{Name: "embyfin-metrics", Namespace: readings.Namespace}, readings.New(sources...))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := p.Write(&out); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

// lines is the page's readings without the comments above each, and without the one line that changes with the Go it was built by.
func lines(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "embyfin_metrics_build_info") {
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
		got := write(t, emby(), down())
		got = strings.Replace(got, runtime.Version(), "go", 1)

		if pointer.From(update) {
			if err := os.WriteFile(saved, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		want, err := os.ReadFile(saved)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("the page is not the one saved in %s; if the change is meant, run go test ./lib/readings -update and read the difference\n%s", saved, got)
		}
	})
}

// The names follow the conventions Prometheus checks for: a unit at the end, _total on what only goes up, help on everything. The one exception
// is a bitrate, which the check would have in bytes: it is in bits a second here, as both servers and every player give one.
func TestThePageFollowsTheNamingConventions(t *testing.T) {
	t.Parallel()

	problems, err := promlint.New(strings.NewReader(write(t, emby(), down()))).Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		if p.Metric == "embyfin_transcode_bits_per_second" && strings.Contains(p.Text, `instead of "bits"`) {
			continue
		}

		t.Errorf("%s: %s", p.Metric, p.Text)
	}
}

// A server that has never been asked has no readings at all, and one that was asked and did not answer has only how the asking went: a reading
// of zero sessions would say something nobody knows.
func TestAServerThatNeverAnsweredHasNoReadingsOfItsState(t *testing.T) {
	t.Parallel()

	if got := lines(write(t, fixed{Kind: "emby"})); len(got) != 0 {
		t.Errorf("a server never asked has readings: %v", got)
	}

	got := write(t, down())
	want := []string{
		`embyfin_poll_duration_seconds{poll="sessions",server="jellyfin"} 15`,
		`embyfin_poll_failures_total{poll="sessions",server="jellyfin"} 1`,
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

	got := write(t, fixed{Kind: "emby", Sessions: answered([]poll.Session{}), Tasks: answered([]poll.Task{})})
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
		`embyfin_scheduled_tasks{server="emby",state="running"} 0`,
		`embyfin_scheduled_tasks_failed{server="emby"} 0`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s", want)
		}
	}

	// with no scan among the tasks there is nothing to say of one, nor of a task with none listed or a reason with no stream
	for _, absent := range []string{"embyfin_library_scan", "embyfin_scheduled_task_last_run", "embyfin_transcode_reasons"} {
		if has(got, absent) {
			t.Errorf("the page has a reading of %s with nothing to report:\n%s", absent, got)
		}
	}
}

// A session whose server does not say how it plays, or says something new, is counted as unknown rather than left out of the streams, and a state
// that is neither playing nor paused is idle: the counts always add up to the sessions there are.
func TestWhatTheServerDoesNotSayIsStillCounted(t *testing.T) {
	t.Parallel()

	got := write(t, fixed{
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
		got := write(t, fixed{Kind: "emby", Tasks: answered([]poll.Task{
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
		got := write(t, fixed{Kind: "emby", Tasks: answered([]poll.Task{{Name: "Scan media library", State: state, Progress: 0.4, LibraryScan: true}})})
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

// Two readings with the same labels would lose the whole page, so two libraries of one name are added together.
func TestTwoLibrariesOfOneNameAreAddedTogether(t *testing.T) {
	t.Parallel()

	got := write(t, fixed{Kind: "emby", Libraries: answered([]poll.Library{
		{Name: "Films", Items: map[string]int{"movie": 10}},
		{Name: "Films", Items: map[string]int{"movie": 5, "video": 1}},
	})})
	for _, want := range []string{
		`embyfin_libraries{server="emby"} 2`,
		`embyfin_library_items{library="Films",server="emby",type="movie"} 15`,
		`embyfin_library_items{library="Films",server="emby",type="video"} 1`,
		`embyfin_items{server="emby",type="movie"} 15`,
	} {
		if !has(got, want) {
			t.Errorf("the page lacks %s:\n%s", want, got)
		}
	}
}

// Whether an account counts as active is measured when the page is written, not when the server was asked: an account last seen 23 hours ago
// stops being active within the day two hours later, with no poll in between.
func TestActiveIsMeasuredWhenThePageIsWritten(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		p := fixed{Kind: "emby", Users: answered([]poll.User{{LastActive: time.Now().Add(-23 * time.Hour)}})}

		if got := write(t, p); !has(got, `embyfin_users_active{server="emby",window="1d"} 1`) {
			t.Errorf("an account seen 23 hours ago is not active within the day:\n%s", got)
		}

		time.Sleep(2 * time.Hour)
		got := write(t, p)
		if !has(got, `embyfin_users_active{server="emby",window="1d"} 0`) || !has(got, `embyfin_users_active{server="emby",window="7d"} 1`) {
			t.Errorf("two hours on, the account should be active within 7 days and no longer within one:\n%s", got)
		}
	})
}
