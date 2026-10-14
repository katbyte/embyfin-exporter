// Package readings is what the pollers know of each server, as Prometheus readings for a metrics page to hold.
package readings

import (
	"slices"
	"strings"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/prometheus/client_golang/prometheus"
)

// Namespace starts every reading's name.
const Namespace = "embyfin"

// serverLabel says which server a reading is of: "emby" or "jellyfin".
const serverLabel = "server"

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
	pollDuration    = reading("poll_duration_seconds", "How long the last poll of the server took, by which: sessions, library or history.", "poll")
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
	transcodeBitrate = reading("transcode_bits_per_second", "The bitrate of the streams the server is making, added together, in bits a second.")
	streamsByNetwork = reading("streams_by_network", "Sessions playing or paused, by where the device is: home on the server's own network, remote from outside it, or unknown.", "network")
	streamsBitrate   = reading("streams_bits_per_second", "The bitrate of what is playing as it leaves the server, added together, in bits a second, by where the devices are.", "network")
	clientReasons    = reading("client_transcode_reasons", "Sessions the server is making a stream for, by the device, its app and each reason the server gives. Only when the settings ask for devices to be named.", "device", "client", "reason")

	sessionInfo     = reading("session_info", "Always 1, for each session playing or paused: the kind of thing it plays, how it reaches the device, whether it is paused, whether the device is at home or remote, and why the server makes a stream of it when it does. Whose it is, what it plays and on what are filled in only when the settings ask.", "session", "user", "title", "type", "device", "client", "method", "state", "network", "reasons")
	sessionBitrate  = reading("session_bits_per_second", "How many bits a second what each session plays comes to as it leaves the server: the stream the server makes, or the file's own when it is sent as it is.", "session")
	sessionPosition = reading("session_position_seconds", "How far through what it is playing each session is.", "session")
	sessionRuntime  = reading("session_runtime_seconds", "How long the whole of what each session is playing is.", "session")

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
	libraryAdded = reading("library_items_added", "Items added to each library within the window: the last day, 7 days or 30 days. Films, episodes, tracks and the like, not the shows and albums that hold them. From Emby, which alone can count them.", "library", "window")
	items        = reading("items", "Items in every library of the server added together, by kind.", "type")

	users         = reading("users", "How many accounts the server has.")
	usersDisabled = reading("users_disabled", "Accounts that have been disabled.")
	usersActive   = reading("users_active", "Accounts seen within the window: the last day, 7 days or 30 days.", "window")
	userLastSeen  = reading("user_last_seen_timestamp_seconds", "When each account last did anything, in seconds since 1970. Only when the settings ask for accounts to be named.", "user")
	devices       = reading("devices", "How many devices the server knows of, which is every one it has ever seen.")
	devicesActive = reading("devices_active", "Devices seen within the window: the last day, 7 days or 30 days.", "window")
	logins        = reading("logins_total", "Times an account was signed in to (ok) and tries that were refused (failed), by the week they were made in, named by its Monday in UTC, and where they came from: home, remote or unknown. This week and the last are on the page.", "week", "result", "network")
	userLogins    = reading("user_logins_total", "The same sign-ins by account, with tries on a name the server has no account of under unknown. Only when the settings ask for accounts to be named.", "week", "user", "result")

	plugins        = reading("plugins", "How many plugins the server has installed.")
	pluginInfo     = reading("plugin_info", "Always 1, one for each plugin installed, with its version.", "plugin", "version")
	pluginsBehind  = reading("plugins_update_available", "Plugins the server's own catalogue has a newer version of than the one installed, that the server could run. Not on the page while the server cannot reach its catalogue.")
	pluginIsBehind = reading("plugin_update_available", "Always 1, one for each plugin a newer version is on offer for, with the version installed and the newest on offer.", "plugin", "version", "latest")

	historyInstalled = reading("playback_reporting_installed", "Whether the server has the Playback Reporting plugin, which is where the readings of what was played come from (1) or not (0).")
	playbackSeconds  = reading("playback_seconds_total", "Seconds played, by the kind of thing and how it reached the device, as the server's Playback Reporting plugin has them. Plays are kept under the week they began in, named by its Monday; this week and the last are on the page, and neither ever goes down.", "week", "type", "method")
	plays            = reading("plays_total", "Plays, by the week they began in, the kind of thing and how it reached the device. A play counts again each time how it reaches the device changes.", "week", "type", "method")
	userSeconds      = reading("user_playback_seconds_total", "Seconds played, by the week the plays began in, account and the kind of thing. Only when the settings ask for accounts to be named.", "week", "user", "type")
	userPlays        = reading("user_plays_total", "Plays, by the week they began in, account and the kind of thing. Only when the settings ask for accounts to be named.", "week", "user", "type")
	titleSeconds     = reading("title_playback_seconds_total", "Seconds played of each show and each film, by the week the plays began in. Only when the settings ask for titles to be named.", "week", "title", "type")
	titlePlays       = reading("title_plays_total", "Plays of each show and each film, by the week they began in. Only when the settings ask for titles to be named.", "week", "title", "type")
	shortPlays       = reading("short_plays_total", "Plays that ended within a minute of beginning, which is what one that failed looks like, by the week they began in, the kind of thing and how it reached the device. A play is counted five minutes after it stopped.", "week", "type", "method")
	reasonSeconds    = reading("transcode_reason_seconds_total", "Seconds played of streams the server made, by the week the plays began in and each reason the server gave for not sending the file as it is. A play with two reasons is under both. From Emby's plugin, which alone keeps the reasons.", "week", "reason")
	reasonPlays      = reading("transcode_reason_plays_total", "Plays the server made a stream for, by the week they began in and each reason it gave.", "week", "reason")
	clientSeconds    = reading("client_transcode_reason_seconds_total", "Seconds played of streams the server made, by the week the plays began in, the device, its app and each reason. Only when the settings ask for devices to be named.", "week", "device", "client", "reason")
	clientPlays      = reading("client_transcode_reason_plays_total", "Plays the server made a stream for, by the week they began in, the device, its app and each reason. Only when the settings ask for devices to be named.", "week", "device", "client", "reason")
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
		w.logins(&s)
		w.plugins(&s)
		w.history(&s)
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

// total writes one reading that only goes up, until it starts again from 0.
func (w writer) total(d *prometheus.Desc, n int, labels ...string) {
	w.ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(n), append([]string{w.server}, labels...)...)
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
		w.total(pollFailures, r.Failures, string(round))
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

// stream tells one device and app's reason for a stream from another's.
type stream struct{ device, client, reason string }

// sessions counts the devices by state and the playing ones by method, by kind and by where they are, and then says of each playing one what it
// is doing. Every state, method, kind and network is written, at 0 when there is none, so a reading does not vanish when the last stream stops.
// The reasons are the servers' own and there is no saying which there may be, so a reason is on the page only while a stream has it.
func (w writer) sessions(s *poll.State) {
	if !s.Sessions.Known() {
		return
	}

	states, methods, kinds, reasons := map[poll.SessionState]int{}, map[poll.Method]int{}, map[poll.Kind]int{}, map[string]int{}
	networks, outgoing, byClient := map[poll.Network]int{}, map[poll.Network]int{}, map[stream]int{}
	var hardware, software, bitrate int
	var playing []poll.Session
	for _, session := range s.Sessions.Value {
		if session.State != poll.Playing && session.State != poll.Paused {
			states[poll.Idle]++
			continue
		}
		states[session.State]++

		// what the server does not say, or says in a word not known here, is still counted
		if !slices.Contains(poll.Methods, session.Method) {
			session.Method = poll.UnknownMethod
		}
		if !slices.Contains(poll.Kinds, session.Kind) {
			session.Kind = poll.OtherKind
		}
		if !slices.Contains(poll.Networks, session.Network) {
			session.Network = poll.UnknownNetwork
		}
		methods[session.Method]++
		kinds[session.Kind]++
		networks[session.Network]++
		outgoing[session.Network] += session.Outgoing

		if session.VideoTranscode && session.Hardware {
			hardware++
		} else if session.VideoTranscode {
			software++
		}

		bitrate += session.Bitrate
		for _, reason := range session.Reasons {
			reasons[reason]++
			// a device has a name only when the settings ask for one
			if session.Device != "" || session.Client != "" {
				byClient[stream{session.Device, session.Client, reason}]++
			}
		}

		playing = append(playing, session)
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
	w.count(transcodeBitrate, bitrate)
	for reason, n := range reasons {
		w.count(transcodeReasons, n, reason)
	}
	for _, network := range poll.Networks {
		w.count(streamsByNetwork, networks[network], string(network))
		w.count(streamsBitrate, outgoing[network], string(network))
	}
	for by, n := range byClient {
		w.count(clientReasons, n, by.device, by.client, by.reason)
	}

	w.playing(playing)
}

// playing says of each session playing or paused what it is doing, under the server's own id for the session, which is what ties its readings
// together. A session with no id cannot be told from another and is left out, and so is a second one of the same id: two readings with the same
// labels would spoil the whole page. How fast it is being sent and how long it is are on the page only when the server says.
func (w writer) playing(sessions []poll.Session) {
	written := map[string]bool{}
	for i := range sessions {
		session := &sessions[i]
		if session.ID == "" || written[session.ID] {
			continue
		}
		written[session.ID] = true

		w.gauge(sessionInfo, 1, session.ID, session.User, session.Title, string(session.Kind), session.Device, session.Client, string(session.Method), string(session.State), string(session.Network), strings.Join(session.Reasons, ","))
		w.gauge(sessionPosition, session.Position.Seconds(), session.ID)
		if session.Outgoing > 0 {
			w.count(sessionBitrate, session.Outgoing, session.ID)
		}
		if session.Runtime > 0 {
			w.gauge(sessionRuntime, session.Runtime.Seconds(), session.ID)
		}
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
	byName, total, added := map[string]map[string]int{}, map[string]int{}, map[string]map[string]int{}
	for _, library := range s.Libraries.Value {
		if byName[library.Name] == nil {
			byName[library.Name] = map[string]int{}
		}
		for kind, n := range library.Items {
			byName[library.Name][kind] += n
			total[kind] += n
		}

		// only a server that can count what is new says anything of it
		if library.Added != nil && added[library.Name] == nil {
			added[library.Name] = map[string]int{}
		}
		for window, n := range library.Added {
			added[library.Name][window] += n
		}
	}

	for name, windows := range added {
		for window, n := range windows {
			w.count(libraryAdded, n, name, window)
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

	// an account has a name only when the settings ask for one. Two of one name are one on the page, seen when the later one was: two readings
	// with the same labels would spoil the whole page
	lastSeen := map[string]time.Time{}
	for _, user := range s.Users.Value {
		if user.Name != "" && user.LastActive.After(lastSeen[user.Name]) {
			lastSeen[user.Name] = user.LastActive
		}
	}
	for name, at := range lastSeen {
		w.at(userLastSeen, at, name)
	}
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
	for _, window := range poll.Windows {
		var active int
		for _, at := range seen {
			if !at.IsZero() && now.Sub(at) <= window.Span {
				active++
			}
		}
		w.count(d, active, window.Label)
	}
}

// history is what was played in this week and the last, for a server whose Playback Reporting plugin keeps a record of it. Each reading is the
// plugin's own sum of the plays begun in one week, so it comes back as it was after a restart and never goes down: a new week is a new reading
// and not the old one started again. Only what has been played is written: there is no saying which accounts or titles there may be.
func (w writer) history(s *poll.State) {
	if !s.History.Known() {
		return
	}

	h := s.History.Value
	w.flag(historyInstalled, h.Installed)

	for _, p := range h.Plays {
		w.total(playbackSeconds, p.Seconds, p.Week, string(p.Kind), string(p.Method))
		w.total(plays, p.Plays, p.Week, string(p.Kind), string(p.Method))
		// written at 0 beside the plays, so the first short one is a rise a query can see
		w.total(shortPlays, p.Short, p.Week, string(p.Kind), string(p.Method))
	}
	for _, p := range h.ByUser {
		w.total(userSeconds, p.Seconds, p.Week, p.User, string(p.Kind))
		w.total(userPlays, p.Plays, p.Week, p.User, string(p.Kind))
	}
	for _, p := range h.ByTitle {
		w.total(titleSeconds, p.Seconds, p.Week, p.Title, string(p.Kind))
		w.total(titlePlays, p.Plays, p.Week, p.Title, string(p.Kind))
	}
	for _, p := range h.ByReason {
		w.total(reasonSeconds, p.Seconds, p.Week, p.Reason)
		w.total(reasonPlays, p.Plays, p.Week, p.Reason)
	}
	for _, p := range h.ByClient {
		w.total(clientSeconds, p.Seconds, p.Week, p.Device, p.Client, p.Reason)
		w.total(clientPlays, p.Plays, p.Week, p.Device, p.Client, p.Reason)
	}
}

// The two ways a sign-in can go, as the page names them.
const (
	loginOK     = "ok"
	loginFailed = "failed"
)

// logins is how the signing in went in this week and the last: every sign-in and refused try by where it came from, and by account for a server
// asked to name accounts. As with what was played, each is under the week it was made in, so no reading ever goes down.
func (w writer) logins(s *poll.State) {
	if !s.Logins.Known() {
		return
	}

	type place struct{ week, result, network string }
	type account struct{ week, user, result string }

	byPlace, byAccount := map[place]int{}, map[account]int{}
	for _, login := range s.Logins.Value {
		result := loginOK
		if login.Failed {
			result = loginFailed
		}
		if !slices.Contains(poll.Networks, login.Network) {
			login.Network = poll.UnknownNetwork
		}

		byPlace[place{login.Week, result, string(login.Network)}] += login.Count
		if login.User != "" {
			byAccount[account{login.Week, login.User, result}] += login.Count
		}
	}

	for by, n := range byPlace {
		w.total(logins, n, by.week, by.result, by.network)
	}
	for by, n := range byAccount {
		w.total(userLogins, n, by.week, by.user, by.result)
	}
}

// plugins is what the server has installed, and which of it a newer version is on offer for. Which are behind is only said when the server's
// catalogue answered: without it none being behind is not known. A plugin listed twice has one line, since two the same would lose the whole page.
func (w writer) plugins(s *poll.State) {
	if !s.Plugins.Known() {
		return
	}

	type line struct{ name, version string }

	lines, behind := map[line]bool{}, 0
	for _, plugin := range s.Plugins.Value.Installed {
		at := line{plugin.Name, plugin.Version}
		if lines[at] {
			continue
		}
		lines[at] = true

		w.gauge(pluginInfo, 1, plugin.Name, plugin.Version)
		if plugin.Latest != "" {
			behind++
			w.gauge(pluginIsBehind, 1, plugin.Name, plugin.Version, plugin.Latest)
		}
	}

	w.count(plugins, len(lines))
	if s.Plugins.Value.Checked {
		w.count(pluginsBehind, behind)
	}
}
