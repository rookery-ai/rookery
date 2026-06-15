package gateway

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/memory"
	"github.com/ilijad1/simple-agents/internal/reminder"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

// TextHandler is called for non-command messages (one-off chat or within a session).
// history contains prior turns when an active session is present; empty for stateless chat.
type TextHandler func(ctx context.Context, userID string, history []db.ChatMessage, text string, send func(string)) error

// AgentRunHandler is called when /run <name> is issued.
// Implemented in Phase 6 (AgentRunner).
type AgentRunHandler func(ctx context.Context, userID, agentName string, send func(string)) error

// secretChallenge holds a pending master-password request for a secret operation.
type secretChallenge struct {
	action string // "show" or "delete"
	name   string // secret name
}

// Router dispatches incoming messages to the appropriate handler.
type Router struct {
	db         *db.DB
	onText     TextHandler
	onAgentRun AgentRunHandler
	designFlow *agentdesigner.Flow
	memory     *memory.Store

	mu         sync.Mutex
	challenges map[string]*secretChallenge // userID → pending challenge
}

// NewRouter creates a Router. textHandler, agentRunHandler, and designFlow may be nil
// until the corresponding phases are wired in; the router will reply with stub messages.
func NewRouter(database *db.DB, textHandler TextHandler, agentRunHandler AgentRunHandler, flow *agentdesigner.Flow, mem *memory.Store) *Router {
	return &Router{
		db:         database,
		onText:     textHandler,
		onAgentRun: agentRunHandler,
		designFlow: flow,
		memory:     mem,
		challenges: make(map[string]*secretChallenge),
	}
}

// Handle dispatches msg to the right handler and uses send() for replies.
// deleteIncoming removes the user's incoming message (used to redact typed passwords).
// sendAutoDelete sends a message that is automatically deleted after 30 s (used for secret values).
func (r *Router) Handle(ctx context.Context, msg Message, send func(string), deleteIncoming func(), sendAutoDelete func(string)) error {
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
	case "memory":
		return r.handleMemory(ctx, msg, arg, send)
	case "":
		return r.handleText(ctx, msg, send, deleteIncoming, sendAutoDelete)
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
	rest := ""
	if len(parts) > 1 {
		rest = strings.Join(parts[1:], " ")
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
		b.WriteString("*Stored secrets:*\n")
		for _, n := range names {
			b.WriteString("• `" + escapeMarkdown(n) + "`\n")
		}
		b.WriteString("\n_/secret show \\<name\\> · /secret delete \\<name\\>_")
		send(b.String())

	case "show", "get":
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /secret show \\<name\\>")
			return nil
		}
		names, _ := r.db.ListSecretNames(msg.UserID)
		found := false
		for _, n := range names {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			send("Secret `" + escapeMarkdown(name) + "` not found\\.")
			return nil
		}
		r.mu.Lock()
		r.challenges[msg.UserID] = &secretChallenge{action: "show", name: name}
		r.mu.Unlock()
		send("🔐 Enter your master password to reveal `" + escapeMarkdown(name) + "`:\n\n_⚠️ The value will appear in this chat\\. Telegram stores chat history\\._")

	case "delete":
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /secret delete \\<name\\>")
			return nil
		}
		names, _ := r.db.ListSecretNames(msg.UserID)
		found := false
		for _, n := range names {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			send("Secret `" + escapeMarkdown(name) + "` not found\\.")
			return nil
		}
		r.mu.Lock()
		r.challenges[msg.UserID] = &secretChallenge{action: "delete", name: name}
		r.mu.Unlock()
		send("🔐 Enter your master password to confirm deletion of `" + escapeMarkdown(name) + "`:")

	default:
		send("Usage: /secret list · /secret show \\<name\\> · /secret delete \\<name\\>\n\n_To add secrets, use the web dashboard → Secrets_")
	}
	return nil
}

// resolveMasterPwChallenge handles the master-password reply for a pending challenge.
// It deletes the incoming password message from chat and does NOT persist this
// exchange to session history. The secret value (if shown) is sent via
// sendAutoDelete so it is automatically removed after 30 seconds.
func (r *Router) resolveMasterPwChallenge(ctx context.Context, msg Message, ch *secretChallenge, send func(string), deleteIncoming func(), sendAutoDelete func(string)) error {
	// Delete the typed password from chat immediately, before doing anything else.
	deleteIncoming()

	masterPw := strings.TrimSpace(msg.Text)
	if masterPw == "" {
		send("Master password cannot be empty\\. Challenge cancelled\\.")
		return nil
	}

	u, err := r.db.GetUserByID(msg.UserID)
	if err != nil {
		send("Error: could not load user\\.")
		return err
	}
	if u.SecretsSalt == "" {
		send("Account setup incomplete — no master password configured\\.")
		return nil
	}

	svc := secrets.New(r.db, msg.UserID, masterPw, u.SecretsSalt)

	switch ch.action {
	case "show":
		val, err := svc.Get(ctx, ch.name)
		if errors.Is(err, secrets.ErrWrongPassword) {
			send("❌ Wrong master password\\.")
			return nil
		}
		if errors.Is(err, secrets.ErrNotFound) {
			send("Secret `" + escapeMarkdown(ch.name) + "` not found\\.")
			return nil
		}
		if err != nil {
			send("Error retrieving secret: " + escapeMarkdown(err.Error()))
			return nil
		}
		sendAutoDelete("🔑 `" + escapeMarkdown(ch.name) + "`:\n`" + escapeMarkdown(val) + "`\n\n_⏱ This message will be deleted in 30 seconds_")

	case "delete":
		if _, err := svc.Get(ctx, ch.name); err != nil {
			if errors.Is(err, secrets.ErrWrongPassword) {
				send("❌ Wrong master password — secret not deleted\\.")
				return nil
			}
		}
		if err := r.db.DeleteSecret(msg.UserID, ch.name); err != nil {
			send("Error deleting secret: " + escapeMarkdown(err.Error()))
			return nil
		}
		send("🗑 Secret `" + escapeMarkdown(ch.name) + "` deleted\\.")
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

func (r *Router) handleText(ctx context.Context, msg Message, send func(string), deleteIncoming func(), sendAutoDelete func(string)) error {
	// Check for a pending master-password challenge. These exchanges are intentionally
	// NOT persisted to session history to keep sensitive values out of the DB.
	r.mu.Lock()
	challenge, hasPending := r.challenges[msg.UserID]
	if hasPending {
		delete(r.challenges, msg.UserID)
	}
	r.mu.Unlock()
	if hasPending {
		return r.resolveMasterPwChallenge(ctx, msg, challenge, send, deleteIncoming, sendAutoDelete)
	}

	// If the user has an active design session, route all text there.
	if r.designFlow != nil && r.designFlow.GetSession(msg.UserID) != nil {
		response, _, _, err := r.designFlow.Step(ctx, msg.UserID, msg.Text)
		if err != nil {
			send("Design session error: " + escapeMarkdown(err.Error()))
			return nil
		}
		send(response)
		return nil
	}

	if r.onText == nil {
		send("Send /agent create \\<name\\> to build an agent, or /help for all commands\\.")
		return nil
	}

	// Look up an active chat session for this user+platform and load its history.
	var history []db.ChatMessage
	var activeSession *db.ChatSession
	if sess, err := r.db.GetActiveSessionForPlatform(msg.UserID, msg.Platform); err == nil && sess != nil {
		activeSession = sess
		history, _ = r.db.ListChatMessages(sess.ID)
	}

	// Intercept send so we can capture the assistant reply for session storage.
	var assistantReply string
	wrappedSend := func(text string) {
		assistantReply = text
		send(text)
	}

	if err := r.onText(ctx, msg.UserID, history, msg.Text, wrappedSend); err != nil {
		return err
	}

	// Persist messages and touch the session.
	if activeSession != nil && assistantReply != "" {
		_ = r.db.AddChatMessage(activeSession.ID, "user", msg.Text)
		_ = r.db.AddChatMessage(activeSession.ID, "assistant", assistantReply)
		_ = r.db.TouchChatSession(activeSession.ID)
	}
	return nil
}

func (r *Router) handleSession(ctx context.Context, msg Message, arg string, send func(string)) error {
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
	case "start", "":
		// Stop any currently active session on this platform first.
		if cur, err := r.db.GetActiveSessionForPlatform(msg.UserID, msg.Platform); err == nil && cur != nil {
			_ = r.db.StopChatSession(cur.ID)
		}
		sess := &db.ChatSession{
			ID:       uuid.New().String(),
			UserID:   msg.UserID,
			Name:     rest,
			Platform: msg.Platform,
			Active:   true,
		}
		if err := r.db.CreateChatSession(sess); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		label := rest
		if label == "" {
			label = sess.ID[:8]
		}
		send(fmt.Sprintf("💬 Session *%s* started \\(`%s`\\)\\. Send /session stop to end it\\.", escapeMarkdown(label), escapeMarkdown(sess.ID[:8])))

	case "list":
		sessions, err := r.db.ListChatSessions(msg.UserID)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			send("No sessions yet\\. Use /session start to begin one\\.")
			return nil
		}
		var b strings.Builder
		b.WriteString("*Sessions:*\n")
		for _, s := range sessions {
			label := s.Name
			if label == "" {
				label = s.ID[:8]
			}
			status := "🟢"
			if !s.Active {
				status = "⚫"
			}
			b.WriteString(fmt.Sprintf("%s *%s* `%s` — %s\n",
				status,
				escapeMarkdown(label),
				escapeMarkdown(s.ID[:8]),
				escapeMarkdown(s.LastSeen.Format("Jan 2 15:04"))))
		}
		b.WriteString("\n_/session resume \\<id\\> · /session delete \\<id\\> · /session stop_")
		send(b.String())

	case "stop":
		if cur, err := r.db.GetActiveSessionForPlatform(msg.UserID, msg.Platform); err == nil && cur != nil {
			_ = r.db.StopChatSession(cur.ID)
			label := cur.Name
			if label == "" {
				label = cur.ID[:8]
			}
			send(fmt.Sprintf("Session *%s* stopped\\.", escapeMarkdown(label)))
		} else {
			send("No active session to stop\\.")
		}

	case "resume":
		if rest == "" {
			send("Usage: /session resume \\<id\\>")
			return nil
		}
		sess, err := r.db.FindSessionByPrefix(msg.UserID, rest)
		if err != nil {
			send("Session not found\\. Use /session list to see your sessions\\.")
			return nil
		}
		// Stop any currently active session first.
		if cur, err := r.db.GetActiveSessionForPlatform(msg.UserID, msg.Platform); err == nil && cur != nil && cur.ID != sess.ID {
			_ = r.db.StopChatSession(cur.ID)
		}
		if err := r.db.ResumeSession(sess.ID); err != nil {
			return fmt.Errorf("resume session: %w", err)
		}
		label := sess.Name
		if label == "" {
			label = sess.ID[:8]
		}
		history, _ := r.db.ListChatMessages(sess.ID)
		send(fmt.Sprintf("💬 Session *%s* resumed \\(%d messages\\)\\. Continue chatting\\.", escapeMarkdown(label), len(history)))

	case "delete":
		if rest == "" {
			send("Usage: /session delete \\<id\\>")
			return nil
		}
		sess, err := r.db.FindSessionByPrefix(msg.UserID, rest)
		if err != nil {
			send("Session not found\\. Use /session list to see your sessions\\.")
			return nil
		}
		label := sess.Name
		if label == "" {
			label = sess.ID[:8]
		}
		if err := r.db.DeleteChatSession(sess.ID); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
		send(fmt.Sprintf("🗑 Session *%s* deleted\\.", escapeMarkdown(label)))

	default:
		send("Usage: /session start \\[name\\] · /session list · /session stop · /session resume \\<id\\> · /session delete \\<id\\>")
	}
	return nil
}

func (r *Router) handleMemory(ctx context.Context, msg Message, arg string, send func(string)) error {
	if r.memory == nil {
		send("Memory is not available\\.")
		return nil
	}

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
	case "add":
		text := strings.TrimSpace(rest)
		if text == "" {
			send("Usage: /memory add \\<text\\>")
			return nil
		}
		if _, err := r.memory.Append(msg.UserID, text); err != nil {
			return fmt.Errorf("append memory: %w", err)
		}
		send("✅ Saved: _" + escapeMarkdown(text) + "_")

	case "delete":
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil || n < 1 {
			send("Usage: /memory delete \\<number\\> — use /memory list to see numbers")
			return nil
		}
		entries, err := r.memory.List(msg.UserID)
		if err != nil {
			return fmt.Errorf("list memory: %w", err)
		}
		if n > len(entries) {
			send(fmt.Sprintf("No entry \\#%d\\. You have %d entries\\.", n, len(entries)))
			return nil
		}
		if err := r.memory.Delete(msg.UserID, entries[n-1].ID); err != nil {
			return fmt.Errorf("delete memory: %w", err)
		}
		send(fmt.Sprintf("🗑 Deleted entry \\#%d: _%s_", n, escapeMarkdown(entries[n-1].Content)))

	default: // "list" or ""
		entries, err := r.memory.List(msg.UserID)
		if err != nil {
			return fmt.Errorf("list memory: %w", err)
		}
		if len(entries) == 0 {
			send("No memory entries yet\\. Use /memory add \\<text\\> to save one\\.")
			return nil
		}
		var b strings.Builder
		b.WriteString("*Memory entries:*\n")
		for i, e := range entries {
			b.WriteString(fmt.Sprintf("%d\\. _%s_ — `%s`\n",
				i+1,
				escapeMarkdown(e.Content),
				escapeMarkdown(e.CreatedAt.Format("Jan 2"))))
		}
		b.WriteString("\n_/memory add \\<text\\> · /memory delete \\<n\\>_")
		send(b.String())
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
/secret show \<name\> — reveal a secret value \(requires master password\)
/secret delete \<name\> — delete a secret \(requires master password\)
/remind \<when\> to \<message\> — set a reminder \(e\.g\. /remind in 10 minutes to check oven\)
/session start \[name\] — start a chat session \(saves history\)
/session list — list all sessions with IDs
/session stop — stop current session
/session resume \<id\> — resume a previous session
/session delete \<id\> — delete a session and its history
/memory list — list saved memory entries
/memory add \<text\> — save a new memory entry
/memory delete \<n\> — delete entry by number
/help — this message

_Add secrets at the web dashboard — no master password needed to add_`
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
