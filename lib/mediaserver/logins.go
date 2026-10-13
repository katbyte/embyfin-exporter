package mediaserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/embyfin-metrics/lib/poll"
	apiclient "github.com/katbyte/pandorest/client"
)

// activity is one entry of a server's activity log, as far as a sign-in needs it. Emby writes where a sign-in came from in the entry's overview,
// which embyfin-mcp's entry leaves out, so the log is read here.
type activity struct {
	Type     string `json:"Type"`
	Name     string `json:"Name"`
	Overview string `json:"Overview"`
	Short    string `json:"ShortOverview"`
	Date     string `json:"Date"`
}

// activities is what is asked of a server for its activity log, so a test can answer in its place.
type activities interface {
	// Activity is every entry of the log since a time, however many there are up to a limit no two weeks of a home server come near.
	Activity(ctx context.Context, since time.Time) ([]activity, error)
}

// How many entries one request reads, and how many requests one reading of the log makes at the most.
const (
	activityPage  = 1000
	activityPages = 50
)

// Activity reads the log since a time, a page at a time until the server has no more.
func (d *direct) Activity(ctx context.Context, since time.Time) ([]activity, error) {
	var out []activity
	for range activityPages {
		query := url.Values{"MinDate": {since.UTC().Format(time.RFC3339)}, "Limit": {strconv.Itoa(activityPage)}, "StartIndex": {strconv.Itoa(len(out))}}

		req, err := d.base.NewRequest(ctx, apiclient.RequestOptions{ExpectedStatusCodes: []int{http.StatusOK}, HTTPMethod: http.MethodGet, Path: "/System/ActivityLog/Entries?" + query.Encode()})
		if err != nil {
			return nil, err
		}

		resp, err := req.Execute(ctx)
		if err != nil {
			return nil, err
		}

		var page struct {
			Items []activity `json:"Items"`
			Total int        `json:"TotalRecordCount"`
		}
		if err := resp.Unmarshal(&page); err != nil {
			return nil, err
		}

		out = append(out, page.Items...)
		if len(page.Items) == 0 || len(out) >= page.Total {
			return out, nil
		}
	}

	return nil, fmt.Errorf("the activity log has more than %d entries in two weeks, which is more than is read", activityPage*activityPages)
}

// monday is the Monday of the week a moment falls in, in UTC, as the page names a week. A sign-in is kept under the week it was made in, and UTC
// is the one clock that puts it in the same week whatever this program's own is set to.
func monday(t time.Time) time.Time {
	year, month, day := t.UTC().Date()

	return time.Date(year, month, day-(int(t.UTC().Weekday())+6)%7, 0, 0, 0, 0, time.UTC)
}

// Logins is how many times accounts were signed in to and how many tries were refused, in this week and the last, by where each came from and,
// when accounts are to be named, by which account. It is read from the server's activity log, which keeps both.
func (s *Server) Logins(ctx context.Context) ([]poll.Login, error) {
	entries, err := s.log.Activity(ctx, monday(time.Now()).AddDate(0, 0, -7))
	if err != nil {
		return nil, err
	}

	var accounts []string
	if s.detail.Users {
		users, err := s.client.Users(ctx)
		if err != nil {
			return nil, err
		}
		for i := range users {
			accounts = append(accounts, users[i].Name)
		}
	}

	emby := s.client.Backend() == embyfin.Emby

	counts := map[poll.Login]int{}
	for i := range entries {
		entry := &entries[i]

		failed, isLogin := signIn(entry.Type)
		at := when(entry.Date)
		if !isLogin || at.IsZero() {
			continue
		}

		login := poll.Login{Week: monday(at).Format(time.DateOnly), Failed: failed, Network: cameFrom(entry)}
		if s.detail.Users {
			login.User = signedInAs(entry.Name, emby, failed, accounts)
		}
		counts[login]++
	}

	out := make([]poll.Login, 0, len(counts))
	for login, n := range counts {
		login.Count = n
		out = append(out, login)
	}

	return out, nil
}

// signIn says whether an entry of the activity log is a sign-in, and whether it was refused, from the kind the server gives it. Emby has called
// them user.authenticated and user.authenticationfailed, and before that what Jellyfin still does: AuthenticationSucceeded and AuthenticationFailed.
func signIn(kind string) (failed, is bool) {
	switch strings.ToLower(kind) {
	case "user.authenticated", "authenticationsucceeded":
		return false, true
	case "user.authenticationfailed", "authenticationfailed":
		return true, true
	default:
		return false, false
	}
}

// cameFrom is where a sign-in came from, home or remote, from the address the server writes into the entry: Jellyfin after "IP address:" in its
// short overview, Emby at the head of its overview, after the device's name for one that was let in. The first thing in either that reads as an
// address is taken, and the address goes no further than this.
func cameFrom(entry *activity) poll.Network {
	for word := range strings.FieldsSeq(entry.Short + " " + entry.Overview) {
		if n := network(strings.Trim(word, ",;()")); n != poll.UnknownNetwork {
			return n
		}
	}

	return poll.UnknownNetwork
}

// signedInAs is the account a sign-in was to, or was tried on, read out of the sentence the server writes for it, in the English both write by
// default: "ada Has Authenticated on den" and "Failed Login Attempt from ada on den" on Emby, "ada successfully authenticated" and "Failed login
// attempt from ada" on Jellyfin. A failed try may name anyone at all, so a name is only taken when it is one of the server's own accounts, and
// is "unknown" when it is not, or when the sentence is in another language: what someone outside types never becomes a label.
func signedInAs(sentence string, emby, failed bool, accounts []string) string {
	said := strings.ToLower(sentence)

	var name string
	for _, account := range accounts {
		is := strings.ToLower(account)

		var matches bool
		switch {
		case emby && failed:
			matches = strings.HasPrefix(said, "failed login attempt from "+is+" on ")
		case emby:
			matches = strings.HasPrefix(said, is+" has authenticated on ")
		case failed:
			matches = said == "failed login attempt from "+is
		default:
			matches = said == is+" successfully authenticated"
		}

		// the longest account that fits, should one's name begin another's
		if matches && len(account) > len(name) {
			name = account
		}
	}

	if name == "" {
		return unknownUser
	}

	return name
}
