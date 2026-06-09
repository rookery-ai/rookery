package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

// TextHandler is called for non-command messages (one-off chat).
// Implemented in Phase 4 (Coder).
type TextHandler func(ctx context.Context, userID, text string, send func(string)) error

// AgentRunHandler is called when /run <name> is issued.
// Implemented in Phase 6 (AgentRunner).
type AgentRunHandler func(ctx context.Context, userID, agentName string, send func(string)) error

// Router dispatches incoming messages to the appropriate handler.
type Router struct {
	db             *db.DB
	onText         TextHandler
	onAgentRun     AgentRunHandler
}

// NewRouter creates a Router. textHandler and agentRunHandler may be nil
// until Phase 4/6 are wired in; the router will reply with a stub message.
func NewRouter(database *db.DB, textHandler TextHandler, agentRunHandler AgentRunHandler) *Router {
	return &Router{
		db:         database,
		onText:     textHandler,
		onAgentRun: agentRunHandler,
	}
}

// Handle dispatches msg to the right handler and uses send() for replies.
func (r *Router) Handle(ctx context.Context, msg Message, send func(string)) error {
	cmd, arg := ParseCommand(msg.Text)

	switch cmd {
	case "start":
		return r.handleStart(ctx, msg, send)
	case "help":
		send(helpText())
		return nil
	case "agent":
		return r.handleAgent(ctx, msg, arg, send)
	case "secret":
		return r.handleSecret(ctx, msg, arg, send)
	case "remind":
		return r.handleRemind(ctx, msg, arg, send)
	case "run":
		return r.handleRun(ctx, msg, arg, send)
	case "":
		// Plain text — one-off chat
		return r.handleText(ctx, msg, send)
	default:
		send(fmt.Sprintf("Unknown command /%s — try /help", cmd))
		return nil
	}
}

// ─── Command handlers ──────────────────────────────────────────────────────────

func (r *Router) handleStart(ctx context.Context, msg Message, send func(string)) error {
	// Link the Telegram user to the internal user (one-to-one: only owner's chat ID).
	existing, _ := r.db.GetPlatformIdentity(msg.Platform, msg.PlatformUserID)
	if existing != nil {
		send("You're already linked! Send /help to see available commands.")
		return nil
	}

	// Check if this owner already has an identity linked — personal bots are 1:1.
	// We allow only one linked Telegram identity per user per platform.
	// If a different chat ID is already linked, reject.
	rows, _ := r.db.ListPlatformIdentities(msg.UserID, msg.Platform)
	if len(rows) > 0 {
		send("This bot is already linked to another Telegram account. Contact your administrator to reset the link.")
		return nil
	}

	if err := r.db.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID:             uuid.New().String(),
		UserID:         msg.UserID,
		Platform:       msg.Platform,
		PlatformUserID: msg.PlatformUserID,
	}); err != nil {
		return fmt.Errorf("link identity: %w", err)
	}

	u, err := r.db.GetUserByID(msg.UserID)
	if err != nil {
		send("Linked successfully! Send /help to get started.")
		return nil
	}

	send(fmt.Sprintf("Hi *%s*\\! Your Telegram account is now linked\\. Send /help to see what you can do\\.", escapeMarkdown(u.Username)))
	return nil
}

func (r *Router) handleAgent(ctx context.Context, msg Message, arg string, send func(string)) error {
	parts := strings.Fields(arg)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}

	switch sub {
	case "list", "":
		agents, err := r.db.ListAgents(msg.UserID)
		if err != nil {
			return err
		}
		if len(agents) == 0 {
			send("You have no agents yet\\. Create one at the web dashboard\\.")
			return nil
		}
		var b strings.Builder
		b.WriteString("*Your agents:*\n")
		for _, a := range agents {
			status := "●"
			if !a.Active {
				status = "○"
			}
			b.WriteString(fmt.Sprintf("%s *%s*", status, escapeMarkdown(a.Name)))
			if a.Description != "" {
				b.WriteString(" — " + escapeMarkdown(a.Description))
			}
			b.WriteByte('\n')
		}
		b.WriteString("\n_Use /run \\<name\\> to run an agent_")
		send(b.String())
	default:
		send("Usage: /agent list")
	}
	return nil
}

func (r *Router) handleRun(ctx context.Context, msg Message, arg string, send func(string)) error {
	name := strings.TrimSpace(arg)
	if name == "" {
		send("Usage: /run \\<agent\\_name\\>")
		return nil
	}

	if r.onAgentRun == nil {
		send("Agent execution is not yet available\\. Check back soon\\!")
		return nil
	}

	send(fmt.Sprintf("Running agent *%s*\\.\\.\\.", escapeMarkdown(name)))
	return r.onAgentRun(ctx, msg.UserID, name, send)
}

func (r *Router) handleSecret(ctx context.Context, msg Message, arg string, send func(string)) error {
	parts := strings.Fields(arg)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}

	switch sub {
	case "list", "":
		names, err := r.db.ListSecretNames(msg.UserID)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			send("You have no secrets stored yet\\. Add them at the web dashboard → Secrets\\.")
			return nil
		}
		var b strings.Builder
		b.WriteString("*Stored secrets \\(names only\\):*\n")
		for _, n := range names {
			b.WriteString("• `" + escapeMarkdown(n) + "`\n")
		}
		b.WriteString("\n_Values are never shown here for security\\. Use the web dashboard to manage secrets\\._")
		send(b.String())
	default:
		send("Usage: /secret list\n\n_To add or delete secrets, use the web dashboard → Secrets_")
	}
	return nil
}

func (r *Router) handleRemind(ctx context.Context, msg Message, arg string, send func(string)) error {
	// Full implementation in Phase 7 (reminder service with natural language parsing).
	send("Reminders via chat are coming in a future update\\. Use the web dashboard → Reminders to set them now\\.")
	return nil
}

func (r *Router) handleText(ctx context.Context, msg Message, send func(string)) error {
	if r.onText == nil {
		send("One\\-off chat is coming soon\\! For now, use /help to see what's available\\.")
		return nil
	}
	return r.onText(ctx, msg.UserID, msg.Text, send)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func helpText() string {
	return `*Simple Agents — Commands*

/agent list — list your agents
/run \<name\> — run an agent
/secret list — list stored secret names
/remind — set a reminder \(coming soon\)
/help — this message

_Manage agents, secrets & settings at the web dashboard_`
}

// escapeMarkdown escapes special characters for Telegram MarkdownV2.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(s)
}
