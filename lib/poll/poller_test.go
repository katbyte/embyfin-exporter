package poll_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/katbyte/embyfin-metrics/lib/poll/polltest"
)

// every is how often the tests poll: the program's own defaults.
var every = poll.Intervals{Sessions: 15 * time.Second, Library: 5 * time.Minute}

// errDown is a server that is not there.
var errDown = errors.New("connection refused")

func newPoller(t *testing.T, server poll.Server) *poll.Poller {
	t.Helper()

	p, err := poll.New(server, every)
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
		p.Poll(t.Context(), poll.RoundSessions)
		s := p.State()

		// three questions, two seconds each
		if r := s.SessionsRound; !r.Ran || !r.OK || r.Took != 6*time.Second || !r.LastSuccess.Equal(start.Add(6*time.Second)) || r.Failures != 0 {
			t.Errorf("sessions round = %+v, want one that ran, answered and took 6s", r)
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
	})
}

// One question failing must not cost the others their answers, nor the question the answer it last had: a server that refuses one read is still
// reported on for the rest.
func TestAFailedQuestionKeepsTheLastGoodAnswer(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{UsersAnswer: []poll.User{{}, {}}, DevicesAnswer: make([]poll.Device, 3)}
		p := newPoller(t, server)

		p.Poll(t.Context(), poll.RoundLibrary)
		first := time.Now()

		time.Sleep(time.Minute)
		server.Fail(polltest.Users, errDown)
		server.DevicesAnswer = make([]poll.Device, 4)
		p.Poll(t.Context(), poll.RoundLibrary)
		s := p.State()

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
		p.Poll(t.Context(), poll.RoundLibrary)
		if r := p.State().LibraryRound; !r.OK || r.Failures != 1 {
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

		if s := p.State(); s.Up() || s.SessionsRound.Ran {
			t.Error("a server never asked is up")
		}

		server.Fail(polltest.Libraries, errDown)
		p.Poll(t.Context(), poll.RoundSessions)
		p.Poll(t.Context(), poll.RoundLibrary)
		if s := p.State(); !s.Up() {
			t.Error("a failed library round took the server down")
		}

		server.Fail("", errDown)
		p.Poll(t.Context(), poll.RoundSessions)
		if s := p.State(); s.Up() || s.SessionsRound.Failures != 1 {
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
		p.Poll(t.Context(), poll.RoundSessions)
		p.Poll(t.Context(), poll.RoundLibrary)
		server.Fail("", errDown)

		time.Sleep(3 * every.Sessions)
		p.Poll(t.Context(), poll.RoundSessions)
		if s := p.State(); !s.Sessions.Known() || !s.Info.Known() || s.Up() {
			t.Errorf("after three intervals: sessions known %t, info known %t, up %t; want the last answers still served and the server down", s.Sessions.Known(), s.Info.Known(), s.Up())
		}

		time.Sleep(time.Second)
		if s := p.State(); s.Sessions.Known() || s.Info.Known() || s.Tasks.Known() {
			t.Error("the sessions round's answers outlived three intervals")
		}
		if s := p.State(); !s.Devices.Known() || !s.Libraries.Known() {
			t.Error("the library round's answers were dropped on the sessions round's clock")
		}

		time.Sleep(3 * every.Library)
		if s := p.State(); s.Devices.Known() || s.Libraries.Known() || s.Users.Known() {
			t.Error("the library round's answers outlived three intervals")
		}
	})
}

// Run asks at once, so the page has something on it as soon as it is served, and then on each round's own interval until it is told to stop.
func TestRunPollsOnBothSchedules(t *testing.T) {
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
		if server.Asked(polltest.Sessions) != 1 || server.Asked(polltest.Libraries) != 1 {
			t.Errorf("on start: sessions asked %d times and libraries %d, want once each", server.Asked(polltest.Sessions), server.Asked(polltest.Libraries))
		}

		// five minutes of a round every 15 seconds is 20 more, and one more of the round every five minutes
		time.Sleep(every.Library + time.Second)
		synctest.Wait()
		if server.Asked(polltest.Sessions) != 21 || server.Asked(polltest.Libraries) != 2 {
			t.Errorf("after five minutes: sessions asked %d times and libraries %d, want 21 and 2", server.Asked(polltest.Sessions), server.Asked(polltest.Libraries))
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

		p.Poll(t.Context(), poll.RoundSessions)

		if r := p.State().SessionsRound; !r.Ran || r.OK || r.Took != every.Sessions || r.Failures != 1 {
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
		p.Poll(ctx, poll.RoundSessions)

		if r := p.State().SessionsRound; r.Ran || r.Failures != 0 {
			t.Errorf("sessions round = %+v, want nothing recorded for a round that was stopped", r)
		}
	})
}

// An interval of nothing would poll without pause.
func TestNewRefusesAnIntervalOfNothing(t *testing.T) {
	t.Parallel()

	for _, bad := range []poll.Intervals{{}, {Sessions: time.Second}, {Library: time.Second}, {Sessions: -time.Second, Library: time.Second}} {
		if _, err := poll.New(&polltest.Server{}, bad); err == nil {
			t.Errorf("New accepted %+v", bad)
		}
	}
}

// The poller is named for its server, which is the server label on every reading.
func TestKind(t *testing.T) {
	t.Parallel()

	p := newPoller(t, &polltest.Server{Named: "jellyfin"})
	if p.Kind() != "jellyfin" || p.State().Kind != "jellyfin" {
		t.Errorf("kind = %q and %q, want jellyfin", p.Kind(), p.State().Kind)
	}
}
