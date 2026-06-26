package agentdesigner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/robfig/cron/v3"
)

// DesignState is the current step in the conversational agent creation wizard.
type DesignState int

const (
	StateIdle           DesignState = iota
	StateAwaitingResume             // a draft exists; waiting for user to pick "resume" or "new"
	StateDescribing                 // Telegram: waiting for description after /agent create <name>
	StateDesigning                  // free-form Q&A until user says "approve"
	StateVerifying                  // test run shown; waiting for user to confirm or request changes
	StateDone
)

func (s DesignState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateAwaitingResume:
		return "awaiting_resume"
	case StateDescribing:
		return "describing"
	case StateDesigning:
		return "designing"
	case StateVerifying:
		return "verifying"
	case StateDone:
		return "done"
	}
	return "unknown"
}

// DesignSession holds all state for one in-progress agent creation or edit.
type DesignSession struct {
	UserID             string
	AgentID            string
	AgentName          string
	State              DesignState
	History            []db.ChatMessage // full conversation fed to coder on every turn
	Skills             []string         // installed skill names, loaded once on Start
	ConnectedPlatforms []string         // e.g. ["telegram"] — loaded from platform_connections
	UserProfile        string           // rendered "[User profile]" block, loaded once on session start
	UserMemory         string           // bullet list of saved memory entries, loaded once on session start
	CreatedAt          time.Time

	// ComposioEnabled is true when the user has stored a COMPOSIO_API_KEY secret,
	// indicating their agents can use Composio to access external services.
	ComposioEnabled bool

	// IsEdit distinguishes an edit-of-existing-agent session from a fresh create.
	// AgentID is the *existing* agent's ID (not a freshly minted one) when true.
	IsEdit bool

	// ExistingAgentMD holds the live agent's AGENT.md, reconciled with the real DB
	// schedule, as of session start. Used to seed the staging workspace during
	// generation. Empty for create sessions.
	ExistingAgentMD string

	// ExistingTools holds the live agent's tool scripts (relpath→content) as of
	// session start, so the edit conversation can reason about the actual code.
	// Empty for create sessions.
	ExistingTools map[string]string

	// Set after generation; cleared on finalize or when user requests changes.
	PendingAgentMD string
	PendingTools   map[string]string

	// pendingName holds the agent name the user originally typed in the
	// StateAwaitingResume flow, so the "new" branch can start a fresh create
	// session with that name once the draft is dismissed.
	pendingName string

	// Generation cancellation and progress. All fields are set in runGeneration
	// and cleared / closed in Cancel.
	cancelGenerate context.CancelFunc // cancels the in-flight coder.Generate() call
	progressFunc   func(string)       // Telegram: edits the placeholder message mid-run
	progressCh     chan string        // Web SSE: buffered milestone channel
}

type dbDesignStore interface {
	ListSkills(userID string) ([]*db.Skill, error)
	ListUserPlatformConnections(userID string) ([]*db.PlatformConnection, error)
	UpsertAgentSchedule(s *db.AgentSchedule) error
	GetAgent(id string) (*db.Agent, error)
	GetScheduleForAgent(agentID string) (*db.AgentSchedule, error)
	DeleteAgentSchedule(agentID string) error
	GetSetting(userID, key string) (string, error)
	SecretExists(userID, name string) (bool, error)

	UpsertAgentDraft(d *db.AgentDraft) error
	GetAgentDraft(userID string) (*db.AgentDraft, error)
	DeleteAgentDraft(userID string) error
}

// memoryStore is satisfied by *memory.Store — kept local to avoid the import.
type memoryStore interface {
	ContextString(userID string) (string, error)
}

// kbLister enumerates the user's knowledge-base note paths (vault-relative) so
// the designer can be shown what notes already exist. Satisfied by *vault.Vault.
type kbLister interface {
	NotePaths(userID string) []string
}

// loadKBManifest returns a rendered bullet list of the user's existing note paths
// (capped), or "" if no lister is attached or the knowledge base is empty.
func (f *Flow) loadKBManifest(userID string) string {
	if f.kb == nil {
		return ""
	}
	paths := f.kb.NotePaths(userID)
	if len(paths) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, p := range paths {
		if i >= 30 {
			fmt.Fprintf(&sb, "- …and %d more\n", len(paths)-30)
			break
		}
		sb.WriteString("- ")
		sb.WriteString(p)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Flow manages per-user design sessions and drives the FSM.
// It is safe for concurrent use.
type Flow struct {
	mu       sync.Mutex
	sessions map[string]*DesignSession // keyed by userID

	coderFor      func(userID string) *coder.Coder
	designer      *AgentDesigner
	db            dbDesignStore
	memStore      memoryStore // optional; nil = no memory injected
	kb            kbLister     // optional; nil = no KB manifest injected
	secretsLoader func(ctx context.Context, userID string) (map[string]string, error)
}

// NewFlow creates a Flow. coderResolver maps a userID to the right coder.
func NewFlow(coderResolver func(userID string) *coder.Coder, designer *AgentDesigner) *Flow {
	return &Flow{
		sessions: make(map[string]*DesignSession),
		coderFor: coderResolver,
		designer: designer,
	}
}

// WithDB attaches a database handle so the Flow can list skills, query connected platforms,
// and create schedules during agent creation.
func (f *Flow) WithDB(database dbDesignStore) *Flow {
	f.db = database
	return f
}

// WithMemory attaches a memory store so saved user facts are injected into design sessions.
func (f *Flow) WithMemory(m memoryStore) *Flow {
	f.memStore = m
	return f
}

// WithKBLister attaches a knowledge-base enumerator so the designer is told what
// notes the user already has (a manifest of note paths is injected into each
// design turn). nil = no manifest.
func (f *Flow) WithKBLister(k kbLister) *Flow {
	f.kb = k
	return f
}

// WithSecretsLoader attaches a loader that decrypts all secrets for a user.
// The loader is called during agent generation to inject secrets like COMPOSIO_API_KEY
// into the coder subprocess so real API calls can be made during validation.
func (f *Flow) WithSecretsLoader(fn func(ctx context.Context, userID string) (map[string]string, error)) *Flow {
	f.secretsLoader = fn
	return f
}

// Start creates a new Telegram design session for userID.
// Returns the opening prompt asking for a description.
func (f *Flow) Start(userID, agentName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.sessions[userID]; exists {
		return "", fmt.Errorf("you already have an active design session; send /agent cancel to start over")
	}

	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	userProfile := f.loadUserProfile(userID)
	userMemory := f.loadUserMemory(userID)
	composioEnabled := f.loadComposioEnabled(userID)
	f.sessions[userID] = &DesignSession{
		UserID:             userID,
		AgentID:            uuid.New().String(),
		AgentName:          agentName,
		State:              StateDescribing,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
		UserMemory:         userMemory,
		ComposioEnabled:    composioEnabled,
		CreatedAt:          time.Now(),
	}

	return fmt.Sprintf(
		"Starting agent \"%s\".\n\nDescribe what this agent should do. Be specific: what should it monitor or fetch, what actions should it take, and what should it output?",
		agentName,
	), nil
}

// StartDesign is the web path: creates a session already in StateDesigning
// with the user's first message and returns the coder's first response.
func (f *Flow) StartDesign(ctx context.Context, userID, agentName, firstMessage string) (string, error) {
	f.mu.Lock()

	if _, exists := f.sessions[userID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("design session already active; cancel it first")
	}

	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	userProfile := f.loadUserProfile(userID)
	userMemory := f.loadUserMemory(userID)
	composioEnabled := f.loadComposioEnabled(userID)
	sess := &DesignSession{
		UserID:             userID,
		AgentID:            uuid.New().String(),
		AgentName:          agentName,
		State:              StateDesigning,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
		UserMemory:         userMemory,
		ComposioEnabled:    composioEnabled,
		CreatedAt:          time.Now(),
	}
	f.sessions[userID] = sess
	f.mu.Unlock()

	return f.callCoder(ctx, userID, firstMessage)
}

// StartEdit creates a new Telegram edit session for an existing agentID.
// Ownership of agentID is assumed pre-checked by the caller (same pattern as the
// other agent handlers, e.g. the Telegram /agent and web delete/run endpoints).
// Returns the opening prompt summarizing current behavior and asking what to change.
func (f *Flow) StartEdit(userID, agentID string) (string, error) {
	f.mu.Lock()
	if _, exists := f.sessions[userID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("you already have an active design session; send /agent cancel to start over")
	}
	f.mu.Unlock()

	agentName, reconciledMD, tools, err := f.loadAgentForEdit(userID, agentID)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	userProfile := f.loadUserProfile(userID)
	userMemory := f.loadUserMemory(userID)
	composioEnabled := f.loadComposioEnabled(userID)
	f.sessions[userID] = &DesignSession{
		UserID:             userID,
		AgentID:            agentID,
		AgentName:          agentName,
		State:              StateDescribing,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
		UserMemory:         userMemory,
		ComposioEnabled:    composioEnabled,
		CreatedAt:          time.Now(),
		IsEdit:             true,
		ExistingAgentMD:    reconciledMD,
		ExistingTools:      tools,
	}
	f.mu.Unlock()

	return fmt.Sprintf(
		"Editing agent \"%s\". Here's what it currently does:\n\n---\n%s\n---\n\nWhat would you like to change?",
		agentName, codePreview(reconciledMD, 30),
	), nil
}

// StartEditDesign is the web path for editing: creates a session already in
// StateDesigning with the user's first change request and returns the coder's
// first response. Mirrors StartDesign.
func (f *Flow) StartEditDesign(ctx context.Context, userID, agentID, firstMessage string) (string, error) {
	f.mu.Lock()
	if _, exists := f.sessions[userID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("design session already active; cancel it first")
	}
	f.mu.Unlock()

	agentName, reconciledMD, tools, err := f.loadAgentForEdit(userID, agentID)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	userProfile := f.loadUserProfile(userID)
	userMemory := f.loadUserMemory(userID)
	composioEnabled := f.loadComposioEnabled(userID)
	sess := &DesignSession{
		UserID:             userID,
		AgentID:            agentID,
		AgentName:          agentName,
		State:              StateDesigning,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
		UserMemory:         userMemory,
		ComposioEnabled:    composioEnabled,
		CreatedAt:          time.Now(),
		IsEdit:             true,
		ExistingAgentMD:    reconciledMD,
		ExistingTools:      tools,
	}
	f.sessions[userID] = sess
	f.mu.Unlock()

	return f.callCoder(ctx, userID, firstMessage)
}

// loadAgentForEdit loads an existing agent's name and AGENT.md, reconciling the
// AGENT.md's "# Suggested schedule:" first line with the real agent_schedules row
// before returning it. The schedule UI writes the DB directly and never touches
// AGENT.md, so the on-disk line can be stale; reconciling here makes AGENT.md the
// single source of truth for the rest of the edit (both what the coder sees and what
// finalize compares against).
func (f *Flow) loadAgentForEdit(userID, agentID string) (agentName, reconciledMD string, tools map[string]string, err error) {
	if f.db == nil {
		return "", "", nil, fmt.Errorf("no database configured")
	}

	agent, err := f.db.GetAgent(agentID)
	if err != nil {
		return "", "", nil, fmt.Errorf("agent not found: %w", err)
	}

	raw, err := os.ReadFile(AgentDescPath(f.designer.agentsDir, userID, agentID))
	if err != nil {
		return "", "", nil, fmt.Errorf("read AGENT.md: %w", err)
	}
	agentMD := strings.TrimSpace(string(raw))

	scheduleLine := "# Suggested schedule: none"
	if sched, schedErr := f.db.GetScheduleForAgent(agentID); schedErr == nil && sched != nil && sched.Enabled {
		scheduleLine = "# Suggested schedule: " + sched.CronExpr
	}

	lines := strings.SplitN(agentMD, "\n", 2)
	if len(lines) == 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# Suggested schedule:") {
		agentMD = scheduleLine + "\n" + lines[1]
	} else {
		agentMD = scheduleLine + "\n" + agentMD
	}

	// Load the existing tool scripts so the edit *conversation* can see the actual
	// code (not just AGENT.md). Without this the coder has no file access during Q&A
	// and asks the user where the scripts are. Best-effort: missing tools/ is fine.
	tools, _ = readToolsFromDisk(AgentDir(f.designer.agentsDir, userID, agentID))

	return agent.Name, agentMD, tools, nil
}

// Step processes one message and advances the FSM.
// Returns (response, isDone, agentID, err).
// agentID is non-empty only when isDone=true.
func (f *Flow) Step(ctx context.Context, userID, input string) (string, bool, string, error) {
	f.mu.Lock()
	sess, ok := f.sessions[userID]
	if !ok {
		f.mu.Unlock()
		return "", false, "", fmt.Errorf("no active design session; use /agent create <name> to start one")
	}
	state := sess.State
	f.mu.Unlock()

	switch state {
	case StateAwaitingResume:
		return f.stepAwaitingResume(ctx, userID, input)
	case StateDescribing:
		return f.stepDescribing(ctx, userID, input)
	case StateDesigning:
		return f.stepDesigning(ctx, userID, input)
	case StateVerifying:
		return f.stepVerifying(ctx, userID, input)
	default:
		return "", false, "", fmt.Errorf("unexpected state: %s", state)
	}
}

// Cancel removes the user's active session without saving. If a coder subprocess
// is currently running, its context is cancelled (killing the process).
// The progress channel is NOT closed here — runGeneration detects the
// context.Canceled error and calls closeProgress itself, making it the sole
// closer of the channel. Closing here in addition would race with notify()
// sends and panic even inside a select (select only guards against a full
// channel, not a closed one).
func (f *Flow) Cancel(userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[userID]
	if !ok {
		return
	}
	if sess.cancelGenerate != nil {
		sess.cancelGenerate()
	}
	delete(f.sessions, userID)
}

// SetProgressHandler stores a function that will be called with milestone
// messages during the next generation phase for this user's session. The
// router calls this before Step() when it detects an approval message so that
// Telegram can update its placeholder message with live progress.
func (f *Flow) SetProgressHandler(userID string, fn func(string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sess, ok := f.sessions[userID]; ok {
		sess.progressFunc = fn
	}
}

// GetProgressChan returns the buffered progress channel for the user's active
// session. The Web SSE handler reads from this channel to stream milestone
// events to the browser. Returns (nil, false) if no session exists.
func (f *Flow) GetProgressChan(userID string) (<-chan string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[userID]
	if !ok || sess.progressCh == nil {
		return nil, false
	}
	return sess.progressCh, true
}

// GetSession returns the user's active session, or nil.
func (f *Flow) GetSession(userID string) *DesignSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[userID]
}

// ─── Draft save / resume ──────────────────────────────────────────────────────

// draftTTL is how long a saved design draft remains resumable.
const draftTTL = 7 * 24 * time.Hour

// saveDraft serializes the current session and upserts it as the user's draft.
// Called while the Flow mutex is held; a single SQLite upsert is fast enough that
// holding the lock is acceptable (consistent with other db calls in runGeneration).
// Only "designing" and "verifying" states are persisted — StateAwaitingResume is
// a transient prompt and never saved.
func (f *Flow) saveDraft(sess *DesignSession) {
	if f.db == nil {
		return
	}
	histJSON, _ := json.Marshal(sess.History)
	toolsJSON, _ := json.Marshal(sess.PendingTools)
	state := "designing"
	if sess.State == StateVerifying {
		state = "verifying"
	}
	_ = f.db.UpsertAgentDraft(&db.AgentDraft{
		UserID:           sess.UserID,
		AgentID:          sess.AgentID,
		AgentName:        sess.AgentName,
		IsEdit:           sess.IsEdit,
		State:            state,
		HistoryJSON:      string(histJSON),
		PendingAgentMD:   sess.PendingAgentMD,
		PendingToolsJSON: string(toolsJSON),
		ExpiresAt:        time.Now().Add(draftTTL),
	})
}

// deleteDraft removes the user's draft. Called after a successful finalize so the
// draft prompt never reappears for an agent that was already saved.
func (f *Flow) deleteDraft(userID string) {
	if f.db == nil {
		return
	}
	_ = f.db.DeleteAgentDraft(userID)
}

// HasDraft returns the user's draft if one exists and is not expired; nil otherwise.
func (f *Flow) HasDraft(userID string) *db.AgentDraft {
	if f.db == nil {
		return nil
	}
	draft, err := f.db.GetAgentDraft(userID)
	if err != nil {
		return nil
	}
	return draft
}

// DismissDraft deletes the user's draft. For create-mode drafts in "verifying"
// state it also removes the agent's pre-approved directory so orphaned files don't
// accumulate on disk — that dir was created by runGeneration but never finalized.
func (f *Flow) DismissDraft(userID string) error {
	if f.db == nil {
		return nil
	}
	draft := f.HasDraft(userID)
	if draft == nil {
		return nil
	}
	_ = f.db.DeleteAgentDraft(userID)
	if !draft.IsEdit && draft.State == "verifying" && draft.AgentID != "" {
		_ = os.RemoveAll(AgentDir(f.designer.agentsDir, userID, draft.AgentID))
	}
	return nil
}

// ResumeDraft reconstructs a DesignSession from the saved draft and returns the
// message to show the user to continue the conversation. Derived context (Skills,
// ConnectedPlatforms, UserProfile, UserMemory) is reloaded the same way Start()/
// StartDesign() do — it is cheap to reload and may have changed since the draft
// was saved, so it is never stored in the draft itself.
//
// For edit drafts it re-runs loadAgentForEdit(userID, agentID); if the agent no
// longer exists the draft is dismissed and an error is returned so callers can
// tell the user.
//
// The coder is never re-run on resume — generation only happens when the user
// next says "approve".
func (f *Flow) ResumeDraft(ctx context.Context, userID string) (string, error) {
	if f.db == nil {
		return "", fmt.Errorf("no database configured")
	}
	draft, err := f.db.GetAgentDraft(userID)
	if err != nil {
		return "", fmt.Errorf("no draft to resume")
	}

	sess := &DesignSession{
		UserID:             userID,
		AgentID:            draft.AgentID,
		AgentName:          draft.AgentName,
		IsEdit:             draft.IsEdit,
		PendingAgentMD:     draft.PendingAgentMD,
		Skills:             f.loadSkillNames(userID),
		ConnectedPlatforms: f.loadConnectedPlatforms(userID),
		UserProfile:        f.loadUserProfile(userID),
		UserMemory:         f.loadUserMemory(userID),
		ComposioEnabled:    f.loadComposioEnabled(userID),
		CreatedAt:          time.Now(),
	}
	_ = json.Unmarshal([]byte(draft.HistoryJSON), &sess.History)
	if draft.PendingToolsJSON != "" {
		_ = json.Unmarshal([]byte(draft.PendingToolsJSON), &sess.PendingTools)
	}

	if draft.IsEdit {
		agentName, reconciledMD, tools, err := f.loadAgentForEdit(userID, draft.AgentID)
		if err != nil {
			// The agent being edited is gone — drop the draft and any shell session.
			_ = f.DismissDraft(userID)
			f.mu.Lock()
			delete(f.sessions, userID)
			f.mu.Unlock()
			return "", fmt.Errorf("the agent being edited no longer exists; draft dismissed")
		}
		sess.AgentName = agentName
		sess.ExistingAgentMD = reconciledMD
		sess.ExistingTools = tools
	}

	if draft.State == "verifying" {
		sess.State = StateVerifying
	} else {
		sess.State = StateDesigning
	}

	f.mu.Lock()
	f.sessions[userID] = sess
	f.mu.Unlock()

	if sess.State == StateVerifying {
		preview := sess.PendingAgentMD
		if len(preview) > 600 {
			preview = preview[:600] + "…"
		}
		return fmt.Sprintf(
			"Resuming your draft for **%s**. The coder has already built this version:\n\n```\n%s\n```\n\nType `approve` to save it, or describe any changes you'd like.",
			sess.AgentName, preview,
		), nil
	}
	return fmt.Sprintf(
		"Resuming your draft for **%s**. Here's the conversation so far — continue, or type 'approve' when ready to generate.",
		sess.AgentName,
	), nil
}

// OfferDraftResume creates a minimal session in StateAwaitingResume and returns the
// prompt to send the user. pendingAgentName is stored so the "new" branch of
// stepAwaitingResume can start a fresh create session with the name the user
// originally typed.
func (f *Flow) OfferDraftResume(userID, pendingAgentName string, draft *db.AgentDraft) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[userID] = &DesignSession{
		UserID:      userID,
		AgentID:     draft.AgentID,
		AgentName:   draft.AgentName,
		State:       StateAwaitingResume,
		IsEdit:      draft.IsEdit,
		pendingName: pendingAgentName,
		CreatedAt:   time.Now(),
	}
	return fmt.Sprintf(
		"Found an unfinished draft for \"%s\". Reply 'resume' to continue it, or 'new' to start fresh.",
		draft.AgentName,
	), nil
}

// ─── FSM step handlers ────────────────────────────────────────────────────────

// stepAwaitingResume handles the draft-resume offer. "resume" reconstructs the
// session from the saved draft; "new" (or anything else) dismisses the draft and
// starts a fresh create session with the name the user originally typed.
// Runs without the Flow mutex held (Step releases it before dispatching), so it
// can call ResumeDraft / Start directly.
func (f *Flow) stepAwaitingResume(ctx context.Context, userID, msg string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	pendingName := ""
	if sess != nil {
		pendingName = sess.pendingName
	}
	f.mu.Unlock()

	lower := strings.TrimSpace(strings.ToLower(msg))
	if lower == "resume" {
		resp, err := f.ResumeDraft(ctx, userID)
		if err != nil {
			return "", false, "", err
		}
		return resp, false, "", nil
	}

	// "new" or anything else → dismiss the draft and start a fresh create session.
	_ = f.DismissDraft(userID)
	f.mu.Lock()
	delete(f.sessions, userID) // drop the awaiting-resume shell so Start() doesn't refuse
	f.mu.Unlock()
	if pendingName == "" {
		pendingName = "agent"
	}
	resp, err := f.Start(userID, pendingName)
	if err != nil {
		return "", false, "", err
	}
	return resp, false, "", nil
}

// stepDescribing (Telegram only): user sends their first description.
func (f *Flow) stepDescribing(ctx context.Context, userID, description string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	sess.State = StateDesigning
	f.mu.Unlock()

	response, err := f.callCoder(ctx, userID, description)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// stepDesigning: free-form Q&A until "approve".
func (f *Flow) stepDesigning(ctx context.Context, userID, input string) (string, bool, string, error) {
	if isApproval(input) {
		return f.runGeneration(ctx, userID)
	}

	response, err := f.callCoder(ctx, userID, input)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// stepVerifying: test output was shown; wait for approval or change request.
func (f *Flow) stepVerifying(ctx context.Context, userID, input string) (string, bool, string, error) {
	if isApproval(input) {
		return f.finalizeAgent(ctx, userID)
	}

	// User wants changes — drop pending content, return to designing.
	f.mu.Lock()
	sess := f.sessions[userID]
	sess.State = StateDesigning
	sess.PendingAgentMD = ""
	sess.PendingTools = nil
	f.mu.Unlock()

	response, err := f.callCoder(ctx, userID, input)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// ─── Coder conversation ───────────────────────────────────────────────────────

// callCoder sends a conversational turn to the coder and appends to session history.
func (f *Flow) callCoder(ctx context.Context, userID, userMessage string) (string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	coderSvc := f.coderFor(userID)
	f.mu.Unlock()

	if coderSvc == nil {
		return "", fmt.Errorf("no coder configured for this user")
	}

	systemPrompt := prompts.BuildDesignSystemPrompt(prompts.DesignSystemParams{
		AgentName:          sess.AgentName,
		IsEdit:             sess.IsEdit,
		ExistingAgentMD:    sess.ExistingAgentMD,
		ExistingTools:      sess.ExistingTools,
		ConnectedPlatforms: sess.ConnectedPlatforms,
		ChatApps:           prompts.ChatAppsForPlatforms(sess.ConnectedPlatforms),
		Skills:             sess.Skills,
		UserProfile:        sess.UserProfile,
		UserMemory:         sess.UserMemory,
		ComposioEnabled:    sess.ComposioEnabled,
		KBManifest:         f.loadKBManifest(userID),
	})

	// Use WithNoTools so the design conversation outputs plain text and never
	// attempts to write files or request permissions.
	result, err := coderSvc.WithNoTools().Chat(ctx, userID, sess.History, systemPrompt, userMessage)
	if err != nil {
		if errors.Is(err, coder.ErrUsageLimit) {
			return fmt.Sprintf("⚠️ %s hit its usage limit. The design session is still active — try again in a while.", coderSvc.Name()), nil
		}
		return "", fmt.Errorf("coder: %w", err)
	}

	f.mu.Lock()
	sess.History = append(sess.History,
		db.ChatMessage{Role: "user", Content: userMessage},
		db.ChatMessage{Role: "assistant", Content: result.Text},
	)
	// Persist the draft so a reload/restart can resume from this turn. Covers
	// every conversation turn including StartDesign's first message.
	f.saveDraft(sess)
	f.mu.Unlock()

	return result.Text, nil
}

// dbMessagesToPrompt converts db.ChatMessage slice to prompts.ChatMessage slice.
func dbMessagesToPrompt(msgs []db.ChatMessage) []prompts.ChatMessage {
	out := make([]prompts.ChatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = prompts.ChatMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

// ─── Generation (triggered by approval) ──────────────────────────────────────

// runGeneration creates agent files by giving Claude Code full tool access to
// write files, run them, fix errors, and verify output — all in one pass.
// Only after the coder confirms things work does the user see the results.
func (f *Flow) runGeneration(ctx context.Context, userID string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	coderSvc := f.coderFor(userID)
	agentIDSnap := sess.AgentID
	agentNameSnap := sess.AgentName
	isEdit := sess.IsEdit
	existingAgentMD := sess.ExistingAgentMD
	historySnap := make([]db.ChatMessage, len(sess.History))
	copy(historySnap, sess.History)
	// The capability spec (e.g. Composio v3) must travel into the generation prompt
	// because Generate() carries no system prompt the way the design Chat() does.
	var backendType string
	if coderSvc != nil {
		backendType = prompts.MapCoderBackend(coderSvc.BackendType())
	}
	implParams := prompts.ImplementationParams{
		ComposioEnabled:    sess.ComposioEnabled,
		ConnectedPlatforms: sess.ConnectedPlatforms,
		ChatApps:           prompts.ChatAppsForPlatforms(sess.ConnectedPlatforms),
		BackendType:        backendType,
	}

	// Set up a buffered progress channel for SSE and snapshot the Telegram progress func.
	if sess.progressCh == nil {
		sess.progressCh = make(chan string, 8)
	}
	progressCh := sess.progressCh
	progressFunc := sess.progressFunc

	// Create a child context so Cancel() can kill the subprocess without
	// cancelling the outer request context (which would close the SSE stream).
	genCtx, cancelGenerate := context.WithCancel(ctx)
	sess.cancelGenerate = cancelGenerate
	f.mu.Unlock()

	// notify sends a milestone string to both the SSE channel (non-blocking)
	// and the Telegram progress callback.
	notify := func(msg string) {
		select {
		case progressCh <- msg:
		default:
		}
		if progressFunc != nil {
			progressFunc(msg)
		}
	}

	// closeProgress closes the local progressCh ref so SSE handlers unblock.
	// It uses a Once so it is safe to call on every return path without risk of
	// double-close. It closes the *local* ref (not via a session lookup) because
	// Cancel() may have already deleted the session from f.sessions by the time
	// Generate() returns — a session lookup would then silently no-op and the SSE
	// goroutine would block forever.
	var progressOnce sync.Once
	closeProgress := func() {
		progressOnce.Do(func() {
			// Nil out the session's field under lock so GetProgressChan can't hand
			// out the closed channel to a new caller.
			f.mu.Lock()
			if s, ok := f.sessions[userID]; ok {
				s.progressCh = nil
			}
			f.mu.Unlock()
			close(progressCh)
		})
	}

	if coderSvc == nil {
		closeProgress()
		return "", false, "", fmt.Errorf("no coder configured for this user")
	}

	notify("⚙️ Preparing workspace…")

	var workDir, prompt string
	var cleanupOnFail, cleanupOnSuccess func()

	if isEdit {
		// Edit mode: never let the coder touch the live agent dir before approval —
		// it may be scheduled and running unattended. Generate against a sibling
		// staging copy instead; the live dir is only overwritten in finalizeAgent.
		liveDir := AgentDir(f.designer.agentsDir, userID, agentIDSnap)
		stagingDir := liveDir + "-edit-staging"
		if err := copyAgentWorkspace(liveDir, stagingDir, existingAgentMD); err != nil {
			closeProgress()
			return "", false, "", fmt.Errorf("prepare staging workspace: %w", err)
		}
		workDir = stagingDir
		prompt = prompts.BuildEditImplementationPrompt(agentNameSnap, dbMessagesToPrompt(historySnap), implParams)
		remove := func() { _ = os.RemoveAll(stagingDir) }
		cleanupOnFail, cleanupOnSuccess = remove, remove
	} else {
		// Create the agent directory structure on disk before the coder runs so it
		// has a clean workspace to write into.
		agentDir := AgentDir(f.designer.agentsDir, userID, agentIDSnap)
		for _, sub := range []string{".", "tools", "logs", "notes"} {
			if err := os.MkdirAll(filepath.Join(agentDir, sub), 0o750); err != nil {
				closeProgress()
				return "", false, "", fmt.Errorf("create agent dir: %w", err)
			}
		}
		if err := os.WriteFile(filepath.Join(agentDir, "state.json"), []byte("{}"), 0o640); err != nil {
			closeProgress()
			return "", false, "", fmt.Errorf("write state.json: %w", err)
		}
		workDir = agentDir
		prompt = prompts.BuildImplementationPrompt(agentNameSnap, dbMessagesToPrompt(historySnap), implParams)
		cleanupOnFail = func() { _ = os.RemoveAll(agentDir) }
		cleanupOnSuccess = func() {} // the dir IS the pending agent; keep it until finalize/iterate
	}

	notify("🤖 Coder is building your agent — this can take a few minutes…")

	// Run the coder WITH full tools so it can write files, execute them, debug
	// errors, and confirm the implementation works — all in one session.
	// genCtx (not ctx) is used so Cancel() can kill the subprocess without
	// also cancelling the outer HTTP/SSE context.
	// Inject secrets (e.g., COMPOSIO_API_KEY) so the coder can make real API calls
	// during validation instead of using mock data.
	generationCoder := coderSvc.WithDir(workDir).WithAllowedTools("Bash,Write,Edit,Read")
	if f.secretsLoader != nil {
		if env, err := f.secretsLoader(genCtx, userID); err == nil && len(env) > 0 {
			generationCoder = generationCoder.WithExtraEnv(env)
		}
	}
	result, err := generationCoder.Generate(genCtx, userID, prompt)
	if err != nil {
		cleanupOnFail()
		closeProgress()
		if errors.Is(err, context.Canceled) {
			return "Agent creation was cancelled.", false, "", nil
		}
		if errors.Is(err, coder.ErrUsageLimit) {
			return fmt.Sprintf("⚠️ %s hit its usage limit during generation. Your design session is still active — try again in a while, or simplify what you asked for.", coderSvc.Name()), false, "", nil
		}
		if strings.Contains(err.Error(), "timed out") {
			return "⚠️ The coder timed out — the task may be too complex to build in one go. Try breaking it into simpler steps, then type approve.", false, "", nil
		}
		return "", false, "", fmt.Errorf("coder: %w", err)
	}

	// Coder determined the task is impossible — return soft message so user stays in designing.
	if blocked := parseBlockedOutput(result.Text); blocked != "" {
		cleanupOnFail()
		closeProgress()
		return "The coder ran into a blocker:\n\n" + blocked + "\n\nTell me how you'd like to proceed, or describe a different approach.", false, "", nil
	}

	notify("🔍 Validating agent safety checks…")

	// Ground truth: read what the coder actually wrote to disk.
	agentMDBytes, err := os.ReadFile(filepath.Join(workDir, "AGENT.md"))
	if err != nil {
		cleanupOnFail()
		closeProgress()
		return "The coder didn't create AGENT.md. Tell me what to change and I'll try again.", false, "", nil
	}
	agentMD := strings.TrimSpace(string(agentMDBytes))

	tools, err := readToolsFromDisk(workDir)
	if err != nil {
		cleanupOnFail()
		closeProgress()
		return "", false, "", fmt.Errorf("read tools: %w", err)
	}

	// Guardrails on the actual content the coder wrote.
	if err := CheckEthics(agentMD, ""); err != nil {
		cleanupOnFail()
		closeProgress()
		return fmt.Sprintf("Agent failed safety checks: %s\n\nPlease rephrase.", err.Error()), false, "", nil
	}
	for filename, code := range tools {
		if err := RunToolGuardrails(filename, code); err != nil {
			cleanupOnFail()
			closeProgress()
			// The generated code didn't meet the contract (e.g. wrong Composio API
			// version). Keep the user in StateDesigning so "approve" rebuilds — the
			// generation prompt now restates the correct spec, so a retry should fix it.
			return fmt.Sprintf("The generated tool %s didn't pass validation: %s\n\nType **approve** to rebuild, or tell me what to change.", filename, err.Error()), false, "", nil
		}
	}

	// Content is captured in memory now — discard the workspace (staging dir for
	// edits; create mode keeps its pending dir on disk until finalize/iterate).
	cleanupOnSuccess()
	closeProgress()

	testOut := parseTestOutput(result.Text)

	// No [TEST_OUTPUT] means the coder didn't complete the test step (silent agents
	// should still emit [TEST_OUTPUT]No chat output — agent only updates state.[/TEST_OUTPUT]).
	// Keep the user in StateDesigning: "approve" here retries generation rather than saving.
	if testOut == "" {
		return "The agent was built but the coder didn't produce verifiable test output. " +
			"Tell me what to adjust, or type **approve** to attempt a rebuild.", false, "", nil
	}

	// Test verified — move to StateVerifying so the user can approve or request changes.
	f.mu.Lock()
	sess = f.sessions[userID]
	sess.State = StateVerifying
	sess.PendingAgentMD = agentMD
	sess.PendingTools = tools
	// Persist the generated content so a reload before final approval can resume
	// without re-running the (quota-consuming) coder generation.
	f.saveDraft(sess)
	f.mu.Unlock()

	return fmt.Sprintf(
		"Here's what a test run produces:\n\n---\n%s\n---\n\nDoes this look right? Type **approve** to save the agent, or tell me what to change.",
		testOut,
	), false, "", nil
}

// copyAgentWorkspace creates a fresh staging directory containing the editable
// surface of a live agent: AGENT.md (the reconciled version, not the raw on-disk
// one), state.json, and the full tools/ project tree (nested modules, tests,
// requirements.txt, …). Used so an edit's test generation never touches the live
// agent. liveDir's logs/ and agent.json are intentionally not copied — the coder
// doesn't need them to make or test changes.
func copyAgentWorkspace(liveDir, stagingDir, reconciledAgentMD string) error {
	if err := os.RemoveAll(stagingDir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(stagingDir, "tools"), 0o750); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(stagingDir, "AGENT.md"), []byte(reconciledAgentMD), 0o640); err != nil {
		return err
	}

	state, err := os.ReadFile(filepath.Join(liveDir, "state.json"))
	if err != nil {
		state = []byte("{}")
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "state.json"), state, 0o640); err != nil {
		return err
	}

	// Copy the full project tree (nested dirs, tests, requirements.txt, …) so the
	// staging copy mirrors the live agent's layout exactly.
	tools, err := ReadToolsTree(filepath.Join(liveDir, "tools"))
	if err != nil {
		return err
	}
	return WriteToolsTree(filepath.Join(stagingDir, "tools"), tools)
}

// parseTestOutput extracts content between [TEST_OUTPUT] and [/TEST_OUTPUT].
func parseTestOutput(text string) string {
	start := strings.Index(text, "[TEST_OUTPUT]")
	if start < 0 {
		return ""
	}
	start += len("[TEST_OUTPUT]")
	end := strings.Index(text[start:], "[/TEST_OUTPUT]")
	if end < 0 {
		return strings.TrimSpace(text[start:])
	}
	return strings.TrimSpace(text[start : start+end])
}

// parseBlockedOutput extracts the coder's explanation from a [BLOCKED]...[/BLOCKED] block.
// Returns "" if no blocked marker is present.
func parseBlockedOutput(text string) string {
	start := strings.Index(text, "[BLOCKED]")
	if start < 0 {
		return ""
	}
	start += len("[BLOCKED]")
	end := strings.Index(text[start:], "[/BLOCKED]")
	if end < 0 {
		return strings.TrimSpace(text[start:])
	}
	return strings.TrimSpace(text[start : start+end])
}

// readToolsFromDisk reads the full project tree under agentDir/tools/ (nested
// dirs, tests, requirements.txt and other non-.py files) and returns it as a
// relpath→content map. See ReadToolsTree for the include/exclude and size rules.
func readToolsFromDisk(agentDir string) (map[string]string, error) {
	return ReadToolsTree(filepath.Join(agentDir, "tools"))
}

// finalizeAgent saves the pending agent content and cleans up the session.
// Called from stepVerifying when the user approves the test output.
func (f *Flow) finalizeAgent(ctx context.Context, userID string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	agentMD := sess.PendingAgentMD
	tools := sess.PendingTools
	isEdit := sess.IsEdit
	f.mu.Unlock()

	var resp string
	var done bool
	var agentID string
	var err error
	if isEdit {
		resp, done, agentID, err = f.updateAndFinish(ctx, userID, agentMD, tools)
	} else {
		resp, done, agentID, err = f.saveAndFinish(ctx, userID, agentMD, tools)
	}
	// On a successful save the agent is persisted — drop the draft so the resume
	// prompt never reappears for an already-created/updated agent.
	if err == nil {
		f.deleteDraft(userID)
	}
	return resp, done, agentID, err
}

// saveAndFinish writes a brand-new agent to disk/DB and terminates the session.
func (f *Flow) saveAndFinish(ctx context.Context, userID, agentMD string, tools map[string]string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	agentIDSnap := sess.AgentID
	agentNameSnap := sess.AgentName
	skillsSnap := sess.Skills
	f.mu.Unlock()

	requiredSecrets := parseRequiredSecrets(agentMD)
	description := extractDescription(agentMD, agentNameSnap)

	if err := f.designer.SaveAgent(userID, agentIDSnap, agentNameSnap, description, agentMD, tools, skillsSnap, requiredSecrets); err != nil {
		return "", false, "", fmt.Errorf("save agent: %w", err)
	}

	// Auto-create schedule if coder embedded a suggested cron expression.
	scheduleMsg := ""
	if cronExpr := parseSuggestedSchedule(agentMD); cronExpr != "" && f.db != nil {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if sched, err := parser.Parse(cronExpr); err == nil {
			nextRun := sched.Next(time.Now())
			_ = f.db.UpsertAgentSchedule(&db.AgentSchedule{
				ID:        uuid.New().String(),
				AgentID:   agentIDSnap,
				UserID:    userID,
				CronExpr:  cronExpr,
				NextRunAt: &nextRun,
				Enabled:   true,
			})
			scheduleMsg = fmt.Sprintf(" Schedule set: %s.", cronExpr)
		}
	}

	f.mu.Lock()
	delete(f.sessions, userID)
	f.mu.Unlock()

	return fmt.Sprintf(
		"Agent \"%s\" created!%s",
		agentNameSnap, scheduleMsg,
	), true, agentIDSnap, nil
}

// updateAndFinish overwrites an existing agent's files/DB row with the edited
// content and terminates the session. The schedule line in agentMD was reconciled
// with the real DB schedule at edit-session start (loadAgentForEdit), so it is now
// the authoritative source of truth: a valid cron upserts (reusing the existing
// schedule row's ID — never minting a new one, since agent_id has no unique
// constraint and a fresh ID would create a duplicate, double-firing schedule), and
// "none"/invalid where a schedule previously existed removes it.
func (f *Flow) updateAndFinish(ctx context.Context, userID, agentMD string, tools map[string]string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	agentIDSnap := sess.AgentID
	agentNameSnap := sess.AgentName
	skillsSnap := sess.Skills
	f.mu.Unlock()

	requiredSecrets := parseRequiredSecrets(agentMD)
	description := extractDescription(agentMD, agentNameSnap)

	if err := f.designer.UpdateAgent(userID, agentIDSnap, agentNameSnap, description, agentMD, tools, skillsSnap, requiredSecrets); err != nil {
		return "", false, "", fmt.Errorf("update agent: %w", err)
	}

	scheduleMsg := reconcileScheduleOnSave(f.db, userID, agentIDSnap, agentMD)

	f.mu.Lock()
	delete(f.sessions, userID)
	f.mu.Unlock()

	return fmt.Sprintf(
		"Agent \"%s\" updated!%s",
		agentNameSnap, scheduleMsg,
	), true, agentIDSnap, nil
}

// reconcileScheduleOnSave applies agentMD's "# Suggested schedule:" line to the
// agent_schedules table on an edit save, returning a short human-readable suffix
// describing what happened (or "" if nothing changed). It always reuses an existing
// schedule row's ID on upsert — agent_id has no unique constraint, so minting a new
// ID here would insert a duplicate row and fire the agent twice per tick — and
// deletes the row outright when the (now-reconciled) line says "none"/invalid and a
// schedule previously existed.
func reconcileScheduleOnSave(database dbDesignStore, userID, agentID, agentMD string) string {
	if database == nil {
		return ""
	}
	existing, _ := database.GetScheduleForAgent(agentID)
	cronExpr := parseSuggestedSchedule(agentMD)
	if cronExpr != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(cronExpr)
		if err != nil {
			return ""
		}
		nextRun := sched.Next(time.Now())
		schedID := uuid.New().String()
		if existing != nil {
			schedID = existing.ID
		}
		_ = database.UpsertAgentSchedule(&db.AgentSchedule{
			ID:        schedID,
			AgentID:   agentID,
			UserID:    userID,
			CronExpr:  cronExpr,
			NextRunAt: &nextRun,
			Enabled:   true,
		})
		return fmt.Sprintf(" Schedule set: %s.", cronExpr)
	}
	if existing != nil {
		_ = database.DeleteAgentSchedule(agentID)
		return " Schedule removed."
	}
	return ""
}

// parseSuggestedSchedule reads the first line of AGENT.md for "# Suggested schedule: <cron>".
// Returns "" if the line is missing, "none", or the expression fails validation.
func parseSuggestedSchedule(agentMD string) string {
	first := strings.SplitN(strings.TrimSpace(agentMD), "\n", 2)[0]
	after, ok := strings.CutPrefix(strings.TrimSpace(first), "# Suggested schedule:")
	if !ok {
		return ""
	}
	expr := strings.TrimSpace(after)
	if expr == "" || strings.EqualFold(expr, "none") {
		return ""
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(expr); err != nil {
		return ""
	}
	return expr
}

// parseRequiredSecrets extracts SECRET_NAME from "# - SECRET_NAME: description" header lines.
func parseRequiredSecrets(agentMD string) []string {
	var secrets []string
	inBlock := false
	for _, line := range strings.Split(agentMD, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# Required secrets:" {
			inBlock = true
			continue
		}
		if inBlock {
			if !strings.HasPrefix(trimmed, "# -") {
				break
			}
			after := strings.TrimPrefix(trimmed, "# -")
			after = strings.TrimSpace(after)
			if idx := strings.Index(after, ":"); idx > 0 {
				name := strings.TrimSpace(after[:idx])
				if name != "" {
					secrets = append(secrets, name)
				}
			}
		}
	}
	return secrets
}

// extractDescription builds a one-line description from the AGENT.md content.
func extractDescription(agentMD, fallback string) string {
	for _, line := range strings.Split(agentMD, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 200 {
			line = line[:200] + "..."
		}
		return line
	}
	return fallback
}

// ─── Approval detection ───────────────────────────────────────────────────────

// isApproval matches only explicit approval phrases that cannot arise naturally
// in Q&A answers. "yes", "ok", "sure" are deliberately excluded.
func isApproval(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	for _, trigger := range []string{"approve", "go ahead", "build it", "create it", "/approve"} {
		if lower == trigger {
			return true
		}
	}
	return false
}

// ─── Skills helpers ───────────────────────────────────────────────────────────

func (f *Flow) loadSkillNames(userID string) []string {
	if f.db == nil {
		return nil
	}
	skills, _ := f.db.ListSkills(userID)
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, s.Name)
	}
	return names
}

func (f *Flow) loadConnectedPlatforms(userID string) []string {
	if f.db == nil {
		return nil
	}
	conns, _ := f.db.ListUserPlatformConnections(userID)
	out := make([]string, 0, len(conns))
	for _, c := range conns {
		out = append(out, c.Platform)
	}
	return out
}

// loadUserProfile returns the rendered "[User profile]" context block for
// userID, or "" if no db is attached or no profile fields are set.
func (f *Flow) loadUserProfile(userID string) string {
	if f.db == nil {
		return ""
	}
	return profile.Load(f.db, userID).ContextString()
}

// loadComposioEnabled returns true if the user has stored a COMPOSIO_API_KEY secret.
// Uses a presence-only check so no master password is required.
func (f *Flow) loadComposioEnabled(userID string) bool {
	if f.db == nil {
		return false
	}
	ok, _ := f.db.SecretExists(userID, "COMPOSIO_API_KEY")
	return ok
}

// loadUserMemory returns saved memory entries as a bullet list, or "" if none.
func (f *Flow) loadUserMemory(userID string) string {
	if f.memStore == nil {
		return ""
	}
	mem, _ := f.memStore.ContextString(userID)
	return mem
}

// ─── Text helpers ─────────────────────────────────────────────────────────────

// cleanCodeFence strips leading/trailing markdown code fences from LLM output.
func cleanCodeFence(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"```python", "```"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// codePreview returns at most maxLines lines with a truncation marker.
func codePreview(code string, maxLines int) string {
	lines := strings.Split(code, "\n")
	if len(lines) <= maxLines {
		return code
	}
	return strings.Join(lines[:maxLines], "\n") + "\n# ... (truncated)"
}
