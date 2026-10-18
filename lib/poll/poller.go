package poll

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/katbyte/go-kt/prometheus/poller"
)

// Round is one of the schedules a server is asked on, each for one group of readings and on its own clock, by the word the intervals setting knows
// it by.
type Round string

const (
	// RoundServer is the server's own state, which says whether it answers at all. It always runs.
	RoundServer Round = "server"
	// RoundSessions is sessions and streams, why a stream is made and who is streaming now: the three groups come from the one answer.
	RoundSessions Round = "sessions"
	// RoundTasks is scheduled tasks and the library scan.
	RoundTasks Round = "tasks"
	// RoundLibraries is the libraries and what they hold.
	RoundLibraries Round = "libraries"
	// RoundAccounts is accounts and devices.
	RoundAccounts Round = "accounts"
	// RoundSignIns is sign-ins and refused tries.
	RoundSignIns Round = "sign-ins"
	// RoundPlugins is the plugins and their catalogue.
	RoundPlugins Round = "plugins"
	// RoundPlayed is what was played, which a plugin answers and not the server itself: a plugin that fails does not make the server look down.
	RoundPlayed Round = "played"
)

// Rounds is every round, in the order the page's documentation lists their groups.
var Rounds = []Round{RoundServer, RoundSessions, RoundTasks, RoundLibraries, RoundAccounts, RoundSignIns, RoundPlugins, RoundPlayed}

// roundOf is the round that asks for a group's readings.
func roundOf(g Group) Round {
	switch g {
	case GroupSessions, GroupReasons, GroupStreaming:
		return RoundSessions
	default:
		return Round(g)
	}
}

// Every is how often each round is asked unless the settings say otherwise, by the round's word: what changes by the minute twice as often as a
// scrape every 30 seconds asks, what changes when a scan runs or someone is added every few minutes, and what was played once a scrape.
var Every = map[string]time.Duration{
	string(RoundServer):    15 * time.Second,
	string(RoundSessions):  15 * time.Second,
	string(RoundTasks):     15 * time.Second,
	string(RoundLibraries): 5 * time.Minute,
	string(RoundAccounts):  5 * time.Minute,
	string(RoundSignIns):   5 * time.Minute,
	string(RoundPlugins):   5 * time.Minute,
	string(RoundPlayed):    30 * time.Second,
}

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

	// Rounds is how each round last went, and has no round that has not run.
	Rounds map[Round]RoundState
}

// Up says whether the server answered the last time it was asked for its own state, which is asked whatever the page leaves off.
func (s *State) Up() bool { return s.Rounds[RoundServer].OK }

// Round is how one round last went.
func (s *State) Round(round Round) RoundState { return s.Rounds[round] }

// Poller asks each server its questions, each group's on its own schedule, and remembers the last good answer to each. A group left off the page is
// not asked for, and its answers are never known.
type Poller struct {
	*poller.Poller[string]

	skip Skip

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

// New is a poller for these servers, each under its kind, asking each round as often as every says and none of the groups skip leaves off. It asks
// nothing until Run or Poll.
func New(every map[string]time.Duration, skip Skip, servers ...Server) (*Poller, error) {
	byKind := map[string]Server{}
	for _, server := range servers {
		if _, twice := byKind[server.Kind()]; twice {
			return nil, fmt.Errorf("two servers are both %s", server.Kind())
		}
		byKind[server.Kind()] = server
	}

	runs := map[Round]bool{}
	for _, round := range skip.Runs() {
		runs[round] = true
	}

	for _, round := range Rounds {
		if runs[round] && every[string(round)] <= 0 {
			return nil, fmt.Errorf("the %s round must be asked more often than never, not every %s", round, every[string(round)])
		}
	}

	p, err := poller.New(poller.Options[string]{Namespace: "embyfin", Label: serverWord, Kind: serverWord, Targets: poller.Fixed(slices.Sorted(maps.Keys(byKind))...)})
	if err != nil {
		return nil, err
	}

	// schedule is the schedule of a round that runs, and none for one that does not
	schedule := func(round Round) *poller.Schedule[string] {
		if !runs[round] {
			return nil
		}

		return p.Schedule(string(round), every[string(round)])
	}

	accounts := schedule(RoundAccounts)

	return &Poller{
		Poller:    p,
		skip:      skip,
		info:      ask(schedule(RoundServer), "server info", byKind, Server.Info),
		sessions:  ask(schedule(RoundSessions), "sessions", byKind, Server.Sessions),
		tasks:     ask(schedule(RoundTasks), "scheduled tasks", byKind, Server.Tasks),
		libraries: ask(schedule(RoundLibraries), "libraries", byKind, Server.Libraries),
		users:     ask(accounts, "users", byKind, Server.Users),
		devices:   ask(accounts, "devices", byKind, Server.Devices),
		logins:    ask(schedule(RoundSignIns), "sign-ins", byKind, Server.Logins),
		plugins:   ask(schedule(RoundPlugins), "plugins", byKind, Server.Plugins),
		history:   ask(schedule(RoundPlayed), "what was played", byKind, Server.History),
	}, nil
}

// ask puts one question to each server, every round of the schedule, and is nothing for a round that does not run.
func ask[V any](s *poller.Schedule[string], what string, servers map[string]Server, question func(Server, context.Context) (V, error)) *poller.Kept[V] {
	if s == nil {
		return nil
	}

	return poller.Ask(s, what, func(ctx context.Context, kind string) (V, error) { return question(servers[kind], ctx) })
}

// answer is the last good answer about one server, and none for a question that is not asked.
func answer[V any](k *poller.Kept[V], kind string) Answer[V] {
	if k == nil {
		return Answer[V]{}
	}

	return k.For(kind)
}

// keep keeps an answer about one server as given, unless the question is not asked.
func keep[V any](k *poller.Kept[V], kind string, a Answer[V]) {
	if k != nil {
		k.Set(kind, a)
	}
}

// Skipped is the groups left off the page.
func (p *Poller) Skipped() Skip { return p.skip }

// Poll runs one round now for every server, and says what failed. A round that does not run is an error.
func (p *Poller) Poll(ctx context.Context, round Round) error {
	return p.Poller.Poll(ctx, string(round))
}

// State is what is known of one server now, without asking it anything. An answer older than three of its round's intervals is left out: the server
// has stopped answering, and what it last said is no longer worth showing as current.
func (p *Poller) State(kind string) State {
	rounds := map[Round]RoundState{}
	for _, round := range Rounds {
		if r := p.Round(kind, string(round)); r.Ran {
			rounds[round] = r
		}
	}

	return State{
		Kind:      kind,
		Info:      answer(p.info, kind),
		Sessions:  answer(p.sessions, kind),
		Tasks:     answer(p.tasks, kind),
		Libraries: answer(p.libraries, kind),
		Users:     answer(p.users, kind),
		Devices:   answer(p.devices, kind),
		Logins:    answer(p.logins, kind),
		Plugins:   answer(p.plugins, kind),
		History:   answer(p.history, kind),
		Rounds:    rounds,
	}
}

// Set makes what is known of one server the state given, as though it had been asked: for a test's made-up state.
func (p *Poller) Set(s *State) {
	p.SetTarget(poller.Target[string]{Key: s.Kind, Value: s.Kind, Listed: true, At: time.Now()})

	keep(p.info, s.Kind, s.Info)
	keep(p.sessions, s.Kind, s.Sessions)
	keep(p.tasks, s.Kind, s.Tasks)
	keep(p.libraries, s.Kind, s.Libraries)
	keep(p.users, s.Kind, s.Users)
	keep(p.devices, s.Kind, s.Devices)
	keep(p.logins, s.Kind, s.Logins)
	keep(p.plugins, s.Kind, s.Plugins)
	keep(p.history, s.Kind, s.History)

	for round, r := range s.Rounds {
		p.SetRound(s.Kind, string(round), r)
	}
}
