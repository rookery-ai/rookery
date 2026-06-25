// Package reminder polls for due reminders and delivers them via the gateway.
package reminder

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/vault"
)

const pollInterval = 60 * time.Second

// Sender delivers a message to a user on their connected platform.
type Sender interface {
	// SendToUser looks up the user's connected platform and sends the message.
	// It picks the first active platform gateway for the user.
	SendToUser(userID, text string) error
}

// Service polls for due reminders and sends them.
type Service struct {
	db        *db.DB
	sender    Sender
	reflector *vault.Reflector // optional; mirrors reminders into the user's vault
	searcher  vault.Searcher   // optional; enriches fired reminders with related KB notes
}

// New creates a Service.
func New(database *db.DB, sender Sender) *Service {
	return &Service{db: database, sender: sender}
}

// WithReflector enables mirroring fired reminders into each user's vault.
func (s *Service) WithReflector(r *vault.Reflector) *Service {
	s.reflector = r
	return s
}

// WithSearcher enables enriching fired reminder messages with related vault notes.
func (s *Service) WithSearcher(sr vault.Searcher) *Service {
	s.searcher = sr
	return s
}

// Run starts the polling loop and blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	slog.Info("reminder service: started")

	s.tick()

	for {
		select {
		case <-ctx.Done():
			slog.Info("reminder service: stopped")
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Service) tick() {
	ctx := context.Background()
	now := time.Now()
	reminders, err := s.db.ListDueReminders(now)
	if err != nil {
		slog.Error("reminder: list due", "err", err)
		return
	}

	for _, r := range reminders {
		// Skip users with no platform connected — they have no way to receive
		// the reminder. Keep it pending so it fires once they link a platform.
		if !s.db.HasPlatformIdentity(r.UserID) {
			slog.Warn("reminder: skipping — user has no platform connected",
				"reminder_id", r.ID, "user_id", r.UserID)
			continue
		}

		msg := s.buildReminderMessage(ctx, r, now)
		if err := s.sender.SendToUser(r.UserID, msg); err != nil {
			slog.Error("reminder: send failed", "reminder_id", r.ID, "user_id", r.UserID, "err", err)
			continue
		}
		if err := s.db.MarkReminderSent(r.ID); err != nil {
			slog.Error("reminder: mark sent failed", "reminder_id", r.ID, "err", err)
		}
	}
}

// buildReminderMessage builds the outgoing reminder text, optionally enriching it
// with related knowledge-base notes when a Searcher is configured.
func (s *Service) buildReminderMessage(ctx context.Context, r *db.Reminder, now time.Time) string {
	prefix := "⏰ Reminder"
	if time.Since(r.RemindAt) > 2*time.Hour {
		prefix = "⏰ Delayed reminder"
	}
	msg := fmt.Sprintf("%s: %s", prefix, r.Message)

	if s.searcher == nil {
		return msg
	}

	// Search the vault with the reminder text to surface related notes.
	hits, err := s.searcher.Search(ctx, r.UserID, r.Message)
	if err != nil || len(hits) == 0 {
		return msg
	}

	msg += "\n\n📎 Related notes:"
	for i, h := range hits {
		if i >= 3 {
			break
		}
		msg += fmt.Sprintf("\n• %s: %s", h.Path, h.Snippet)
	}
	return msg
}
