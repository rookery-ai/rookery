// Package reminder polls for due reminders and delivers them via the gateway.
package reminder

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/vault"
)

const pollInterval = 60 * time.Second

// Sender delivers a message to a user on their connected platform.
type Sender interface {
	// SendToUser looks up the user's connected platform and sends the message.
	// It picks the first active platform gateway for the user.
	SendToUser(workspaceID, text string) error
}

// Service polls for due reminders and sends them.
//
// It writes nothing to the vault — hence no Reflector. Reminders live only in
// the DB and the reminders UI tab.
type Service struct {
	db       *db.DB
	sender   Sender
	searcher vault.Searcher // optional; enriches fired reminders with related KB notes
}

// New creates a Service.
func New(database *db.DB, sender Sender) *Service {
	return &Service{db: database, sender: sender}
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
		msg := s.buildReminderMessage(ctx, r, now)

		// The inbox is a real delivery channel with its own UI, badge, and vault
		// reflection — a chat platform is no longer the only way output reaches the user.
		// So the chat send is best-effort ON TOP of the inbox, never a precondition for it.
		//
		// This path previously had two early exits — skip when no platform identity, and
		// `continue` on a send error — and BOTH bypassed recordInbox *and*
		// MarkReminderSent. The visible symptom was a reminder that never showed up
		// anywhere without a chat app; the quieter one was that such a reminder stayed
		// due and re-fired on every single tick, forever.
		if s.db.HasPlatformIdentity(r.WorkspaceID) {
			if err := s.sender.SendToUser(r.WorkspaceID, msg); err != nil {
				// Logged, not fatal: the inbox delivery below still stands, so the user
				// gets the reminder and it is not retried indefinitely.
				slog.Error("reminder: chat send failed — delivering to inbox only",
					"reminder_id", r.ID, "user_id", r.WorkspaceID, "err", err)
			}
		} else {
			slog.Info("reminder: no chat platform connected — delivering to inbox only",
				"reminder_id", r.ID, "user_id", r.WorkspaceID)
		}

		s.recordInbox(r.WorkspaceID, r.ID, msg)
		if err := s.db.MarkReminderSent(r.ID); err != nil {
			slog.Error("reminder: mark sent failed", "reminder_id", r.ID, "err", err)
		}
	}
}

// recordInbox drops one inbox notification for a fired reminder whose body is
// the exact message that was just sent. Best-effort; never blocks the send.
//
// Nothing is written to the vault. Reminders live only in the DB and the
// reminders UI tab — reflecting the fired notification was a back door around
// that rule, and it produced a pile of notes all titled "⏰ Reminder".
func (s *Service) recordInbox(workspaceID, reminderID, body string) {
	if s.db == nil || body == "" {
		return
	}
	if err := s.db.CreateInboxMessage(&db.InboxMessage{
		ID: uuid.New().String(), WorkspaceID: workspaceID, Source: "reminder",
		RefID: reminderID, Body: body, Status: "ok", CreatedAt: time.Now().UTC(),
	}); err != nil {
		slog.Warn("inbox: create reminder", "reminder_id", reminderID, "err", err)
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
	hits, err := s.searcher.Search(ctx, r.WorkspaceID, r.Message)
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
