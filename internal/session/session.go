// Package session provides the background auto-stop service for chat sessions.
// Sessions that have been inactive for more than 30 minutes are automatically stopped.
package session

import (
	"context"
	"log/slog"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/vault"
)

const (
	pollInterval = time.Minute
	idleTimeout  = 30 * time.Minute
)

// Service polls for stale sessions and stops them.
type Service struct {
	db        *db.DB
	reflector *vault.Reflector // optional; mirrors stopped transcripts into the vault
}

// New creates a Service.
func New(database *db.DB) *Service {
	return &Service{db: database}
}

// WithReflector enables mirroring stopped session transcripts into each user's vault.
func (s *Service) WithReflector(r *vault.Reflector) *Service {
	s.reflector = r
	return s
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
			continue
		}
		slog.Info("session service: auto-stopped idle session", "id", sess.ID, "user", sess.UserID)
		s.reflectTranscript(sess)
	}
}

// reflectTranscript mirrors a stopped session's full transcript into the vault.
func (s *Service) reflectTranscript(sess *db.ChatSession) {
	if s.reflector == nil {
		return
	}
	msgs, err := s.db.ListChatMessages(sess.ID)
	if err != nil {
		slog.Warn("session service: list messages for reflection", "id", sess.ID, "err", err)
		return
	}
	note := vault.SessionNote{
		ID: sess.ID, Name: sess.Name, Platform: sess.Platform, CreatedAt: sess.CreatedAt,
	}
	for _, m := range msgs {
		note.Messages = append(note.Messages, vault.SessionMessage{
			Role: m.Role, Content: m.Content, CreatedAt: m.CreatedAt,
		})
	}
	if err := s.reflector.ReflectSession(sess.UserID, note); err != nil {
		slog.Warn("session service: reflect transcript", "id", sess.ID, "err", err)
	}
}
