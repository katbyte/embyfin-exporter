// Package readings is what the pollers know of each server, as Prometheus readings for a metrics page to hold.
package readings

import (
	"slices"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/prometheus/client_golang/prometheus"
)

// Namespace starts every reading's name.
const Namespace = "embyfin"

// serverLabel says which server a reading is of: "emby" or "jellyfin".
const serverLabel = "server"

// bitsInAByte turns the bits a second the servers give a stream's bitrate in into the bytes a second the page gives it in.
const bitsInAByte = 8

// day is the unit the windows below are counted in.
const day = 24 * time.Hour

// windows is how recently an account or a device has to have been seen to count as active, under the label each is given.
var windows = []struct {
	label string
	span  time.Duration
}{{"1d", day}, {"7d", 7 * day}, {"30d", 30 * day}}

// all is every reading the page can hold, for Describe.
var all []*prometheus.Desc

// reading describes one reading of a server; labels are the ones it has beside the server's.
func reading(name, help string, labels ...string) *prometheus.Desc {
	d := prometheus.NewDesc(Namespace+"_"+name, help, append([]string{serverLabel}, labels...), nil)
	all = append(all, d)

	return d
}

var (
	up              = reading("up", "Whether the server answered the last time it was asked for its sessions (1) or not (0).")
	pollDuration    = reading("poll_duration_seconds", "How long the last poll of the server took, by which: sessions or library.", "poll")
	pollLastSuccess = reading("poll_last_success_timestamp_seconds", "When a poll of the server was last answered in full, in seconds since 1970.", "poll")
	pollFailures    = reading("poll_failures_total", "How many polls of the server have failed since embyfin-metrics started.", "poll")

	serverInfo      = reading("server_info", "Always 1, with the server's name, its version and the version of its API the client was built from.", "name", "version", "sdk_api_version")
	restartPending  = reading("server_restart_pending", "Whether the server is waiting to be restarted (1) or not (0).")
	updateAvailable = reading("server_update_available", "Whether the server says a newer version of itself is available (1) or not (0).")

	sessions         = reading("sessions", "Devices connected to the server, by what they are doing.", "state")
	streams          = reading("streams", "Sessions playing or paused, by how what they play reaches the device.", "method")
	streamsByType    = reading("streams_by_type", "Sessions playing or paused, by the kind of thing they play.", "type")
	videoTranscodes  = reading("video_transcodes", "Sessions whose picture the server is re-encoding, by what does the work.", "encoder")
	transcodeReasons = reading("transcode_reasons", "Sessions the server is making a stream for, by each reason it gives for not sending the file as it is. A session with two reasons is under both.", "reason")
	transcodeBytes   = reading("transcode_bytes_per_second", "How many bytes a second the streams the server is making come to between them: their bitrates added together, over 8.")

	scheduledTasks       = reading("scheduled_tasks", "The server's scheduled tasks, by what they are doing.", "state")
	scheduledTasksFailed = reading("scheduled_tasks_failed", "Scheduled tasks whose last run failed.")
	taskLastFailed       = reading("scheduled_task_last_run_failed", "Whether the last run of each scheduled task that has run failed (1) or not (0).", "task")
	taskLastEnd          = reading("scheduled_task_last_run_timestamp_seconds", "When the last run of each scheduled task ended, in seconds since 1970.", "task")
	scanRunning          = reading("library_scan_running", "Whether the scan of every library is running (1) or not (0).")
	scanProgress         = reading("library_scan_progress_ratio", "How far the scan of every library has got, from 0 to 1, while it runs.")
	scanLastOK           = reading("library_scan_last_run_ok", "Whether the last scan of every library finished as it should (1) or not (0).")
	scanLastEnd          = reading("library_scan_last_run_timestamp_seconds", "When the last scan of every library ended, in seconds since 1970.")
	scanLastTook         = reading("library_scan_last_run_duration_seconds", "How long the last scan of every library took.")

	libraries    = reading("libraries", "How many libraries the server has.")
	libraryItems = reading("library_items", "Items in each library, by kind.", "library", "type")
	items        = reading("items", "Items in every library of the server added together, by kind.", "type")

	users         = reading("users", "How many accounts the server has.")
	usersDisabled = reading("users_disabled", "Accounts that have been disabled.")
	usersActive   = reading("users_active", "Accounts seen within the window: the last day, 7 days or 30 days.", "window")
	devices       = reading("devices", "How many devices the server knows of, which is every one it has ever seen.")
	devicesActive = reading("devices_active", "Devices seen within the window: the last day, 7 days or 30 days.", "window")
)

// Source is where one server's state comes from: a poller, or a test's own.
type Source interface {
	State() poll.State
}

// Readings writes what each source knows as readings. It asks no server anything, so a scrape is answered from memory however slow a server is.
type Readings struct {
	sources []Source
}

// New is the readings of these servers.
func New(sources ...Source) *Readings {
	return &Readings{sources: sources}
}

// Describe names every reading there can be.
func (*Readings) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range all {
		ch <- d
	}
}

// Collect writes every reading there is an answer for. A server that has never answered a question has no reading of it, which is not the same as a
// reading of zero.
func (r *Readings) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	for _, source := range r.sources {
		s := source.State()
		w := writer{ch: ch, server: s.Kind}

		w.rounds(&s)
		w.info(&s)
		w.sessions(&s)
		w.tasks(&s)
		w.libraries(&s)
		w.users(&s, now)
		w.devices(&s, now)
	}
}

// writer writes one server's readings.
type writer struct {
	ch     chan<- prometheus.Metric
	server string
}

// gauge writes one reading that can go up and down.
func (w writer) gauge(d *prometheus.Desc, value float64, labels ...string) {
	w.ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, value, append([]string{w.server}, labels...)...)
}

// count is gauge for a whole number.
func (w writer) count(d *prometheus.Desc, n int, labels ...string) {
	w.gauge(d, float64(n), labels...)
}

// flag is gauge for a yes or no: 1 or 0.
func (w writer) flag(d *prometheus.Desc, yes bool, labels ...string) {
	if yes {
		w.gauge(d, 1, labels...)
		return
	}

	w.gauge(d, 0, labels...)
}

// at is gauge for a moment, in seconds since 1970.
func (w writer) at(d *prometheus.Desc, t time.Time, labels ...string) {
	w.gauge(d, float64(t.UnixMilli())/1000, labels...)
}

// rounds is how the polling itself is going, which is what says how far to trust the rest.
func (w writer) rounds(s *poll.State) {
	if s.SessionsRound.Ran {
		w.flag(up, s.Up())
	}

	for _, round := range poll.Rounds {
		r := s.Round(round)
		if !r.Ran {
			continue
		}

		w.gauge(pollDuration, r.Took.Seconds(), string(round))
		w.ch <- prometheus.MustNewConstMetric(pollFailures, prometheus.CounterValue, float64(r.Failures), w.server, string(round))
		if !r.LastSuccess.IsZero() {
			w.at(pollLastSuccess, r.LastSuccess, string(round))
		}
	}
}

func (w writer) info(s *poll.State) {
	if !s.Info.Known() {
		return
	}

	i := s.Info.Value
	w.gauge(serverInfo, 1, i.Name, i.Version, i.APIVersion)
	w.flag(restartPending, i.RestartPending)
	w.flag(updateAvailable, i.UpdateAvailable)
}

// sessions counts the devices by state and the playing ones by method and by kind. Every state, method and kind is written, at 0 when there is
// none, so a reading does not vanish when the last stream stops. The reasons are the servers' own and there is no saying which there may be, so a
// reason is on the page only while a stream has it.
func (w writer) sessions(s *poll.State) {
	if !s.Sessions.Known() {
		return
	}

	states, methods, kinds, reasons := map[poll.SessionState]int{}, map[poll.Method]int{}, map[poll.Kind]int{}, map[string]int{}
	var hardware, software, bitrate int
	for _, session := range s.Sessions.Value {
		if session.State != poll.Playing && session.State != poll.Paused {
			states[poll.Idle]++
			continue
		}
		states[session.State]++

		if slices.Contains(poll.Methods, session.Method) {
			methods[session.Method]++
		} else {
			methods[poll.UnknownMethod]++
		}

		if slices.Contains(poll.Kinds, session.Kind) {
			kinds[session.Kind]++
		} else {
			kinds[poll.OtherKind]++
		}

		if session.VideoTranscode && session.Hardware {
			hardware++
		} else if session.VideoTranscode {
			software++
		}

		bitrate += session.Bitrate
		for _, reason := range session.Reasons {
			reasons[reason]++
		}
	}

	for _, state := range poll.SessionStates {
		w.count(sessions, states[state], string(state))
	}
	for _, method := range poll.Methods {
		w.count(streams, methods[method], string(method))
	}
	for _, kind := range poll.Kinds {
		w.count(streamsByType, kinds[kind], string(kind))
	}
	w.count(videoTranscodes, hardware, "hardware")
	w.count(videoTranscodes, software, "software")
	w.gauge(transcodeBytes, float64(bitrate)/bitsInAByte)
	for reason, n := range reasons {
		w.count(transcodeReasons, n, reason)
	}
}

func (w writer) tasks(s *poll.State) {
	if !s.Tasks.Known() {
		return
	}

	states := map[poll.TaskState]int{}
	var failed int
	var scan *poll.Task
	for i := range s.Tasks.Value {
		task := &s.Tasks.Value[i]
		if slices.Contains(poll.TaskStates, task.State) {
			states[task.State]++
		} else {
			states[poll.TaskIdle]++
		}
		if task.LastRun != nil && task.LastRun.Failed {
			failed++
		}
		if task.LibraryScan {
			scan = task
		}
	}

	for _, state := range poll.TaskStates {
		w.count(scheduledTasks, states[state], string(state))
	}
	w.count(scheduledTasksFailed, failed)
	w.lastRuns(s.Tasks.Value)

	if scan == nil {
		return
	}

	running := scan.State == poll.TaskRunning || scan.State == poll.TaskCancelling
	w.flag(scanRunning, running)
	if running {
		w.gauge(scanProgress, scan.Progress)
	}

	run := scan.LastRun
	if run == nil {
		return
	}
	w.flag(scanLastOK, run.OK)
	if !run.Ended.IsZero() {
		w.at(scanLastEnd, run.Ended)
	}
	if run.Took > 0 {
		w.gauge(scanLastTook, run.Took.Seconds())
	}
}

// lastRuns says of each task that has run whether its last run failed and when it ended, under the task's name. Two tasks of one name are one on
// the page, failed if either did and ended when the later one did: two readings with the same labels would spoil the whole page.
func (w writer) lastRuns(tasks []poll.Task) {
	byName := map[string]poll.Run{}
	for i := range tasks {
		task := &tasks[i]
		if task.Name == "" || task.LastRun == nil {
			continue
		}

		run := *task.LastRun
		if other, seen := byName[task.Name]; seen {
			run.Failed = run.Failed || other.Failed
			if other.Ended.After(run.Ended) {
				run.Ended = other.Ended
			}
		}
		byName[task.Name] = run
	}

	for name, run := range byName {
		w.flag(taskLastFailed, run.Failed, name)
		if !run.Ended.IsZero() {
			w.at(taskLastEnd, run.Ended, name)
		}
	}
}

// libraries counts the libraries, what each holds, and every library's added together. The server's own total is not asked for: it counts what is
// in no library too, such as the theme songs and extras kept beside a film.
func (w writer) libraries(s *poll.State) {
	if !s.Libraries.Known() {
		return
	}

	w.count(libraries, len(s.Libraries.Value))

	// two libraries of one name are added together: two readings with the same labels would spoil the whole page
	byName, total := map[string]map[string]int{}, map[string]int{}
	for _, library := range s.Libraries.Value {
		if byName[library.Name] == nil {
			byName[library.Name] = map[string]int{}
		}
		for kind, n := range library.Items {
			byName[library.Name][kind] += n
			total[kind] += n
		}
	}

	for name, kinds := range byName {
		for kind, n := range kinds {
			w.count(libraryItems, n, name, kind)
		}
	}
	for kind, n := range total {
		w.count(items, n, kind)
	}
}

// users counts the accounts, and those seen within each window of now. The windows are measured as the page is written, so an account stops
// counting as active on time even between two rounds.
func (w writer) users(s *poll.State, now time.Time) {
	if !s.Users.Known() {
		return
	}

	w.count(users, len(s.Users.Value))

	var disabled int
	for _, user := range s.Users.Value {
		if user.Disabled {
			disabled++
		}
	}
	w.count(usersDisabled, disabled)

	seen := make([]time.Time, 0, len(s.Users.Value))
	for _, user := range s.Users.Value {
		seen = append(seen, user.LastActive)
	}
	w.active(usersActive, seen, now)
}

// devices counts the devices, and those seen within each window of now. A server keeps every device it has ever seen, so the windows say more of
// how many are in use than the count does.
func (w writer) devices(s *poll.State, now time.Time) {
	if !s.Devices.Known() {
		return
	}

	w.count(devices, len(s.Devices.Value))

	seen := make([]time.Time, 0, len(s.Devices.Value))
	for _, device := range s.Devices.Value {
		seen = append(seen, device.LastActive)
	}
	w.active(devicesActive, seen, now)
}

// active counts how many of those times fall within each window of now. One that was never seen has no time and is in none.
func (w writer) active(d *prometheus.Desc, seen []time.Time, now time.Time) {
	for _, window := range windows {
		var active int
		for _, at := range seen {
			if !at.IsZero() && now.Sub(at) <= window.span {
				active++
			}
		}
		w.count(d, active, window.label)
	}
}
