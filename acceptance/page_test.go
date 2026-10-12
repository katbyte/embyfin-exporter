//go:build acceptance

package acceptance_test

import (
	"strings"
	"testing"
	"time"
)

// A server that is there answers both rounds in full, and says what it is.
func TestTheServerIsUp(t *testing.T) {
	live(t)

	text := scrape(t)
	expect(t, text, map[string]float64{
		reading("embyfin_up"): 1,
		reading("embyfin_poll_failures_total", "poll", "sessions"): 0,
		reading("embyfin_poll_failures_total", "poll", "library"):  0,
	})

	for _, name := range []string{reading("embyfin_server_restart_pending"), reading("embyfin_server_update_available"), reading("embyfin_poll_duration_seconds", "poll", "sessions"), reading("embyfin_poll_duration_seconds", "poll", "library")} {
		if _, found := value(text, name); !found {
			t.Errorf("the page has no %s", name)
		}
	}

	for _, poll := range []string{"sessions", "library"} {
		if at, _ := value(text, reading("embyfin_poll_last_success_timestamp_seconds", "poll", poll)); time.Since(time.Unix(int64(at), 0)) > time.Minute {
			t.Errorf("the %s round last succeeded at %v, want just now", poll, time.Unix(int64(at), 0))
		}
	}

	// the name and the version are the server's own to choose; that it gives both, and which API the client was built from, is what is checked
	var info string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "embyfin_server_info{") {
			info = line
		}
	}
	if !strings.Contains(info, `server="`+backend+`"`) || !strings.HasSuffix(info, " 1") || strings.Contains(info, `name=""`) || strings.Contains(info, `version=""`) || strings.Contains(info, `sdk_api_version=""`) {
		t.Errorf("the server says of itself %q, want a name, a version and an API version", info)
	}
}

// Each library holds what the catalogue has, by kind, and the whole server is the libraries added together. Making a collection gives both servers a
// library of collections; a playlist gives Emby one of playlists, where Jellyfin keeps its playlists in no library. Emby counts a music library's
// artists; Jellyfin gives every artist it knows whichever library is asked after, so its are not counted.
func TestWhatTheLibrariesHold(t *testing.T) {
	live(t)

	want := map[string]float64{
		reading("embyfin_library_items", "library", filmLibrary, "type", "movie"):        films,
		reading("embyfin_library_items", "library", showLibrary, "type", "series"):       series,
		reading("embyfin_library_items", "library", showLibrary, "type", "season"):       seasons,
		reading("embyfin_library_items", "library", showLibrary, "type", "episode"):      episodes,
		reading("embyfin_library_items", "library", musicLibrary, "type", "album"):       albums,
		reading("embyfin_library_items", "library", musicLibrary, "type", "track"):       tracks,
		reading("embyfin_library_items", "library", "Collections", "type", "collection"): 1,

		reading("embyfin_items", "type", "movie"):      films,
		reading("embyfin_items", "type", "series"):     series,
		reading("embyfin_items", "type", "season"):     seasons,
		reading("embyfin_items", "type", "episode"):    episodes,
		reading("embyfin_items", "type", "album"):      albums,
		reading("embyfin_items", "type", "track"):      tracks,
		reading("embyfin_items", "type", "collection"): 1,

		reading("embyfin_libraries"): 4,
	}
	if backend == "emby" {
		want[reading("embyfin_library_items", "library", musicLibrary, "type", "artist")] = artists
		want[reading("embyfin_library_items", "library", "Playlists", "type", "playlist")] = 1
		want[reading("embyfin_items", "type", "artist")] = artists
		want[reading("embyfin_items", "type", "playlist")] = 1
		want[reading("embyfin_libraries")] = 5
	}

	// the libraries of collections and of playlists appear a moment after the first of each is made
	text, _ := eventually(t, func(text string) bool {
		got, _ := value(text, reading("embyfin_libraries"))

		return got == want[reading("embyfin_libraries")]
	})
	expect(t, text, want)

	if backend == "jellyfin" {
		for _, name := range []string{reading("embyfin_library_items", "library", musicLibrary, "type", "artist"), reading("embyfin_items", "type", "artist"), reading("embyfin_items", "type", "playlist")} {
			if got, found := value(text, name); found {
				t.Errorf("%s = %v, want no such reading on jellyfin", name, got)
			}
		}
	}

	// a kind no library is of is not on the page at all, rather than there at 0
	for _, kind := range []string{"book", "audiobook", "photo", "video", "music_video"} {
		if got, found := value(text, reading("embyfin_items", "type", kind)); found {
			t.Errorf("the page counts %v of kind %s, which no library here is of", got, kind)
		}
	}
}

// The two accounts the server was set up with, neither of them disabled.
func TestTheAccounts(t *testing.T) {
	live(t)

	text := scrape(t)
	expect(t, text, map[string]float64{
		reading("embyfin_users"):          2,
		reading("embyfin_users_disabled"): 0,
	})

	for _, window := range []string{"1d", "7d", "30d"} {
		if got, found := value(text, reading("embyfin_users_active", "window", window)); !found || got > 2 {
			t.Errorf("accounts seen within %s = %v (on the page: %t), want 2 at most", window, got, found)
		}
	}
}

// Every scheduled task the server lists is counted in one state or another, and each that has run is on the page by name with how its last run
// went. The scan of every library, which the suite ran once, is reported as having finished as it should a moment ago, under its own readings and
// under its name.
func TestTheScheduledTasks(t *testing.T) {
	live(t)

	tasks, err := client.Tasks(t.Context())
	if err != nil {
		t.Fatalf("listing the tasks: %v", err)
	}

	text := scrape(t)

	var counted float64
	for _, state := range []string{"idle", "running", "cancelling"} {
		got, found := value(text, reading("embyfin_scheduled_tasks", "state", state))
		if !found {
			t.Errorf("the page has no count of tasks that are %s", state)
		}
		counted += got
	}
	if len(tasks) == 0 || counted != float64(len(tasks)) {
		t.Errorf("the page counts %v scheduled tasks, and the server lists %d", counted, len(tasks))
	}

	expect(t, text, map[string]float64{
		reading("embyfin_scheduled_tasks_failed"):   0,
		reading("embyfin_library_scan_last_run_ok"): 1,
	})

	if _, found := value(text, reading("embyfin_library_scan_running")); !found {
		t.Error("the page does not say whether the scan of every library is running")
	}
	if ended, _ := value(text, reading("embyfin_library_scan_last_run_timestamp_seconds")); time.Since(time.Unix(int64(ended), 0)) > time.Hour {
		t.Errorf("the last scan ended at %v, want within the hour", time.Unix(int64(ended), 0))
	}
	if took, found := value(text, reading("embyfin_library_scan_last_run_duration_seconds")); !found || took <= 0 || took > scanPatience.Seconds() {
		t.Errorf("the last scan took %vs (on the page: %t), want more than nothing and less than %s", took, found, scanPatience)
	}

	// how far a scan has got is only on the page while one runs
	running, _ := value(text, reading("embyfin_library_scan_running"))
	if _, found := value(text, reading("embyfin_library_scan_progress_ratio")); found != (running == 1) {
		t.Errorf("the scan is running: %v, and its progress is on the page: %t", running, found)
	}

	// one reading for each name among the tasks that have run, and the scan's among them
	ran := map[string]bool{}
	var scan string
	for i := range tasks {
		if tasks[i].LastExecutionResult != nil {
			ran[tasks[i].Name] = true
		}
		if tasks[i].IsLibraryScan() {
			scan = tasks[i].Name
		}
	}
	if got := every(text, "embyfin_scheduled_task_last_run_failed"); len(got) != len(ran) {
		t.Errorf("the page says how the last run went of %d tasks, and %d of the server's have run: %v", len(got), len(ran), got)
	}
	expect(t, text, map[string]float64{reading("embyfin_scheduled_task_last_run_failed", "task", scan): 0})
	if ended, _ := value(text, reading("embyfin_scheduled_task_last_run_timestamp_seconds", "task", scan)); time.Since(time.Unix(int64(ended), 0)) > time.Hour {
		t.Errorf("the task %q last ended at %v, want within the hour", scan, time.Unix(int64(ended), 0))
	}
}
