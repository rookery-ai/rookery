// Package chat provides the background auto-stop service for chats and the
// shared user-context builder used by both the Telegram gateway and the web
// chat composer when invoking the coder for one-off conversational turns.
// Chats that have been inactive for more than 30 minutes are automatically
// stopped and their transcript mirrored into the user's vault.
package chat

import (
	"context"
	"log/slog"
	"regexp"
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
		slog.Info("chat service: auto-stopped idle chat", "id", c.ID, "user", c.WorkspaceID)
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
	if err := s.reflector.ReflectChat(c.WorkspaceID, note); err != nil {
		slog.Warn("chat service: reflect transcript", "id", c.ID, "err", err)
	}
}

// ContextStore is the minimal memory-store interface BuildUserContext needs.
type ContextStore interface {
	ContextString(workspaceID string) (string, error)
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
func BuildUserContext(database *db.DB, memStore ContextStore, workspaceID string) string {
	var sb strings.Builder

	if p := profile.Load(database, workspaceID).ContextString(); p != "" {
		sb.WriteString(p)
	}

	if mem, err := memStore.ContextString(workspaceID); err == nil && mem != "" {
		sb.WriteString("[User memory]\n")
		sb.WriteString(mem)
		sb.WriteByte('\n')
	}

	if agents, err := database.ListAgents(workspaceID); err == nil && len(agents) > 0 {
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

	if mcpServers, err := database.ListMCPServers(workspaceID); err == nil && len(mcpServers) > 0 {
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

// defaultChatNameRE matches the auto-generated "Chat <timestamp>" name that
// apiCreateChat / the gateway assign to a fresh chat. A chat still carrying
// this name has never been titled from content, so it is eligible for a
// one-time auto-title; anything else was named by the user or a prior
// auto-title and is left alone.
var defaultChatNameRE = regexp.MustCompile(`^Chat \d{4}-\d{2}-\d{2} \d{2}:\d{2}$`)

func isDefaultChatName(name string) bool {
	return defaultChatNameRE.MatchString(name)
}

// attachmentPrefix is the leading text of the attachment-confirmation turn the
// web chat posts after importing a file (see attachFiles in ChatWindow.tsx).
// Those turns are real coder turns but must NOT drive the chat title, or a
// chat whose first action is a file drop would be named after the file.
const attachmentPrefix = "📎 Attached"

// sanitizeTitle normalizes a model-produced title into a short, single-line
// label: strips surrounding whitespace/quotes, a leading "Title:" prefix,
// trailing sentence punctuation, collapses internal newlines to spaces, and
// caps length. Returns "" when nothing usable remains (caller then skips the
// rename).
func sanitizeTitle(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	// Collapse runs of whitespace produced by the newline replacement.
	s = strings.Join(strings.Fields(s), " ")
	// Drop a leading "Title:" the model sometimes echoes.
	if i := strings.Index(strings.ToLower(s), "title:"); i == 0 {
		s = strings.TrimSpace(s[len("title:"):])
	}
	s = strings.Trim(s, "\"'“”")
	s = strings.TrimRight(s, ".!?, ")
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
		s = strings.TrimSpace(s)
	}
	return s
}

// TitleGenerator produces a short chat title from the first real exchange.
// Injected (not built here) so internal/chat stays free of a coder dependency;
// main.go supplies a closure backed by the per-workspace coder.
type TitleGenerator func(ctx context.Context, workspaceID, firstUserMsg, firstReply string) (string, error)

// MaybeAutoTitle renames a chat exactly once, from its first substantive
// exchange. It is a no-op (never an error, never blocking) unless the chat
// still carries its default "Chat <timestamp>" name AND the user message that
// produced this turn is a real message rather than an attachment confirmation.
// The rename runs in the background so it never delays the user's reply; any
// failure leaves the default name in place.
func MaybeAutoTitle(database *db.DB, gen TitleGenerator, ch *db.Chat, firstUserMsg, firstReply string) {
	if ch == nil || gen == nil {
		return
	}
	if !isDefaultChatName(ch.Name) {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(firstUserMsg), attachmentPrefix) {
		return
	}
	if strings.TrimSpace(firstUserMsg) == "" || strings.TrimSpace(firstReply) == "" {
		return
	}
	chatID := ch.ID
	workspaceID := ch.WorkspaceID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := gen(ctx, workspaceID, firstUserMsg, firstReply)
		if err != nil {
			slog.Debug("auto-title: generator failed", "chat", chatID, "err", err)
			return
		}
		title := sanitizeTitle(raw)
		if title == "" {
			return
		}
		if err := database.UpdateChatName(chatID, title); err != nil {
			slog.Warn("auto-title: update chat name", "chat", chatID, "err", err)
		}
	}()
}
