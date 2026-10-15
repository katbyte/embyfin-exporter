package poll_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-exporter/lib/poll/polltest"
)

// How often the tests poll: the program's own defaults.
var (
	everySessions = poll.Every["sessions"]
	everyLibrary  = poll.Every["library"]
	everyHistory  = poll.Every["history"]
)

// errDown is a server that is not there.
var errDown = errors.New("connection refused")

func newPoller(t *testing.T, server poll.Server) *poll.Poller {
	t.Helper()

	p, err := poll.New(poll.Every, server)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// A round keeps what the server said, and how long the asking took, and leaves the other round's questions unasked.
func TestARoundKeepsItsAnswers(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{
			Slow:           2 * time.Second,
			InfoAnswer:     poll.Info{Name: "nas", Version: "4.10.0.4"},
			SessionsAnswer: []poll.Session{{State: poll.Playing, Method: poll.Transcode}},
		}
		p := newPoller(t, server)

		start := time.Now()
		_ = p.Poll(t.Context(), poll.RoundSessions)
		s := p.State("emby")

		// three questions, two seconds each, asked at once
		if r := s.SessionsRound; !r.Ran || !r.OK || r.Took != 2*time.Second || !r.LastSuccess.Equal(start.Add(2*time.Second)) || r.Failures != 0 {
			t.Errorf("sessions round = %+v, want one that ran, answered and took 2s", r)
		}
		if !s.Up() {
			t.Error("the server answered and is not up")
		}
		if !s.Info.Known() || s.Info.Value.Version != "4.10.0.4" {
			t.Errorf("info = %+v, want the server's", s.Info)
		}
		if !s.Sessions.Known() || len(s.Sessions.Value) != 1 || !s.Tasks.Known() {
			t.Errorf("sessions = %+v and tasks = %+v, want both answered", s.Sessions, s.Tasks)
		}

		if s.LibraryRound.Ran || s.Libraries.Known() || s.Users.Known() || server.Asked(polltest.Libraries) != 0 {
			t.Errorf("the library round ran with the sessions one: %+v", s.LibraryRound)
		}
		if s.HistoryRound.Ran || s.History.Known() || server.Asked(polltest.History) != 0 {
			t.Errorf("the history round ran with the sessions one: %+v", s.HistoryRound)
		}
	})
}

// What was played is asked of a plugin and not of the server itself, so it has a round of its own: a plugin that fails is a failed history round,
// and the server is still up with its sessions on the page. The plays last read are kept for three of its intervals and no longer.
func TestWhatWasPlayedHasARoundOfItsOwn(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{HistoryAnswer: poll.History{Installed: true, Plays: []poll.Played{{Kind: poll.Movie, Method: poll.DirectPlay, Plays: 1, Seconds: 60}}}}
		p := newPoller(t, server)

		_ = p.Poll(t.Context(), poll.RoundHistory)
		first := time.Now()
		if s := p.State("emby"); !s.HistoryRound.OK || !s.History.Known() || len(s.History.Value.Plays) != 1 || s.SessionsRound.Ran || server.Asked(polltest.Sessions) != 0 {
			t.Errorf("history round = %+v and history = %+v, want it answered and nothing else asked", s.HistoryRound, s.History)
		}

		server.Fail(polltest.History, errDown)
		time.Sleep(everyHistory)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		_ = p.Poll(t.Context(), poll.RoundHistory)
		s := p.State("emby")

		if !s.Up() || !s.SessionsRound.OK || s.SessionsRound.Failures != 0 {
			t.Errorf("sessions round = %+v, want the server up while only the plugin fails", s.SessionsRound)
		}
		if r := s.HistoryRound; r.OK || r.Failures != 1 || !r.LastSuccess.Equal(first) {
			t.Errorf("history round = %+v, want one failure and the first round as its last success", r)
		}
		if !s.History.Known() || s.History.Value.Plays[0].Seconds != 60 {
			t.Errorf("history = %+v, want the plays last read", s.History)
		}
		if s.Round(poll.RoundHistory) != s.HistoryRound {
			t.Errorf("Round(history) = %+v, want %+v", s.Round(poll.RoundHistory), s.HistoryRound)
		}

		time.Sleep(2*everyHistory + time.Second)
		if s := p.State("emby"); s.History.Known() {
			t.Error("the plays last read outlived three of the history round's intervals")
		}
	})
}

// One question failing must not cost the others their answers, nor the question the answer it last had: a server that refuses one read is still
// reported on for the rest.
func TestAFailedQuestionKeepsTheLastGoodAnswer(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{UsersAnswer: []poll.User{{}, {}}, DevicesAnswer: make([]poll.Device, 3), LoginsAnswer: []poll.Login{{Week: "1999-12-27", Count: 2}}, PluginsAnswer: poll.Plugins{Installed: []poll.Plugin{{Name: "Reports", Version: "1.0"}}}}
		p := newPoller(t, server)

		_ = p.Poll(t.Context(), poll.RoundLibrary)
		first := time.Now()

		// the sign-ins are asked after with the libraries, which is often enough for them
		if s := p.State("emby"); !s.Logins.Known() || len(s.Logins.Value) != 1 || server.Asked(polltest.Logins) != 1 || server.Asked(polltest.Sessions) != 0 {
			t.Errorf("sign-ins = %+v, asked for %d times, want the one answer from the library round", s.Logins, server.Asked(polltest.Logins))
		}

		// and so are the plugins
		if s := p.State("emby"); !s.Plugins.Known() || len(s.Plugins.Value.Installed) != 1 || server.Asked(polltest.Plugins) != 1 {
			t.Errorf("plugins = %+v, asked for %d times, want the one answer from the library round", s.Plugins, server.Asked(polltest.Plugins))
		}

		time.Sleep(time.Minute)
		server.Fail(polltest.Users, errDown)
		server.DevicesAnswer = make([]poll.Device, 4)
		_ = p.Poll(t.Context(), poll.RoundLibrary)
		s := p.State("emby")

		if r := s.LibraryRound; r.OK || r.Failures != 1 || !r.LastSuccess.Equal(first) {
			t.Errorf("library round = %+v, want one failure and the first round as its last success", r)
		}
		if !s.Users.Known() || len(s.Users.Value) != 2 || !s.Users.At.Equal(first) {
			t.Errorf("users = %+v, want the two from the first round", s.Users)
		}
		if len(s.Devices.Value) != 4 {
			t.Errorf("devices = %d, want the 4 asked for after users failed", len(s.Devices.Value))
		}

		server.Fail(polltest.Users, nil)
		_ = p.Poll(t.Context(), poll.RoundLibrary)
		if r := p.State("emby").LibraryRound; !r.OK || r.Failures != 1 {
			t.Errorf("library round = %+v, want it answering again with the one failure still counted", r)
		}
	})
}

// Up is the sessions round's, the one that runs often enough to tell: a library round that fails says a read went wrong, not that the server is gone.
func TestUpFollowsTheSessionsRound(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{}
		p := newPoller(t, server)

		if s := p.State("emby"); s.Up() || s.SessionsRound.Ran {
			t.Error("a server never asked is up")
		}

		server.Fail(polltest.Libraries, errDown)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		_ = p.Poll(t.Context(), poll.RoundLibrary)
		if s := p.State("emby"); !s.Up() {
			t.Error("a failed library round took the server down")
		}

		server.Fail("", errDown)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		if s := p.State("emby"); s.Up() || s.SessionsRound.Failures != 1 {
			t.Errorf("sessions round = %+v, want the server down after one failure", s.SessionsRound)
		}
	})
}

// A server that has stopped answering is served from its last answers for three intervals and no longer: after that they are no longer worth
// showing as current, least of all the sessions it had.
func TestAnAnswerIsDroppedOnceStale(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{SessionsAnswer: []poll.Session{{State: poll.Playing}}, DevicesAnswer: make([]poll.Device, 3)}
		p := newPoller(t, server)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		_ = p.Poll(t.Context(), poll.RoundLibrary)
		server.Fail("", errDown)

		time.Sleep(3 * everySessions)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		if s := p.State("emby"); !s.Sessions.Known() || !s.Info.Known() || s.Up() {
			t.Errorf("after three intervals: sessions known %t, info known %t, up %t; want the last answers still served and the server down", s.Sessions.Known(), s.Info.Known(), s.Up())
		}

		time.Sleep(time.Second)
		if s := p.State("emby"); s.Sessions.Known() || s.Info.Known() || s.Tasks.Known() {
			t.Error("the sessions round's answers outlived three intervals")
		}
		if s := p.State("emby"); !s.Devices.Known() || !s.Libraries.Known() {
			t.Error("the library round's answers were dropped on the sessions round's clock")
		}

		time.Sleep(3 * everyLibrary)
		if s := p.State("emby"); s.Devices.Known() || s.Libraries.Known() || s.Users.Known() {
			t.Error("the library round's answers outlived three intervals")
		}
	})
}

// Run asks at once, so the page has something on it as soon as it is served, and then on each round's own interval until it is told to stop.
func TestRunPollsOnEverySchedule(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{}
		p := newPoller(t, server)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			p.Run(ctx)
			close(done)
		}()

		synctest.Wait()
		if server.Asked(polltest.Sessions) != 1 || server.Asked(polltest.Libraries) != 1 || server.Asked(polltest.History) != 1 {
			t.Errorf("on start: sessions asked %d times, libraries %d and history %d, want once each", server.Asked(polltest.Sessions), server.Asked(polltest.Libraries), server.Asked(polltest.History))
		}

		// five minutes of a round every 15 seconds is 20 more, of one every 30 seconds 10 more, and one more of the round every five minutes
		time.Sleep(everyLibrary + time.Second)
		synctest.Wait()
		if server.Asked(polltest.Sessions) != 21 || server.Asked(polltest.Libraries) != 2 || server.Asked(polltest.History) != 11 {
			t.Errorf("after five minutes: sessions asked %d times, libraries %d and history %d, want 21, 2 and 11", server.Asked(polltest.Sessions), server.Asked(polltest.Libraries), server.Asked(polltest.History))
		}

		cancel()
		<-done
	})
}

// A round is given until the next is due and no longer, so a server that hangs is reported down rather than holding every later round up behind it.
func TestARoundIsGivenItsInterval(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{Slow: time.Hour}
		p := newPoller(t, server)

		_ = p.Poll(t.Context(), poll.RoundSessions)

		if r := p.State("emby").SessionsRound; !r.Ran || r.OK || r.Took != everySessions || r.Failures != 1 {
			t.Errorf("sessions round = %+v, want one that failed after its 15s", r)
		}
	})
}

// A round cut short because the program is stopping says nothing about the server, and is not counted against it.
func TestStoppingIsNotAFailure(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{Slow: time.Minute}
		p := newPoller(t, server)

		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		_ = p.Poll(ctx, poll.RoundSessions)

		if r := p.State("emby").SessionsRound; r.Ran || r.Failures != 0 {
			t.Errorf("sessions round = %+v, want nothing recorded for a round that was stopped", r)
		}
	})
}

// An interval of nothing would poll without pause.
func TestNewRefusesAnIntervalOfNothing(t *testing.T) {
	t.Parallel()

	for _, bad := range []map[string]time.Duration{{}, {"sessions": time.Second}, {"library": time.Second, "history": time.Second}, {"sessions": -time.Second, "library": time.Second, "history": time.Second}} {
		if _, err := poll.New(bad, &polltest.Server{}); err == nil {
			t.Errorf("New accepted %v", bad)
		}
	}
}

// Each server is known by its kind, which is the server label on every reading, and two of one kind cannot be told apart.
func TestKind(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		p := newPoller(t, &polltest.Server{Named: "jellyfin"})
		_ = p.Poll(t.Context(), poll.RoundSessions)
		if p.State("jellyfin").Kind != "jellyfin" || !p.State("jellyfin").SessionsRound.OK || p.State("emby").SessionsRound.Ran {
			t.Errorf("rounds = %+v for jellyfin and %+v for emby, want only jellyfin asked", p.State("jellyfin").SessionsRound, p.State("emby").SessionsRound)
		}
	})

	if _, err := poll.New(poll.Every, &polltest.Server{}, &polltest.Server{}); err == nil {
		t.Error("New accepted two Embys")
	}
}
