package mediaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katbyte/embyfin-exporter/lib/poll"
	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
)

// fakeLog answers in an activity log's place with the entries it is set to, and remembers since when it was asked for them.
type fakeLog struct {
	entries []activity
	err     error
	since   time.Time
}

func (f *fakeLog) Activity(_ context.Context, since time.Time) ([]activity, error) {
	f.since = since

	return f.entries, f.err
}

// signIns is the sign-ins a server hands on, as week, how it went, where from, whose and how many, in an order that does not change.
func signIns(logins []poll.Login) []string {
	out := make([]string, 0, len(logins))
	for _, l := range logins {
		result := "ok"
		if l.Failed {
			result = "failed"
		}
		out = append(out, strings.Join([]string{l.Week, result, string(l.Network), l.User, strconv.Itoa(l.Count)}, " "))
	}
	slices.Sort(out)

	return out
}

// Sign-ins and refused tries, from entries as a real Emby 4.10 and a real Jellyfin 12.2 write them: each is under the week it was made in, by
// where it came from, and by account when accounts are to be named. A try on a name the server has no account of is under unknown, whatever
// name was tried, and what is neither a sign-in nor a try is not counted.
func TestLogins(t *testing.T) {
	t.Parallel()

	accounts := []embyfin.User{{Name: "ada"}, {Name: "Grace Hopper"}}

	for name, c := range map[string]struct {
		backend embyfin.Backend
		entries []activity
	}{
		"emby": {embyfin.Emby, []activity{
			{Type: "user.authenticated", Name: "ada Has Authenticated on den", Overview: "the television 192.168.1.20\n\n\n\n\nTime\n\nTuesday, October 13, 2026 4:29 AM", Date: "2026-10-13T04:29:00.3900000Z"},
			{Type: "user.authenticated", Name: "ada has Authenticated on den", Overview: "a phone 203.0.113.7\n\n\n\n\nTime", Date: "2026-10-14T04:29:00.3900000Z"},
			{Type: "user.authenticated", Name: "Grace Hopper Has Authenticated on den", Overview: "a phone 203.0.113.7", Date: "2026-10-14T05:00:00.0000000Z"},
			{Type: "user.authenticationfailed", Name: "Failed Login Attempt from ada on den", Overview: "203.0.113.9\n\n\n\n\nTime", Date: "2026-10-14T06:00:00.0000000Z"},
			{Type: "user.authenticationfailed", Name: "Failed Login Attempt from mallory on den", Overview: "203.0.113.9\n\n\n\n\nTime", Date: "2026-10-14T06:00:01.0000000Z"},
			{Type: "user.authenticationfailed", Name: `Failed Login Attempt from admin",evil="1 on den`, Overview: "203.0.113.9", Date: "2026-10-14T06:00:02.0000000Z"},
			{Type: "AuthenticationFailed", Name: "Failed Login Attempt from Grace Hopper on den", Short: "IP address: 192.168.1.30", Date: "2026-10-07T12:00:00.0000000Z"},
			{Type: "playback.start", Name: "ada is playing Heat on the television", Date: "2026-10-14T06:00:02.0000000Z"},
			{Type: "user.authenticated", Name: "ada Has Authenticated on den", Date: "never"},
		}},
		"jellyfin": {embyfin.Jellyfin, []activity{
			{Type: "AuthenticationSucceeded", Name: "ada successfully authenticated", Short: "IP address: 192.168.1.20", Date: "2026-10-13T04:29:14.520627Z"},
			{Type: "AuthenticationSucceeded", Name: "ada successfully authenticated", Short: "IP address: 203.0.113.7", Date: "2026-10-14T04:29:14.520627Z"},
			{Type: "AuthenticationSucceeded", Name: "Grace Hopper successfully authenticated", Short: "IP address: 203.0.113.7", Date: "2026-10-14T05:00:00.52Z"},
			{Type: "AuthenticationFailed", Name: "Failed login attempt from ada", Short: "IP address: 203.0.113.9", Date: "2026-10-14T06:00:00.4220376Z"},
			{Type: "AuthenticationFailed", Name: "Failed login attempt from mallory", Short: "IP address: 203.0.113.9", Date: "2026-10-14T06:00:01.4220376Z"},
			{Type: "AuthenticationFailed", Name: `Failed login attempt from admin",evil="1`, Short: "IP address: 203.0.113.9", Date: "2026-10-14T06:00:02.4220376Z"},
			{Type: "AuthenticationFailed", Name: "Failed login attempt from GRACE HOPPER", Short: "IP address: 192.168.1.30", Date: "2026-10-07T12:00:00.4220376Z"},
			{Type: "SessionStarted", Name: "ada is online from a phone", Short: "IP address: 203.0.113.7", Date: "2026-10-14T06:00:02.4220376Z"},
			{Type: "AuthenticationSucceeded", Name: "ada successfully authenticated", Date: ""},
		}},
	} {
		log := &fakeLog{entries: c.entries}
		s := &Server{client: &fake{backend: c.backend, users: accounts}, log: log, detail: Detail{Users: true}}

		got, err := s.Logins(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		want := []string{
			"2026-10-05 failed home Grace Hopper 1",
			"2026-10-12 failed remote ada 1",
			"2026-10-12 failed remote unknown 2",
			"2026-10-12 ok home ada 1",
			"2026-10-12 ok remote Grace Hopper 1",
			"2026-10-12 ok remote ada 1",
		}
		if !slices.Equal(signIns(got), want) {
			t.Errorf("%s: sign-ins =\n%s\nwant\n%s", name, strings.Join(signIns(got), "\n"), strings.Join(want, "\n"))
		}

		// asked for since the Monday before this week's, at midnight in UTC
		if since := log.since; since.Weekday() != time.Monday || since.Location() != time.UTC || since.Hour()+since.Minute()+since.Second() != 0 || time.Since(since) < 7*24*time.Hour || time.Since(since) >= 14*24*time.Hour {
			t.Errorf("%s: the log was asked for since %s, want the Monday before this week's", name, since)
		}

		// not asked to name accounts, nobody is, and the server is not asked who its accounts are
		plain := &Server{client: &fake{backend: c.backend, err: errDown}, log: &fakeLog{entries: c.entries}}
		got, err = plain.Logins(t.Context())
		want = []string{"2026-10-05 failed home  1", "2026-10-12 failed remote  3", "2026-10-12 ok home  1", "2026-10-12 ok remote  2"}
		if err != nil || !slices.Equal(signIns(got), want) {
			t.Errorf("%s, naming nobody: sign-ins = %v and %v, want %v", name, signIns(got), err, want)
		}
	}
}

// A log that cannot be read, or accounts that cannot be listed when they are to be named, is an error and no sign-ins.
func TestLoginsThatCannotBeRead(t *testing.T) {
	t.Parallel()

	entries := []activity{{Type: "AuthenticationSucceeded", Name: "ada successfully authenticated", Date: "2026-10-13T04:29:14.520627Z"}}

	if got, err := (&Server{client: &fake{}, log: &fakeLog{err: errDown}}).Logins(t.Context()); err == nil || got != nil {
		t.Errorf("with the log failing: sign-ins = %v and %v, want an error and none", got, err)
	}
	if got, err := (&Server{client: &fake{err: errDown}, log: &fakeLog{entries: entries}, detail: Detail{Users: true}}).Logins(t.Context()); err == nil || got != nil {
		t.Errorf("with the accounts failing: sign-ins = %v and %v, want an error and none", got, err)
	}
}

// Whose a sign-in was, read out of the server's own sentence for it. Only one of the server's own accounts is ever named: a try may be on any
// name at all, typed by anyone who can reach the server, and none of that is to become a label.
func TestSignedInAs(t *testing.T) {
	t.Parallel()

	accounts := []string{"ada", "Ada Lovelace", "on", "grace"}

	for _, c := range []struct {
		sentence     string
		emby, failed bool
		want         string
	}{
		{"ada Has Authenticated on den", true, false, "ada"},
		{"ADA has authenticated on den", true, false, "ada"},
		{"Ada Lovelace Has Authenticated on den", true, false, "Ada Lovelace"},
		{"on Has Authenticated on den", true, false, "on"},
		{"mallory Has Authenticated on den", true, false, "unknown"},
		{"Failed Login Attempt from grace on den", true, true, "grace"},
		{"Failed Login Attempt from ada lovelace on the den on the hill", true, true, "Ada Lovelace"},
		{"Failed Login Attempt from mallory on den", true, true, "unknown"},
		{"Failed Login Attempt from ada", true, true, "unknown"},
		{"ada successfully authenticated", false, false, "ada"},
		{"Ada Lovelace successfully authenticated", false, false, "Ada Lovelace"},
		{"mallory successfully authenticated", false, false, "unknown"},
		{"Failed login attempt from grace", false, true, "grace"},
		{"Failed login attempt from grace and more", false, true, "unknown"},
		{`Failed login attempt from x",server="y`, false, true, "unknown"},
		// a server set to another language is not read, rather than read wrongly
		{"ada hat sich erfolgreich angemeldet", false, false, "unknown"},
		{"", true, true, "unknown"},
	} {
		if got := signedInAs(c.sentence, c.emby, c.failed, accounts); got != c.want {
			t.Errorf("signedInAs(%q, emby %t, failed %t) = %q, want %q", c.sentence, c.emby, c.failed, got, c.want)
		}
	}
}

// Which entries of the log are sign-ins, by the kinds each server has given them, and where one came from by what the server wrote beside it.
func TestSignInAndCameFrom(t *testing.T) {
	t.Parallel()

	for kind, want := range map[string][2]bool{
		"user.authenticated":        {false, true},
		"AuthenticationSucceeded":   {false, true},
		"user.authenticationfailed": {true, true},
		"AuthenticationFailed":      {true, true},
		"authenticationfailed":      {true, true},
		"playback.start":            {false, false},
		"UserPasswordChanged":       {false, false},
		"":                          {false, false},
	} {
		if failed, is := signIn(kind); failed != want[0] || is != want[1] {
			t.Errorf("signIn(%q) = failed %t, a sign-in %t; want %t and %t", kind, failed, is, want[0], want[1])
		}
	}

	for want, entry := range map[poll.Network][]activity{
		poll.Home:           {{Short: "IP address: 192.168.1.20"}, {Overview: "the television 10.0.0.9\n\n\nTime"}, {Overview: "172.17.0.1\n\n\n\n\nTime\n\nTuesday, October 13, 2026 4:29 AM"}, {Short: "IP address: ::1"}},
		poll.Remote:         {{Short: "IP address: 203.0.113.7"}, {Overview: "a phone 203.0.113.7\n\nTime"}, {Overview: "2001:db8::7\n\nTime"}, {Short: "IP address: 203.0.113.7,"}},
		poll.UnknownNetwork: {{}, {Short: "IP address:"}, {Overview: "Time\n\nTuesday, October 13, 2026 4:29 AM"}},
	} {
		for i := range entry {
			if got := cameFrom(&entry[i]); got != want {
				t.Errorf("cameFrom(%+v) = %q, want %q", entry[i], got, want)
			}
		}
	}
}

// A week is named by its Monday in UTC, whatever clock the moment is given in.
func TestMonday(t *testing.T) {
	t.Parallel()

	vancouver := time.FixedZone("PDT", -7*3600)

	for want, moment := range map[string]time.Time{
		"2026-10-12": time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		"2026-10-05": time.Date(2026, 10, 11, 23, 59, 59, 0, time.UTC),
		"2026-06-01": time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		// eight in the evening on a Sunday in Vancouver is already Monday in UTC
		"2026-10-19": time.Date(2026, 10, 18, 20, 0, 0, 0, vancouver),
		"2025-12-29": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got := monday(moment); got.Format(time.DateOnly) != want || got.Location() != time.UTC {
			t.Errorf("monday(%s) = %s, want %s", moment, got, want)
		}
	}
}

// The log is read a page at a time until the server has no more, each page asked for from where the last ended and since the same moment, with
// the key in a header. A log with more than is ever read is an error and not half an answer.
func TestTheActivityLogIsReadAPageAtATime(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path+" since "+r.URL.Query().Get("MinDate")+" limit "+r.URL.Query().Get("Limit")+" from "+r.URL.Query().Get("StartIndex")+" keyed "+strconv.FormatBool(strings.Contains(r.Header.Get("X-Emby-Token")+r.Header.Get("Authorization"), testKey)))
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("StartIndex") == "0" {
			_, _ = w.Write([]byte(`{"Items":[{"Type":"AuthenticationFailed","Name":"one","ShortOverview":"IP address: 203.0.113.7","Date":"2026-10-14T06:00:00.4220376Z"},{"Type":"user.authenticated","Name":"two","Overview":"a phone 10.0.0.9"}],"TotalRecordCount":3}`))
			return
		}
		_, _ = w.Write([]byte(`{"Items":[{"Type":"playback.start","Name":"three"}],"TotalRecordCount":3}`))
	}))
	t.Cleanup(server.Close)

	since := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

	for _, backend := range []embyfin.Backend{embyfin.Emby, embyfin.Jellyfin} {
		d, err := connectDirect(backend, server.URL, testKey)
		if err != nil {
			t.Fatal(err)
		}

		got, err := d.Activity(t.Context(), since)
		if err != nil || len(got) != 3 || got[0].Short != "IP address: 203.0.113.7" || got[1].Overview != "a phone 10.0.0.9" || got[2].Name != "three" {
			t.Errorf("%s: the log = %+v and %v, want the three entries of its two pages", backend, got, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	want := []string{
		"/System/ActivityLog/Entries since 2026-10-05T00:00:00Z limit 1000 from 0 keyed true",
		"/System/ActivityLog/Entries since 2026-10-05T00:00:00Z limit 1000 from 2 keyed true",
	}
	if !slices.Equal(asked, append(want, want...)) {
		t.Errorf("the log was asked for as\n%s\nwant these, once for each server\n%s", strings.Join(asked, "\n"), strings.Join(want, "\n"))
	}
}

// A log that never runs out is given up on, with an error, after fifty pages: that is fifty thousand entries in two weeks, which no home
// server has.
func TestALogWithNoEndIsGivenUpOn(t *testing.T) {
	t.Parallel()

	var pages int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		pages++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Items":[{"Type":"playback.start","Name":"again"}],"TotalRecordCount":1000000}`))
	}))
	t.Cleanup(server.Close)

	d, err := connectDirect(embyfin.Emby, server.URL, testKey)
	if err != nil {
		t.Fatal(err)
	}

	got, err := d.Activity(t.Context(), time.Now())
	if err == nil || got != nil || pages != activityPages {
		t.Errorf("the log = %d entries and %v after %d pages, want an error and none after %d", len(got), err, pages, activityPages)
	}
}
