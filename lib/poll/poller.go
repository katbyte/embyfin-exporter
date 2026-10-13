package poll

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/outside"
)

// Round is one of the three schedules a server is asked on.
type Round string

const (
	// RoundSessions is what changes by the minute: sessions, scheduled tasks and the server's own state.
	RoundSessions Round = "sessions"
	// RoundLibrary is what changes when a scan runs or someone is added, or does not need watching by the second: libraries and what they hold,
	// users, devices and sign-ins.
	RoundLibrary Round = "library"
	// RoundHistory is what has been played, which a plugin answers and not the server itself: on a round of its own, a plugin that fails does not
	// make the server look down.
	RoundHistory Round = "history"
)

// Rounds is every round, in the order the page lists them.
var Rounds = []Round{RoundSessions, RoundLibrary, RoundHistory}

// staleAfter is how many intervals an answer is served for once its round stops succeeding: long enough to ride out a failed round or two, short
// enough that a server down for an hour is not still shown with the sessions it had.
const staleAfter = 3

// Intervals is how often each round runs. A round is also given that long to finish, so one never overlaps the next.
type Intervals struct {
	Sessions time.Duration
	Library  time.Duration
	History  time.Duration
}

// of is the interval of one round.
func (i Intervals) of(round Round) time.Duration {
	switch round {
	case RoundLibrary:
		return i.Library
	case RoundHistory:
		return i.History
	default:
		return i.Sessions
	}
}

// Answer is the last good answer to one question.
type Answer[T any] struct {
	Value T
	// At is when it was given, and the zero time for a question never answered.
	At time.Time
}

// Known says whether there is an answer to serve.
func (a Answer[T]) Known() bool { return !a.At.IsZero() }

// RoundState is how one round last went.
type RoundState struct {
	// Ran says a round has finished since the start; until one has, the rest says nothing.
	Ran bool
	// OK says every question of the last round was answered.
	OK   bool
	Took time.Duration
	// LastSuccess is the zero time until a round succeeds.
	LastSuccess time.Time
	// Failures counts the rounds that failed since the start.
	Failures int
}

// State is what is known of one server: the last good answer to each question, and how the rounds that asked went.
type State struct {
	Kind      string
	Info      Answer[Info]
	Sessions  Answer[[]Session]
	Tasks     Answer[[]Task]
	Libraries Answer[[]Library]
	Users     Answer[[]User]
	Devices   Answer[[]Device]
	Logins    Answer[[]Login]
	History   Answer[History]

	SessionsRound RoundState
	LibraryRound  RoundState
	HistoryRound  RoundState
}

// Up says whether the server answered the last sessions round, the one that runs often enough to tell.
func (s *State) Up() bool { return s.SessionsRound.OK }

// Round is how one round last went.
func (s *State) Round(round Round) RoundState {
	return *s.roundOf(round)
}

// roundOf is where one round's state is kept.
func (s *State) roundOf(round Round) *RoundState {
	switch round {
	case RoundLibrary:
		return &s.LibraryRound
	case RoundHistory:
		return &s.HistoryRound
	default:
		return &s.SessionsRound
	}
}

// Poller asks one server its questions on three schedules and remembers the last good answer to each.
type Poller struct {
	server Server
	every  Intervals

	mu    sync.RWMutex
	state State
}

// New is a poller for one server. It asks nothing until Run or Poll.
func New(server Server, every Intervals) (*Poller, error) {
	if every.Sessions <= 0 || every.Library <= 0 || every.History <= 0 {
		return nil, fmt.Errorf("the intervals must be longer than nothing: sessions %s, library %s, history %s", every.Sessions, every.Library, every.History)
	}

	return &Poller{server: server, every: every, state: State{Kind: server.Kind()}}, nil
}

// Kind is which server this poller asks.
func (p *Poller) Kind() string { return p.server.Kind() }

// Run polls until the context ends: every round at once on start, then each on its own interval.
func (p *Poller) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, round := range Rounds {
		wg.Go(func() { p.loop(ctx, round) })
	}
	wg.Wait()
}

// loop runs one round now and then every interval, until the context ends.
func (p *Poller) loop(ctx context.Context, round Round) {
	ticker := time.NewTicker(p.every.of(round))
	defer ticker.Stop()

	for {
		p.Poll(ctx, round)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Poll runs one round now and records how it went. A question that fails leaves its last good answer where it was, and does not stop the others
// being asked.
func (p *Poller) Poll(ctx context.Context, round Round) {
	within, cancel := context.WithTimeout(ctx, p.every.of(round))
	defer cancel()

	start := time.Now()
	err := p.ask(within, round)
	// a round cut short because the program is stopping says nothing about the server
	if err != nil && ctx.Err() != nil {
		return
	}

	p.report(round, p.record(round, time.Since(start), err), err)
}

// record keeps how a round went, and gives back how the one before it did.
func (p *Poller) record(round Round, took time.Duration, err error) RoundState {
	p.mu.Lock()
	defer p.mu.Unlock()

	r := p.state.roundOf(round)
	before := *r
	r.Ran, r.OK, r.Took = true, err == nil, took
	if err != nil {
		r.Failures++
	} else {
		r.LastSuccess = time.Now()
	}

	return before
}

// report logs a round only when it changes, since a server that is down fails every round the same way.
func (p *Poller) report(round Round, before RoundState, err error) {
	switch {
	case err != nil && (before.OK || !before.Ran):
		clog.Log.Warnf("%s: the %s round failed, and will be tried again every %s: %s", p.Kind(), round, p.every.of(round), said(err))
	case err != nil:
		clog.Log.Debugf("%s: the %s round failed again: %s", p.Kind(), round, said(err))
	case before.Ran && !before.OK:
		clog.Log.Infof("%s: the %s round is answering again", p.Kind(), round)
	case !before.Ran:
		clog.Log.Infof("%s: the first %s round answered", p.Kind(), round)
	}
}

// said is what went wrong as one line of a log: a round's failures come one to a line, and a server's own words for one may quote an address with
// its key on it.
func said(err error) string {
	return strings.ReplaceAll(outside.BlankAddresses(err.Error()), "\n", "; ")
}

// ask puts every question of one round, and gives back what failed.
func (p *Poller) ask(ctx context.Context, round Round) error {
	switch round {
	case RoundLibrary:
		return errors.Join(
			keep(ctx, p, "libraries", p.server.Libraries, func(s *State) *Answer[[]Library] { return &s.Libraries }),
			keep(ctx, p, "users", p.server.Users, func(s *State) *Answer[[]User] { return &s.Users }),
			keep(ctx, p, "devices", p.server.Devices, func(s *State) *Answer[[]Device] { return &s.Devices }),
			keep(ctx, p, "sign-ins", p.server.Logins, func(s *State) *Answer[[]Login] { return &s.Logins }),
		)
	case RoundHistory:
		return keep(ctx, p, "what was played", p.server.History, func(s *State) *Answer[History] { return &s.History })
	default:
		return errors.Join(
			keep(ctx, p, "server info", p.server.Info, func(s *State) *Answer[Info] { return &s.Info }),
			keep(ctx, p, "sessions", p.server.Sessions, func(s *State) *Answer[[]Session] { return &s.Sessions }),
			keep(ctx, p, "scheduled tasks", p.server.Tasks, func(s *State) *Answer[[]Task] { return &s.Tasks }),
		)
	}
}

// keep puts one question and keeps its answer in the place into names. An answer is stored whole and never changed afterwards, so State can hand it
// out without copying.
func keep[T any](ctx context.Context, p *Poller, what string, question func(context.Context) (T, error), into func(*State) *Answer[T]) error {
	value, err := question(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	*into(&p.state) = Answer[T]{Value: value, At: time.Now()}

	return nil
}

// State is what is known of the server now, without asking it anything. An answer older than three of its round's intervals is left out: the server
// has stopped answering, and what it last said is no longer worth showing as current.
func (p *Poller) State() State {
	p.mu.RLock()
	defer p.mu.RUnlock()

	s := p.state
	now := time.Now()
	sessions, library, history := staleAfter*p.every.Sessions, staleAfter*p.every.Library, staleAfter*p.every.History

	fresh(&s.Info, now, sessions)
	fresh(&s.Sessions, now, sessions)
	fresh(&s.Tasks, now, sessions)
	fresh(&s.Libraries, now, library)
	fresh(&s.Users, now, library)
	fresh(&s.Devices, now, library)
	fresh(&s.Logins, now, library)
	fresh(&s.History, now, history)

	return s
}

// fresh forgets an answer older than limit.
func fresh[T any](a *Answer[T], now time.Time, limit time.Duration) {
	if a.Known() && now.Sub(a.At) > limit {
		*a = Answer[T]{}
	}
}
