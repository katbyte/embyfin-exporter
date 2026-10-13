// Package poll asks each media server for its state on a schedule and keeps the last good answers, so the metrics page is served from memory and a
// slow or stopped server never holds up a scrape.
//
// Nothing here says whose a session is or which film it is playing unless the settings ask for it: an account's name and a title are fields a server
// leaves empty until it is told to fill them (see mediaserver.Detail), and there is no field for a device's name or an address at all.
package poll

import (
	"context"
	"time"
)

// Server is the questions asked of one media server. Every one of them only reads.
type Server interface {
	// Kind is which server this is, "emby" or "jellyfin": the value of the server label.
	Kind() string
	Info(ctx context.Context) (Info, error)
	Sessions(ctx context.Context) ([]Session, error)
	Tasks(ctx context.Context) ([]Task, error)
	// Libraries is every library with how many items of each kind it holds.
	Libraries(ctx context.Context) ([]Library, error)
	Users(ctx context.Context) ([]User, error)
	// Devices is every device the server knows of, which is every one it has ever seen.
	Devices(ctx context.Context) ([]Device, error)
	// History is what has been played in this week and the last, for a server that keeps a record of it.
	History(ctx context.Context) (History, error)
}

// Info is what a server says of itself.
type Info struct {
	// Name is the server's own name, not a person's.
	Name    string
	Version string
	// APIVersion is the version of the server's API document the client was built from, which is not always the server's own.
	APIVersion      string
	RestartPending  bool
	UpdateAvailable bool
}

// SessionState is what a connected device is doing.
type SessionState string

const (
	Playing SessionState = "playing"
	Paused  SessionState = "paused"
	Idle    SessionState = "idle"
)

// SessionStates is every state, in the order the page lists them.
var SessionStates = []SessionState{Playing, Paused, Idle}

// Method is how what is playing reaches the device.
type Method string

const (
	// DirectPlay is the file as it is.
	DirectPlay Method = "direct_play"
	// DirectStream is its streams repacked, none of them re-encoded.
	DirectStream Method = "direct_stream"
	// Transcode is at least one stream re-encoded by the server.
	Transcode Method = "transcode"
	// UnknownMethod is a playing session whose server does not say.
	UnknownMethod Method = "unknown"
)

// Methods is every method, in the order the page lists them.
var Methods = []Method{DirectPlay, DirectStream, Transcode, UnknownMethod}

// Kind is the kind of thing a session is playing, in the words the item counts use. It is never which one.
type Kind string

const (
	Movie      Kind = "movie"
	Episode    Kind = "episode"
	Track      Kind = "track"
	MusicVideo Kind = "music_video"
	Video      Kind = "video"
	Audiobook  Kind = "audiobook"
	// OtherKind is anything else a server can play: live television, a trailer, a photo.
	OtherKind Kind = "other"
)

// Kinds is every kind, in the order the page lists them.
var Kinds = []Kind{Movie, Episode, Track, MusicVideo, Video, Audiobook, OtherKind}

// Session is one device connected to a server.
type Session struct {
	State SessionState
	// Method and Kind are set for a session that is playing or paused.
	Method Method
	Kind   Kind
	// VideoTranscode says the server is re-encoding the picture, which is the work that loads a GPU, and Hardware that a GPU is doing it.
	VideoTranscode bool
	Hardware       bool
	// Bitrate is how many bits a second the stream the server is making for the session comes to, and Reasons why it is making one, each said once
	// and in the page's words. Both are empty for a file sent as it is.
	Bitrate int
	Reasons []string
}

// TaskState is what a scheduled task is doing.
type TaskState string

const (
	TaskIdle       TaskState = "idle"
	TaskRunning    TaskState = "running"
	TaskCancelling TaskState = "cancelling"
)

// TaskStates is every state, in the order the page lists them.
var TaskStates = []TaskState{TaskIdle, TaskRunning, TaskCancelling}

// Task is one of a server's scheduled tasks.
type Task struct {
	// Name is what the server calls the task, as its own dashboard lists it: a job's name and never a person's.
	Name  string
	State TaskState
	// Progress is how far a running task says it has got, from 0 to 1.
	Progress float64
	// LibraryScan marks the scan of every library.
	LibraryScan bool
	// LastRun is nil for a task that has never run.
	LastRun *Run
}

// Run is how a task's last run went.
type Run struct {
	// OK says it finished as it should, and Failed that it went wrong. A run that was cancelled, or cut short by the server stopping, is neither.
	OK     bool
	Failed bool
	Ended  time.Time
	Took   time.Duration
}

// Library is one library and how many items of each kind it holds.
type Library struct {
	Name  string
	Items map[string]int
}

// User is one account.
type User struct {
	// Name is whose it is, and empty unless the settings ask for accounts to be named.
	Name     string
	Disabled bool
	// LastActive is the zero time for an account never seen.
	LastActive time.Time
}

// Device is one device a server knows of, less everything that says whose it is.
type Device struct {
	// LastActive is the zero time for a device the server gives no date for.
	LastActive time.Time
}

// History is what a server's Playback Reporting plugin has of what was played in this week and the last, on the server's own clock. Each sum is
// the plugin's own and is kept under the week its plays began in, so it is the same after a restart as before and never goes down: a play that runs
// past midnight on Sunday goes on adding to the week it began in.
type History struct {
	// Installed is false for a server without the plugin, which then has nothing below.
	Installed bool
	// Plays is everything played, by the kind of thing and by how it reached the device.
	Plays []Played
	// ByUser is the same plays by account and kind, and ByTitle by show or film. Both are empty unless the settings ask for them.
	ByUser  []Played
	ByTitle []Played
}

// Played is how much was played of one kind in one week: how many plays there were and how long they ran between them. Which of the other fields
// are set depends on the list it is in.
type Played struct {
	// Week is the Monday of the week the plays began in, as 2006-01-02.
	Week   string
	Kind   Kind
	Method Method
	// User is an account's name, and Title a show's or a film's.
	User  string
	Title string

	// Plays counts a play again each time how it reaches the device changes part way, as the plugin does.
	Plays   int
	Seconds int
}
