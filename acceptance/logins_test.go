//go:build acceptance

package acceptance_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
)

// A name no account of the test server has, for a try to sign in that must be refused.
const stranger = "mallory-nobody-knows"

// try is one try to sign in from a device, with a password that may be wrong, and whether the server let it in.
func try(t *testing.T, device, name, pw string) bool {
	t.Helper()

	status, _ := player{device: device, app: laptopApp}.send(t, http.MethodPost, "/Users/AuthenticateByName", map[string]string{"Username": name, "Pw": pw})

	return status == http.StatusOK
}

// laptopApp is the app the sign-ins below are made with: a word on no reading.
const laptopApp = "Laptop Player"

// Sign-ins and refused tries on a real server are on the page: one let in is counted under its account for a program told to name accounts, one
// refused for a wrong password under the account it was tried on, and one on a name the server has no account of under nobody's name at all,
// where the server keeps a note of it. All came from this machine, which is at home to the server. The page of a program told to name nobody
// has the counts and no account.
func TestSignInsAreCounted(t *testing.T) {
	live(t)

	// a device the server has never seen, so that it has a sign-in to note
	device := "the-laptop-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	// the account no other test signs in to, so that one more is exactly one more: a server notes a sign-in a moment after it lets it in, and
	// one a test before this made can land between the two readings (seen on Emby 4.10)
	who := admin

	counted := summed
	before := scrapeNaming(t, poll.RoundLibrary)

	if try(t, device, who, "not-the-password") || try(t, device, stranger, "x") {
		t.Fatal("the server let in a try with a wrong password or no account")
	}
	if !try(t, device, who, password) {
		t.Fatalf("the server refused %s the right password", who)
	}

	var text string
	deadline := time.Now().Add(patience)
	for text = scrapeNaming(t, poll.RoundLibrary); time.Now().Before(deadline); text = scrapeNaming(t, poll.RoundLibrary) {
		if counted(text, "embyfin_user_logins_total", "user", who, "result", "ok") > counted(before, "embyfin_user_logins_total", "user", who, "result", "ok") && counted(text, "embyfin_user_logins_total", "user", who, "result", "failed") > counted(before, "embyfin_user_logins_total", "user", who, "result", "failed") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	for name, labels := range map[string][]string{
		"sign-ins by " + who:           {"user", who, "result", "ok"},
		"tries on " + who + " refused": {"user", who, "result", "failed"},
	} {
		if now, was := counted(text, "embyfin_user_logins_total", labels...), counted(before, "embyfin_user_logins_total", labels...); now != was+1 {
			t.Errorf("%s went from %v to %v, want one more:\n%s", name, was, now, strings.Join(every(text, "embyfin_user_logins_total"), "\n"))
		}
	}

	// Emby notes a try on a name it has no account of, and Jellyfin does not (seen on 4.10 and 12.2)
	unknown := counted(text, "embyfin_user_logins_total", "user", "unknown", "result", "failed") - counted(before, "embyfin_user_logins_total", "user", "unknown", "result", "failed")
	if want := map[string]float64{emby: 1, jellyfin: 0}[backend]; unknown != want {
		t.Errorf("tries on a name with no account went up by %v, want %v", unknown, want)
	}

	for _, result := range []string{"ok", "failed"} {
		if now, was := counted(text, "embyfin_logins_total", "result", result, "network", "home"), counted(before, "embyfin_logins_total", "result", result, "network", "home"); now <= was {
			t.Errorf("sign-ins from home that went %s: %v before and %v now, want more", result, was, now)
		}
		if remote := counted(text, "embyfin_logins_total", "result", result, "network", "remote"); remote != 0 {
			t.Errorf("%v sign-ins that went %s are counted as from outside, and every one was made from this machine", remote, result)
		}
	}

	// the week they are under is this one, by its Monday in UTC
	now := time.Now().UTC()
	monday := now.AddDate(0, 0, -(int(now.Weekday())+6)%7).Format(time.DateOnly)
	if got, found := value(text, reading("embyfin_user_logins_total", "user", who, "result", "failed", "week", monday)); !found || got < 1 {
		t.Errorf("tries on %s refused in the week of %s = %v (on the page: %t), want the one just made", who, monday, got, found)
	}

	// what was typed by whoever tried is on no page, and the page that names nobody has the counts and no account
	plain := scrape(t, poll.RoundLibrary)
	for name, page := range map[string]string{"told to name accounts": text, "told to name nobody": plain} {
		if strings.Contains(page, stranger) || strings.Contains(page, device) || strings.Contains(page, laptopApp) {
			t.Errorf("the page of a program %s has the name tried, the device or its app on it", name)
		}
	}
	if counted(plain, "embyfin_logins_total", "result", "failed", "network", "home") != counted(text, "embyfin_logins_total", "result", "failed", "network", "home") || len(every(plain, "embyfin_user_logins_total")) != 0 {
		t.Errorf("the page that names nobody should have the same count of refused tries and none by account:\n%s", strings.Join(append(every(plain, "embyfin_logins_total"), every(plain, "embyfin_user_logins_total")...), "\n"))
	}
	if found := named.FindAllString(plain, -1); len(found) != 0 {
		t.Errorf("the page that names nobody names %v", found)
	}
}
