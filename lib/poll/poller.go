package poll

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/katbyte/go-kt/prometheus/poller"
)

// Round is one of the three schedules a server is asked on.
type Round string

const (
	// RoundSessions is what changes by the minute: sessions, scheduled tasks and the server's own state.
	RoundSessions Round = "sessions"
	// RoundLibrary is what changes when a scan runs or someone is added, or does not need watching by the second: libraries and what they hold,
	// users, devices, sign-ins and plugins.
	RoundLibrary Round = "library"
	// RoundHistory is what has been played, which a plugin answers and not the server itself: on a round of its own, a plugin that fails does not
	// make the server look down.
	RoundHistory Round = "history"
)

// Rounds is every round, in the order the page lists them.
var Rounds = []Round{RoundSessions, RoundLibrary, RoundHistory}

// Every is how often each round is asked unless the settings say otherwise, by the round's name: sessions twice as often as a scrape every 30
// seconds asks, libraries every few minutes since they only change when a scan runs or someone is added, and what was played once a scrape.
var Every = map[string]time.Duration{string(RoundSessions): 15 * time.Second, string(RoundLibrary): 5 * time.Minute, string(RoundHistory): 30 * time.Second}

// Answer is the last good answer to one question, and the zero time for one never answered.
type Answer[T any] = poller.Answer[T]

// RoundState is how one round last went.
type RoundState = poller.Round

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
	Plugins   Answer[Plugins]
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

// Poller asks each server its questions on three schedules and remembers the last good answer to each.
type Poller struct {
	*poller.Poller[string]

	info      *poller.Kept[Info]
	sessions  *poller.Kept[[]Session]
	tasks     *poller.Kept[[]Task]
	libraries *poller.Kept[[]Library]
	users     *poller.Kept[[]User]
	devices   *poller.Kept[[]Device]
	logins    *poller.Kept[[]Login]
	plugins   *poller.Kept[Plugins]
	history   *poller.Kept[History]
}

// New is a poller for these servers, each under its kind, asking each round as often as every says. It asks nothing until Run or Poll.
func New(every map[string]time.Duration, servers ...Server) (*Poller, error) {
	byKind := map[string]Server{}
	for _, server := range servers {
		if _, twice := byKind[server.Kind()]; twice {
			return nil, fmt.Errorf("two servers are both %s", server.Kind())
		}
		byKind[server.Kind()] = server
	}

	for _, round := range Rounds {
		if every[string(round)] <= 0 {
			return nil, fmt.Errorf("the %s round must be asked more often than never, not every %s", round, every[string(round)])
		}
	}

	p, err := poller.New(poller.Options[string]{Namespace: "embyfin", Label: "server", Kind: "server", Targets: poller.Fixed(slices.Sorted(maps.Keys(byKind))...)})
	if err != nil {
		return nil, err
	}

	sessions, library, history := p.Schedule(string(RoundSessions), every[string(RoundSessions)]), p.Schedule(string(RoundLibrary), every[string(RoundLibrary)]), p.Schedule(string(RoundHistory), every[string(RoundHistory)])

	return &Poller{
		Poller:    p,
		info:      ask(sessions, "server info", byKind, Server.Info),
		sessions:  ask(sessions, "sessions", byKind, Server.Sessions),
		tasks:     ask(sessions, "scheduled tasks", byKind, Server.Tasks),
		libraries: ask(library, "libraries", byKind, Server.Libraries),
		users:     ask(library, "users", byKind, Server.Users),
		devices:   ask(library, "devices", byKind, Server.Devices),
		logins:    ask(library, "sign-ins", byKind, Server.Logins),
		plugins:   ask(library, "plugins", byKind, Server.Plugins),
		// what was played is asked of a plugin and not the server itself: on a round of its own, a plugin that fails does not make the server look down
		history: ask(history, "what was played", byKind, Server.History),
	}, nil
}

// ask puts one question to each server, every round of the schedule.
func ask[V any](s *poller.Schedule[string], what string, servers map[string]Server, question func(Server, context.Context) (V, error)) *poller.Kept[V] {
	return poller.Ask(s, what, func(ctx context.Context, kind string) (V, error) { return question(servers[kind], ctx) })
}

// Poll runs one round now for every server, and says what failed.
func (p *Poller) Poll(ctx context.Context, round Round) error {
	return p.Poller.Poll(ctx, string(round))
}

// State is what is known of one server now, without asking it anything. An answer older than three of its round's intervals is left out: the server
// has stopped answering, and what it last said is no longer worth showing as current.
func (p *Poller) State(kind string) State {
	return State{
		Kind:      kind,
		Info:      p.info.For(kind),
		Sessions:  p.sessions.For(kind),
		Tasks:     p.tasks.For(kind),
		Libraries: p.libraries.For(kind),
		Users:     p.users.For(kind),
		Devices:   p.devices.For(kind),
		Logins:    p.logins.For(kind),
		Plugins:   p.plugins.For(kind),
		History:   p.history.For(kind),

		SessionsRound: p.Round(kind, string(RoundSessions)),
		LibraryRound:  p.Round(kind, string(RoundLibrary)),
		HistoryRound:  p.Round(kind, string(RoundHistory)),
	}
}

// Set makes what is known of one server the state given, as though it had been asked: for a test's made-up state.
func (p *Poller) Set(s *State) {
	p.SetTarget(poller.Target[string]{Key: s.Kind, Value: s.Kind, Listed: true, At: time.Now()})

	p.info.Set(s.Kind, s.Info)
	p.sessions.Set(s.Kind, s.Sessions)
	p.tasks.Set(s.Kind, s.Tasks)
	p.libraries.Set(s.Kind, s.Libraries)
	p.users.Set(s.Kind, s.Users)
	p.devices.Set(s.Kind, s.Devices)
	p.logins.Set(s.Kind, s.Logins)
	p.plugins.Set(s.Kind, s.Plugins)
	p.history.Set(s.Kind, s.History)

	p.SetRound(s.Kind, string(RoundSessions), s.SessionsRound)
	p.SetRound(s.Kind, string(RoundLibrary), s.LibraryRound)
	p.SetRound(s.Kind, string(RoundHistory), s.HistoryRound)
}
