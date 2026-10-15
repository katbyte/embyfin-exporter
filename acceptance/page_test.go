//go:build acceptance

package acceptance_test

import (
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
)

// A server that is there answers both rounds in full, and says what it is. The page says how often each round is asked: every minute for sessions
// and every five for the library, as the test's poller is told.
func TestTheServerIsUp(t *testing.T) {
	live(t)

	text := scrape(t)
	expect(t, text, map[string]float64{
		reading("embyfin_up"): 1,
		reading("embyfin_poll_failures_total", "poll", "sessions"):   0,
		reading("embyfin_poll_failures_total", "poll", "library"):    0,
		reading("embyfin_poll_interval_seconds", "poll", "sessions"): 60,
		reading("embyfin_poll_interval_seconds", "poll", "library"):  300,
	})

	for _, name := range []string{reading("embyfin_server_restart_pending"), reading("embyfin_server_update_available"), reading("embyfin_poll_duration_seconds", "poll", "sessions"), reading("embyfin_poll_duration_seconds", "poll", "library")} {
		if _, found := value(text, name); !found {
			t.Errorf("the page has no %s", name)
		}
	}

	for _, round := range poll.Rounds {
		if at, _ := value(text, reading("embyfin_poll_last_success_timestamp_seconds", "poll", string(round))); time.Since(time.Unix(int64(at), 0)) > time.Minute {
			t.Errorf("the %s round last succeeded at %v, want just now", round, time.Unix(int64(at), 0))
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

// Each library holds what the catalogue has, by kind, and the whole server is the libraries added together: a library of every type the page knows
// how to count, each asked only for the kinds its type holds.
//
// Where the servers differ, so does the page. Making a collection gives both a library of collections; a playlist gives Emby one of playlists, where
// Jellyfin keeps its playlists in no library. Emby counts a music library's artists; Jellyfin gives every artist it knows whichever library is asked
// after, so its are not counted. And an audiobook is an album of tracks on Emby, in a library of its own, and an audiobook on Jellyfin, in a library
// of books.
func TestWhatTheLibrariesHold(t *testing.T) {
	live(t)

	held := func(library, kind string) string {
		return reading("embyfin_library_items", "library", library, "type", kind)
	}
	all := func(kind string) string { return reading("embyfin_items", "type", kind) }

	want := map[string]float64{
		held(filmLibrary, "movie"):            films,
		held(showLibrary, "series"):           series,
		held(showLibrary, "season"):           seasons,
		held(showLibrary, "episode"):          episodes,
		held(musicLibrary, "album"):           albums,
		held(musicLibrary, "track"):           tracks,
		held(clipLibrary, "music_video"):      musicVideos,
		held(homeLibrary, "video"):            homeVideos,
		held(homeLibrary, "photo"):            photos,
		held(bookLibrary, "book"):             books,
		held(mixedLibrary, "movie"):           mixedFilms,
		held(mixedLibrary, "series"):          mixedSeries,
		held(mixedLibrary, "season"):          mixedSeasons,
		held(mixedLibrary, "episode"):         mixedEpisodes,
		held(mixedLibrary, "video"):           0,
		held(collectionLibrary, "collection"): 1,

		all("movie"):       films + mixedFilms,
		all("series"):      series + mixedSeries,
		all("season"):      seasons + mixedSeasons,
		all("episode"):     episodes + mixedEpisodes,
		all("music_video"): musicVideos,
		all("video"):       homeVideos,
		all("photo"):       photos,
		all("book"):        books,
		all("collection"):  1,
	}

	absent := []string{held(musicLibrary, "artist"), all("artist"), all("playlist"), held(audiobookLibrary, "album"), held(audiobookLibrary, "track")}
	if backend == emby {
		want[held(musicLibrary, "artist")] = artists
		want[held(audiobookLibrary, "album")] = audiobooks
		want[held(audiobookLibrary, "track")] = chapters
		want[held(playlistLibrary, "playlist")] = 1
		want[all("artist")] = artists
		want[all("album")] = albums + audiobooks
		want[all("track")] = tracks + chapters
		want[all("playlist")] = 1
		want[reading("embyfin_libraries")] = float64(len(libraries)) + 2

		absent = []string{all("audiobook"), held(bookLibrary, "audiobook"), held(audiobookLibrary, "audiobook")}
	} else {
		want[held(bookLibrary, "audiobook")] = 0
		want[held(audiobookLibrary, "book")] = 0
		want[held(audiobookLibrary, "audiobook")] = chapters
		want[all("album")] = albums
		want[all("track")] = tracks
		want[all("audiobook")] = chapters
		want[reading("embyfin_libraries")] = float64(len(libraries)) + 1
	}

	// the libraries of collections and of playlists appear a moment after the first of each is made
	text, _ := eventually(t, func(text string) bool {
		got, _ := value(text, reading("embyfin_libraries"))

		return got == want[reading("embyfin_libraries")]
	})
	expect(t, text, want)

	// a kind this server does not count, or does not count there, is not on the page at all, rather than there at 0
	for _, name := range absent {
		if got, found := value(text, name); found {
			t.Errorf("%s = %v, want no such reading on %s", name, got, backend)
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

// While the scan of every library runs the page says so and how far it has got, and once it ends the page says when and no longer how far. A scan
// of a catalogue this small is over in a moment, so it is asked for and watched up to five times, until one is caught running.
func TestAScanUnderWay(t *testing.T) {
	live(t)

	ended := func(text string) float64 {
		at, _ := value(text, reading("embyfin_library_scan_last_run_timestamp_seconds"))

		return at
	}

	var caught bool
	for attempt := 0; attempt < 5 && !caught; attempt++ {
		before := ended(scrape(t, poll.RoundSessions))
		if err := client.RefreshLibrary(t.Context()); err != nil {
			t.Fatalf("asking for a scan of every library: %v", err)
		}

		// until the scan has a newer end than it had, looking again every few milliseconds
		deadline := time.Now().Add(scanPatience)
		for {
			text := scrape(t, poll.RoundSessions)
			running, _ := value(text, reading("embyfin_library_scan_running"))
			progress, found := value(text, reading("embyfin_library_scan_progress_ratio"))

			if running == 1 {
				if !caught {
					t.Logf("caught the scan running at the %d. try, %.0f%% of the way through", attempt+1, 100*progress)
				}
				caught = true
				if !found || progress < 0 || progress > 1 {
					t.Errorf("the scan is running and its progress is %v (on the page: %t), want from 0 to 1", progress, found)
				}
			} else if found {
				t.Errorf("no scan is running and the page gives its progress as %v", progress)
			}

			if running == 0 && ended(text) > before {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the scan asked for did not end in %s:\n%s", scanPatience, text)
			}

			time.Sleep(20 * time.Millisecond)
		}
	}
	if !caught {
		t.Error("five scans ran and none was caught running")
	}

	// the last of them finished as it should, so the tests after this one find the server as they expect it
	expect(t, scrape(t, poll.RoundSessions), map[string]float64{reading("embyfin_library_scan_running"): 0, reading("embyfin_library_scan_last_run_ok"): 1})
}

// What is new in each library within a day, a week and a month. Everything in the catalogue was laid down minutes ago, so on Emby each library's
// films, episodes or tracks are all new in every window, and its shows, seasons and albums are not counted among them. Jellyfin cannot count what
// is new, and its page says nothing of it.
func TestWhatIsNewInEachLibrary(t *testing.T) {
	live(t)

	text := scrape(t, poll.RoundLibrary)

	if backend != emby {
		if lines := every(text, "embyfin_library_items_added"); len(lines) != 0 {
			t.Errorf("the page says what is new in a library of a server that cannot count it: %v", lines)
		}

		return
	}

	want := map[string]float64{}
	for library, n := range map[string]float64{filmLibrary: films, showLibrary: episodes, musicLibrary: tracks, clipLibrary: musicVideos, homeLibrary: homeVideos + photos, bookLibrary: books, audiobookLibrary: chapters, mixedLibrary: mixedFilms + mixedEpisodes} {
		for _, window := range []string{"1d", "7d", "30d"} {
			want[reading("embyfin_library_items_added", "library", library, "window", window)] = n
		}
	}
	expect(t, text, want)
}
