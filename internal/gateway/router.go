package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/agentdesigner"
	"github.com/ilijad1/rookery/internal/chat"
	"github.com/ilijad1/rookery/internal/convert"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/memory"
	"github.com/ilijad1/rookery/internal/profile"
	"github.com/ilijad1/rookery/internal/reminder"
	"github.com/ilijad1/rookery/internal/secrets"
	"github.com/ilijad1/rookery/internal/skilldesigner"
	"github.com/ilijad1/rookery/internal/skilllibrary"
	"github.com/ilijad1/rookery/internal/vault"
)

// maxAttachmentBytes caps a chat attachment. Chat platforms already cap uploads
// well below this; the limit exists so a hostile or misbehaving adapter cannot
// hand the router an unbounded buffer. Matches the web upload endpoint's own
// cap (web/api_kb.go's maxUploadBytes) so the two doors behave identically.
const maxAttachmentBytes = 25 << 20

// TextHandler is called for non-command messages (one-off chat or within a chat).
// history contains prior turns when an active chat is present; empty for stateless chat.
type TextHandler func(ctx context.Context, workspaceID string, history []db.ChatMessage, text string, send func(string)) error

// AgentRunHandler is called when /run <name> is issued.
// Implemented in Phase 6 (AgentRunner).
type AgentRunHandler func(ctx context.Context, workspaceID, agentName string, send func(string)) error

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
	skillFlow  *skilldesigner.Flow
	memory     *memory.Store
	vault      *vault.Vault
	titleGen   chat.TitleGenerator // optional; auto-titles a chat from its first exchange

	// timeParserFallback is an optional LLM-backed time parser used when the
	// regex parser in reminder.ParseNaturalTime fails to understand the input.
	timeParserFallback reminder.TimeParserFunc

	// approval handles /pending, /approve, /reject. Nil when the install has no
	// approval service wired, in which case those commands say so.
	approval ApprovalService

	mu                 sync.Mutex
	challenges         map[string]*secretChallenge // workspaceID → pending master-password challenge
	pendingCancel      map[string]cancelKind       // workspaceID → which flow is waiting for a save/discard reply
	pendingReminderMsg map[string]string           // workspaceID → reminder message waiting for a "when" reply
}

// cancelKind records WHICH design flow asked for a save/discard choice, so the
// reply is resolved against that flow. Without it, replying "discard" to a skill
// cancel would dismiss the user's agent draft.
type cancelKind string

const (
	cancelAgent cancelKind = "agent"
	cancelSkill cancelKind = "skill"
)

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
		pendingCancel:      make(map[string]cancelKind),
		pendingReminderMsg: make(map[string]string),
	}
}

// WithSkillFlow attaches the conversational skill creator so /skill is available on
// chat platforms. Leaving it nil keeps /skill responding "not available" rather than
// panicking — the same contract designFlow has.
func (r *Router) WithSkillFlow(f *skilldesigner.Flow) *Router {
	r.skillFlow = f
	return r
}

// WithVault attaches the knowledge-base vault so chat attachments can be
// imported. Leaving it nil keeps attachments responding with a clear
// "unavailable" message rather than panicking — the same contract designFlow
// and skillFlow already have.
func (r *Router) WithVault(v *vault.Vault) *Router {
	r.vault = v
	return r
}

// WithTitleGenerator enables one-time content-based auto-titling of chats.
func (r *Router) WithTitleGenerator(g chat.TitleGenerator) *Router {
	r.titleGen = g
	return r
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
	// A file attachment is routed independently of command/text parsing — it
	// carries no meaningful Text to dispatch on (Telegram documents/photos
	// arrive with an empty or caption-only Text field).
	if msg.Attachment != nil {
		// A failed download is reported and the turn ends HERE — it must never
		// fall through to the text path. msg.Text is empty on a download failure
		// (there was nothing to caption it with beyond what the platform gave us),
		// and an empty text turn reaching handleText with a master-password
		// challenge pending would previously be read as an answer, silently
		// cancelling a security-sensitive pending flow because of an unrelated
		// network hiccup fetching a file.
		if msg.Attachment.Err != nil {
			name := msg.Attachment.Filename
			if strings.TrimSpace(name) == "" {
				name = "your file"
			}
			send(fmt.Sprintf("⚠️ I couldn't fetch **%s** — %s. Please try sending it again.", name, msg.Attachment.Err.Error()))
			return nil
		}
		reply, err := r.handleAttachment(msg.WorkspaceID, *msg.Attachment)
		if err != nil {
			send("⚠️ " + err.Error())
			return nil
		}
		send(reply)
		return nil
	}

	cmd, arg := ParseCommand(msg.Text)

	switch cmd {
	case "start":
		return r.handleStart(ctx, msg, send)
	case "help":
		send(helpText(msg.Platform))
		return nil
	case "agent":
		return r.handleAgent(ctx, msg, arg, send)
	case "skill":
		return r.handleSkill(ctx, msg, arg, send)
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
	case "pending":
		return r.handlePending(ctx, msg, send)
	case "approve":
		return r.handleApprove(ctx, msg, arg, send)
	case "reject":
		return r.handleReject(ctx, msg, arg, send)
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
	rows, _ := r.db.ListPlatformIdentities(msg.WorkspaceID, msg.Platform)
	if len(rows) > 0 {
		send("This bot is already linked to another account. Contact your administrator to reset the link.")
		return nil
	}

	if err := r.db.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID:             uuid.New().String(),
		WorkspaceID:    msg.WorkspaceID,
		Platform:       msg.Platform,
		PlatformUserID: msg.PlatformUserID,
	}); err != nil {
		return fmt.Errorf("link identity: %w", err)
	}

	// The platform's own label, so linking via Discord does not claim Telegram.
	label := msg.Platform
	if spec, ok := CredSpecFor(msg.Platform); ok && spec.Label != "" {
		label = spec.Label
	}

	w, err := r.db.GetWorkspaceByID(msg.WorkspaceID)
	if err != nil {
		send("Linked successfully! Send /help to get started.")
		return nil
	}

	send(fmt.Sprintf("Hi **%s**! Your %s account is now linked. Send /help to see what you can do.",
		w.Name, label))
	return nil
}

// handleAttachment converts a chat attachment and files it in the knowledge
// base, returning the message to send back. A non-nil error is a REFUSAL
// (empty/oversized/no vault wired) — the two are distinct because a refusal
// carries no filesystem detail and is always safe to show verbatim, while a
// deeper import failure is instead reported inline in the returned string
// (see below) so the caller doesn't have to re-derive wording per error kind.
func (r *Router) handleAttachment(workspaceID string, att Attachment) (string, error) {
	if len(att.Data) == 0 {
		return "", fmt.Errorf("attachment was empty")
	}
	if len(att.Data) > maxAttachmentBytes {
		return "", fmt.Errorf("attachment is too large (%d bytes; limit %d)", len(att.Data), maxAttachmentBytes)
	}
	if r.vault == nil {
		return "", fmt.Errorf("knowledge base is unavailable")
	}

	res, err := r.vault.ImportFile(workspaceID, vault.ImportInput{
		Data:     att.Data,
		Filename: att.Filename,
		// BuildPhase is always false here — a chat attachment is always a real
		// user action, never part of an agent/skill build.
		BuildPhase: false,
	})
	if err != nil {
		// Mirrors uploadErrStatus (web/api_kb.go) and the CLI bridge's
		// handleConvert (internal/vault/bridge.go): an unconvertible format or a
		// rejected destination is a property of the FILE the user sent — safe to
		// explain plainly. Anything else is a genuine server fault (disk I/O,
		// etc.) the user can't fix by resending a different file, so the raw
		// error — which can carry a filesystem path — goes to the log only.
		if errors.Is(err, convert.ErrUnsupportedFormat) || errors.Is(err, vault.ErrSystemDir) || errors.Is(err, vault.ErrEscapes) {
			return fmt.Sprintf("I couldn't read **%s** — %s", att.Filename, err.Error()), nil
		}
		slog.Warn("gateway: attachment import failed", "workspace", workspaceID, "filename", att.Filename, "err", err)
		return fmt.Sprintf("I couldn't save **%s** to your knowledge base — something went wrong on my end. Try again in a bit.", att.Filename), nil
	}

	msg := fmt.Sprintf("Saved **%s** to your knowledge base as `%s`.", att.Filename, res.NotePath)
	if len(res.Warnings) > 0 {
		msg += "\n\n_Note: " + strings.Join(res.Warnings, "; ") + "_"
	}
	return msg, nil
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
		agents, err := r.db.ListAgents(msg.WorkspaceID)
		if err != nil {
			return err
		}
		// An unfinished draft is invisible everywhere else in chat, which is how a
		// user ends up believing an agent exists that /run cannot find.
		draftLine := r.unfinishedDraftLine(msg.WorkspaceID)
		if len(agents) == 0 {
			if draftLine != "" {
				send("You have no saved agents yet.\n\n" + draftLine)
				return nil
			}
			send("You have no agents yet. Use /agent create <name> to build one.")
			return nil
		}
		var b strings.Builder
		b.WriteString("**Your agents:**\n")
		for _, a := range agents {
			status := "●"
			if !a.Active {
				status = "○"
			}
			b.WriteString(fmt.Sprintf("%s **%s**", status, a.Name))
			if a.Description != "" {
				b.WriteString(" — " + a.Description)
			}
			b.WriteByte('\n')
		}
		if draftLine != "" {
			b.WriteString("\n" + draftLine + "\n")
		}
		b.WriteString("\n_/run <name> to run · /agent create <name> to build a new one_")
		send(b.String())

	case "create":
		if r.designFlow == nil {
			send("Agent creation is not yet available.")
			return nil
		}
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /agent create <name>")
			return nil
		}
		if blocked := r.otherSessionBlock(msg.WorkspaceID, "agent"); blocked != "" {
			send(blocked)
			return nil
		}
		// If the user has a resumable draft, offer to continue it instead of
		// starting fresh. Subsequent text routes through Step → stepAwaitingResume.
		if draft := r.designFlow.HasDraft(msg.WorkspaceID); draft != nil {
			response, err := r.designFlow.OfferDraftResume(msg.WorkspaceID, name, draft)
			if err != nil {
				send(err.Error())
				return nil
			}
			send(response)
			return nil
		}
		response, err := r.designFlow.Start(msg.WorkspaceID, name)
		if err != nil {
			send(err.Error())
			return nil
		}
		send(response)

	case "edit":
		if r.designFlow == nil {
			send("Agent editing is not yet available.")
			return nil
		}
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /agent edit <name>")
			return nil
		}
		if blocked := r.otherSessionBlock(msg.WorkspaceID, "agent"); blocked != "" {
			send(blocked)
			return nil
		}
		agent, err := r.db.GetAgentByName(msg.WorkspaceID, name)
		if err != nil {
			send("Agent `" + name + "` not found.")
			return nil
		}
		response, err := r.designFlow.StartEdit(msg.WorkspaceID, agent.ID)
		if err != nil {
			send(err.Error())
			return nil
		}
		send(response)

	case "cancel":
		if r.designFlow == nil {
			send("Agent design is not yet available.")
			return nil
		}
		// Stop any in-flight generation / drop the active session now. The draft
		// (auto-saved on every turn) is preserved either way — the question below
		// is only whether to keep it or throw it away.
		r.designFlow.Cancel(msg.WorkspaceID)

		draft := r.designFlow.HasDraft(msg.WorkspaceID)
		if draft == nil {
			send("Agent design session cancelled. No draft to save.")
			return nil
		}
		// Ask the user to choose. The reply is handled in handleText (pendingCancel).
		r.mu.Lock()
		r.pendingCancel[msg.WorkspaceID] = cancelAgent
		r.mu.Unlock()
		send(fmt.Sprintf(
			"Agent design cancelled. You have an unfinished draft: **%s**\n\nReply `save` to keep it as a draft you can resume later, or `discard` to delete it.",
			draft.AgentName,
		))

	default:
		send("Usage: /agent list · /agent create <name> · /agent edit <name> · /agent cancel")
	}
	return nil
}

// otherSessionBlock reports a refusal message when the *other* conversational design
// flow already owns this workspace. At most one design session may be active at a
// time: both consume plain text from the same stream, so two live sessions would
// leave the user with no way to say which one a message is for.
// Returns "" when nothing is in the way.
func (r *Router) otherSessionBlock(workspaceID, starting string) string {
	if starting != "agent" && r.designFlow != nil {
		if s := r.designFlow.GetSession(workspaceID); s != nil {
			return fmt.Sprintf(
				"You're in the middle of building the agent **%s**. Finish it, or send `/agent cancel`, then try again.",
				s.AgentName)
		}
	}
	if starting != "skill" && r.skillFlow != nil {
		if s := r.skillFlow.GetSession(workspaceID); s != nil {
			return fmt.Sprintf(
				"You're in the middle of building the skill **%s**. Finish it, or send `/skill cancel`, then try again.",
				s.SkillName)
		}
	}
	return ""
}

func (r *Router) handleSkill(ctx context.Context, msg Message, arg string, send func(string)) error {
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
		var b strings.Builder
		b.WriteString("**Your skills:**\n")
		userSkills, err := r.db.ListSkills(msg.WorkspaceID)
		if err != nil {
			return err
		}
		if len(userSkills) == 0 {
			b.WriteString("_none yet_\n")
		}
		for _, s := range userSkills {
			b.WriteString(fmt.Sprintf("• **%s**", s.Name))
			if s.Description != "" {
				b.WriteString(" — " + s.Description)
			}
			b.WriteByte('\n')
		}
		// Core skills are always available to every agent, so listing only the
		// user's own would misrepresent what an agent can actually reach.
		b.WriteString("\n**Built-in skills** _(always available)_:\n")
		for _, s := range skilllibrary.LoadBundled() {
			b.WriteString(fmt.Sprintf("• %s\n", s.Name))
		}
		b.WriteString("\n_/skill create <name> to build a new one_")
		send(b.String())

	case "create":
		if r.skillFlow == nil {
			send("Skill creation is not yet available.")
			return nil
		}
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /skill create <name>")
			return nil
		}
		if blocked := r.otherSessionBlock(msg.WorkspaceID, "skill"); blocked != "" {
			send(blocked)
			return nil
		}
		if draft := r.skillFlow.HasDraft(msg.WorkspaceID); draft != nil {
			response, err := r.skillFlow.OfferDraftResume(msg.WorkspaceID, name, draft)
			if err != nil {
				send(err.Error())
				return nil
			}
			send(response)
			return nil
		}
		response, err := r.skillFlow.Start(msg.WorkspaceID, name)
		if err != nil {
			send(err.Error())
			return nil
		}
		send(response)

	case "cancel":
		if r.skillFlow == nil {
			send("Skill design is not yet available.")
			return nil
		}
		r.skillFlow.Cancel(msg.WorkspaceID)

		draft := r.skillFlow.HasDraft(msg.WorkspaceID)
		if draft == nil {
			send("Skill design session cancelled. No draft to save.")
			return nil
		}
		r.mu.Lock()
		r.pendingCancel[msg.WorkspaceID] = cancelSkill
		r.mu.Unlock()
		send(fmt.Sprintf(
			"Skill design cancelled. You have an unfinished draft: **%s**\n\nReply `save` to keep it as a draft you can resume later, or `discard` to delete it.",
			draft.SkillName,
		))

	default:
		send("Usage: /skill list · /skill create <name> · /skill cancel")
	}
	return nil
}

func (r *Router) handleRun(ctx context.Context, msg Message, arg string, send func(string)) error {
	name := strings.TrimSpace(arg)
	if name == "" {
		send("Usage: /run <agent_name>")
		return nil
	}

	// Distinguish "no such agent" from "a build exists but was never saved".
	//
	// A build that never reached approval leaves agents/draft_<slug>/ on disk with
	// no agents row, so /run answered a flat `agent "x" not found` immediately
	// after the designer said it had built one. Two true statements that read as a
	// contradiction, with nothing pointing at the way out.
	//
	// Checked BEFORE the execution-wiring guard: "you never saved it" is the more
	// useful answer either way, and it is true whether or not runs are available.
	if hint := r.unsavedDraftHint(msg.WorkspaceID, name); hint != "" {
		send(hint)
		return nil
	}

	if r.onAgentRun == nil {
		send("Agent execution is not yet available. Check back soon!")
		return nil
	}

	send(fmt.Sprintf("Running agent **%s**...", name))
	return r.onAgentRun(ctx, msg.WorkspaceID, name, send)
}

// unfinishedDraftLine renders the workspace's in-progress draft as a list
// section, or "" when there is none. There is at most one draft per workspace
// (agent_drafts holds a single row per workspace), so this is one line, not a
// list.
func (r *Router) unfinishedDraftLine(workspaceID string) string {
	if r.designFlow == nil {
		return ""
	}
	draft := r.designFlow.HasDraft(workspaceID)
	if draft == nil || strings.TrimSpace(draft.AgentName) == "" {
		return ""
	}
	verb := "create"
	if draft.IsEdit {
		verb = "edit"
	}
	return fmt.Sprintf("**Unfinished:** ○ %s — not saved yet. `/agent %s %s` to pick it up.",
		draft.AgentName, verb, draft.AgentName)
}

// unsavedDraftHint returns a user-facing explanation when `name` matches an
// unfinished draft rather than a saved agent, or "" when the normal run path
// should proceed.
//
// Deliberately conservative: it returns "" whenever it cannot PROVE the agent is
// absent and a matching draft is present, so a DB hiccup degrades to today's
// behaviour instead of refusing to run a real agent.
func (r *Router) unsavedDraftHint(workspaceID, name string) string {
	if r.db == nil || r.designFlow == nil {
		return ""
	}
	if agent, err := r.db.GetAgentByName(workspaceID, name); err == nil && agent != nil {
		return "" // a real agent — run it
	}
	draft := r.designFlow.HasDraft(workspaceID)
	if draft == nil || !strings.EqualFold(strings.TrimSpace(draft.AgentName), name) {
		return ""
	}
	if draft.IsEdit {
		return fmt.Sprintf("**%s** has an unfinished edit. Send `/agent edit %s` to pick it up.", name, name)
	}
	return fmt.Sprintf(
		"**%s** was built but never saved, so there's nothing to run yet. "+
			"Send `/agent create %s` to pick the draft back up and approve it.", name, name)
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
		names, err := r.db.ListSecretNames(msg.WorkspaceID)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			send("You have no secrets stored yet. Add them at the web dashboard → Secrets.")
			return nil
		}
		var b strings.Builder
		b.WriteString("**Stored secrets:**\n")
		for _, n := range names {
			b.WriteString("• `" + n + "`\n")
		}
		b.WriteString("\n_/secret show <name> · /secret delete <name>_")
		send(b.String())

	case "show", "get":
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /secret show <name>")
			return nil
		}
		names, _ := r.db.ListSecretNames(msg.WorkspaceID)
		found := false
		for _, n := range names {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			send("Secret `" + name + "` not found.")
			return nil
		}
		r.mu.Lock()
		r.challenges[msg.WorkspaceID] = &secretChallenge{action: "show", name: name}
		r.mu.Unlock()
		send("🔐 Enter your master password to reveal `" + name + "`:\n\n_⚠️ The value will appear in this chat. Telegram stores chat history._")

	case "delete":
		name := strings.TrimSpace(rest)
		if name == "" {
			send("Usage: /secret delete <name>")
			return nil
		}
		names, _ := r.db.ListSecretNames(msg.WorkspaceID)
		found := false
		for _, n := range names {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			send("Secret `" + name + "` not found.")
			return nil
		}
		r.mu.Lock()
		r.challenges[msg.WorkspaceID] = &secretChallenge{action: "delete", name: name}
		r.mu.Unlock()
		send("🔐 Enter your master password to confirm deletion of `" + name + "`:")

	default:
		send("Usage: /secret list · /secret show <name> · /secret delete <name>\n\n_To add secrets, use the web dashboard → Secrets_")
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
		// Unreachable through handleText today (it only calls in here once it has
		// already confirmed non-empty text) — kept as a second layer of defense
		// in depth rather than trusting that invariant to hold forever.
		send("Master password cannot be empty. Challenge cancelled.")
		return nil
	}

	u, err := r.db.GetWorkspaceByID(msg.WorkspaceID)
	if err != nil {
		send("Error: could not load user.")
		return err
	}
	if u.SecretsSalt == "" {
		send("Account setup incomplete — no master password configured.")
		return nil
	}

	svc := secrets.New(r.db, msg.WorkspaceID, masterPw, u.SecretsSalt)

	switch ch.action {
	case "show":
		val, err := svc.Get(ctx, ch.name)
		if errors.Is(err, secrets.ErrWrongPassword) {
			send("❌ Wrong master password.")
			return nil
		}
		if errors.Is(err, secrets.ErrNotFound) {
			send("Secret `" + ch.name + "` not found.")
			return nil
		}
		if err != nil {
			send("Error retrieving secret: " + err.Error())
			return nil
		}
		sendAutoDelete("🔑 `" + ch.name + "`:\n`" + val + "`\n\n_⏱ This message will be deleted in 30 seconds_")

	case "delete":
		if _, err := svc.Get(ctx, ch.name); err != nil {
			if errors.Is(err, secrets.ErrWrongPassword) {
				send("❌ Wrong master password — secret not deleted.")
				return nil
			}
		}
		if err := r.db.DeleteSecret(msg.WorkspaceID, ch.name); err != nil {
			send("Error deleting secret: " + err.Error())
			return nil
		}
		send("🗑 Secret `" + ch.name + "` deleted.")
	}
	return nil
}

// resolveCancelChoice handles the user's reply to the /agent cancel save/discard
// prompt. "discard" deletes the draft (and the orphaned pre-approved agent
// directory if generation had reached verifying); anything else keeps the draft
// as resumable. The active session was already cancelled when /agent cancel ran.
// resolveCancelChoice applies a save/discard reply to the flow that asked for it.
// kind is what makes that safe: a user can hold an agent draft and a skill draft at
// the same time, and dismissing the wrong one destroys unfinished work.
func (r *Router) resolveCancelChoice(msg Message, kind cancelKind, send func(string)) error {
	lower := strings.ToLower(strings.TrimSpace(msg.Text))
	discard := lower == "discard" || lower == "delete" || lower == "drop"

	if kind == cancelSkill {
		if !discard {
			send("✅ Draft saved. Use `/skill create <name>` to resume it later.")
			return nil
		}
		name := ""
		if d := r.skillFlow.HasDraft(msg.WorkspaceID); d != nil {
			name = d.SkillName
		}
		_ = r.skillFlow.DismissDraft(msg.WorkspaceID)
		if name != "" {
			send(fmt.Sprintf("🗑 Draft **%s** discarded.", name))
		} else {
			send("🗑 Draft discarded.")
		}
		return nil
	}

	if discard {
		name := ""
		if d := r.designFlow.HasDraft(msg.WorkspaceID); d != nil {
			name = d.AgentName
		}
		_ = r.designFlow.DismissDraft(msg.WorkspaceID)
		if name != "" {
			send(fmt.Sprintf("🗑 Draft **%s** discarded.", name))
		} else {
			send("🗑 Draft discarded.")
		}
		return nil
	}

	// "save" (or anything else) → keep the draft. It stays resumable via
	// /agent create <name> (which will offer to resume) or the web dashboard.
	send("✅ Draft saved. Use `/agent create <name>` to resume it later.")
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
		send("Usage: /remind <when> to <message>\nExamples:\n• /remind in 10 minutes to check the oven\n• /remind tomorrow at 3pm to call doctor\n• /remind next Tuesday to pay bills\n• /remind next Friday evening write note about bitcoin price\n• /remind 30m old format still works")
		return nil
	}

	// Subcommands are matched EXACTLY, never by prefix: "list" and "delete" are
	// ordinary English words, and "/remind in 10 minutes to list the groceries"
	// must keep creating a reminder. Anything that isn't an exact match falls
	// through to the creation path untouched.
	if fields := strings.Fields(arg); len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "list":
			if len(fields) == 1 {
				return r.listReminders(msg.WorkspaceID, send)
			}
		case "delete":
			if len(fields) == 2 {
				if n, err := strconv.Atoi(fields[1]); err == nil && n >= 1 {
					return r.deleteReminderByIndex(msg.WorkspaceID, n, send)
				}
			}
		}
	}

	now := time.Now()
	loc := profile.LoadLocation(r.db, msg.WorkspaceID)

	// Time + message extraction is the shared, pure resolver used by both the
	// web and Telegram surfaces (strip filler → " to " split + regex → LLM →
	// legacy duration). The stateful "understood the message, now reply with a
	// time" follow-up stays here in the router — ParseReminderText is pure.
	remindAt, message, err := reminder.ParseReminderText(ctx, arg, now, loc, r.timeParserFallback, msg.WorkspaceID)
	if err != nil {
		send("Couldn't understand that time. Try:\n• /remind in 10 minutes to check oven\n• /remind next Tuesday to call doctor\n• /remind next Friday evening write note about bitcoin\n• /remind 30m old format\n• /remind to write a note _(I'll ask when)_")
		return nil
	}

	if remindAt.IsZero() {
		// Understood the message but found no time — ask for one, remembering
		// the message so a bare time reply resumes it.
		if message == "" {
			message = arg
		}
		r.mu.Lock()
		r.pendingReminderMsg[msg.WorkspaceID] = message
		r.mu.Unlock()
		send(fmt.Sprintf("⏰ When should I remind you about **%s**?\nReply with a time, e.g. 'in 10 minutes', 'tomorrow at 9am', 'next Friday evening'", message))
		return nil
	}

	if message == "" {
		send("Please include a reminder message. Example: /remind in 10 minutes to check the oven")
		return nil
	}

	return r.createReminder(ctx, msg.WorkspaceID, message, remindAt, send)
}

// listReminders renders the workspace's pending reminders, numbered. The numbers
// are what /remind delete <n> indexes, so both use db.ListReminders' ordering.
// Times render in the workspace's timezone — a UTC listing is wrong for every
// install that isn't on UTC.
func (r *Router) listReminders(workspaceID string, send func(string)) error {
	items, err := r.db.ListReminders(workspaceID)
	if err != nil {
		return fmt.Errorf("list reminders: %w", err)
	}
	if len(items) == 0 {
		send("No reminders set. Use /remind <when> to <message> to add one.")
		return nil
	}

	loc := profile.LoadLocation(r.db, workspaceID)
	var b strings.Builder
	b.WriteString("**Your reminders:**\n")
	for i, rm := range items {
		b.WriteString(fmt.Sprintf("%d. _%s_ — `%s`",
			i+1, rm.Message, rm.RemindAt.In(loc).Format("Mon Jan 2, 15:04")))
		if rm.Sent {
			b.WriteString(" ✓")
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n_/remind delete <number> to remove one_")
	send(b.String())
	return nil
}

func (r *Router) deleteReminderByIndex(workspaceID string, n int, send func(string)) error {
	items, err := r.db.ListReminders(workspaceID)
	if err != nil {
		return fmt.Errorf("list reminders: %w", err)
	}
	if n > len(items) {
		send(fmt.Sprintf("No reminder #%d. You have %d.", n, len(items)))
		return nil
	}
	target := items[n-1]
	if err := r.db.DeleteReminder(target.ID); err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}
	send(fmt.Sprintf("🗑 Deleted reminder #%d: _%s_", n, target.Message))
	return nil
}

func (r *Router) createReminder(ctx context.Context, workspaceID, message string, remindAt time.Time, send func(string)) error {
	rm := &db.Reminder{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Message:     message,
		RemindAt:    remindAt,
	}
	if err := r.db.CreateReminder(rm); err != nil {
		return fmt.Errorf("create reminder: %w", err)
	}
	when := remindAt.Format("Jan 2 at 15:04")
	send(fmt.Sprintf("⏰ Reminder set for **%s**: _%s_", when, message))
	return nil
}

// resolvePendingReminder handles the user's reply to a "when?" prompt for a reminder
// that had no time expression. The message is already known; this call parses the time.
func (r *Router) resolvePendingReminder(ctx context.Context, msg Message, reminderMsg string, send func(string)) error {
	now := time.Now()
	loc := profile.LoadLocation(r.db, msg.WorkspaceID)

	// Try standard regex first.
	if t, err := reminder.ParseNaturalTime(msg.Text, now, loc); err == nil {
		return r.createReminder(ctx, msg.WorkspaceID, reminderMsg, t, send)
	}

	// Try LLM fallback.
	if r.timeParserFallback != nil {
		when, _, err := r.timeParserFallback(ctx, msg.WorkspaceID, msg.Text, now, loc)
		if err == nil && !when.IsZero() {
			return r.createReminder(ctx, msg.WorkspaceID, reminderMsg, when, send)
		}
	}

	// Re-queue the pending message and ask again.
	r.mu.Lock()
	r.pendingReminderMsg[msg.WorkspaceID] = reminderMsg
	r.mu.Unlock()
	send("Couldn't understand that time. Please try again, e.g. 'tomorrow at 9am' or 'next Friday'")
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
	//
	// Peek (don't consume yet) — an inbound message with no usable text is NOT a
	// genuine reply to the challenge, even if one is pending. This is defense in
	// depth independent of the attachment-error short-circuit in Handle(): should
	// an empty-text message ever reach here some other way (a sticker, a voice
	// note, an adapter bug), it must not be read as an answer and silently cancel
	// a security-sensitive pending flow. Only a real, non-empty text reply
	// resolves — and consumes — the challenge.
	r.mu.Lock()
	challenge, hasPending := r.challenges[msg.WorkspaceID]
	r.mu.Unlock()
	if hasPending {
		if strings.TrimSpace(msg.Text) == "" {
			send("🔐 Still waiting on your master password — reply with the password itself, or send the /secret command again to cancel.")
			return nil
		}
		r.mu.Lock()
		delete(r.challenges, msg.WorkspaceID)
		r.mu.Unlock()
		return r.resolveMasterPwChallenge(ctx, msg, challenge, send, deleteIncoming, sendAutoDelete)
	}

	// Check for a pending /agent cancel save/discard choice. This runs before the
	// design-session routing because Cancel() already dropped any active session.
	r.mu.Lock()
	cancelFlow, hasCancelChoice := r.pendingCancel[msg.WorkspaceID]
	if hasCancelChoice {
		delete(r.pendingCancel, msg.WorkspaceID)
	}
	r.mu.Unlock()
	if hasCancelChoice {
		return r.resolveCancelChoice(msg, cancelFlow, send)
	}

	// Check for a pending reminder "when?" prompt — the user is supplying a time
	// for a reminder message that had no time expression.
	r.mu.Lock()
	pendingMsg, hasPendingReminder := r.pendingReminderMsg[msg.WorkspaceID]
	if hasPendingReminder {
		delete(r.pendingReminderMsg, msg.WorkspaceID)
	}
	r.mu.Unlock()
	if hasPendingReminder {
		return r.resolvePendingReminder(ctx, msg, pendingMsg, send)
	}

	// If the user has an active design session, route all text there.
	if r.designFlow != nil && r.designFlow.GetSession(msg.WorkspaceID) != nil {
		// Reject a turn while a build is running. The web surface has always done
		// this (handleDesignChat); chat never did, so a message sent mid-build
		// stepped the FSM concurrently with the build still writing to the same
		// session — which is how a live build ended up answered as ordinary chat.
		if r.designFlow.IsGenerating(msg.WorkspaceID) {
			send("⏳ Still building your agent — I'll send the result here as soon as it's done.")
			return nil
		}
		sess := r.designFlow.GetSession(msg.WorkspaceID)
		// Register the progress callback for ANY message while in Designing, so the
		// Telegram placeholder streams milestones for every build trigger — not only
		// "approve" but also "keep going", "try again", and "keep it as-is" after a
		// block. Harmless when the message doesn't launch a build (never called).
		if sess != nil && sess.State == agentdesigner.StateDesigning && sendProgress != nil {
			r.designFlow.SetProgressHandler(msg.WorkspaceID, sendProgress)
		}
		response, _, _, err := r.designFlow.Step(ctx, msg.WorkspaceID, msg.Text)
		if err != nil {
			send(friendlyDesignError("agent", err))
			return nil
		}
		send(response)
		return nil
	}

	// Same for an active skill design session. The two are mutually exclusive (see
	// otherSessionBlock), so the order of these two branches never decides anything.
	if r.skillFlow != nil && r.skillFlow.GetSession(msg.WorkspaceID) != nil {
		if r.skillFlow.IsGenerating(msg.WorkspaceID) {
			send("⏳ Still building your skill — I'll send the result here as soon as it's done.")
			return nil
		}
		sess := r.skillFlow.GetSession(msg.WorkspaceID)
		if sess != nil && sess.State == skilldesigner.StateDesigning && sendProgress != nil {
			r.skillFlow.SetProgressHandler(msg.WorkspaceID, sendProgress)
		}
		response, _, _, err := r.skillFlow.Step(ctx, msg.WorkspaceID, msg.Text)
		if err != nil {
			send(friendlyDesignError("skill", err))
			return nil
		}
		send(response)
		return nil
	}

	if r.onText == nil {
		send("Send /agent create <name> to build an agent, or /help for all commands.")
		return nil
	}

	// Look up an active chat for this user+platform and load its history.
	var history []db.ChatMessage
	var activeChat *db.Chat
	if c, err := r.db.GetActiveChatForPlatform(msg.WorkspaceID, msg.Platform); err == nil && c != nil {
		activeChat = c
		history, _ = r.db.ListChatMessages(c.ID)
	}

	// Intercept send so we can capture the assistant reply for chat storage.
	var assistantReply string
	wrappedSend := func(text string) {
		assistantReply = text
		send(text)
	}

	if err := r.onText(ctx, msg.WorkspaceID, history, msg.Text, wrappedSend); err != nil {
		return err
	}

	// Persist messages and touch the chat.
	if activeChat != nil && assistantReply != "" {
		_ = r.db.AddChatMessage(activeChat.ID, "user", msg.Text)
		_ = r.db.AddChatMessage(activeChat.ID, "assistant", assistantReply)
		_ = r.db.TouchChat(activeChat.ID)
		chat.MaybeAutoTitle(r.db, r.titleGen, activeChat, msg.Text, assistantReply)
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
		if cur, err := r.db.GetActiveChatForPlatform(msg.WorkspaceID, msg.Platform); err == nil && cur != nil {
			_ = r.db.StopChat(cur.ID)
		}
		c := &db.Chat{
			ID:          uuid.New().String(),
			WorkspaceID: msg.WorkspaceID,
			Name:        rest,
			Platform:    msg.Platform,
			Active:      true,
		}
		if err := r.db.CreateChat(c); err != nil {
			return fmt.Errorf("create chat: %w", err)
		}
		label := rest
		if label == "" {
			label = c.ID[:8]
		}
		send(fmt.Sprintf("💬 Chat **%s** started (`%s`). Send /chat stop to end it.", label, c.ID[:8]))

	case "list":
		chats, err := r.db.ListChats(msg.WorkspaceID)
		if err != nil {
			return err
		}
		if len(chats) == 0 {
			send("No chats yet. Use /chat start to begin one.")
			return nil
		}
		var b strings.Builder
		b.WriteString("**Chats:**\n")
		for _, c := range chats {
			label := c.Name
			if label == "" {
				label = c.ID[:8]
			}
			status := "🟢"
			if !c.Active {
				status = "⚫"
			}
			b.WriteString(fmt.Sprintf("%s **%s** `%s` — %s\n",
				status,
				label,
				c.ID[:8],
				c.LastSeen.Format("Jan 2 15:04")))
		}
		b.WriteString("\n_/chat resume <id> · /chat delete <id> · /chat stop_")
		send(b.String())

	case "stop":
		if cur, err := r.db.GetActiveChatForPlatform(msg.WorkspaceID, msg.Platform); err == nil && cur != nil {
			_ = r.db.StopChat(cur.ID)
			label := cur.Name
			if label == "" {
				label = cur.ID[:8]
			}
			send(fmt.Sprintf("Chat **%s** stopped.", label))
		} else {
			send("No active chat to stop.")
		}

	case "resume":
		if rest == "" {
			send("Usage: /chat resume <id>")
			return nil
		}
		c, err := r.db.FindChatByPrefix(msg.WorkspaceID, rest)
		if err != nil {
			send("Chat not found. Use /chat list to see your chats.")
			return nil
		}
		// Stop any currently active chat first.
		if cur, err := r.db.GetActiveChatForPlatform(msg.WorkspaceID, msg.Platform); err == nil && cur != nil && cur.ID != c.ID {
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
		send(fmt.Sprintf("💬 Chat **%s** resumed (%d messages). Continue chatting.", label, len(history)))

	case "delete":
		if rest == "" {
			send("Usage: /chat delete <id>")
			return nil
		}
		c, err := r.db.FindChatByPrefix(msg.WorkspaceID, rest)
		if err != nil {
			send("Chat not found. Use /chat list to see your chats.")
			return nil
		}
		label := c.Name
		if label == "" {
			label = c.ID[:8]
		}
		if err := r.db.DeleteChat(c.ID); err != nil {
			return fmt.Errorf("delete chat: %w", err)
		}
		// Mirrors the web handler's cleanup: deleting over chat must remove the
		// reflected transcript from the knowledge base too, or the same chat
		// counts as deleted on one surface and present on the other.
		if r.vault != nil {
			_ = r.vault.Reflector().UnreflectChat(msg.WorkspaceID, c.ID)
		}
		send(fmt.Sprintf("🗑 Chat **%s** deleted.", label))

	default:
		send("Usage: /chat start [name] · /chat list · /chat stop · /chat resume <id> · /chat delete <id>")
	}
	return nil
}

func (r *Router) handleMemory(ctx context.Context, msg Message, arg string, send func(string)) error {
	if r.memory == nil {
		send("Memory is not available.")
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
			send("Usage: /memory add <text>")
			return nil
		}
		if _, err := r.memory.Append(msg.WorkspaceID, text); err != nil {
			return fmt.Errorf("append memory: %w", err)
		}
		send("✅ Saved: _" + text + "_")

	case "delete":
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil || n < 1 {
			send("Usage: /memory delete <number> — use /memory list to see numbers")
			return nil
		}
		entries, err := r.memory.List(msg.WorkspaceID)
		if err != nil {
			return fmt.Errorf("list memory: %w", err)
		}
		if n > len(entries) {
			send(fmt.Sprintf("No entry #%d. You have %d entries.", n, len(entries)))
			return nil
		}
		if err := r.memory.Delete(msg.WorkspaceID, entries[n-1].ID); err != nil {
			return fmt.Errorf("delete memory: %w", err)
		}
		send(fmt.Sprintf("🗑 Deleted entry #%d: _%s_", n, entries[n-1].Content))

	default: // "list" or ""
		entries, err := r.memory.List(msg.WorkspaceID)
		if err != nil {
			return fmt.Errorf("list memory: %w", err)
		}
		if len(entries) == 0 {
			send("No memory entries yet. Use /memory add <text> to save one.")
			return nil
		}
		var b strings.Builder
		b.WriteString("**Memory entries:**\n")
		for i, e := range entries {
			b.WriteString(fmt.Sprintf("%d. _%s_ — `%s`\n",
				i+1,
				e.Content,
				e.CreatedAt.Format("Jan 2")))
		}
		b.WriteString("\n_/memory add <text> · /memory delete <n>_")
		send(b.String())
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// helpText renders /help. All three platforms (Telegram, Discord, Slack) now
// implement attachment download — Slack's Socket Mode handler imports the
// first file off a file_share message via SlackGateway — so the file-upload
// line applies uniformly and is no longer conditioned on platform.
func helpText(platform string) string {
	fileLine := "\nSend a file (document/photo) to save it to your knowledge base.\n"
	return `**Rookery — Commands**

/agent list — list your agents
/agent create <name> — build a new agent with AI wizard
/agent edit <name> — change an existing agent with AI wizard
/agent cancel — cancel active agent creation or edit
/skill list — list your skills and the built-in ones
/skill create <name> — build a new skill with AI wizard
/skill cancel — cancel active skill creation
/run <name> — run an agent
/secret list — list stored secret names
/secret show <name> — reveal a secret value (requires master password)
/secret delete <name> — delete a secret (requires master password)
/remind <when> to <message> — set a reminder (e.g. /remind in 10 minutes to check oven)
/remind list — list your reminders
/remind delete <n> — delete a reminder by number
/chat start [name] — start a chat (saves history)
/chat list — list all chats with IDs
/chat stop — stop current chat
/chat resume <id> — resume a previous chat
/chat delete <id> — delete a chat and its history
/memory list — list saved memory entries
/memory add <text> — save a new memory entry
/memory delete <n> — delete entry by number
/pending — list posts waiting for your approval
/approve <id> — publish a waiting post
/reject <id> — decline a waiting post
/help — this message
` + fileLine + `
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
