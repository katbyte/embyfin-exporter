package mediaserver

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/embyfin-mcp/sdk/emby/4.10.0.40/emby"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/embyfin-mcp/sdk/jf/12.1.0/jf"
	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/katbyte/go-kt/clog"
	apiclient "github.com/katbyte/pandorest/client"
)

// Detail is what the page may say of who plays what, which is nothing unless the settings ask. A name or a title that was not asked for is never
// asked of the server, so it cannot reach the page by mistake.
type Detail struct {
	// Users names accounts, and Titles shows and films.
	Users  bool
	Titles bool
}

// unknownUser is whose a play was when the account has since been deleted, in the plugin's own word for them.
const unknownUser = "unknown"

// reporter is what is asked of a server's Playback Reporting plugin, so a test can answer in its place.
type reporter interface {
	// Query puts one question, in SQL, to the plugin's record of what was played, and gives back its rows. names has it give an account's name where
	// the question asks for UserId. installed is false for a server without the plugin.
	Query(ctx context.Context, sql string, names bool) (rows [][]string, installed bool, err error)
}

// plugin asks the Playback Reporting plugin of a real server. The plugin is no part of either server's own API, so embyfin-mcp has no call for it:
// the request is made here, through the same kind of client that makes embyfin-mcp's.
type plugin struct {
	base *apiclient.Client
}

// connectPlugin is the plugin of the server of that kind at address, asked with the server's own key.
func connectPlugin(backend embyfin.Backend, address, token string, opts ...apiclient.Option) (*plugin, error) {
	connect := jf.New
	if backend == embyfin.Emby {
		connect = emby.New
	}

	base, err := connect(address, token, opts...)
	if err != nil {
		return nil, err
	}

	return &plugin{base: base}, nil
}

// nothingFound is how the plugin's message begins when a question ran and gave no rows.
const nothingFound = "Query executed"

// errBusy is the plugin's record being written to as the question arrived. Jellyfin's plugin writes a play's length every few seconds while it
// plays and fails a read that lands on a write, sometimes saying the database is locked and sometimes with a bare HTTP 500 (both seen on
// 19.0.0.0); asked again a moment later it answers.
var errBusy = errors.New("the Playback Reporting plugin's record was being written to")

// How often a question that found the record busy is put, and how long is left between one try and the next.
const (
	busyTries = 4
	busyWait  = 250 * time.Millisecond
)

// cell is one value of an answer. The plugin writes every one as text, a number included, and nothing at all for an empty one.
type cell string

func (c *cell) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*c = cell(text)
		return nil
	}

	// a number written bare
	*c = cell(data)

	return nil
}

// Query sends the question with a POST, the only way the plugin takes one. It is still only a read: the question is one this program wrote, and it
// asks for sums.
func (p *plugin) Query(ctx context.Context, sql string, names bool) (rows [][]string, installed bool, err error) {
	req, err := p.base.NewRequest(ctx, apiclient.RequestOptions{ExpectedStatusCodes: []int{http.StatusOK}, HTTPMethod: http.MethodPost, Path: "/user_usage_stats/submit_custom_query"})
	if err != nil {
		return nil, false, err
	}

	question := struct {
		Query string `json:"CustomQueryString"`
		Names bool   `json:"ReplaceUserId"`
	}{sql, names}
	if err := req.Marshal(question); err != nil {
		return nil, false, err
	}

	resp, err := req.Execute(ctx)
	switch apiclient.StatusCode(err) {
	case http.StatusNotFound:
		// a server without the plugin has nothing at its address
		return nil, false, nil
	case http.StatusInternalServerError:
		return nil, false, fmt.Errorf("%w: %w", errBusy, err)
	}
	if err != nil {
		return nil, false, err
	}

	var answer struct {
		Results [][]cell `json:"results"`
		Message string   `json:"message"`
	}
	if err := resp.Unmarshal(&answer); err != nil {
		return nil, false, err
	}
	// a question it could not answer comes back as a success with a message, a page of the plugin's own workings after its first line. Jellyfin's
	// plugin also sends one when a question ran and nothing matched (seen on 19.0.0.0), which is an answer of no rows
	if answer.Message != "" && !strings.HasPrefix(answer.Message, nothingFound) {
		if strings.Contains(answer.Message, "database is locked") {
			return nil, false, errBusy
		}

		said, _, _ := strings.Cut(answer.Message, "<pre>")
		return nil, false, fmt.Errorf("the Playback Reporting plugin could not answer: %s", strings.ReplaceAll(said, "</br>", ": "))
	}

	rows = make([][]string, 0, len(answer.Results))
	for _, result := range answer.Results {
		row := make([]string, 0, len(result))
		for _, value := range result {
			row = append(row, string(value))
		}
		rows = append(rows, row)
	}

	return rows, true, nil
}

// The two weeks the page carries, as the plugin's database is asked for them, on the server's own clock: the Monday of the week a play began in,
// and the Monday before this week's. The plugin deletes what is older than it is set to keep, a month at the least, so the sums of these two weeks
// never go down; a longer stretch, a whole month say, could.
const (
	weekBegun = "date(DateCreated, 'weekday 0', '-6 days')"
	lastWeek  = "date('now', 'localtime', 'weekday 0', '-13 days')"
)

// historyQuery is the question put to the plugin: how many plays there were and how long they ran, in this week and the last, by kind and by how
// each reached the device, and by account and by title only when detail asks for those. Nothing in it comes from outside this program.
func historyQuery(detail Detail) string {
	by := []string{weekBegun, "ItemType", "PlaybackMethod"}
	if detail.Users {
		by = append(by, "UserId")
	}
	if detail.Titles {
		by = append(by, "ItemName")
	}
	columns := strings.Join(by, ", ")

	return "SELECT " + columns + ", COUNT(*), SUM(PlayDuration) FROM PlaybackActivity WHERE DateCreated >= " + lastWeek + " GROUP BY " + columns
}

// History is what the server's Playback Reporting plugin has of what was played in this week and the last. A server without the plugin has no
// history, which is an answer and not a failure.
func (s *Server) History(ctx context.Context) (poll.History, error) {
	rows, installed, err := s.askPlugin(ctx)
	if err != nil {
		return poll.History{}, err
	}

	s.noteHistory(installed)
	if !installed {
		return poll.History{}, nil
	}

	plays, byUser, byTitle := map[poll.Played]poll.Played{}, map[poll.Played]poll.Played{}, map[poll.Played]poll.Played{}
	for _, row := range rows {
		p, err := historyRow(row, s.detail)
		if err != nil {
			return poll.History{}, err
		}

		tally(plays, poll.Played{Week: p.Week, Kind: p.Kind, Method: p.Method}, p)
		if s.detail.Users {
			tally(byUser, poll.Played{Week: p.Week, Kind: p.Kind, User: p.User}, p)
		}
		// only a show or a film has a title here
		if s.detail.Titles && p.Title != "" {
			tally(byTitle, poll.Played{Week: p.Week, Kind: p.Kind, Title: p.Title}, p)
		}
	}

	return poll.History{Installed: true, Plays: listed(plays), ByUser: listed(byUser), ByTitle: listed(byTitle)}, nil
}

// askPlugin puts the question to the plugin, and puts it again a moment later when the plugin's record was being written to as it arrived.
func (s *Server) askPlugin(ctx context.Context) (rows [][]string, installed bool, err error) {
	for try := 1; ; try++ {
		rows, installed, err = s.reports.Query(ctx, historyQuery(s.detail), s.detail.Users)
		if !errors.Is(err, errBusy) || try == busyTries {
			return rows, installed, err
		}

		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(busyWait):
		}
	}
}

// noteHistory says in the log, once and again only when it changes, whether the server has the plugin: without it the play time readings are simply
// not on the page.
func (s *Server) noteHistory(installed bool) {
	state := int32(1)
	if installed {
		state = 2
	}
	if s.history.Swap(state) == state {
		return
	}

	if installed {
		clog.Log.Infof("%s: reading what was played from the server's Playback Reporting plugin", s.Kind())
		return
	}

	clog.Log.Infof("%s: the server has no Playback Reporting plugin, so there are no readings of what was played", s.Kind())
}

// historyRow is one row of the plugin's answer in the page's words. The columns are in the order historyQuery asked for them.
func historyRow(row []string, detail Detail) (poll.Played, error) {
	want := 5
	if detail.Users {
		want++
	}
	if detail.Titles {
		want++
	}
	if len(row) != want {
		return poll.Played{}, fmt.Errorf("the Playback Reporting plugin gave a row of %d values where %d were asked for", len(row), want)
	}
	if _, err := time.Parse(time.DateOnly, row[0]); err != nil {
		return poll.Played{}, fmt.Errorf("the Playback Reporting plugin gave %q where the day a week began was asked for", row[0])
	}

	p := poll.Played{Week: row[0], Kind: playing(row[1]), Method: reported(row[2])}
	next := 3
	if detail.Users {
		p.User = account(row[next])
		next++
	}
	if detail.Titles {
		p.Title = title(p.Kind, row[next])
		next++
	}

	var errs [2]error
	p.Plays, errs[0] = number(row[next])
	p.Seconds, errs[1] = number(row[next+1])

	return p, errors.Join(errs[:]...)
}

// number is a count as the plugin writes one, and nothing for an empty one.
func number(text string) (int, error) {
	if text == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("the Playback Reporting plugin gave %q where a number was asked for", text)
	}

	return max(n, 0), nil
}

// tally adds one row's plays to the sum kept under by, which names what tells that sum from the others.
func tally(into map[poll.Played]poll.Played, by, row poll.Played) {
	sum, seen := into[by]
	if !seen {
		sum = by
	}
	sum.Plays += row.Plays
	sum.Seconds += row.Seconds

	into[by] = sum
}

// listed is the sums in an order that does not change from one asking to the next.
func listed(sums map[poll.Played]poll.Played) []poll.Played {
	out := make([]poll.Played, 0, len(sums))
	for _, sum := range sums {
		out = append(out, sum)
	}

	slices.SortFunc(out, func(a, b poll.Played) int {
		return cmp.Or(cmp.Compare(a.Week, b.Week), cmp.Compare(a.User, b.User), cmp.Compare(a.Title, b.Title), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Method, b.Method))
	})

	return out
}

// reported is how a play reached the device, from the plugin's word for it: the device's own, and for a stream the server made what was done to
// the picture and the sound, as in "Transcode (v:direct a:aac)". As with a session, a stream with nothing in it re-encoded is a direct stream.
func reported(word string) poll.Method {
	name, streams, _ := strings.Cut(word, " (")

	m := method(name)
	if m == poll.Transcode && strings.Contains(streams, "v:direct") && strings.Contains(streams, "a:direct") {
		return poll.DirectStream
	}

	return m
}

// gone is what the plugin leaves in place of a name for an account that has been deleted: the account's id.
var gone = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// account is whose a play was, from the name the plugin gives.
func account(name string) string {
	if name == "" || gone.MatchString(name) {
		return unknownUser
	}

	return name
}

// numbered is where the plugin's name for an episode stops being the show's: the " - s01e02 - " of "Dark - s01e02 - Lies".
var numbered = regexp.MustCompile(` - s\d+e\d+ - `)

// title is the show an episode is of or the film's own name, from the plugin's name for what was played. Nothing else has a title here: a song
// or a trailer would be a reading for every one ever played.
func title(kind poll.Kind, name string) string {
	switch kind {
	case poll.Movie:
		return name
	case poll.Episode:
		if at := numbered.FindStringIndex(name); at != nil {
			return name[:at[0]]
		}

		return name
	default:
		return ""
	}
}
