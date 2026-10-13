// Package polltest is a media server for tests: one that answers what it is told to, fails when told to, and counts what it was asked.
package polltest

import (
	"context"
	"sync"
	"time"

	"github.com/katbyte/embyfin-metrics/lib/poll"
)

// The questions a server is asked, as Fail and Asked name them.
const (
	Info      = "info"
	Sessions  = "sessions"
	Tasks     = "tasks"
	Libraries = "libraries"
	Users     = "users"
	Devices   = "devices"
	History   = "history"
	Logins    = "logins"
)

// Server answers each question with the value set for it. The zero value is an Emby with nothing on it; set the fields before it is polled.
type Server struct {
	// Named is the kind it says it is, "emby" when empty.
	Named string

	InfoAnswer      poll.Info
	SessionsAnswer  []poll.Session
	TasksAnswer     []poll.Task
	LibrariesAnswer []poll.Library
	UsersAnswer     []poll.User
	DevicesAnswer   []poll.Device
	HistoryAnswer   poll.History
	LoginsAnswer    []poll.Login

	// Slow is how long every question takes to answer, or until the context ends if that is sooner.
	Slow time.Duration

	mu      sync.Mutex
	failing map[string]error
	asked   map[string]int
}

// Fail makes one question fail with err from now on, and every question when question is "". A nil err puts it right again.
func (s *Server) Fail(question string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failing == nil {
		s.failing = map[string]error{}
	}
	s.failing[question] = err
}

// Asked is how many times a question has been put.
func (s *Server) Asked(question string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.asked[question]
}

// answer counts the question, takes as long as Slow says, and gives the error it was told to fail with.
func (s *Server) answer(ctx context.Context, question string) error {
	s.mu.Lock()
	if s.asked == nil {
		s.asked = map[string]int{}
	}
	s.asked[question]++
	err := s.failing[question]
	if err == nil {
		err = s.failing[""]
	}
	s.mu.Unlock()

	if s.Slow > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Slow):
		}
	}

	return err
}

func (s *Server) Kind() string {
	if s.Named == "" {
		return "emby"
	}

	return s.Named
}

func (s *Server) Info(ctx context.Context) (poll.Info, error) {
	return s.InfoAnswer, s.answer(ctx, Info)
}

func (s *Server) Sessions(ctx context.Context) ([]poll.Session, error) {
	return s.SessionsAnswer, s.answer(ctx, Sessions)
}

func (s *Server) Tasks(ctx context.Context) ([]poll.Task, error) {
	return s.TasksAnswer, s.answer(ctx, Tasks)
}

func (s *Server) Libraries(ctx context.Context) ([]poll.Library, error) {
	return s.LibrariesAnswer, s.answer(ctx, Libraries)
}

func (s *Server) Users(ctx context.Context) ([]poll.User, error) {
	return s.UsersAnswer, s.answer(ctx, Users)
}

func (s *Server) Devices(ctx context.Context) ([]poll.Device, error) {
	return s.DevicesAnswer, s.answer(ctx, Devices)
}

func (s *Server) History(ctx context.Context) (poll.History, error) {
	return s.HistoryAnswer, s.answer(ctx, History)
}

func (s *Server) Logins(ctx context.Context) ([]poll.Login, error) {
	return s.LoginsAnswer, s.answer(ctx, Logins)
}
