// Package chat provides the background auto-stop service for chats and the
// shared user-context builder used by both the Telegram gateway and the web
// chat composer when invoking the coder for one-off conversational turns.
// Chats that have been inactive for more than 30 minutes are automatically
// stopped and their transcript mirrored into the user's vault.
package chat

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/vault"
)

const (
	pollInterval = time.Minute
	idleTimeout  = 30 * time.Minute
)

// Service polls for stale chats and stops them.
type Service struct {
	db        *db.DB
	reflector *vault.Reflector // optional; mirrors stopped transcripts into the vault
}

// New creates a Service.
func New(database *db.DB) *Service {
	return &Service{db: database}
}

// WithReflector enables mirroring stopped chat transcripts into each user's vault.
func (s *Service) WithReflector(r *vault.Reflector) *Service {
	s.reflector = r
	return s
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	slog.Info("chat service: started")
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
	stale, err := s.db.ListStaleChats(time.Now().Add(-idleTimeout))
	if err != nil {
		slog.Error("chat service: list stale chats", "err", err)
		return
	}
	for _, c := range stale {
		if err := s.db.StopChat(c.ID); err != nil {
			slog.Error("chat service: stop chat", "id", c.ID, "err", err)
			continue
		}
		slog.Info("chat service: auto-stopped idle chat", "id", c.ID, "user", c.UserID)
		s.reflectTranscript(c)
	}
}

// reflectTranscript mirrors a stopped chat's full transcript into the vault.
func (s *Service) reflectTranscript(c *db.Chat) {
	if s.reflector == nil {
		return
	}
	msgs, err := s.db.ListChatMessages(c.ID)
	if err != nil {
		slog.Warn("chat service: list messages for reflection", "id", c.ID, "err", err)
		return
	}
	note := vault.ChatNote{
		ID: c.ID, Name: c.Name, Platform: c.Platform, CreatedAt: c.CreatedAt,
	}
	for _, m := range msgs {
		note.Messages = append(note.Messages, vault.ChatTurn{
			Role: m.Role, Content: m.Content, CreatedAt: m.CreatedAt,
		})
	}
	if err := s.reflector.ReflectChat(c.UserID, note); err != nil {
		slog.Warn("chat service: reflect transcript", "id", c.ID, "err", err)
	}
}

// ContextStore is the minimal memory-store interface BuildUserContext needs.
type ContextStore interface {
	ContextString(userID string) (string, error)
}

// BuildUserContext assembles the always-on system context for the coder: the user's
// profile, memory, agents, and MCP tools. This is identity-level context that is small
// and intentionally present on every turn.
//
// The user's broader knowledge base (notes, journals, user-created files) is NOT injected
// here — the chat coder has read+write file tools (see prompts.BuildChatSystemPrompt) and
// retrieves that knowledge ON DEMAND, only on turns that need it.
//
// Shared by the Telegram textHandler and the web chat composer so both surfaces feed the
// coder identical context for one-off conversational turns.
func BuildUserContext(database *db.DB, memStore ContextStore, userID string) string {
	var sb strings.Builder

	if p := profile.Load(database, userID).ContextString(); p != "" {
		sb.WriteString(p)
	}

	if mem, err := memStore.ContextString(userID); err == nil && mem != "" {
		sb.WriteString("[User memory]\n")
		sb.WriteString(mem)
		sb.WriteByte('\n')
	}

	if agents, err := database.ListAgents(userID); err == nil && len(agents) > 0 {
		sb.WriteString("[User's agents]\n")
		for _, a := range agents {
			sb.WriteString("- ")
			sb.WriteString(a.Name)
			if a.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(a.Description)
			}
			sb.WriteByte('\n')
		}
	}

	if mcpServers, err := database.ListMCPServers(userID); err == nil && len(mcpServers) > 0 {
		sb.WriteString("[User's MCP tools]\n")
		for _, s := range mcpServers {
			if s.Enabled {
				sb.WriteString("- ")
				sb.WriteString(s.Name)
				sb.WriteByte('\n')
			}
		}
	}

	return sb.String()
}