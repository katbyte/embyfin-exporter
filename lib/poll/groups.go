package poll

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// serverWord is what a server is called: the label each reading names it under, the kind of thing the log says it is, and the group of its own
// readings, which is always on.
const serverWord = "server"

// Group is one of the groups the page's readings are in, by the word the metrics and skip settings know it by. The server's own readings are in none: they say
// whether it answers, and so how far to trust the rest, and are always on the page.
type Group string

const (
	// GroupPoll is how the asking goes: how often each round is asked, how long it took, when it last answered and how often it failed. It has no
	// round of its own, and leaving it off saves the servers nothing.
	GroupPoll Group = "poll"
	// GroupSessions is sessions and streams: devices playing, paused or idle, and how what they play reaches them.
	GroupSessions Group = "sessions"
	// GroupReasons is why a stream is made: the server's reasons for converting, and what the streams it makes come to.
	GroupReasons Group = "reasons"
	// GroupStreaming is who is streaming now: a line for each stream.
	GroupStreaming Group = "streaming"
	// GroupTasks is scheduled tasks and the library scan.
	GroupTasks Group = "tasks"
	// GroupPlugins is the plugins each server has, and which have a newer version on offer.
	GroupPlugins Group = "plugins"
	// GroupLibraries is the libraries and what each holds.
	GroupLibraries Group = "libraries"
	// GroupAccounts is accounts and devices.
	GroupAccounts Group = "accounts"
	// GroupSignIns is sign-ins and refused tries.
	GroupSignIns Group = "sign-ins"
	// GroupPlayed is what was played, from the Playback Reporting plugin.
	GroupPlayed Group = "played"
)

// Groups is every group that can be left off, in the order the page's documentation lists them.
var Groups = []Group{GroupPoll, GroupSessions, GroupReasons, GroupStreaming, GroupTasks, GroupPlugins, GroupLibraries, GroupAccounts, GroupSignIns, GroupPlayed}

// Skip is the groups to leave off the page. A server is not asked for what only they would show.
type Skip map[Group]bool

// Has says whether a group is left off. No skip at all leaves nothing off.
func (s Skip) Has(g Group) bool { return s[g] }

// String is the groups left off, as the setting writes them.
func (s Skip) String() string {
	var words []string
	for _, g := range Groups {
		if s[g] {
			words = append(words, string(g))
		}
	}

	return strings.Join(words, ",")
}

// ParseGroups reads groups from a setting written as their words with commas between them, as plugins,played is. A word that is not a group is an
// error that names the ones there are, and so is the server's own, which is always on and named in neither setting.
func ParseGroups(setting string) ([]Group, error) {
	var groups []Group
	for word := range strings.SplitSeq(setting, ",") {
		switch g := Group(strings.ToLower(strings.TrimSpace(word))); {
		case g == "":
		case g == serverWord:
			return nil, errors.New("the server's own readings are always on the page, since they say whether it answers and so how far to trust the rest, and are not named")
		case slices.Contains(Groups, g):
			groups = append(groups, g)
		default:
			words := make([]string, 0, len(Groups))
			for _, g := range Groups {
				words = append(words, string(g))
			}

			return nil, fmt.Errorf("the groups are %s, with commas between them, and not %q", strings.Join(words, ", "), strings.TrimSpace(word))
		}
	}

	return groups, nil
}

// Leave is the groups to leave off the page: every one metrics does not name when it names any, and every one skip names.
func Leave(metrics, skip []Group) Skip {
	off := Skip{}
	for _, g := range Groups {
		if (len(metrics) != 0 && !slices.Contains(metrics, g)) || slices.Contains(skip, g) {
			off[g] = true
		}
	}

	return off
}

// Runs is the rounds run when these groups are left off, in order: the server's own always, and any other while a group it asks for is on the page.
// How the asking goes is asked for by no round.
func (s Skip) Runs() []Round {
	runs := []Round{RoundServer}
	for _, round := range Rounds[1:] {
		if slices.ContainsFunc(Groups, func(g Group) bool { return g != GroupPoll && roundOf(g) == round && !s.Has(g) }) {
			runs = append(runs, round)
		}
	}

	return runs
}
