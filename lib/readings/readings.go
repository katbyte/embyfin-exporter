// Package readings is what the pollers know of each server, as Prometheus readings for a metrics page to hold.
package readings

import (
	"slices"
	"strings"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/go-kt/prometheus/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// Namespace starts every reading's name.
const Namespace = "embyfin"

// serverLabel says which server a reading is of: "emby" or "jellyfin".
const serverLabel = "server"

// list is every reading the page can hold, each of one server.
var list = metrics.NewList(Namespace, serverLabel)

var (
	up = list.Reading("up", "Whether the server answered the last time it was asked for its own state (1) or not (0).")

	serverInfo      = list.Reading("server_info", "Always 1, with the server's name, its version and the version of its API the client was built from.", "name", "version", "sdk_api_version")
	restartPending  = list.Reading("server_restart_pending", "Whether the server is waiting to be restarted (1) or not (0).")
	updateAvailable = list.Reading("server_update_available", "Whether the server says a newer version of itself is available (1) or not (0).")

	sessions         = list.Reading("sessions", "Devices connected to the server, by what they are doing.", "state")
	streams          = list.Reading("streams", "Sessions playing or paused, by how what they play reaches the device.", "method")
	streamsByType    = list.Reading("streams_by_type", "Sessions playing or paused, by the kind of thing they play.", "type")
	videoTranscodes  = list.Reading("video_transcodes", "Sessions whose picture the server is re-encoding, by what does the work.", "encoder")
	transcodeReasons = list.Reading("transcode_reasons", "Sessions the server is making a stream for, by each reason it gives for not sending the file as it is. A session with two reasons is under both.", "reason")
	transcodeBitrate = list.Reading("transcode_bits_per_second", "The bitrate of the streams the server is making, added together, in bits a second.")
	streamsByNetwork = list.Reading("streams_by_network", "Sessions playing or paused, by where the device is: home on the server's own network, remote from outside it, or unknown.", "network")
	streamsBitrate   = list.Reading("streams_bits_per_second", "The bitrate of what is playing as it leaves the server, added together, in bits a second, by where the devices are.", "network")
	clientReasons    = list.Reading("client_transcode_reasons", "Sessions the server is making a stream for, by the device, its app and each reason the server gives. Only when the settings ask for devices to be named.", "device", "client", "reason")

	sessionInfo     = list.Reading("session_info", "Always 1, for each session playing or paused: the kind of thing it plays, how it reaches the device, whether it is paused, whether the device is at home or remote, and why the server makes a stream of it when it does. Whose it is, what it plays and on what are filled in only when the settings ask.", "session", "user", "title", "type", "device", "client", "method", "state", "network", "reasons")
	sessionBitrate  = list.Reading("session_bits_per_second", "How many bits a second what each session plays comes to as it leaves the server: the stream the server makes, or the file's own when it is sent as it is.", "session")
	sessionPosition = list.Reading("session_position_seconds", "How far through what it is playing each session is.", "session")
	sessionRuntime  = list.Reading("session_runtime_seconds", "How long the whole of what each session is playing is.", "session")

	scheduledTasks       = list.Reading("scheduled_tasks", "The server's scheduled tasks, by what they are doing.", "state")
	scheduledTasksFailed = list.Reading("scheduled_tasks_failed", "Scheduled tasks whose last run failed.")
	taskLastFailed       = list.Reading("scheduled_task_last_run_failed", "Whether the last run of each scheduled task that has run failed (1) or not (0).", "task")
	taskLastEnd          = list.Reading("scheduled_task_last_run_timestamp_seconds", "When the last run of each scheduled task ended, in seconds since 1970.", "task")
	scanRunning          = list.Reading("library_scan_running", "Whether the scan of every library is running (1) or not (0).")
	scanProgress         = list.Reading("library_scan_progress_ratio", "How far the scan of every library has got, from 0 to 1, while it runs.")
	scanLastOK           = list.Reading("library_scan_last_run_ok", "Whether the last scan of every library finished as it should (1) or not (0).")
	scanLastEnd          = list.Reading("library_scan_last_run_timestamp_seconds", "When the last scan of every library ended, in seconds since 1970.")
	scanLastTook         = list.Reading("library_scan_last_run_duration_seconds", "How long the last scan of every library took.")

	libraries    = list.Reading("libraries", "How many libraries the server has.")
	libraryItems = list.Reading("library_items", "Items in each library, by kind.", "library", "type")
	libraryAdded = list.Reading("library_items_added", "Items added to each library within the window: the last day, 7 days or 30 days. Films, episodes, tracks and the like, not the shows and albums that hold them. From Emby, which alone can count them.", "library", "window")
	items        = list.Reading("items", "Items in every library of the server added together, by kind.", "type")

	users         = list.Reading("users", "How many accounts the server has.")
	usersDisabled = list.Reading("users_disabled", "Accounts that have been disabled.")
	usersActive   = list.Reading("users_active", "Accounts seen within the window: the last day, 7 days or 30 days.", "window")
	userLastSeen  = list.Reading("user_last_seen_timestamp_seconds", "When each account last did anything, in seconds since 1970. Only when the settings ask for accounts to be named.", "user")
	devices       = list.Reading("devices", "How many devices the server knows of, which is every one it has ever seen.")
	devicesActive = list.Reading("devices_active", "Devices seen within the window: the last day, 7 days or 30 days.", "window")
	logins        = list.Reading("logins_total", "Times an account was signed in to (ok) and tries that were refused (failed), by the week they were made in, named by its Monday in UTC, and where they came from: home, remote or unknown. This week and the last are on the page.", "week", "result", "network")
	userLogins    = list.Reading("user_logins_total", "The same sign-ins by account, with tries on a name the server has no account of under unknown. Only when the settings ask for accounts to be named.", "week", "user", "result")

	plugins        = list.Reading("plugins", "How many plugins the server has installed.")
	pluginInfo     = list.Reading("plugin_info", "Always 1, one for each plugin installed, with its version.", "plugin", "version")
	pluginsBehind  = list.Reading("plugins_update_available", "Plugins the server's own catalogue has a newer version of than the one installed, that the server could run. Not on the page while the server cannot reach its catalogue.")
	pluginIsBehind = list.Reading("plugin_update_available", "Always 1, one for each plugin a newer version is on offer for, with the version installed and the newest on offer.", "plugin", "version", "latest")

	historyInstalled = list.Reading("playback_reporting_installed", "Whether the server has the Playback Reporting plugin, which is where the readings of what was played come from (1) or not (0).")
	playbackSeconds  = list.Reading("playback_seconds_total", "Seconds played, by the kind of thing and how it reached the device, as the server's Playback Reporting plugin has them. Plays are kept under the week they began in, named by its Monday; this week and the last are on the page, and neither ever goes down.", "week", "type", "method")
	plays            = list.Reading("plays_total", "Plays, by the week they began in, the kind of thing and how it reached the device. A play counts again each time how it reaches the device changes.", "week", "type", "method")
	userSeconds      = list.Reading("user_playback_seconds_total", "Seconds played, by the week the plays began in, account and the kind of thing. Only when the settings ask for accounts to be named.", "week", "user", "type")
	userPlays        = list.Reading("user_plays_total", "Plays, by the week they began in, account and the kind of thing. Only when the settings ask for accounts to be named.", "week", "user", "type")
	titleSeconds     = list.Reading("title_playback_seconds_total", "Seconds played of each show and each film, by the week the plays began in. Only when the settings ask for titles to be named.", "week", "title", "type")
	titlePlays       = list.Reading("title_plays_total", "Plays of each show and each film, by the week they began in. Only when the settings ask for titles to be named.", "week", "title", "type")
	shortPlays       = list.Reading("short_plays_total", "Plays that ended within a minute of beginning, which is what one that failed looks like, by the week they began in, the kind of thing and how it reached the device. A play is counted five minutes after it stopped.", "week", "type", "method")
	reasonSeconds    = list.Reading("transcode_reason_seconds_total", "Seconds played of streams the server made, by the week the plays began in and each reason the server gave for not sending the file as it is. A play with two reasons is under both. From Emby's plugin, which alone keeps the reasons.", "week", "reason")
	reasonPlays      = list.Reading("transcode_reason_plays_total", "Plays the server made a stream for, by the week they began in and each reason it gave.", "week", "reason")
	clientSeconds    = list.Reading("client_transcode_reason_seconds_total", "Seconds played of streams the server made, by the week the plays began in, the device, its app and each reason. Only when the settings ask for devices to be named.", "week", "device", "client", "reason")
	clientPlays      = list.Reading("client_transcode_reason_plays_total", "Plays the server made a stream for, by the week they began in, the device, its app and each reason. Only when the settings ask for devices to be named.", "week", "device", "client", "reason")
)

// Source is where each server's state comes from, and which groups the page leaves off: the poller, or a test's own.
type Source interface {
	Targets() []string
	State(kind string) poll.State
	Skipped() poll.Skip
}

// Readings writes what the source knows of each server as readings. It asks no server anything, so a scrape is answered from memory however slow a
// server is.
type Readings struct {
	source Source
}

// New is the readings of the servers the source knows of.
func New(source Source) *Readings {
	return &Readings{source: source}
}

// Page is every source of the page's readings: what the poller knows of each server, and how its asking goes unless that group is left off.
func Page(p *poll.Poller) []prometheus.Collector {
	collectors := []prometheus.Collector{New(p)}
	if !p.Skipped().Has(poll.GroupPoll) {
		collectors = append(collectors, p)
	}

	return collectors
}

// Describe names every reading there can be.
func (*Readings) Describe(ch chan<- *prometheus.Desc) {
	list.Describe(ch)
}

// Collect writes every reading there is an answer for, but those of the groups the page leaves off. A server that has never answered a question has
// no reading of it, which is not the same as a reading of zero.
func (r *Readings) Collect(ch chan<- prometheus.Metric) {
	now, skip := time.Now(), r.source.Skipped()
	for _, kind := range r.source.Targets() {
		s := r.source.State(kind)
		w := writer{metrics.NewWriter(ch, s.Kind), skip}

		w.up(&s)
		w.info(&s)
		w.sessions(&s)
		if !skip.Has(poll.GroupTasks) {
			w.tasks(&s)
		}
		if !skip.Has(poll.GroupLibraries) {
			w.libraries(&s)
		}
		if !skip.Has(poll.GroupAccounts) {
			w.users(&s, now)
			w.devices(&s, now)
		}
		if !skip.Has(poll.GroupSignIns) {
			w.logins(&s)
		}
		if !skip.Has(poll.GroupPlugins) {
			w.plugins(&s)
		}
		if !skip.Has(poll.GroupPlayed) {
			w.history(&s)
		}
	}
}

// writer writes one server's readings, leaving off the groups skip names.
type writer struct {
	*metrics.Writer
	skip poll.Skip
}

// up is whether the server answered the last time it was asked for its own state, which is what says how far to trust the rest. How each round went
// is the poller's own readings.
func (w writer) up(s *poll.State) {
	if r := s.Round(poll.RoundServer); r.Ran {
		w.Flag(up, r.OK)
	}
}

func (w writer) info(s *poll.State) {
	if !s.Info.Known() {
		return
	}

	i := s.Info.Value
	w.Gauge(serverInfo, 1, i.Name, i.Version, i.APIVersion)
	w.Flag(restartPending, i.RestartPending)
	w.Flag(updateAvailable, i.UpdateAvailable)
}

// stream tells one device and app's reason for a stream from another's.
type stream struct{ device, client, reason string }

// sessions counts the devices by state and the playing ones by method, by kind and by where they are, and then says of each playing one what it
// is doing. Every state, method, kind and network is written, at 0 when there is none, so a reading does not vanish when the last stream stops.
// The reasons are the servers' own and there is no saying which there may be, so a reason is on the page only while a stream has it. The three
// groups all come from the one answer, and each is left off on its own.
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

	if !w.skip.Has(poll.GroupSessions) {
		w.Counts(sessions, poll.SessionStates, states)
		w.Counts(streams, poll.Methods, methods)
		w.Counts(streamsByType, poll.Kinds, kinds)
		w.Gauge(videoTranscodes, hardware, "hardware")
		w.Gauge(videoTranscodes, software, "software")
		w.Counts(streamsByNetwork, poll.Networks, networks)
		w.Counts(streamsBitrate, poll.Networks, outgoing)
	}

	if !w.skip.Has(poll.GroupReasons) {
		w.Gauge(transcodeBitrate, bitrate)
		w.Counts(transcodeReasons, nil, reasons)
		for by, n := range byClient {
			w.Gauge(clientReasons, n, by.device, by.client, by.reason)
		}
	}

	if !w.skip.Has(poll.GroupStreaming) {
		w.playing(playing)
	}
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

		w.Gauge(sessionInfo, 1, session.ID, session.User, session.Title, string(session.Kind), session.Device, session.Client, string(session.Method), string(session.State), string(session.Network), strings.Join(session.Reasons, ","))
		w.Gauge(sessionPosition, session.Position.Seconds(), session.ID)
		if session.Outgoing > 0 {
			w.Gauge(sessionBitrate, session.Outgoing, session.ID)
		}
		if session.Runtime > 0 {
			w.Gauge(sessionRuntime, session.Runtime.Seconds(), session.ID)
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

	w.Counts(scheduledTasks, poll.TaskStates, states)
	w.Gauge(scheduledTasksFailed, failed)
	w.lastRuns(s.Tasks.Value)

	if scan == nil {
		return
	}

	running := scan.State == poll.TaskRunning || scan.State == poll.TaskCancelling
	w.Flag(scanRunning, running)
	if running {
		w.Gauge(scanProgress, scan.Progress)
	}

	run := scan.LastRun
	if run == nil {
		return
	}
	w.Flag(scanLastOK, run.OK)
	w.At(scanLastEnd, run.Ended)
	if run.Took > 0 {
		w.Gauge(scanLastTook, run.Took.Seconds())
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
		w.Flag(taskLastFailed, run.Failed, name)
		w.At(taskLastEnd, run.Ended, name)
	}
}

// libraries counts the libraries, what each holds, and every library's added together. The server's own total is not asked for: it counts what is
// in no library too, such as the theme songs and extras kept beside a film.
func (w writer) libraries(s *poll.State) {
	if !s.Libraries.Known() {
		return
	}

	w.Gauge(libraries, len(s.Libraries.Value))

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
			w.Gauge(libraryAdded, n, name, window)
		}
	}

	for name, kinds := range byName {
		for kind, n := range kinds {
			w.Gauge(libraryItems, n, name, kind)
		}
	}
	for kind, n := range total {
		w.Gauge(items, n, kind)
	}
}

// users counts the accounts, and those seen within each window of now. The windows are measured as the page is written, so an account stops
// counting as active on time even between two rounds.
func (w writer) users(s *poll.State, now time.Time) {
	if !s.Users.Known() {
		return
	}

	w.Gauge(users, len(s.Users.Value))

	var disabled int
	for _, user := range s.Users.Value {
		if user.Disabled {
			disabled++
		}
	}
	w.Gauge(usersDisabled, disabled)

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
		w.At(userLastSeen, at, name)
	}
}

// devices counts the devices, and those seen within each window of now. A server keeps every device it has ever seen, so the windows say more of
// how many are in use than the count does.
func (w writer) devices(s *poll.State, now time.Time) {
	if !s.Devices.Known() {
		return
	}

	w.Gauge(devices, len(s.Devices.Value))

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
		w.Gauge(d, active, window.Label)
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
	w.Flag(historyInstalled, h.Installed)

	for _, p := range h.Plays {
		w.Total(playbackSeconds, p.Seconds, p.Week, string(p.Kind), string(p.Method))
		w.Total(plays, p.Plays, p.Week, string(p.Kind), string(p.Method))
		// written at 0 beside the plays, so the first short one is a rise a query can see
		w.Total(shortPlays, p.Short, p.Week, string(p.Kind), string(p.Method))
	}
	for _, p := range h.ByUser {
		w.Total(userSeconds, p.Seconds, p.Week, p.User, string(p.Kind))
		w.Total(userPlays, p.Plays, p.Week, p.User, string(p.Kind))
	}
	for _, p := range h.ByTitle {
		w.Total(titleSeconds, p.Seconds, p.Week, p.Title, string(p.Kind))
		w.Total(titlePlays, p.Plays, p.Week, p.Title, string(p.Kind))
	}
	for _, p := range h.ByReason {
		w.Total(reasonSeconds, p.Seconds, p.Week, p.Reason)
		w.Total(reasonPlays, p.Plays, p.Week, p.Reason)
	}
	for _, p := range h.ByClient {
		w.Total(clientSeconds, p.Seconds, p.Week, p.Device, p.Client, p.Reason)
		w.Total(clientPlays, p.Plays, p.Week, p.Device, p.Client, p.Reason)
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
		w.Total(logins, n, by.week, by.result, by.network)
	}
	for by, n := range byAccount {
		w.Total(userLogins, n, by.week, by.user, by.result)
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

		w.Gauge(pluginInfo, 1, plugin.Name, plugin.Version)
		if plugin.Latest != "" {
			behind++
			w.Gauge(pluginIsBehind, 1, plugin.Name, plugin.Version, plugin.Latest)
		}
	}

	w.Gauge(plugins, len(lines))
	if s.Plugins.Value.Checked {
		w.Gauge(pluginsBehind, behind)
	}
}
