// Package session provides the background auto-stop service for chat sessions.
// Sessions that have been inactive for more than 30 minutes are automatically stopped.
package session

import (
	"context"
	"log/slog"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
)

const (
	pollInterval = time.Minute
	idleTimeout  = 30 * time.Minute
)

// Service polls for stale sessions and stops them.
type Service struct {
	db *db.DB
}

// New creates a Service.
func New(database *db.DB) *Service {
	return &Service{db: database}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	slog.Info("session service: started")
	s.tick()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Service) tick() {
	stale, err := s.db.ListStaleSessions(time.Now().Add(-idleTimeout))
	if err != nil {
		slog.Error("session service: list stale sessions", "err", err)
		return
	}
	for _, sess := range stale {
		if err := s.db.StopChatSession(sess.ID); err != nil {
			slog.Error("session service: stop session", "id", sess.ID, "err", err)
		} else {
			slog.Info("session service: auto-stopped idle session", "id", sess.ID, "user", sess.UserID)
		}
	}
}
