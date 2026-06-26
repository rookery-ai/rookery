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
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/reminder"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

// TextHandler is called for non-command messages (one-off chat or within a chat).
// history contains prior turns when an active chat is present; empty for stateless chat.
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

	// timeParserFallback is an optional LLM-backed time parser used when the
	// regex parser in reminder.ParseNaturalTime fails to understand the input.
	timeParserFallback reminder.TimeParserFunc

	mu                sync.Mutex
	challenges        map[string]*secretChallenge // userID → pending master-password challenge
	pendingCancel     map[string]bool             // userID → waiting for save/discard reply to /agent cancel
	pendingReminderMsg map[string]string           // userID → reminder message waiting for a "when" reply
}

// NewRouter creates a Router. textHandler, agentRunHandler, and designFlow may be nil
// until the corresponding phases are wired in; the router will reply with stub messages.
func NewRouter(database *db.DB, textHandler TextHandler, agentRunHandler AgentRunHandler, flow *agentdesigner.Flow, mem *memory.Store) *Router {
	return &Router{
		db:                 database,
		onText:             textHandler,
		onAgentRun:         agentRunHandler,
		designFlow:         flow,
		memory:             mem,
		challenges:         make(map[string]*secretChallenge),
		pendingCancel:      make(map[string]bool),
		pendingReminderMsg: make(map[string]string),
	}
}

// WithTimeParserFallback sets an LLM-backed time parser to use when the built-in
// regex parser fails. The fallback is also used when a reminder message is provided
// without an explicit time expression.
func (r *Router) WithTimeParserFallback(fn reminder.TimeParserFunc) *Router {
	r.timeParserFallback = fn
	return r
}

// Handle dispatches msg to the right handler and uses send() for replies.
// deleteIncoming removes the user's incoming message (used to redact typed passwords).
// sendAutoDelete sends a message that is automatically deleted after 30 s (used for secret values).
// sendProgress edits the current Telegram placeholder WITHOUT consuming it, used for mid-generation milestones.
func (r *Router) Handle(ctx context.Context, msg Message, send func(string), deleteIncoming func(), sendAutoDelete func(string), sendProgress func(string)) error {
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
	case "chat":
		return r.handleChat(ctx, msg, arg, send)
	case "memory":
		return r.handleMemory(ctx, msg, arg, send)
	case "":
		return r.handleText(ctx, msg, send, deleteIncoming, sendAutoDelete, sendProgress)
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
		// If the user has a resumable draft, offer to continue it instead of
		// starting fresh. Subsequent text routes through Step → stepAwaitingResume.
		if draft := r.designFlow.HasDraft(msg.UserID); draft != nil {
			response, err := r.designFlow.OfferDraftResume(msg.UserID, name, draft)
			if err != nil {
				send(escapeMarkdown(err.Error()))
				return nil
			}
			send(response)
			return nil
		}
		response, err := r.designFlow.Start(msg.UserID, name)
		if err != nil {
			send(escapeMarkdown(err.Error()))
			return nil
		}
		send(response)

	case "edit":
		if r.designFlow == nil {
			send("Agent editing is not yet available\\.")
			return nil
		}
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /agent edit \\<name\\>")
			return nil
		}
		agent, err := r.db.GetAgentByName(msg.UserID, name)
		if err != nil {
			send("Agent `" + escapeMarkdown(name) + "` not found\\.")
			return nil
		}
		response, err := r.designFlow.StartEdit(msg.UserID, agent.ID)
		if err != nil {
			send(escapeMarkdown(err.Error()))
			return nil
		}
		send(response)

	case "cancel":
		if r.designFlow == nil {
			send("Agent design is not yet available\\.")
			return nil
		}
		// Stop any in-flight generation / drop the active session now. The draft
		// (auto-saved on every turn) is preserved either way — the question below
		// is only whether to keep it or throw it away.
		r.designFlow.Cancel(msg.UserID)

		draft := r.designFlow.HasDraft(msg.UserID)
		if draft == nil {
			send("Agent design session cancelled\\. No draft to save\\.")
			return nil
		}
		// Ask the user to choose. The reply is handled in handleText (pendingCancel).
		r.mu.Lock()
		r.pendingCancel[msg.UserID] = true
		r.mu.Unlock()
		send(fmt.Sprintf(
			"Agent design cancelled\\. You have an unfinished draft: *%s*\n\nReply `save` to keep it as a draft you can resume later, or `discard` to delete it\\.",
			escapeMarkdown(draft.AgentName),
		))

	default:
		send("Usage: /agent list · /agent create \\<name\\> · /agent edit \\<name\\> · /agent cancel")
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

// resolveCancelChoice handles the user's reply to the /agent cancel save/discard
// prompt. "discard" deletes the draft (and the orphaned pre-approved agent
// directory if generation had reached verifying); anything else keeps the draft
// as resumable. The active session was already cancelled when /agent cancel ran.
func (r *Router) resolveCancelChoice(msg Message, send func(string)) error {
	lower := strings.ToLower(strings.TrimSpace(msg.Text))

	if lower == "discard" || lower == "delete" || lower == "drop" {
		name := ""
		if d := r.designFlow.HasDraft(msg.UserID); d != nil {
			name = d.AgentName
		}
		_ = r.designFlow.DismissDraft(msg.UserID)
		if name != "" {
			send(fmt.Sprintf("🗑 Draft *%s* discarded\\.", escapeMarkdown(name)))
		} else {
			send("🗑 Draft discarded\\.")
		}
		return nil
	}

	// "save" (or anything else) → keep the draft. It stays resumable via
	// /agent create <name> (which will offer to resume) or the web dashboard.
	send("✅ Draft saved\\. Use `/agent create \\<name\\>` to resume it later\\.")
	return nil
}

// handleRemind parses natural language reminders. Accepts flexible input:
//
//	/remind in 10 minutes to check the oven
//	/remind next Tuesday to call doctor
//	/remind next Friday evening write a note about my bitcoin price
//	/remind to write a note about my bitcoin price      (asks for time)
//	/remind 30m old format still works
func (r *Router) handleRemind(ctx context.Context, msg Message, arg string, send func(string)) error {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		send("Usage: /remind \\<when\\> to \\<message\\>\nExamples:\n• /remind in 10 minutes to check the oven\n• /remind tomorrow at 3pm to call doctor\n• /remind next Tuesday to pay bills\n• /remind next Friday evening write note about bitcoin price\n• /remind 30m old format still works")
		return nil
	}

	// Strip optional leading "me "
	arg = strings.TrimPrefix(arg, "me ")
	arg = strings.TrimSpace(arg)

	now := time.Now()
	loc := profile.LoadLocation(r.db, msg.UserID)

	// Strategy: try to extract a time from the full arg using LLM (smartest path),
	// falling back to the " to " split + regex approach for speed when possible.
	var timeExpr, message string
	var remindAt time.Time

	// 1. Try " to " split → parse time expression with regex first.
	if idx := strings.Index(arg, " to "); idx >= 0 {
		timeExpr = strings.TrimSpace(arg[:idx])
		message = strings.TrimSpace(arg[idx+4:])
		if t, err := reminder.ParseNaturalTime(timeExpr, now, loc); err == nil {
			remindAt = t
		} else if d, err2 := parseDuration(timeExpr); err2 == nil {
			remindAt = now.Add(d)
		}
	}

	// 2. If the above didn't resolve, send the whole arg to the LLM.
	if remindAt.IsZero() {
		if r.timeParserFallback != nil {
			when, extractedMsg, err := r.timeParserFallback(ctx, msg.UserID, arg, now, loc)
			if err == nil && !when.IsZero() {
				remindAt = when
				if extractedMsg != "" && extractedMsg != arg {
					message = extractedMsg
				}
			} else if err == nil && when.IsZero() {
				// LLM says no time in the input — ask the user.
				if extractedMsg != "" {
					message = extractedMsg
				} else {
					message = arg
				}
				r.mu.Lock()
				r.pendingReminderMsg[msg.UserID] = message
				r.mu.Unlock()
				send(fmt.Sprintf("⏰ When should I remind you about *%s*?\nReply with a time, e.g\\. 'in 10 minutes', 'tomorrow at 9am', 'next Friday evening'", escapeMarkdown(message)))
				return nil
			}
		}
	}

	// 3. Legacy fallback: first word as duration (backward compat).
	if remindAt.IsZero() {
		parts := strings.SplitN(arg, " ", 2)
		if len(parts) == 2 {
			if d, err := parseDuration(parts[0]); err == nil {
				remindAt = now.Add(d)
				if message == "" {
					message = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	if remindAt.IsZero() {
		send("Couldn't understand that time\\. Try:\n• /remind in 10 minutes to check oven\n• /remind next Tuesday to call doctor\n• /remind next Friday evening write note about bitcoin\n• /remind 30m old format\n• /remind to write a note _(I'll ask when)_")
		return nil
	}

	if message == "" {
		send("Please include a reminder message\\. Example: /remind in 10 minutes to check the oven")
		return nil
	}

	return r.createReminder(ctx, msg.UserID, message, remindAt, send)
}

func (r *Router) createReminder(ctx context.Context, userID, message string, remindAt time.Time, send func(string)) error {
	rm := &db.Reminder{
		ID:       uuid.New().String(),
		UserID:   userID,
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

// resolvePendingReminder handles the user's reply to a "when?" prompt for a reminder
// that had no time expression. The message is already known; this call parses the time.
func (r *Router) resolvePendingReminder(ctx context.Context, msg Message, reminderMsg string, send func(string)) error {
	now := time.Now()
	loc := profile.LoadLocation(r.db, msg.UserID)

	// Try standard regex first.
	if t, err := reminder.ParseNaturalTime(msg.Text, now, loc); err == nil {
		return r.createReminder(ctx, msg.UserID, reminderMsg, t, send)
	}

	// Try LLM fallback.
	if r.timeParserFallback != nil {
		when, _, err := r.timeParserFallback(ctx, msg.UserID, msg.Text, now, loc)
		if err == nil && !when.IsZero() {
			return r.createReminder(ctx, msg.UserID, reminderMsg, when, send)
		}
	}

	// Re-queue the pending message and ask again.
	r.mu.Lock()
	r.pendingReminderMsg[msg.UserID] = reminderMsg
	r.mu.Unlock()
	send("Couldn't understand that time\\. Please try again, e\\.g\\. 'tomorrow at 9am' or 'next Friday'")
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

func (r *Router) handleText(ctx context.Context, msg Message, send func(string), deleteIncoming func(), sendAutoDelete func(string), sendProgress func(string)) error {
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

	// Check for a pending /agent cancel save/discard choice. This runs before the
	// design-session routing because Cancel() already dropped any active session.
	r.mu.Lock()
	hasCancelChoice := r.pendingCancel[msg.UserID]
	if hasCancelChoice {
		delete(r.pendingCancel, msg.UserID)
	}
	r.mu.Unlock()
	if hasCancelChoice {
		return r.resolveCancelChoice(msg, send)
	}

	// Check for a pending reminder "when?" prompt — the user is supplying a time
	// for a reminder message that had no time expression.
	r.mu.Lock()
	pendingMsg, hasPendingReminder := r.pendingReminderMsg[msg.UserID]
	if hasPendingReminder {
		delete(r.pendingReminderMsg, msg.UserID)
	}
	r.mu.Unlock()
	if hasPendingReminder {
		return r.resolvePendingReminder(ctx, msg, pendingMsg, send)
	}

	// If the user has an active design session, route all text there.
	if r.designFlow != nil && r.designFlow.GetSession(msg.UserID) != nil {
		sess := r.designFlow.GetSession(msg.UserID)
		// When the user approves generation, register the progress callback so the
		// Telegram placeholder gets edited with milestone messages during the run.
		if sess != nil && sess.State == agentdesigner.StateDesigning && isApprovalText(msg.Text) && sendProgress != nil {
			r.designFlow.SetProgressHandler(msg.UserID, sendProgress)
		}
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

	// Look up an active chat for this user+platform and load its history.
	var history []db.ChatMessage
	var activeChat *db.Chat
	if c, err := r.db.GetActiveChatForPlatform(msg.UserID, msg.Platform); err == nil && c != nil {
		activeChat = c
		history, _ = r.db.ListChatMessages(c.ID)
	}

	// Intercept send so we can capture the assistant reply for chat storage.
	var assistantReply string
	wrappedSend := func(text string) {
		assistantReply = text
		send(text)
	}

	if err := r.onText(ctx, msg.UserID, history, msg.Text, wrappedSend); err != nil {
		return err
	}

	// Persist messages and touch the chat.
	if activeChat != nil && assistantReply != "" {
		_ = r.db.AddChatMessage(activeChat.ID, "user", msg.Text)
		_ = r.db.AddChatMessage(activeChat.ID, "assistant", assistantReply)
		_ = r.db.TouchChat(activeChat.ID)
	}
	return nil
}

func (r *Router) handleChat(ctx context.Context, msg Message, arg string, send func(string)) error {
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
		// Stop any currently active chat on this platform first.
		if cur, err := r.db.GetActiveChatForPlatform(msg.UserID, msg.Platform); err == nil && cur != nil {
			_ = r.db.StopChat(cur.ID)
		}
		c := &db.Chat{
			ID:       uuid.New().String(),
			UserID:   msg.UserID,
			Name:     rest,
			Platform: msg.Platform,
			Active:   true,
		}
		if err := r.db.CreateChat(c); err != nil {
			return fmt.Errorf("create chat: %w", err)
		}
		label := rest
		if label == "" {
			label = c.ID[:8]
		}
		send(fmt.Sprintf("💬 Chat *%s* started \\(`%s`\\)\\. Send /chat stop to end it\\.", escapeMarkdown(label), escapeMarkdown(c.ID[:8])))

	case "list":
		chats, err := r.db.ListChats(msg.UserID)
		if err != nil {
			return err
		}
		if len(chats) == 0 {
			send("No chats yet\\. Use /chat start to begin one\\.")
			return nil
		}
		var b strings.Builder
		b.WriteString("*Chats:*\n")
		for _, c := range chats {
			label := c.Name
			if label == "" {
				label = c.ID[:8]
			}
			status := "🟢"
			if !c.Active {
				status = "⚫"
			}
			b.WriteString(fmt.Sprintf("%s *%s* `%s` — %s\n",
				status,
				escapeMarkdown(label),
				escapeMarkdown(c.ID[:8]),
				escapeMarkdown(c.LastSeen.Format("Jan 2 15:04"))))
		}
		b.WriteString("\n_/chat resume \\<id\\> · /chat delete \\<id\\> · /chat stop_")
		send(b.String())

	case "stop":
		if cur, err := r.db.GetActiveChatForPlatform(msg.UserID, msg.Platform); err == nil && cur != nil {
			_ = r.db.StopChat(cur.ID)
			label := cur.Name
			if label == "" {
				label = cur.ID[:8]
			}
			send(fmt.Sprintf("Chat *%s* stopped\\.", escapeMarkdown(label)))
		} else {
			send("No active chat to stop\\.")
		}

	case "resume":
		if rest == "" {
			send("Usage: /chat resume \\<id\\>")
			return nil
		}
		c, err := r.db.FindChatByPrefix(msg.UserID, rest)
		if err != nil {
			send("Chat not found\\. Use /chat list to see your chats\\.")
			return nil
		}
		// Stop any currently active chat first.
		if cur, err := r.db.GetActiveChatForPlatform(msg.UserID, msg.Platform); err == nil && cur != nil && cur.ID != c.ID {
			_ = r.db.StopChat(cur.ID)
		}
		if err := r.db.ResumeChat(c.ID); err != nil {
			return fmt.Errorf("resume chat: %w", err)
		}
		label := c.Name
		if label == "" {
			label = c.ID[:8]
		}
		history, _ := r.db.ListChatMessages(c.ID)
		send(fmt.Sprintf("💬 Chat *%s* resumed \\(%d messages\\)\\. Continue chatting\\.", escapeMarkdown(label), len(history)))

	case "delete":
		if rest == "" {
			send("Usage: /chat delete \\<id\\>")
			return nil
		}
		c, err := r.db.FindChatByPrefix(msg.UserID, rest)
		if err != nil {
			send("Chat not found\\. Use /chat list to see your chats\\.")
			return nil
		}
		label := c.Name
		if label == "" {
			label = c.ID[:8]
		}
		if err := r.db.DeleteChat(c.ID); err != nil {
			return fmt.Errorf("delete chat: %w", err)
		}
		send(fmt.Sprintf("🗑 Chat *%s* deleted\\.", escapeMarkdown(label)))

	default:
		send("Usage: /chat start \\[name\\] · /chat list · /chat stop · /chat resume \\<id\\> · /chat delete \\<id\\>")
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
/agent edit \<name\> — change an existing agent with AI wizard
/agent cancel — cancel active agent creation or edit
/run \<name\> — run an agent
/secret list — list stored secret names
/secret show \<name\> — reveal a secret value \(requires master password\)
/secret delete \<name\> — delete a secret \(requires master password\)
/remind \<when\> to \<message\> — set a reminder \(e\.g\. /remind in 10 minutes to check oven\)
/chat start \[name\] — start a chat \(saves history\)
/chat list — list all chats with IDs
/chat stop — stop current chat
/chat resume \<id\> — resume a previous chat
/chat delete \<id\> — delete a chat and its history
/memory list — list saved memory entries
/memory add \<text\> — save a new memory entry
/memory delete \<n\> — delete entry by number
/help — this message

_Add secrets at the web dashboard — no master password needed to add_`
}

// isApprovalText returns true when the user message is one of the exact approval
// triggers recognised by agentdesigner.Flow. Mirrors the list in flow.go so the
// router can register a progress callback before Step() blocks on generation.
func isApprovalText(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "approve", "go ahead", "build it", "create it", "/approve":
		return true
	}
	return false
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
