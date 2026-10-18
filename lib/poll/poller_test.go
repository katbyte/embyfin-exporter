package poll_test

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-exporter/lib/poll/polltest"
)

// How often the tests poll: the program's own defaults.
var (
	everySessions  = poll.Every["sessions"]
	everyLibraries = poll.Every["libraries"]
	everyPlayed    = poll.Every["played"]
)

// errDown is a server that is not there.
var errDown = errors.New("connection refused")

func newPoller(t *testing.T, server poll.Server) *poll.Poller {
	t.Helper()

	p, err := poll.New(poll.Every, nil, server)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// A round keeps what the server said, and how long the asking took, and leaves every other round's questions unasked.
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

		if r := s.Round(poll.RoundSessions); !r.Ran || !r.OK || r.Took != 2*time.Second || !r.LastSuccess.Equal(start.Add(2*time.Second)) || r.Failures != 0 {
			t.Errorf("sessions round = %+v, want one that ran, answered and took 2s", r)
		}
		if !s.Sessions.Known() || len(s.Sessions.Value) != 1 {
			t.Errorf("sessions = %+v, want the server's", s.Sessions)
		}

		for _, question := range []string{polltest.Info, polltest.Tasks, polltest.Libraries, polltest.Users, polltest.Plugins, polltest.History} {
			if server.Asked(question) != 0 {
				t.Errorf("%s was asked in the sessions round", question)
			}
		}
		if len(s.Rounds) != 1 || s.Info.Known() || s.Tasks.Known() || s.Up() {
			t.Errorf("rounds %v ran, info known %t, tasks known %t, up %t; want the sessions round alone", s.Rounds, s.Info.Known(), s.Tasks.Known(), s.Up())
		}

		_ = p.Poll(t.Context(), poll.RoundServer)
		if s := p.State("emby"); !s.Up() || !s.Info.Known() || s.Info.Value.Version != "4.10.0.4" {
			t.Errorf("after the server's own round: up %t, info %+v; want it up with its version", s.Up(), s.Info)
		}
	})
}

// What was played is asked of a plugin and not of the server itself, on a round of its own: a plugin that fails is a failed round of what was played,
// and the server is still up. The plays last read are kept for three of its intervals and no longer.
func TestWhatWasPlayedHasARoundOfItsOwn(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{HistoryAnswer: poll.History{Installed: true, Plays: []poll.Played{{Kind: poll.Movie, Method: poll.DirectPlay, Plays: 1, Seconds: 60}}}}
		p := newPoller(t, server)

		_ = p.Poll(t.Context(), poll.RoundPlayed)
		first := time.Now()
		if s := p.State("emby"); !s.Round(poll.RoundPlayed).OK || !s.History.Known() || len(s.History.Value.Plays) != 1 || server.Asked(polltest.Sessions) != 0 {
			t.Errorf("played round = %+v and history = %+v, want it answered and nothing else asked", s.Round(poll.RoundPlayed), s.History)
		}

		server.Fail(polltest.History, errDown)
		time.Sleep(everyPlayed)
		_ = p.Poll(t.Context(), poll.RoundServer)
		_ = p.Poll(t.Context(), poll.RoundPlayed)
		s := p.State("emby")

		if !s.Up() {
			t.Error("the server is down while only the plugin fails")
		}
		if r := s.Round(poll.RoundPlayed); r.OK || r.Failures != 1 || !r.LastSuccess.Equal(first) {
			t.Errorf("played round = %+v, want one failure and the first round as its last success", r)
		}
		if !s.History.Known() || s.History.Value.Plays[0].Seconds != 60 {
			t.Errorf("history = %+v, want the plays last read", s.History)
		}

		time.Sleep(2*everyPlayed + time.Second)
		if s := p.State("emby"); s.History.Known() {
			t.Error("the plays last read outlived three of the played round's intervals")
		}
	})
}

// One question failing must not cost the others their answers, nor the question the answer it last had: a server that refuses one read is still
// reported on for the rest. Users and devices are one round; sign-ins and plugins each have their own.
func TestAFailedQuestionKeepsTheLastGoodAnswer(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{UsersAnswer: []poll.User{{}, {}}, DevicesAnswer: make([]poll.Device, 3), LoginsAnswer: []poll.Login{{Week: "1999-12-27", Count: 2}}, PluginsAnswer: poll.Plugins{Installed: []poll.Plugin{{Name: "Reports", Version: "1.0"}}}}
		p := newPoller(t, server)

		_ = p.Poll(t.Context(), poll.RoundAccounts)
		_ = p.Poll(t.Context(), poll.RoundSignIns)
		_ = p.Poll(t.Context(), poll.RoundPlugins)
		first := time.Now()

		if s := p.State("emby"); !s.Logins.Known() || len(s.Logins.Value) != 1 || !s.Plugins.Known() || len(s.Plugins.Value.Installed) != 1 || server.Asked(polltest.Logins) != 1 || server.Asked(polltest.Plugins) != 1 || server.Asked(polltest.Libraries) != 0 {
			t.Errorf("sign-ins = %+v and plugins = %+v, asked %d and %d times, libraries %d; want each answered once in its own round", s.Logins, s.Plugins, server.Asked(polltest.Logins), server.Asked(polltest.Plugins), server.Asked(polltest.Libraries))
		}

		time.Sleep(time.Minute)
		server.Fail(polltest.Users, errDown)
		server.DevicesAnswer = make([]poll.Device, 4)
		_ = p.Poll(t.Context(), poll.RoundAccounts)
		s := p.State("emby")

		if r := s.Round(poll.RoundAccounts); r.OK || r.Failures != 1 || !r.LastSuccess.Equal(first) {
			t.Errorf("accounts round = %+v, want one failure and the first round as its last success", r)
		}
		if !s.Users.Known() || len(s.Users.Value) != 2 || !s.Users.At.Equal(first) {
			t.Errorf("users = %+v, want the two from the first round", s.Users)
		}
		if len(s.Devices.Value) != 4 {
			t.Errorf("devices = %d, want the 4 asked for after users failed", len(s.Devices.Value))
		}

		server.Fail(polltest.Users, nil)
		_ = p.Poll(t.Context(), poll.RoundAccounts)
		if r := p.State("emby").Rounds[poll.RoundAccounts]; !r.OK || r.Failures != 1 {
			t.Errorf("accounts round = %+v, want it answering again with the one failure still counted", r)
		}
	})
}

// Up is the server's own round: a round of anything else that fails says a read went wrong, not that the server is gone.
func TestUpFollowsTheServersOwnRound(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{}
		p := newPoller(t, server)

		if s := p.State("emby"); s.Up() || s.Round(poll.RoundServer).Ran {
			t.Error("a server never asked is up")
		}

		server.Fail(polltest.Libraries, errDown)
		server.Fail(polltest.Sessions, errDown)
		_ = p.Poll(t.Context(), poll.RoundServer)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		_ = p.Poll(t.Context(), poll.RoundLibraries)
		if s := p.State("emby"); !s.Up() {
			t.Error("failed sessions and libraries rounds took the server down")
		}

		server.Fail("", errDown)
		_ = p.Poll(t.Context(), poll.RoundServer)
		if s := p.State("emby"); s.Up() || s.Round(poll.RoundServer).Failures != 1 {
			t.Errorf("server round = %+v, want the server down after one failure", s.Round(poll.RoundServer))
		}
	})
}

// A server that has stopped answering is served from its last answers for three intervals and no longer, each on its own round's clock: after that
// they are no longer worth showing as current, least of all the sessions it had.
func TestAnAnswerIsDroppedOnceStale(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{SessionsAnswer: []poll.Session{{State: poll.Playing}}, LibrariesAnswer: []poll.Library{{Name: "Films"}}}
		p := newPoller(t, server)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		_ = p.Poll(t.Context(), poll.RoundLibraries)
		server.Fail("", errDown)

		time.Sleep(3 * everySessions)
		_ = p.Poll(t.Context(), poll.RoundSessions)
		if s := p.State("emby"); !s.Sessions.Known() {
			t.Error("after three intervals the last sessions are no longer served")
		}

		time.Sleep(time.Second)
		if s := p.State("emby"); s.Sessions.Known() {
			t.Error("the sessions outlived three of their round's intervals")
		}
		if s := p.State("emby"); !s.Libraries.Known() {
			t.Error("the libraries were dropped on the sessions round's clock")
		}

		time.Sleep(3 * everyLibraries)
		if s := p.State("emby"); s.Libraries.Known() {
			t.Error("the libraries outlived three of their round's intervals")
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
		for _, question := range []string{polltest.Info, polltest.Sessions, polltest.Tasks, polltest.Libraries, polltest.Users, polltest.Devices, polltest.Logins, polltest.Plugins, polltest.History} {
			if server.Asked(question) != 1 {
				t.Errorf("on start %s was asked %d times, want once", question, server.Asked(question))
			}
		}

		// five minutes of a round every 15 seconds is 20 more, of one every 30 seconds 10 more, and one more of each round every five minutes
		time.Sleep(everyLibraries + time.Second)
		synctest.Wait()
		for question, want := range map[string]int{polltest.Info: 21, polltest.Sessions: 21, polltest.Tasks: 21, polltest.Libraries: 2, polltest.Users: 2, polltest.Logins: 2, polltest.Plugins: 2, polltest.History: 11} {
			if got := server.Asked(question); got != want {
				t.Errorf("after five minutes %s was asked %d times, want %d", question, got, want)
			}
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

		_ = p.Poll(t.Context(), poll.RoundServer)

		if r := p.State("emby").Rounds[poll.RoundServer]; !r.Ran || r.OK || r.Took != poll.Every["server"] || r.Failures != 1 {
			t.Errorf("server round = %+v, want one that failed after its 15s", r)
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

		if r := p.State("emby").Rounds[poll.RoundSessions]; r.Ran || r.Failures != 0 {
			t.Errorf("sessions round = %+v, want nothing recorded for a round that was stopped", r)
		}
	})
}

// An interval of nothing would poll without pause, for any round that runs; one that does not run needs none.
func TestNewRefusesAnIntervalOfNothing(t *testing.T) {
	t.Parallel()

	for _, round := range poll.Rounds {
		every := maps.Clone(poll.Every)
		every[string(round)] = 0

		if _, err := poll.New(every, nil, &polltest.Server{}); err == nil {
			t.Errorf("New accepted the %s round asked every 0s", round)
		}
	}

	if _, err := poll.New(map[string]time.Duration{"server": time.Second}, poll.Leave(nil, poll.Groups), &polltest.Server{}); err != nil {
		t.Errorf("with every group left off, only the server's own round needs an interval: %v", err)
	}
}

// Each server is known by its kind, which is the server label on every reading, and two of one kind cannot be told apart.
func TestKind(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		p := newPoller(t, &polltest.Server{Named: "jellyfin"})
		_ = p.Poll(t.Context(), poll.RoundServer)
		jellyfin, emby := p.State("jellyfin"), p.State("emby")
		if jellyfin.Kind != "jellyfin" || !jellyfin.Up() || emby.Round(poll.RoundServer).Ran {
			t.Errorf("rounds = %v for jellyfin and %v for emby, want only jellyfin asked", jellyfin.Rounds, emby.Rounds)
		}
	})

	if _, err := poll.New(poll.Every, nil, &polltest.Server{}, &polltest.Server{}); err == nil {
		t.Error("New accepted two Embys")
	}
}

// A group left off is not asked for, and a round left with nothing to ask is not run, so leaving off what was played stops the plugin being asked at
// all. The server's own round always runs: whether it answers is what up is.
func TestSkippedGroupsAreNotAsked(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := &polltest.Server{}
		p, err := poll.New(poll.Every, poll.Leave(nil, []poll.Group{poll.GroupPlugins, poll.GroupSignIns, poll.GroupPlayed}), server)
		if err != nil {
			t.Fatal(err)
		}

		for _, round := range poll.Rounds {
			err := p.Poll(t.Context(), round)
			if skipped := round == poll.RoundPlugins || round == poll.RoundSignIns || round == poll.RoundPlayed; skipped != (err != nil) {
				t.Errorf("polling the %s round = %v, want it run unless its group is left off", round, err)
			}
		}

		for question, want := range map[string]int{polltest.Info: 1, polltest.Sessions: 1, polltest.Tasks: 1, polltest.Libraries: 1, polltest.Users: 1, polltest.Devices: 1, polltest.Plugins: 0, polltest.Logins: 0, polltest.History: 0} {
			if got := server.Asked(question); got != want {
				t.Errorf("%s asked %d times, want %d", question, got, want)
			}
		}

		if s := p.State("emby"); !s.Up() || s.Plugins.Known() || s.Logins.Known() || !s.Users.Known() || s.Round(poll.RoundPlayed).Ran {
			t.Errorf("state = up %t, plugins known %t, sign-ins known %t, users known %t, rounds %v", s.Up(), s.Plugins.Known(), s.Logins.Known(), s.Users.Known(), s.Rounds)
		}
	})
}

// The three groups that come from the sessions answer are one round: it runs while any of them is on the page, and not once all three are left off.
func TestSkippingWhatSharesAQuestion(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		some := &polltest.Server{}
		p, err := poll.New(poll.Every, poll.Leave(nil, []poll.Group{poll.GroupSessions, poll.GroupReasons}), some)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Poll(t.Context(), poll.RoundSessions); err != nil || some.Asked(polltest.Sessions) != 1 {
			t.Errorf("sessions round = %v, asked %d times, with who is streaming still on the page; want it run once", err, some.Asked(polltest.Sessions))
		}

		none := &polltest.Server{}
		p, err = poll.New(poll.Every, poll.Leave([]poll.Group{poll.GroupTasks}, nil), none)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Poll(t.Context(), poll.RoundSessions); err == nil {
			t.Error("the sessions round ran with only tasks asked for")
		}
		_ = p.Poll(t.Context(), poll.RoundServer)
		_ = p.Poll(t.Context(), poll.RoundTasks)
		if s := p.State("emby"); none.Asked(polltest.Sessions) != 0 || none.Asked(polltest.Tasks) != 1 || !s.Up() {
			t.Errorf("sessions asked %d times and tasks %d, up %t; want 0, 1 and up", none.Asked(polltest.Sessions), none.Asked(polltest.Tasks), s.Up())
		}
	})
}

// The groups settings take the groups' words in any case and spacing, say which words there are when given one they do not know, and do not name
// the server's own readings, which are always on.
func TestParseGroups(t *testing.T) {
	t.Parallel()

	groups, err := poll.ParseGroups(" Plugins, sign-ins ,,played ")
	if err != nil || len(groups) != 3 || groups[0] != poll.GroupPlugins || groups[1] != poll.GroupSignIns || groups[2] != poll.GroupPlayed {
		t.Errorf("ParseGroups = %v (%v), want plugins, sign-ins and played", groups, err)
	}

	if groups, err := poll.ParseGroups(""); err != nil || len(groups) != 0 {
		t.Errorf("ParseGroups of nothing = %v (%v), want none", groups, err)
	}

	if _, err := poll.ParseGroups("plugins,everything"); err == nil || !strings.Contains(err.Error(), `"everything"`) || !strings.Contains(err.Error(), "sign-ins") {
		t.Errorf("an unknown word = %v, want an error naming it and the words there are", err)
	}

	if _, err := poll.ParseGroups("server"); err == nil {
		t.Error("the server's own readings were named as a group")
	}
}

// Every group is on the page unless the metrics setting names others or the skip setting names it: what is left off is every group metrics does not
// name, when it names any, and every group skip names.
func TestLeave(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		metrics, skip []poll.Group
		want          string
	}{
		"neither":             {want: ""},
		"metrics":             {metrics: []poll.Group{poll.GroupSessions, poll.GroupPlugins}, want: "poll,reasons,streaming,tasks,libraries,accounts,sign-ins,played"},
		"skip":                {skip: []poll.Group{poll.GroupPlugins, poll.GroupPlayed}, want: "plugins,played"},
		"both":                {metrics: []poll.Group{poll.GroupPoll, poll.GroupSessions, poll.GroupPlugins, poll.GroupPlayed}, skip: []poll.Group{poll.GroupPlayed}, want: "reasons,streaming,tasks,libraries,accounts,sign-ins,played"},
		"skip what is not on": {metrics: []poll.Group{poll.GroupTasks}, skip: []poll.Group{poll.GroupPlugins}, want: "poll,sessions,reasons,streaming,plugins,libraries,accounts,sign-ins,played"},
		"skip poll":           {skip: []poll.Group{poll.GroupPoll}, want: "poll"},
	} {
		if got := poll.Leave(c.metrics, c.skip).String(); got != c.want {
			t.Errorf("%s: leaves off %q, want %q", name, got, c.want)
		}
	}

	if runs := poll.Leave([]poll.Group{poll.GroupStreaming, poll.GroupPlayed}, nil).Runs(); len(runs) != 3 || runs[0] != poll.RoundServer || runs[1] != poll.RoundSessions || runs[2] != poll.RoundPlayed {
		t.Errorf("streaming and played run %v, want the server's own round, sessions and played", runs)
	}

	// how the asking goes has no round: on or off, every other round runs as it would
	if on, off := poll.Leave(nil, nil).Runs(), poll.Leave(nil, []poll.Group{poll.GroupPoll}).Runs(); len(on) != len(poll.Rounds) || len(off) != len(poll.Rounds) {
		t.Errorf("rounds run with poll on %v and off %v, want every round both times", on, off)
	}
}
