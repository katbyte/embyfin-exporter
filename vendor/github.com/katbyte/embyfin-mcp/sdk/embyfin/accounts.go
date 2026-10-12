package embyfin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/katbyte/embyfin-mcp/sdk/emby/4.10.0.40/emby"
	"github.com/katbyte/embyfin-mcp/sdk/jf/12.1.0/jf"
	"github.com/katbyte/go-kt/lock"
	apiclient "github.com/katbyte/pandorest/client"
)

// Making, changing and removing accounts, as measured on Emby 4.10.1 and Jellyfin 12.2. A new account is an ordinary one: no administrator, every
// library, remote access on. It always gets a password here, because one without is open to whoever knows its name.

// ErrNoPassword refuses an account with no password to be made with or changed to.
var ErrNoPassword = errors.New("a password is needed: on both servers an empty one takes the account's password away, and anyone who knows its name can then log in as it")

// userByID is one account, read with the others so that it is mapped as they are.
func (c *Client) userByID(ctx context.Context, id string) (*User, error) {
	users, err := c.Users(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		if strings.EqualFold(users[i].ID, id) {
			return &users[i], nil
		}
	}

	return nil, fmt.Errorf("no account has the id %s", id)
}

// CreateUser makes an ordinary account with a password and answers with it as the server holds it. On Emby the password is a second call, and an
// account it fails for is removed again, since it would have none. On Jellyfin the account is then cleared of another's watch state
// (clearLeftover): the Leftover says how that went, and is not an error, the account being made either way.
func (c *Client) CreateUser(ctx context.Context, name, password string) (*User, Leftover, error) {
	if password == "" {
		return nil, Leftover{}, ErrNoPassword
	}

	var id string
	var left Leftover
	if c.isEmby() {
		res, err := c.emby.User.PostUsersNew(ctx, emby.CreateUserByName{Name: name})
		if err != nil {
			return nil, left, err
		}
		if res.Answer == nil || res.Answer.Id == "" {
			return nil, left, errors.New("the server answered the new account without an id")
		}
		id = string(res.Answer.Id)

		if err := c.SetUserPassword(ctx, id, password); err != nil {
			if _, gone := c.emby.User.DeleteUsersById(ctx, emby.UserID(id)); gone != nil {
				return nil, left, fmt.Errorf("the account %s was made (id %s) and its password could not be set: %w. Removing it again failed too (%w), so it is still there WITH NO PASSWORD: delete it, or set one", name, id, err, gone)
			}

			return nil, left, fmt.Errorf("the account %s was made, its password could not be set, and so it was removed again: %w", name, err)
		}
	} else {
		res, err := c.jf.User.CreateUserByName(ctx, jf.CreateUserByName{Name: name, Password: password})
		if err != nil {
			return nil, left, err
		}
		if res.Answer == nil || res.Answer.Id == "" {
			return nil, left, errors.New("the server answered the new account without an id")
		}
		id = string(res.Answer.Id)
		left = c.clearLeftover(ctx, id)
	}

	user, err := c.userByID(ctx, id)
	if err != nil {
		return nil, left, fmt.Errorf("the account %s was made (id %s), and reading it back failed: %w", name, id, err)
	}

	return user, left, nil
}

// Leftover is how clearing a new Jellyfin account of another's watch state went.
type Leftover struct {
	// Cleared is how many items it was listed with watch state on, each cleared again.
	Cleared int
	// Unchecked is why that could not be looked for or cleared, "" when it was: its lists may then still show another's.
	Unchecked string
}

// clearLeftover takes from a new Jellyfin account the watch state of an account deleted before it. Jellyfin 12.2 lists a new account with the
// other's watched marks, favourites, ratings and resume points until it is restarted, when no account made since still exists; a read of the one
// item and a filter both say the truth, which is how it hides. Un-marking each item sets the lists right, so its view is read, cleared, and read
// again to see it gone.
func (c *Client) clearLeftover(ctx context.Context, userID string) Leftover {
	listed, err := c.listedWithState(ctx, userID)
	if err != nil {
		return Leftover{Unchecked: err.Error()}
	}

	var left Leftover
	for i := range listed {
		if err := c.clearState(ctx, userID, &listed[i]); err != nil {
			left.Unchecked = fmt.Sprintf("%d of the %d items it is listed with another account's watch state on were cleared, and clearing %s failed: %v", left.Cleared, len(listed), listed[i].Name, err)

			return left
		}
		left.Cleared++
	}
	if left.Cleared == 0 {
		return left
	}

	still, err := c.listedWithState(ctx, userID)
	if err != nil {
		left.Unchecked = fmt.Sprintf("%d items it was listed with another account's watch state on were cleared, and reading them again to see it gone failed: %v", left.Cleared, err)
	} else if len(still) > 0 {
		left.Unchecked = fmt.Sprintf("%d items it was listed with another account's watch state on were cleared, and %d are still listed with some (%s, for one)", left.Cleared, len(still), still[0].Name)
	}

	return left
}

// listedWithState is every item in an account's view that a list shows with state of the account's own. A read the library changed under fails,
// since it cannot say there are no others.
func (c *Client) listedWithState(ctx context.Context, userID string) ([]Item, error) {
	var listed []Item
	read, err := c.ReadAll(ctx, SearchOptions{UserID: userID, EnableUserData: true, Fields: readAgainFields}, ToAct, func(items []Item) bool {
		for i := range items {
			if items[i].UserData.Any() {
				listed = append(listed, items[i])
			}
		}

		return true
	})
	if err != nil {
		return nil, err
	}
	if changed := read.Changed(); changed != "" {
		return nil, errors.New(changed)
	}

	return listed, nil
}

// clearState takes a Jellyfin account's state off an item, as much as it is listed with; marking it unwatched also clears its play count, date and
// resume point.
func (c *Client) clearState(ctx context.Context, userID string, it *Item) error {
	d := it.UserData
	if d.Played || d.PlayCount > 0 || d.PlaybackPositionTicks > 0 || d.LastPlayedDate != "" {
		if err := c.SetPlayed(ctx, userID, it.ID, false); err != nil {
			return err
		}
	}
	if d.IsFavourite {
		if err := c.SetFavourite(ctx, userID, it.ID, false); err != nil {
			return err
		}
	}
	if d.Rated {
		if _, err := c.jf.UserData.DeleteUserItemRating(ctx, jf.ItemID(it.ID), jf.UserID(userID)); err != nil {
			return err
		}
	}

	return nil
}

// SetUserPassword gives an account a new password, by the key's own right and without the old one.
func (c *Client) SetUserPassword(ctx context.Context, userID, password string) error {
	if password == "" {
		return ErrNoPassword
	}
	if c.isEmby() {
		_, err := c.emby.User.PostUsersByIdPassword(ctx, emby.UserID(userID), emby.UpdateUserPassword{NewPw: password})

		return err
	}

	_, err := c.jf.User.UpdateUserPassword(ctx, jf.UpdateUserPassword{NewPw: password}, jf.UserID(userID))

	return err
}

// ErrUserPlaying is why Jellyfin is not asked to delete an account that is playing something.
var ErrUserPlaying = errors.New("the whole Jellyfin server exits when an account is deleted within half a second of its watch state changing, and playback changes it every few seconds (seen on 12.2: its change notifier fails on the account that is gone, \"Invalid user ID\"). Stop what the account is playing, then delete it")

// DeleteUser removes an account and reads the accounts again to see it gone; its playlists stay on both servers, belonging to nobody. Jellyfin 12.2
// exits, the whole server, when an account is deleted within half a second of its watch state changing, so there one that is playing is refused
// (ErrUserPlaying) and any other is deleted only after userGrace.
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	var err error
	if c.isEmby() {
		_, err = c.emby.User.DeleteUsersById(ctx, emby.UserID(userID))
	} else {
		if err := c.jellyfinMayDelete(ctx, userID); err != nil {
			return err
		}
		_, err = c.jf.User.DeleteUser(ctx, jf.UserID(userID))
	}
	if err != nil {
		return err
	}

	users, err := c.Users(ctx)
	if err != nil {
		return fmt.Errorf("the server took the delete, and the accounts could not be read again to see it gone: %w", err)
	}
	for i := range users {
		if strings.EqualFold(users[i].ID, userID) {
			return fmt.Errorf("the server answered the delete of %s and still lists the account", users[i].Name)
		}
	}

	return nil
}

// jellyfinMayDelete holds a delete back until Jellyfin can take it (see DeleteUser): never while the account plays, and not before userGrace.
func (c *Client) jellyfinMayDelete(ctx context.Context, userID string) error {
	user, err := c.userByID(ctx, userID)
	if err != nil {
		return err
	}
	sessions, err := c.Sessions(ctx)
	if err != nil {
		return fmt.Errorf("could not tell whether %s is playing anything, so it was not deleted: %w", user.Name, err)
	}
	for i := range sessions {
		if s := &sessions[i]; s.NowPlayingItem != nil && strings.EqualFold(s.UserName, user.Name) {
			return fmt.Errorf("%s is playing %s on %s, and was not deleted: %w", user.Name, s.NowPlayingItem.Name, s.DeviceName, ErrUserPlaying)
		}
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("%s was not deleted: %w", user.Name, ctx.Err())
	case <-time.After(c.userGrace):
		return nil
	}
}

// SetUserLibraries says which libraries an account sees, every one or only those given, and answers with the account before and after. The policy
// is read whole and sent back whole with the two settings changed, since neither server takes one setting alone. Neither checks the ids it is
// given, so the libraries are the caller's to have read; changes to one account from this process take turns, or the later would undo the earlier.
func (c *Client) SetUserLibraries(ctx context.Context, userID string, all bool, libraries []VirtualFolder) (before, after *User, err error) {
	unlock := lock.ByString(c.baseURL + " account " + strings.ToLower(userID))
	defer unlock()

	if before, err = c.userByID(ctx, userID); err != nil {
		return nil, nil, err
	}

	var account struct {
		Policy map[string]any `json:"Policy"`
	}
	if err := c.document(ctx, "/Users/"+userID, "the account's settings", &account); err != nil {
		return nil, nil, err
	}
	if account.Policy == nil {
		return nil, nil, errors.New("the server answered the account without its policy, so nothing was changed")
	}

	enabled := make([]string, 0, len(libraries))
	for i := range libraries {
		id := libraries[i].ItemID
		if libraries[i].GUID != "" {
			id = libraries[i].GUID
		}
		if id == "" {
			return nil, nil, fmt.Errorf("the library %s has no id to give the account", libraries[i].Name)
		}
		enabled = append(enabled, id)
	}
	if all {
		enabled = []string{}
	}
	account.Policy["EnableAllFolders"], account.Policy["EnabledFolders"] = all, enabled

	req, err := c.base().NewRequest(ctx, apiclient.RequestOptions{
		ContentType:         "application/json",
		ExpectedStatusCodes: []int{http.StatusNoContent, http.StatusOK},
		HTTPMethod:          http.MethodPost,
		Path:                "/Users/" + userID + "/Policy",
	})
	if err != nil {
		return nil, nil, err
	}
	if err := req.Marshal(account.Policy); err != nil {
		return nil, nil, err
	}
	if _, err := req.Execute(ctx); err != nil {
		return nil, nil, err
	}

	if after, err = c.userByID(ctx, userID); err != nil {
		return before, nil, fmt.Errorf("the server took the change, and the account could not be read again to check it: %w", err)
	}

	return before, after, nil
}
