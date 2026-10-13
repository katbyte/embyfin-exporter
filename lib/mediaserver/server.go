package mediaserver

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/katbyte/embyfin-mcp/sdk/embyfin"
	"github.com/katbyte/embyfin-metrics/lib/poll"
	"github.com/katbyte/go-kt/clog"
	apiclient "github.com/katbyte/pandorest/client"
)

// retry is how a question is put again when the server drops the connection or its proxy answers 502, 503 or 504: once more, a second later. The
// client's own rule waits up to fourteen seconds, which is a whole sessions round, and the next round asks again anyway.
var retry = apiclient.Retry{Tries: 2, Wait: func(int) time.Duration { return time.Second }}

// client is what is asked of embyfin-mcp's client, so a test can answer in a server's place.
type client interface {
	Backend() embyfin.Backend
	APIVersion() string
	SystemInfo(ctx context.Context) (*embyfin.SystemInfo, error)
	Sessions(ctx context.Context) ([]embyfin.Session, error)
	Tasks(ctx context.Context) ([]embyfin.Task, error)
	VirtualFolders(ctx context.Context) ([]embyfin.VirtualFolder, error)
	Count(ctx context.Context, opts embyfin.SearchOptions) (int, error)
	Users(ctx context.Context) ([]embyfin.User, error)
	Devices(ctx context.Context) ([]embyfin.Device, error)
}

// Server asks one Emby or Jellyfin the page's questions. It only reads, and what it hands on is counts and states: an account's name or a title is
// in it only when detail asks for one, and a device's name or an address never.
type Server struct {
	client  client
	reports reporter
	files   fileRates
	detail  Detail

	// history is whether the server was last seen to have the Playback Reporting plugin (2) or not (1), so the log says so once
	history atomic.Int32
}

// Connect is the server of that kind, "emby" or "jellyfin", at address, asked with that key, and telling of who plays what only as far as detail
// allows. Nothing is sent until the first question. Set EMBYFIN_METRICS_LOG=TRACE to see every request and its answer, with the key hidden.
func Connect(kind, address, token string, detail Detail) (*Server, error) {
	opts := []apiclient.Option{apiclient.WithLog(clog.Log), apiclient.WithRetry(retry)}

	c, err := embyfin.New(embyfin.Backend(kind), address, token, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", kind, err)
	}

	d, err := connectDirect(c.Backend(), address, token, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", kind, err)
	}

	return &Server{client: c, reports: d, files: d, detail: detail}, nil
}

// Kind is "emby" or "jellyfin".
func (s *Server) Kind() string { return string(s.client.Backend()) }

func (s *Server) Info(ctx context.Context) (poll.Info, error) {
	info, err := s.client.SystemInfo(ctx)
	if err != nil {
		return poll.Info{}, err
	}

	return poll.Info{Name: info.ServerName, Version: info.Version, APIVersion: s.client.APIVersion(), RestartPending: info.HasPendingRestart, UpdateAvailable: info.HasUpdateAvailable}, nil
}

func (s *Server) Sessions(ctx context.Context) ([]poll.Session, error) {
	sessions, err := s.client.Sessions(ctx)
	if err != nil {
		return nil, err
	}

	rates := s.fileRates(ctx, sessions)

	out := make([]poll.Session, 0, len(sessions))
	for i := range sessions {
		out = append(out, session(&sessions[i], s.detail, rates[sessions[i].ID]))
	}

	return out, nil
}

// fileRates is what the file each session plays comes to, by the session's id, asked only when some session is being sent a file as it is: a
// stream the server makes has a bitrate of its own. It is an extra, so when it cannot be had the sessions are still the sessions, less that.
func (s *Server) fileRates(ctx context.Context, sessions []embyfin.Session) map[string]int {
	if s.files == nil || !slices.ContainsFunc(sessions, func(session embyfin.Session) bool { return session.NowPlayingItem != nil && session.Transcoding == nil }) {
		return nil
	}

	rates, err := s.files.FileRates(ctx)
	if err != nil {
		clog.Log.Debugf("%s: what the files being played come to could not be read: %s", s.Kind(), err)
	}

	return rates
}

// session is what one session is doing and how. What the server is doing says more than the device's word for it: a stream the server is making
// is a direct stream when nothing in it is re-encoded and a transcode when anything is, whatever the device called it. Whose it is, what it plays
// and on what are handed on only as far as detail allows, and where it connects from only as home or remote.
func session(s *embyfin.Session, detail Detail, fileRate int) poll.Session {
	state := sessionState(s.NowPlayingItem != nil, s.PlayState.IsPaused)
	if state == poll.Idle {
		return poll.Session{State: state}
	}

	item := s.NowPlayingItem
	out := poll.Session{ID: s.ID, State: state, Method: method(s.PlayState.PlayMethod), Kind: playing(item.Type), Network: network(s.RemoteEndPoint), Outgoing: max(fileRate, 0), Position: ticks(s.PlayState.PositionTicks), Runtime: ticks(item.RunTimeTicks)}
	if detail.Users {
		out.User = s.UserName
	}
	if detail.Titles {
		out.Title = nowPlaying(item)
	}
	if detail.Devices {
		out.Device, out.Client = s.DeviceName, s.Client
	}

	t := s.Transcoding
	if t == nil {
		return out
	}

	picture := reencoded(t.VideoDirect, t.VideoCodec)
	out.Method = poll.DirectStream
	if picture || reencoded(t.AudioDirect, t.AudioCodec) {
		out.Method = poll.Transcode
	}
	out.VideoTranscode = picture
	out.Hardware = picture && hardware(t.EncoderHardware, t.HardwareAcceleration)
	out.Bitrate = max(t.Bitrate, 0)
	out.Reasons = reasons(t.Reasons)
	// what leaves the server is then the stream it makes and not the file
	out.Outgoing = out.Bitrate

	return out
}

func (s *Server) Tasks(ctx context.Context) ([]poll.Task, error) {
	tasks, err := s.client.Tasks(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]poll.Task, 0, len(tasks))
	for i := range tasks {
		out = append(out, task(&tasks[i]))
	}

	return out, nil
}

// task is what one scheduled task is doing and how its last run went. It goes by the name the server's dashboard lists it under: Emby gives several
// tasks one key and some none (seen on 4.11).
func task(t *embyfin.Task) poll.Task {
	out := poll.Task{Name: cmp.Or(t.Name, t.Key), State: taskState(t.State), LibraryScan: t.IsLibraryScan()}
	if out.State != poll.TaskIdle {
		out.Progress = progress(t.Progress)
	}

	if r := t.LastExecutionResult; r != nil {
		ended := when(r.EndTimeUtc)
		out.LastRun = &poll.Run{OK: ranOK(r.Status), Failed: failed(r.Status), Ended: ended, Took: between(when(r.StartTimeUtc), ended)}
	}

	return out
}

// Libraries is every library with how many items of each kind it holds, asked one kind at a time without fetching any of the items. One count
// failing fails the whole answer, so the page keeps the last full set rather than showing half of a new one.
//
// The counts are the server's own, asked as no user in particular. Emby counts every file of a film or an episode it holds in several versions,
// where Jellyfin counts the film once.
func (s *Server) Libraries(ctx context.Context) ([]poll.Library, error) {
	folders, err := s.client.VirtualFolders(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]poll.Library, 0, len(folders))
	for i := range folders {
		folder := &folders[i]
		library := poll.Library{Name: folder.Name, Items: map[string]int{}}

		// a library with no id cannot be asked what it holds: a count with no library named is of the whole server
		if folder.ItemID == "" {
			out = append(out, library)
			continue
		}

		for _, k := range kindsIn(s.client.Backend(), folder.CollectionType) {
			n, err := s.client.Count(ctx, embyfin.SearchOptions{ParentID: folder.ItemID, IncludeItemTypes: k.server})
			if err != nil {
				return nil, fmt.Errorf("counting the %s kind in the %s library: %w", k.label, folder.Name, err)
			}
			library.Items[k.label] = n
		}

		out = append(out, library)
	}

	return out, nil
}

// Users is every account: whether it is disabled and when it was last seen, and whose it is only when the settings ask for accounts to be named.
func (s *Server) Users(ctx context.Context) ([]poll.User, error) {
	users, err := s.client.Users(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]poll.User, 0, len(users))
	for i := range users {
		user := poll.User{Disabled: users[i].Policy.IsDisabled, LastActive: when(users[i].LastActivityDate)}
		if s.detail.Users {
			user.Name = users[i].Name
		}

		out = append(out, user)
	}

	return out, nil
}

// Devices is every device the server knows of: when each was last seen, and nothing of whose it is.
func (s *Server) Devices(ctx context.Context) ([]poll.Device, error) {
	devices, err := s.client.Devices(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]poll.Device, 0, len(devices))
	for i := range devices {
		out = append(out, poll.Device{LastActive: when(devices[i].DateLastActivity)})
	}

	return out, nil
}
