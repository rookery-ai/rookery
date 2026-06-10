package gateway

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/reminder"
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
	designFlow     *agentdesigner.Flow // may be nil until Phase 5 is wired
}

// NewRouter creates a Router. textHandler, agentRunHandler, and designFlow may be nil
// until the corresponding phases are wired in; the router will reply with stub messages.
func NewRouter(database *db.DB, textHandler TextHandler, agentRunHandler AgentRunHandler, flow *agentdesigner.Flow) *Router {
	return &Router{
		db:         database,
		onText:     textHandler,
		onAgentRun: agentRunHandler,
		designFlow: flow,
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
	case "session":
		return r.handleSession(ctx, msg, arg, send)
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
	rest := ""
	if len(parts) > 1 {
		rest = strings.Join(parts[1:], " ")
	}

	switch sub {
	case "list", "":
		agents, err := r.db.ListAgents(msg.UserID)
		if err != nil {
			return err
		}
		if len(agents) == 0 {
			send("You have no agents yet\\. Use /agent create \\<name\\> to build one\\.")
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
		b.WriteString("\n_/run \\<name\\> to run · /agent create \\<name\\> to build a new one_")
		send(b.String())

	case "create":
		if r.designFlow == nil {
			send("Agent creation is not yet available\\.")
			return nil
		}
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /agent create \\<name\\>")
			return nil
		}
		response, err := r.designFlow.Start(msg.UserID, name)
		if err != nil {
			send(escapeMarkdown(err.Error()))
			return nil
		}
		send(response)

	case "cancel":
		if r.designFlow != nil {
			r.designFlow.Cancel(msg.UserID)
		}
		send("Agent creation cancelled\\.")

	default:
		send("Usage: /agent list · /agent create \\<name\\> · /agent cancel")
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

// handleRemind parses natural language reminders:
//   /remind in 10 minutes to check the oven
//   /remind next Tuesday to call doctor
//   /remind me in 10 minutes to start fire
//   /remind 30m old format still works
func (r *Router) handleRemind(ctx context.Context, msg Message, arg string, send func(string)) error {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		send("Usage: /remind \\<when\\> to \\<message\\>\nExamples:\n• /remind in 10 minutes to check the oven\n• /remind tomorrow at 3pm to call doctor\n• /remind next Tuesday to pay bills\n• /remind 30m old format still works")
		return nil
	}

	// Strip optional leading "me "
	arg = strings.TrimPrefix(arg, "me ")
	arg = strings.TrimSpace(arg)

	// Split on " to " to separate time expression from message.
	// Falls back to first-word-is-duration for backward compat.
	var timeExpr, message string
	if idx := strings.Index(arg, " to "); idx >= 0 {
		timeExpr = strings.TrimSpace(arg[:idx])
		message = strings.TrimSpace(arg[idx+4:])
	} else {
		parts := strings.SplitN(arg, " ", 2)
		if len(parts) < 2 {
			send("Please include a message\\. Example: /remind in 10 minutes to check the oven")
			return nil
		}
		timeExpr, message = parts[0], strings.TrimSpace(parts[1])
	}

	if message == "" {
		send("Please include a message after 'to'\\. Example: /remind in 10 minutes to check the oven")
		return nil
	}

	// Try natural language parser first, then legacy duration format.
	remindAt, err := reminder.ParseNaturalTime(timeExpr, time.Now(), time.UTC)
	if err != nil {
		d, err2 := parseDuration(timeExpr)
		if err2 != nil {
			send("Couldn't understand that time\\. Try:\n• /remind in 10 minutes to check oven\n• /remind next Tuesday to call doctor\n• /remind 30m old format")
			return nil
		}
		remindAt = time.Now().Add(d)
	}

	rm := &db.Reminder{
		ID:       uuid.New().String(),
		UserID:   msg.UserID,
		Message:  message,
		RemindAt: remindAt,
	}
	if err := r.db.CreateReminder(rm); err != nil {
		return fmt.Errorf("create reminder: %w", err)
	}

	when := remindAt.Format("Jan 2 at 15:04")
	send(fmt.Sprintf("⏰ Reminder set for *%s*: _%s_", escapeMarkdown(when), escapeMarkdown(message)))
	return nil
}

// parseDuration parses reminder durations: 30m, 1h, 2h30m, 1d, 2d.
func parseDuration(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	var total time.Duration
	remaining := s

	// days
	if idx := strings.Index(remaining, "d"); idx >= 0 {
		n, err := strconv.Atoi(remaining[:idx])
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid days")
		}
		total += time.Duration(n) * 24 * time.Hour
		remaining = remaining[idx+1:]
	}

	// hours
	if idx := strings.Index(remaining, "h"); idx >= 0 {
		n, err := strconv.Atoi(remaining[:idx])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid hours")
		}
		total += time.Duration(n) * time.Hour
		remaining = remaining[idx+1:]
	}

	// minutes
	if idx := strings.Index(remaining, "m"); idx >= 0 {
		n, err := strconv.Atoi(remaining[:idx])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid minutes")
		}
		total += time.Duration(n) * time.Minute
		remaining = remaining[idx+1:]
	}

	if remaining != "" && remaining != "s" {
		return 0, fmt.Errorf("unrecognised duration suffix: %q", remaining)
	}
	if total <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return total, nil
}

func (r *Router) handleText(ctx context.Context, msg Message, send func(string)) error {
	// If the user has an active design session, route all text there.
	if r.designFlow != nil && r.designFlow.GetSession(msg.UserID) != nil {
		response, isDone, err := r.designFlow.Step(ctx, msg.UserID, msg.Text)
		if err != nil {
			send("Design session error: " + escapeMarkdown(err.Error()))
			return nil
		}
		send(response)
		_ = isDone
		return nil
	}

	if r.onText == nil {
		send("Send /agent create \\<name\\> to build an agent, or /help for all commands\\.")
		return nil
	}
	return r.onText(ctx, msg.UserID, msg.Text, send)
}

func (r *Router) handleSession(ctx context.Context, msg Message, arg string, send func(string)) error {
	parts := strings.Fields(arg)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}
	name := ""
	if len(parts) > 1 {
		name = strings.Join(parts[1:], " ")
	}

	switch sub {
	case "start", "":
		sess := &db.ChatSession{
			ID:       uuid.New().String(),
			UserID:   msg.UserID,
			Name:     name,
			Platform: string(msg.Platform),
			Active:   true,
		}
		if err := r.db.CreateChatSession(sess); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		label := name
		if label == "" {
			label = sess.ID[:8]
		}
		send(fmt.Sprintf("💬 Session *%s* started\\. Send /session stop to end it\\.", escapeMarkdown(label)))

	case "list":
		sessions, err := r.db.ListChatSessions(msg.UserID)
		if err != nil {
			return err
		}
		active := sessions[:0]
		for _, s := range sessions {
			if s.Active {
				active = append(active, s)
			}
		}
		if len(active) == 0 {
			send("No active sessions\\. Use /session start to begin one\\.")
			return nil
		}
		var b strings.Builder
		b.WriteString("*Active sessions:*\n")
		for _, s := range active {
			label := s.Name
			if label == "" {
				label = s.ID[:8]
			}
			b.WriteString(fmt.Sprintf("• *%s* — %s\n", escapeMarkdown(label), escapeMarkdown(s.LastSeen.Format("Jan 2 15:04"))))
		}
		send(b.String())

	case "stop":
		sessions, err := r.db.ListChatSessions(msg.UserID)
		if err != nil {
			return err
		}
		stopped := 0
		for _, s := range sessions {
			if s.Active && s.Platform == string(msg.Platform) {
				_ = r.db.StopChatSession(s.ID)
				stopped++
			}
		}
		if stopped == 0 {
			send("No active sessions to stop\\.")
		} else {
			send(fmt.Sprintf("Session stopped\\. %d session\\(s\\) closed\\.", stopped))
		}

	default:
		send("Usage: /session start \\[name\\] · /session list · /session stop")
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func helpText() string {
	return `*Simple Agents — Commands*

/agent list — list your agents
/agent create \<name\> — build a new agent with AI wizard
/agent cancel — cancel active agent creation
/run \<name\> — run an agent
/secret list — list stored secret names
/remind \<when\> to \<message\> — set a reminder \(e\.g\. /remind in 10 minutes to check oven\)
/session start \[name\] — start a chat session
/session list — list active sessions
/session stop — stop current session
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
