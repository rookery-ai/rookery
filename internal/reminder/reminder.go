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
	reminders, err := s.db.ListDueReminders(time.Now())
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
		if err := s.sender.SendToUser(r.UserID, fmt.Sprintf("Reminder: %s", r.Message)); err != nil {
			slog.Error("reminder: send failed", "reminder_id", r.ID, "user_id", r.UserID, "err", err)
			continue
		}
		if err := s.db.MarkReminderSent(r.ID); err != nil {
			slog.Error("reminder: mark sent failed", "reminder_id", r.ID, "err", err)
		}
		if err := s.reflector.ReflectReminder(r.UserID, vault.ReminderNote{
			ID: r.ID, Message: r.Message, RemindAt: r.RemindAt,
			Recurrence: r.Recurrence, Sent: true, CreatedAt: r.CreatedAt,
		}); err != nil {
			slog.Warn("reminder: reflect to vault failed", "reminder_id", r.ID, "err", err)
		}
	}
}
