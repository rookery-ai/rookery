package agentdesigner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/composioassets"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
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
	WorkspaceID        string
	AgentID            string
	AgentName          string
	State              DesignState
	History            []db.ChatMessage   // full conversation fed to coder on every turn
	Skills             []prompts.SkillRef // installed skills (name+description), loaded once on Start
	ConnectedPlatforms []string           // e.g. ["telegram"] — loaded from platform_connections
	UserProfile        string             // rendered "[User profile]" block, loaded once on session start
	UserMemory         string             // bullet list of saved memory entries, loaded once on session start
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

	// GenerationFailed is true when the last generation attempt soft-failed (a blocker
	// with no presentable build on disk, or a not-presentable/guardrail outcome) and the
	// session stayed in StateDesigning. While set, a forgiving retry phrase ("try again",
	// "fix it", "yes", "ok") re-runs generation — otherwise the strict isApproval would
	// route those to the design chat and the coder would just re-present the plan
	// (the "approve-loop"). Cleared at the start of every runGeneration attempt.
	GenerationFailed bool

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
	ListSkills(workspaceID string) ([]*db.Skill, error)
	ListWorkspacePlatformConnections(workspaceID string) ([]*db.PlatformConnection, error)
	UpsertAgentSchedule(s *db.AgentSchedule) error
	GetAgent(id string) (*db.Agent, error)
	GetScheduleForAgent(agentID string) (*db.AgentSchedule, error)
	DeleteAgentSchedule(agentID string) error
	GetSetting(workspaceID, key string) (string, error)
	SecretExists(workspaceID, name string) (bool, error)

	UpsertAgentDraft(d *db.AgentDraft) error
	GetAgentDraft(workspaceID string) (*db.AgentDraft, error)
	DeleteAgentDraft(workspaceID string) error
}

// memoryStore is satisfied by *memory.Store — kept local to avoid the import.
type memoryStore interface {
	ContextString(workspaceID string) (string, error)
}

// kbLister enumerates the user's knowledge-base note paths (vault-relative) so
// the designer can be shown what notes already exist. Satisfied by *vault.Vault.
type kbLister interface {
	NotePaths(workspaceID string) []string
}

// loadKBManifest returns a rendered bullet list of the user's existing note paths
// (capped), or "" if no lister is attached or the knowledge base is empty.
func (f *Flow) loadKBManifest(workspaceID string) string {
	if f.kb == nil {
		return ""
	}
	paths := f.kb.NotePaths(workspaceID)
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
	sessions map[string]*DesignSession // keyed by workspaceID

	coderFor      func(workspaceID string) *coder.Coder
	designer      *AgentDesigner
	db            dbDesignStore
	memStore      memoryStore // optional; nil = no memory injected
	kb            kbLister    // optional; nil = no KB manifest injected
	secretsLoader func(ctx context.Context, workspaceID string) (map[string]string, error)
}

// NewFlow creates a Flow. coderResolver maps a workspaceID to the right coder.
func NewFlow(coderResolver func(workspaceID string) *coder.Coder, designer *AgentDesigner) *Flow {
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
func (f *Flow) WithSecretsLoader(fn func(ctx context.Context, workspaceID string) (map[string]string, error)) *Flow {
	f.secretsLoader = fn
	return f
}

// Start creates a new Telegram design session for workspaceID.
// Returns the opening prompt asking for a description.
func (f *Flow) Start(workspaceID, agentName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.sessions[workspaceID]; exists {
		return "", fmt.Errorf("you already have an active design session; send /agent cancel to start over")
	}

	skills := f.loadSkillNames(workspaceID)
	platforms := f.loadConnectedPlatforms(workspaceID)
	userProfile := f.loadUserProfile(workspaceID)
	userMemory := f.loadUserMemory(workspaceID)
	composioEnabled := f.loadComposioEnabled(workspaceID)
	f.sessions[workspaceID] = &DesignSession{
		WorkspaceID:        workspaceID,
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
func (f *Flow) StartDesign(ctx context.Context, workspaceID, agentName, firstMessage string) (string, error) {
	f.mu.Lock()

	if _, exists := f.sessions[workspaceID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("design session already active; cancel it first")
	}

	skills := f.loadSkillNames(workspaceID)
	platforms := f.loadConnectedPlatforms(workspaceID)
	userProfile := f.loadUserProfile(workspaceID)
	userMemory := f.loadUserMemory(workspaceID)
	composioEnabled := f.loadComposioEnabled(workspaceID)
	sess := &DesignSession{
		WorkspaceID:        workspaceID,
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
	f.sessions[workspaceID] = sess
	f.mu.Unlock()

	return f.callCoder(ctx, workspaceID, firstMessage)
}

// StartEdit creates a new Telegram edit session for an existing agentID.
// Ownership of agentID is assumed pre-checked by the caller (same pattern as the
// other agent handlers, e.g. the Telegram /agent and web delete/run endpoints).
// Returns the opening prompt summarizing current behavior and asking what to change.
func (f *Flow) StartEdit(workspaceID, agentID string) (string, error) {
	f.mu.Lock()
	if _, exists := f.sessions[workspaceID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("you already have an active design session; send /agent cancel to start over")
	}
	f.mu.Unlock()

	agentName, reconciledMD, tools, err := f.loadAgentForEdit(workspaceID, agentID)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	skills := f.loadSkillNames(workspaceID)
	platforms := f.loadConnectedPlatforms(workspaceID)
	userProfile := f.loadUserProfile(workspaceID)
	userMemory := f.loadUserMemory(workspaceID)
	composioEnabled := f.loadComposioEnabled(workspaceID)
	f.sessions[workspaceID] = &DesignSession{
		WorkspaceID:        workspaceID,
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
func (f *Flow) StartEditDesign(ctx context.Context, workspaceID, agentID, firstMessage string) (string, error) {
	f.mu.Lock()
	if _, exists := f.sessions[workspaceID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("design session already active; cancel it first")
	}
	f.mu.Unlock()

	agentName, reconciledMD, tools, err := f.loadAgentForEdit(workspaceID, agentID)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	skills := f.loadSkillNames(workspaceID)
	platforms := f.loadConnectedPlatforms(workspaceID)
	userProfile := f.loadUserProfile(workspaceID)
	userMemory := f.loadUserMemory(workspaceID)
	composioEnabled := f.loadComposioEnabled(workspaceID)
	sess := &DesignSession{
		WorkspaceID:        workspaceID,
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
	f.sessions[workspaceID] = sess
	f.mu.Unlock()

	return f.callCoder(ctx, workspaceID, firstMessage)
}

// loadAgentForEdit loads an existing agent's name and AGENT.md, reconciling the
// AGENT.md's "# Suggested schedule:" first line with the real agent_schedules row
// before returning it. The schedule UI writes the DB directly and never touches
// AGENT.md, so the on-disk line can be stale; reconciling here makes AGENT.md the
// single source of truth for the rest of the edit (both what the coder sees and what
// finalize compares against).
func (f *Flow) loadAgentForEdit(workspaceID, agentID string) (agentName, reconciledMD string, tools map[string]string, err error) {
	if f.db == nil {
		return "", "", nil, fmt.Errorf("no database configured")
	}

	agent, err := f.db.GetAgent(agentID)
	if err != nil {
		return "", "", nil, fmt.Errorf("agent not found: %w", err)
	}

	raw, err := os.ReadFile(AgentDescPath(f.designer.agentsDir, workspaceID, agentID))
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
	tools, _ = readToolsFromDisk(AgentDir(f.designer.agentsDir, workspaceID, agentID))

	return agent.Name, agentMD, tools, nil
}

// Step processes one message and advances the FSM.
// Returns (response, isDone, agentID, err).
// agentID is non-empty only when isDone=true.
func (f *Flow) Step(ctx context.Context, workspaceID, input string) (string, bool, string, error) {
	f.mu.Lock()
	sess, ok := f.sessions[workspaceID]
	if !ok {
		f.mu.Unlock()
		return "", false, "", fmt.Errorf("no active design session; use /agent create <name> to start one")
	}
	state := sess.State
	f.mu.Unlock()

	switch state {
	case StateAwaitingResume:
		return f.stepAwaitingResume(ctx, workspaceID, input)
	case StateDescribing:
		return f.stepDescribing(ctx, workspaceID, input)
	case StateDesigning:
		return f.stepDesigning(ctx, workspaceID, input)
	case StateVerifying:
		return f.stepVerifying(ctx, workspaceID, input)
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
func (f *Flow) Cancel(workspaceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[workspaceID]
	if !ok {
		return
	}
	if sess.cancelGenerate != nil {
		sess.cancelGenerate()
	}
	delete(f.sessions, workspaceID)
}

// SetProgressHandler stores a function that will be called with milestone
// messages during the next generation phase for this user's session. The
// router calls this before Step() when it detects an approval message so that
// Telegram can update its placeholder message with live progress.
func (f *Flow) SetProgressHandler(workspaceID string, fn func(string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sess, ok := f.sessions[workspaceID]; ok {
		sess.progressFunc = fn
	}
}

// GetProgressChan returns the buffered progress channel for the user's active
// session. The Web SSE handler reads from this channel to stream milestone
// events to the browser. Returns (nil, false) if no session exists.
func (f *Flow) GetProgressChan(workspaceID string) (<-chan string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[workspaceID]
	if !ok || sess.progressCh == nil {
		return nil, false
	}
	return sess.progressCh, true
}

// GetSession returns the user's active session, or nil.
func (f *Flow) GetSession(workspaceID string) *DesignSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[workspaceID]
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
		WorkspaceID:      sess.WorkspaceID,
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
func (f *Flow) deleteDraft(workspaceID string) {
	if f.db == nil {
		return
	}
	_ = f.db.DeleteAgentDraft(workspaceID)
}

// HasDraft returns the user's draft if one exists and is not expired; nil otherwise.
func (f *Flow) HasDraft(workspaceID string) *db.AgentDraft {
	if f.db == nil {
		return nil
	}
	draft, err := f.db.GetAgentDraft(workspaceID)
	if err != nil {
		return nil
	}
	return draft
}

// DismissDraft deletes the user's draft. For create-mode drafts in "verifying"
// state it also removes the agent's pre-approved directory so orphaned files don't
// accumulate on disk — that dir was created by runGeneration but never finalized.
func (f *Flow) DismissDraft(workspaceID string) error {
	if f.db == nil {
		return nil
	}
	draft := f.HasDraft(workspaceID)
	if draft == nil {
		return nil
	}
	_ = f.db.DeleteAgentDraft(workspaceID)
	if !draft.IsEdit && draft.State == "verifying" && draft.AgentID != "" {
		_ = os.RemoveAll(AgentDir(f.designer.agentsDir, workspaceID, draft.AgentID))
	}
	return nil
}

// ResumeDraft reconstructs a DesignSession from the saved draft and returns the
// message to show the user to continue the conversation. Derived context (Skills,
// ConnectedPlatforms, UserProfile, UserMemory) is reloaded the same way Start()/
// StartDesign() do — it is cheap to reload and may have changed since the draft
// was saved, so it is never stored in the draft itself.
//
// For edit drafts it re-runs loadAgentForEdit(workspaceID, agentID); if the agent no
// longer exists the draft is dismissed and an error is returned so callers can
// tell the user.
//
// The coder is never re-run on resume — generation only happens when the user
// next says "approve".
func (f *Flow) ResumeDraft(ctx context.Context, workspaceID string) (string, error) {
	if f.db == nil {
		return "", fmt.Errorf("no database configured")
	}
	draft, err := f.db.GetAgentDraft(workspaceID)
	if err != nil {
		return "", fmt.Errorf("no draft to resume")
	}

	sess := &DesignSession{
		WorkspaceID:        workspaceID,
		AgentID:            draft.AgentID,
		AgentName:          draft.AgentName,
		IsEdit:             draft.IsEdit,
		PendingAgentMD:     draft.PendingAgentMD,
		Skills:             f.loadSkillNames(workspaceID),
		ConnectedPlatforms: f.loadConnectedPlatforms(workspaceID),
		UserProfile:        f.loadUserProfile(workspaceID),
		UserMemory:         f.loadUserMemory(workspaceID),
		ComposioEnabled:    f.loadComposioEnabled(workspaceID),
		CreatedAt:          time.Now(),
	}
	_ = json.Unmarshal([]byte(draft.HistoryJSON), &sess.History)
	if draft.PendingToolsJSON != "" {
		_ = json.Unmarshal([]byte(draft.PendingToolsJSON), &sess.PendingTools)
	}

	if draft.IsEdit {
		agentName, reconciledMD, tools, err := f.loadAgentForEdit(workspaceID, draft.AgentID)
		if err != nil {
			// The agent being edited is gone — drop the draft and any shell session.
			_ = f.DismissDraft(workspaceID)
			f.mu.Lock()
			delete(f.sessions, workspaceID)
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
	f.sessions[workspaceID] = sess
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
func (f *Flow) OfferDraftResume(workspaceID, pendingAgentName string, draft *db.AgentDraft) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
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
func (f *Flow) stepAwaitingResume(ctx context.Context, workspaceID, msg string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	pendingName := ""
	if sess != nil {
		pendingName = sess.pendingName
	}
	f.mu.Unlock()

	lower := strings.TrimSpace(strings.ToLower(msg))
	if lower == "resume" {
		resp, err := f.ResumeDraft(ctx, workspaceID)
		if err != nil {
			return "", false, "", err
		}
		return resp, false, "", nil
	}

	// "new" or anything else → dismiss the draft and start a fresh create session.
	_ = f.DismissDraft(workspaceID)
	f.mu.Lock()
	delete(f.sessions, workspaceID) // drop the awaiting-resume shell so Start() doesn't refuse
	f.mu.Unlock()
	if pendingName == "" {
		pendingName = "agent"
	}
	resp, err := f.Start(workspaceID, pendingName)
	if err != nil {
		return "", false, "", err
	}
	return resp, false, "", nil
}

// stepDescribing (Telegram only): user sends their first description.
func (f *Flow) stepDescribing(ctx context.Context, workspaceID, description string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	sess.State = StateDesigning
	f.mu.Unlock()

	response, err := f.callCoder(ctx, workspaceID, description)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// stepDesigning: free-form Q&A until "approve".
func (f *Flow) stepDesigning(ctx context.Context, workspaceID, input string) (string, bool, string, error) {
	// Strict approval always launches a build. Additionally, right after a generation
	// soft-failed (GenerationFailed), a forgiving confirmation ("try again", "fix it",
	// "yes", "ok") also re-runs generation — the state where the user most needs a
	// lenient retry is exactly where isApproval is strictest, which otherwise traps them
	// in an approve-loop (only the literal word "approve" would re-run).
	f.mu.Lock()
	genFailed := false
	if sess := f.sessions[workspaceID]; sess != nil {
		genFailed = sess.GenerationFailed
	}
	f.mu.Unlock()

	if isApproval(input) {
		return f.runGeneration(ctx, workspaceID)
	}
	if genFailed && isRetryApproval(input) {
		// Capture the retry message in History before re-running, so any instruction it
		// carries ("fix it by using the results field") reaches the generation coder — a
		// bare "try again" adds harmless context, a specific one steers the rebuild.
		f.appendUserHistory(workspaceID, input)
		return f.runGeneration(ctx, workspaceID)
	}

	response, err := f.callCoder(ctx, workspaceID, input)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// stepVerifying: test output was shown; wait for approval or change request.
func (f *Flow) stepVerifying(ctx context.Context, workspaceID, input string) (string, bool, string, error) {
	if isVerifyApproval(input) {
		return f.finalizeAgent(ctx, workspaceID)
	}

	// User wants changes — return to designing to revise. KEEP the generated
	// agent in memory (PendingAgentMD/Tools): the next approve re-generates with
	// the change context and overwrites it, but a misfire no longer silently
	// discards the whole build. Previously this cleared the pending content,
	// which lost the generated agent if the user's reply wasn't an exact
	// "approve" (e.g. "yes", "save", "ok", "approve!").
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	sess.State = StateDesigning
	f.mu.Unlock()

	response, err := f.callCoder(ctx, workspaceID, input)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// ─── Coder conversation ───────────────────────────────────────────────────────

// callCoder sends a conversational turn to the coder and appends to session history.
func (f *Flow) callCoder(ctx context.Context, workspaceID, userMessage string) (string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	coderSvc := f.coderFor(workspaceID)
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
		KBManifest:         f.loadKBManifest(workspaceID),
	})

	// Use WithNoTools so the design conversation outputs plain text and never
	// attempts to write files or request permissions.
	result, err := coderSvc.WithNoTools().Chat(ctx, workspaceID, sess.History, systemPrompt, userMessage)
	if err != nil {
		if errors.Is(err, coder.ErrUsageLimit) || errors.Is(err, coder.ErrRateLimited) {
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
func (f *Flow) runGeneration(ctx context.Context, workspaceID string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	// Reset the failure flag for this fresh attempt; a soft-fail below re-sets it (with
	// the failure recorded into History so this attempt isn't context-blind — see
	// recordGenerationFailure). Snapshot History AFTER any prior failure was appended.
	sess.GenerationFailed = false
	coderSvc := f.coderFor(workspaceID)
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
			if s, ok := f.sessions[workspaceID]; ok {
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
		liveDir := AgentDir(f.designer.agentsDir, workspaceID, agentIDSnap)
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
		agentDir := AgentDir(f.designer.agentsDir, workspaceID, agentIDSnap)
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

	// Seed the Composio helper scripts deterministically — never let the coder author
	// them from prompt text. Overwrites any previous (possibly LLM-corrupted, possibly
	// stale) copy on every generation/edit pass, so it self-heals and can never drift
	// from the verified-correct version. Only relevant when the user has Composio
	// connected, but harmless to write unconditionally too; gated on ComposioEnabled to
	// avoid noise in agents that never touch it.
	if implParams.ComposioEnabled {
		if err := composioassets.WriteHelperFiles(filepath.Join(workDir, "tools")); err != nil {
			slog.Warn("agentdesigner: seed composio helper files", "workspace_id", workspaceID, "err", err)
		}
	}

	notify("🤖 Coder is building your agent — this can take a few minutes…")

	// Run the coder WITH full tools so it can write files, execute them, debug
	// errors, and confirm the implementation works — all in one session.
	// genCtx (not ctx) is used so Cancel() can kill the subprocess without
	// also cancelling the outer HTTP/SSE context.
	// Inject secrets (e.g., COMPOSIO_API_KEY) so the coder can make real API calls
	// during validation instead of using mock data.
	// Same tool set as an agent run (Bash,WebFetch,Read,Write,Edit) so the coder can do
	// REAL end-to-end tests against the live services during the build — not mock-only.
	// Secrets are injected below so the real API calls the agent will make at run time are
	// actually exercised here (the only exception is sending real outbound messages,
	// enforced by the testing-rules prompt AND, for Composio, a code-level guard in the
	// seeded composio_helper.py keyed off SA_BUILD_PHASE below).
	generationCoder := coderSvc.WithDir(workDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit")
	// WithExtraEnv replaces rather than merges, so build the full env map once: secrets
	// (if any) plus the build-phase marker that tells composio_execute() this is a
	// generation/verification pass, not a real run — it must never be set for a real run.
	extraEnv := map[string]string{composioassets.BuildPhaseEnvVar: composioassets.BuildPhaseGeneration}
	if f.secretsLoader != nil {
		if env, err := f.secretsLoader(genCtx, workspaceID); err == nil {
			for k, v := range env {
				extraEnv[k] = v
			}
		}
	}
	generationCoder = generationCoder.WithExtraEnv(extraEnv)
	result, err := generationCoder.Generate(genCtx, workspaceID, prompt)
	if err != nil {
		cleanupOnFail()
		closeProgress()
		if errors.Is(err, context.Canceled) {
			return "Agent creation was cancelled.", false, "", nil
		}
		if errors.Is(err, coder.ErrUsageLimit) || errors.Is(err, coder.ErrRateLimited) {
			return fmt.Sprintf("⚠️ %s hit its usage limit during generation. Your design session is still active — try again in a while, or simplify what you asked for.", coderSvc.Name()), false, "", nil
		}
		if errors.Is(err, coder.ErrMaxTurns) {
			// Normally unreachable — runToolLoop's grace turn (api_engine.go) converts this
			// into a [BLOCKED] result handled below, this only fires if that grace call
			// itself also failed.
			return "⚠️ The coder ran out of attempts without finishing — the task may need to be broken into simpler steps. Tell me what to adjust, or type **approve** to try again.", false, "", nil
		}
		if strings.Contains(err.Error(), "timed out") {
			return "⚠️ The coder timed out — the task may be too complex to build in one go. Try breaking it into simpler steps, then type approve.", false, "", nil
		}
		return "", false, "", fmt.Errorf("coder: %w", err)
	}

	notify("🔍 Validating agent safety checks…")

	// Decide, from what the coder actually wrote to disk + its raw output, whether the
	// build is presentable for review. Pure/file-reading (no cleanup, no state mutation,
	// no logging) so the transition it drives — the headline convergence fix — is
	// unit-testable without a coder subprocess (see build_outcome_test.go).
	decision := decideBuildOutcome(workDir, result.Text, backendType)

	// Reconcile a [BLOCKED] marker against ground truth on disk (see reconcileBlockedOutcome).
	// The turn-budget grace turn (api_engine.go) makes even a completed build emit [BLOCKED];
	// deleting the files on the marker alone would destroy correct work and force a
	// context-blind rebuild.
	if decision.hardErr != nil {
		cleanupOnFail()
		closeProgress()
		return "", false, "", decision.hardErr
	}

	blocked := parseBlockedOutput(result.Text)
	outcome := reconcileBlockedOutcome(decision, blocked, backendType)

	if !outcome.advance {
		cleanupOnFail()
		closeProgress()
		// The specific reason (regex/AST internals, raw endpoint paths, HTTP codes) is an
		// implementation detail the non-technical user never sees — log it server-side and
		// show the plain-language message reconcileBlockedOutcome chose.
		if decision.logReason != "" {
			slog.Warn("agentdesigner: build not presentable", "workspace_id", workspaceID, "agent_id", agentIDSnap, "reason", decision.logReason)
		}
		// Record the failure so a forgiving retry re-runs generation WITH context of what
		// went wrong, instead of the user being trapped in an approve-loop (C1/C2).
		f.recordGenerationFailure(workspaceID, outcome.recordFailNote)
		return outcome.message, false, "", nil
	}

	// Content is captured now — discard the workspace (staging dir for edits; create
	// mode keeps its pending dir on disk until finalize/iterate).
	cleanupOnSuccess()
	closeProgress()

	// Move to StateVerifying so the user can approve or request changes.
	f.mu.Lock()
	sess = f.sessions[workspaceID]
	sess.State = StateVerifying
	sess.PendingAgentMD = decision.agentMD
	sess.PendingTools = decision.tools
	// Persist the generated content so a reload before final approval can resume
	// without re-running the (quota-consuming) coder generation.
	f.saveDraft(sess)
	f.mu.Unlock()

	return outcome.message, false, "", nil
}

// reconciledOutcome is the result of folding a [BLOCKED] marker into a buildDecision.
type reconciledOutcome struct {
	message        string // user-facing message
	advance        bool   // true → advance to StateVerifying (keep the build)
	recordFailNote string // when !advance, the detail to record for a context-aware retry
}

// reconcileBlockedOutcome decides the final outcome from a build's on-disk decision plus any
// [BLOCKED] marker the coder emitted. It is pure (no IO/state) so the headline reconciliation
// — a [BLOCKED] must NOT destroy a build that is actually complete on disk — is unit-testable.
//
//   - Not presentable: stay in designing. A genuine blocker (no usable build) shows the
//     coder's plain-language explanation; otherwise the generic not-presentable message. The
//     failure note is recorded so a forgiving retry re-runs with context.
//   - Presentable + a blocker: advance to review anyway (keep the files so the user can
//     inspect them), but prepend an honest caveat — the grace turn fired, so it isn't
//     confirmed to work end to end. EXCEPTION (capability-gated): on a weak tool-calling
//     API coder (BackendToolCalling) that authored its own script, a blocker means the
//     build-time self-verify topped out with no proof the script runs — so we do NOT
//     advance; we stay in StateDesigning and record a retry note, mirroring the
//     not-presentable path. Capable CLI / basic backends keep the advance-with-caveat.
//   - Presentable, no blocker: the normal review message, unchanged.
func reconcileBlockedOutcome(d buildDecision, blocked, backendType string) reconciledOutcome {
	if !d.presentable {
		// Weak backend held back an unverified authored script (decideBuildOutcome's
		// verification gate: hasAuthoredScript is set ONLY there, never on a genuine
		// ethics/guardrail hard-fail). The build actually PASSED ethics + guardrails, so
		// the generic "rejected by a safety/quality check" note below would mislead the
		// retry into rewriting clean code. Give it a STEERING note instead — run the
		// script and show real output, or drop it and reason directly — so "keep going"
		// converges instead of regenerating the same unverified script. Takes priority
		// over the blocked/safety branches, and keeps decideBuildOutcome's user message.
		if backendType == prompts.BackendToolCalling && d.hasAuthoredScript {
			return reconciledOutcome{
				message:        d.message,
				advance:        false,
				recordFailNote: "the helper script it wrote was never confirmed to run. Next attempt: actually run the script and show its real output, or drop the script and do the task by reasoning directly.",
			}
		}
		if blocked != "" {
			return reconciledOutcome{
				message:        "I wasn't able to finish building this:\n\n" + blocked + "\n\nTell me how you'd like to proceed, or say \"try again\" and I'll take another run at it.",
				advance:        false,
				recordFailNote: blocked,
			}
		}
		// No blocker: the note goes into History, which is ALSO replayed to the text-only
		// design coder that talks to the user. d.logReason is technical (e.g. "generated tool
		// failed guardrails: ast check: forbidden: subprocess.run()") — recording it verbatim
		// would let the design coder echo that jargon to a non-technical user (the exact leak
		// this audit fixes elsewhere). Keep the technical detail in the caller's server-side
		// slog.Warn (which reads d.logReason directly), and record a PLAIN, still-steering
		// note here. The generation coder re-runs the guardrails anyway, so it will re-hit the
		// same wall if it repeats the code — it does not need the guardrail's exact wording.
		return reconciledOutcome{
			message:        d.message,
			advance:        false,
			recordFailNote: "the last build was rejected by an automated safety/quality check",
		}
	}
	if blocked != "" {
		if backendType == prompts.BackendToolCalling && d.hasAuthoredScript {
			// Weak backend + a script we never confirmed runs: don't ship it. Stay in
			// designing so a forgiving retry re-runs with context (same shape as the
			// not-presentable blocker path above).
			return reconciledOutcome{
				message: "I built this, but on your configured model I couldn't confirm the helper it " +
					"wrote actually runs — so I won't save it as working yet. Say **keep going** and " +
					"I'll take another pass (I'll try a simpler approach), or tell me to keep it as-is.",
				advance:        false,
				recordFailNote: "build blocked on weak backend with an unverified authored script: " + blocked,
			}
		}
		return reconciledOutcome{
			message: "⚠️ Heads up — I built this but couldn't fully confirm it works end to end: " + blocked + "\n\n" + d.message,
			advance: true,
		}
	}
	return reconciledOutcome{message: d.message, advance: true}
}

// recordGenerationFailure marks the session so a forgiving retry can re-run generation
// (see stepDesigning), and appends a note to History so the next generation attempt is
// aware the previous one failed and why — without this the re-run repeats the same build
// context-blind. detail may carry technical specifics; History is the coder's channel, not
// the user's, so precision there helps the coder fix the actual problem.
func (f *Flow) recordGenerationFailure(workspaceID, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess := f.sessions[workspaceID]
	if sess == nil {
		return
	}
	sess.GenerationFailed = true
	note := "I attempted to build the agent but it did not succeed."
	if strings.TrimSpace(detail) != "" {
		note += " Reason: " + strings.TrimSpace(detail) + "."
	}
	note += " On the next attempt I must diagnose and take a different approach so this does not recur."
	sess.History = append(sess.History, db.ChatMessage{Role: "assistant", Content: note})
	f.saveDraft(sess)
}

// appendUserHistory records a user turn without invoking the coder — used by the
// post-failure retry path so the retry instruction is part of the history the next
// generation attempt sees (runGeneration snapshots History under the same lock afterward).
func (f *Flow) appendUserHistory(workspaceID, input string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess := f.sessions[workspaceID]
	if sess == nil {
		return
	}
	sess.History = append(sess.History, db.ChatMessage{Role: "user", Content: input})
	f.saveDraft(sess)
}

// buildDecision is the outcome decideBuildOutcome derives from a finished coder
// generation pass: whether the result is presentable for review, the user-facing
// message, and (when presentable) the AGENT.md + tools to stage as pending.
type buildDecision struct {
	presentable       bool              // true → advance to StateVerifying with agentMD/tools
	message           string            // user-facing message (soft-fail reason OR review prompt)
	agentMD           string            // set only when presentable
	tools             map[string]string // set only when presentable
	thinProof         bool              // advanced without a clean [TEST_OUTPUT] marker
	hasAuthoredScript bool              // coder wrote a non-seeded tools/*.py of its own
	hardErr           error             // a real Go error (e.g. tools unreadable) — abort the run
	logReason         string            // server-side detail for a soft failure (never shown to user)
}

// decideBuildOutcome inspects what the coder wrote to workDir plus its raw output and
// decides whether the build can be shown to the user for approval. It stays in
// StateDesigning (presentable=false) on genuine hard failures: no AGENT.md, an
// ethics-blocked AGENT.md, or a tool that fails guardrails.
//
// Verification strictness is capability-gated. On a capable CLI coder (or a basic model)
// it advances to review even without a clean [TEST_OUTPUT], tolerating the missing marker
// via the prose fallback — that path reliably self-runs its scripts, so a missing marker
// is a formatting slip, not a broken build. But a weak tool-calling API coder
// (BackendToolCalling) that authored its OWN helper script and produced no confirmed test
// run is NOT presented as done: we stay in StateDesigning so the coder keeps iterating
// rather than shipping a script we never saw work. A pure-reasoning build (no authored
// script) has nothing to break, so it still advances on that backend too.
//
// It performs NO cleanup, state mutation, or logging, so it is pure enough to unit-test.
func decideBuildOutcome(workDir, resultText, backendType string) buildDecision {
	// Ground truth: read what the coder actually wrote to disk.
	agentMDBytes, err := os.ReadFile(filepath.Join(workDir, "AGENT.md"))
	if err != nil {
		return buildDecision{message: "The coder didn't create AGENT.md. Tell me what to change and I'll try again."}
	}
	agentMD := strings.TrimSpace(string(agentMDBytes))

	tools, err := readToolsFromDisk(workDir)
	if err != nil {
		return buildDecision{hardErr: fmt.Errorf("read tools: %w", err)}
	}

	if err := CheckEthics(agentMD, ""); err != nil {
		return buildDecision{
			message:   "That request tripped a safety check I can't get around. Try describing it differently — for example, avoid destructive or high-risk actions (deleting things in bulk, financial transfers, credentials handling).",
			logReason: "agent failed safety checks: " + err.Error(),
		}
	}
	hasAuthoredScript := false
	for filename, code := range tools {
		if composioassets.IsSeededFilename(filepath.Base(filename)) {
			// Go-authored, deterministically seeded — nothing to vet. Guarding here also
			// means a coder that (against instructions) rewrote the file doesn't get a
			// pass; WriteHelperFiles re-seeds the verified version next generation anyway.
			// A seeded helper is NOT a coder-authored script, so it must not trip the
			// weak-backend verification gate below (else a pure-reasoning Composio agent
			// that only uses the seeded helper would be falsely blocked).
			continue
		}
		hasAuthoredScript = true
		if err := RunToolGuardrails(filename, code); err != nil {
			return buildDecision{
				message:   "One of the files the build produced didn't pass an internal check, so I held off saving it. Type **approve** to have it rebuilt, or tell me what to change.",
				logReason: "generated tool failed guardrails: " + filename + ": " + err.Error(),
			}
		}
	}

	// Hard gates passed. Prefer a clean [TEST_OUTPUT] as the review sample; fall back to
	// whatever user-facing prose the coder produced so we still advance.
	testOut := parseTestOutput(resultText)
	thinProof := false
	if testOut == "" {
		testOut = generationPreviewFallback(resultText)
		thinProof = true
	}
	if testOut == "" {
		testOut = "(No sample output was captured.)"
		thinProof = true
	}

	// Weak-backend verification gate: a tool-calling API coder that wrote its own helper
	// script but produced no confirmed test run must NOT be presented as done — the API
	// engine's build-time self-verify (verifyFinishNudge) topped out, so we have no proof
	// the script runs. Stay in StateDesigning and let it keep iterating (the caller records
	// a retry note). Capable CLI / basic backends keep their lenient advance; a build with
	// no authored script has nothing unverified to hold back.
	if backendType == prompts.BackendToolCalling && hasAuthoredScript && thinProof {
		return buildDecision{
			hasAuthoredScript: true,
			message: "I built this, but on your configured model I couldn't confirm the helper it " +
				"wrote actually runs — so I won't save it as working yet. Say **keep going** and " +
				"I'll take another pass (I'll try a simpler approach), or tell me to keep it as-is.",
			logReason: "weak backend (tool-calling) authored a script with no confirmed test run",
		}
	}

	var message string
	if thinProof {
		// Be HONEST: passing safety checks is not the same as being verified to work.
		// Do not imply it runs — invite the user to look it over before saving.
		message = fmt.Sprintf(
			"I built the assistant and it passed the safety checks, but I couldn't capture a clean test run — so I haven't confirmed it works end to end yet. Here's what it produced:\n\n---\n%s\n---\n\nPlease look it over. Type **approve** to save it, or tell me what to change.",
			testOut,
		)
	} else {
		message = fmt.Sprintf(
			"Here's what a test run produces:\n\n---\n%s\n---\n\nDoes this look right? Type **approve** to save the agent, or tell me what to change.",
			testOut,
		)
	}
	return buildDecision{presentable: true, message: message, agentMD: agentMD, tools: tools, thinProof: thinProof, hasAuthoredScript: hasAuthoredScript}
}

// cleanupTestArtifacts removes downloaded files, run outputs, scratch probes, and caches
// left in agentDir by the coder's real end-to-end test step, so only the agent's own
// source remains on disk after the build. Called post-save (after user approves) so
// artifacts persist through StateVerifying as proof for the user, then are cleaned up once
// the agent is persisted. Uses isTestArtifact (toolstree.go) as the shared classifier.
// Also removes root-level scratch .json files (e.g. acc.json, probe.json) that are direct
// children of agentDir but are not state.json or agent.json.
func cleanupTestArtifacts(agentDir string) {
	toolsDir := filepath.Join(agentDir, "tools")
	absAgentDir, _ := filepath.Abs(agentDir)
	_ = filepath.Walk(agentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != agentDir {
				name := info.Name()
				if name == "__pycache__" || name == ".pytest_cache" || strings.HasPrefix(name, ".") {
					_ = os.RemoveAll(path)
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := info.Name()
		if isTestArtifact(path, name, toolsDir) {
			_ = os.Remove(path)
			return nil
		}
		// Root-level scratch .json (e.g. acc.json, probe.json) — direct children of the
		// agent dir only; files in subdirs (tools/, notes/, logs/) are unaffected.
		if filepath.Ext(name) == ".json" && name != "state.json" && name != "agent.json" {
			parent, err2 := filepath.Abs(filepath.Dir(path))
			if err2 == nil && parent == absAgentDir {
				_ = os.Remove(path)
			}
		}
		return nil
	})
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

// generationPreviewFallback derives a user-facing preview from the coder's raw
// generation output when it did NOT emit a clean [TEST_OUTPUT]…[/TEST_OUTPUT]
// block. It strips every agent-protocol / spec marker ([TECHNICAL SPEC], [STATE],
// [CALL], [SILENT], [BLOCKED], stray [TEST_OUTPUT]/[CHAT] markers) and returns the
// remaining prose — favouring [CHAT] content the coder produced, so the user still
// sees something reviewable instead of being bounced into a rebuild loop. Returns ""
// if nothing meaningful remains. Length-capped so a verbose reasoning trace can't be
// dumped at the user.
func generationPreviewFallback(text string) string {
	// Prefer explicit [CHAT] content if any is present — that's the coder's own
	// user-facing message and the closest thing to a sample output.
	var chat []string
	for _, seg := range strings.Split(text, "[CHAT]") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		// Cut a [CHAT] segment at the next protocol marker on its own line.
		for _, marker := range []string{"[STATE]", "[CALL:", "[SILENT]", "[TEST_OUTPUT]", "[TECHNICAL SPEC]", "[/CHAT]"} {
			if i := strings.Index(seg, marker); i >= 0 {
				seg = strings.TrimSpace(seg[:i])
			}
		}
		if seg != "" {
			chat = append(chat, seg)
		}
	}

	var cleaned string
	if len(chat) > 0 {
		cleaned = strings.Join(chat, "\n")
	} else {
		// No [CHAT] at all — strip markers from the whole body and keep the prose.
		body := text
		for _, pair := range [][2]string{
			{"[TECHNICAL SPEC]", "[/TECHNICAL SPEC]"},
			{"[TEST_OUTPUT]", "[/TEST_OUTPUT]"},
			{"[STATE]", "[/STATE]"},
			{"[BLOCKED]", "[/BLOCKED]"},
		} {
			body = stripBlock(body, pair[0], pair[1])
		}
		var kept []string
		for _, line := range strings.Split(body, "\n") {
			t := strings.TrimSpace(line)
			if t == "[SILENT]" || strings.HasPrefix(t, "[CALL:") ||
				t == "[TEST_OUTPUT]" || t == "[/TEST_OUTPUT]" || strings.HasPrefix(t, "[TECHNICAL SPEC]") {
				continue
			}
			kept = append(kept, line)
		}
		cleaned = strings.TrimSpace(strings.Join(kept, "\n"))
	}

	for strings.Contains(cleaned, "\n\n\n") {
		cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	}
	cleaned = strings.TrimSpace(cleaned)
	const maxPreview = 2000
	if len(cleaned) > maxPreview {
		cleaned = cleaned[:maxPreview] + "\n…"
	}
	return cleaned
}

// stripBlock removes every open…close delimited block (inclusive) from s. Tolerant
// of an unterminated open marker (drops to end of string).
func stripBlock(s, open, close string) string {
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		rest := s[i+len(open):]
		j := strings.Index(rest, close)
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + rest[j+len(close):]
	}
}

// readToolsFromDisk reads the full project tree under agentDir/tools/ (nested
// dirs, tests, requirements.txt and other non-.py files) and returns it as a
// relpath→content map. See ReadToolsTree for the include/exclude and size rules.
func readToolsFromDisk(agentDir string) (map[string]string, error) {
	return ReadToolsTree(filepath.Join(agentDir, "tools"))
}

// finalizeAgent saves the pending agent content and cleans up the session.
// Called from stepVerifying when the user approves the test output.
func (f *Flow) finalizeAgent(ctx context.Context, workspaceID string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	agentMD := sess.PendingAgentMD
	tools := sess.PendingTools
	isEdit := sess.IsEdit
	f.mu.Unlock()

	var resp string
	var done bool
	var agentID string
	var err error
	if isEdit {
		resp, done, agentID, err = f.updateAndFinish(ctx, workspaceID, agentMD, tools)
	} else {
		resp, done, agentID, err = f.saveAndFinish(ctx, workspaceID, agentMD, tools)
	}
	// On a successful save the agent is persisted — drop the draft so the resume
	// prompt never reappears for an already-created/updated agent.
	if err == nil {
		f.deleteDraft(workspaceID)
	}
	return resp, done, agentID, err
}

// saveAndFinish writes a brand-new agent to disk/DB and terminates the session.
func (f *Flow) saveAndFinish(ctx context.Context, workspaceID, agentMD string, tools map[string]string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	agentIDSnap := sess.AgentID
	agentNameSnap := sess.AgentName
	skillRefs := sess.Skills
	f.mu.Unlock()

	requiredSecrets := parseRequiredSecrets(agentMD)
	description := extractDescription(agentMD, agentNameSnap)

	skillsSnap := parseSkillsLine(agentMD, skillRefs)
	if skillsSnap == nil {
		// No "# Skills:" line in AGENT.md → the agent declared no skills. Leave
		// the manifest empty (the user can assign skills on the agent page).
		// Previously this fell back to ALL installed skills, which polluted the
		// manifest and made the agent page show every skill as assigned.
		skillsSnap = []string{}
	}

	if err := f.designer.SaveAgent(workspaceID, agentIDSnap, agentNameSnap, description, agentMD, tools, skillsSnap, requiredSecrets); err != nil {
		return "", false, "", fmt.Errorf("save agent: %w", err)
	}

	// Remove test artifacts (downloaded files, scratch probes, run outputs) from the live
	// agent dir now that the agent is saved. Artifacts persist through StateVerifying so
	// the user can see real test output as proof; this is the post-approval cleanup.
	cleanupTestArtifacts(AgentDir(f.designer.agentsDir, workspaceID, agentIDSnap))

	// Auto-create schedule if coder embedded a suggested cron expression.
	scheduleMsg := ""
	if cronExpr := parseSuggestedSchedule(agentMD); cronExpr != "" && f.db != nil {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if sched, err := parser.Parse(cronExpr); err == nil {
			nextRun := sched.Next(time.Now())
			_ = f.db.UpsertAgentSchedule(&db.AgentSchedule{
				ID:          uuid.New().String(),
				AgentID:     agentIDSnap,
				WorkspaceID: workspaceID,
				CronExpr:    cronExpr,
				NextRunAt:   &nextRun,
				Enabled:     true,
			})
			scheduleMsg = fmt.Sprintf(" Schedule set: %s.", cronExpr)
		}
	}

	f.mu.Lock()
	delete(f.sessions, workspaceID)
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
func (f *Flow) updateAndFinish(ctx context.Context, workspaceID, agentMD string, tools map[string]string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	agentIDSnap := sess.AgentID
	agentNameSnap := sess.AgentName
	skillRefs := sess.Skills
	f.mu.Unlock()

	requiredSecrets := parseRequiredSecrets(agentMD)
	description := extractDescription(agentMD, agentNameSnap)

	skillsSnap := parseSkillsLine(agentMD, skillRefs)
	if skillsSnap == nil {
		// No "# Skills:" line in AGENT.md → the agent declared no skills.
		skillsSnap = []string{}
	}

	if err := f.designer.UpdateAgent(workspaceID, agentIDSnap, agentNameSnap, description, agentMD, tools, skillsSnap, requiredSecrets); err != nil {
		return "", false, "", fmt.Errorf("update agent: %w", err)
	}

	// Remove any test artifacts left in the live agent dir post-save. For edits the
	// staging dir is already gone; this cleans root-level scratch from the live dir.
	cleanupTestArtifacts(AgentDir(f.designer.agentsDir, workspaceID, agentIDSnap))

	scheduleMsg := reconcileScheduleOnSave(f.db, workspaceID, agentIDSnap, agentMD)

	f.mu.Lock()
	delete(f.sessions, workspaceID)
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
func reconcileScheduleOnSave(database dbDesignStore, workspaceID, agentID, agentMD string) string {
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
			ID:          schedID,
			AgentID:     agentID,
			WorkspaceID: workspaceID,
			CronExpr:    cronExpr,
			NextRunAt:   &nextRun,
			Enabled:     true,
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
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.TrimRight(s, ".!?,;:)")
	for _, trigger := range []string{"approve", "go ahead", "build it", "create it", "/approve"} {
		if s == trigger {
			return true
		}
	}
	return false
}

// isVerifyApproval is the forgiving approval test used in StateVerifying, where
// the agent is already built and the user is only confirming whether to save it.
// Common confirmations ("yes", "save", "ok", "looks good") count so a natural
// reply saves the build instead of being treated as a change request that
// discards it. A negative cue ("don't", "not yet", "change", "wait", "instead")
// means the user wants changes, not approval. isApproval (used in StateDesigning)
// stays strict by design so a casual "ok"/"yes" while answering design questions
// doesn't launch a full generation run.
func isVerifyApproval(input string) bool {
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.TrimRight(s, ".!?,;:)")
	if strings.Contains(s, "don't") || strings.Contains(s, "do not") ||
		strings.Contains(s, "not yet") || strings.Contains(s, "change") ||
		strings.Contains(s, "wait") || strings.Contains(s, "instead") {
		return false
	}
	for _, trigger := range []string{
		"approve", "go ahead", "build it", "create it", "/approve",
		"yes", "save", "save it", "ok", "okay", "looks good", "looks good to me",
		"confirm", "confirmed", "go", "do it", "ship it", "lgtm", "perfect", "great",
	} {
		if s == trigger {
			return true
		}
	}
	return false
}

// isRetryApproval is the MOST forgiving approval test, used only right after a generation
// failed (DesignSession.GenerationFailed) to re-run the build. It accepts everything
// isVerifyApproval does PLUS natural retry phrases ("try again", "fix it", "retry") which
// weak-model users reach for after a failure — the exact phrases the strict isApproval
// rejected, trapping the user in an approve-loop. The same negative-cue guard applies, and
// crucially "change" still routes to the design chat (a change request is not a bare retry),
// so "change X then try again" refines the design instead of blindly re-running.
func isRetryApproval(input string) bool {
	if isVerifyApproval(input) {
		return true
	}
	s := strings.ToLower(strings.TrimSpace(input))
	if strings.Contains(s, "don't") || strings.Contains(s, "do not") ||
		strings.Contains(s, "not yet") || strings.Contains(s, "change") ||
		strings.Contains(s, "wait") || strings.Contains(s, "instead") {
		return false
	}
	for _, phrase := range []string{"try again", "try it again", "fix it", "retry", "another try", "give it another", "have another go"} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}

// ─── Skills helpers ───────────────────────────────────────────────────────────

func (f *Flow) loadSkillNames(workspaceID string) []prompts.SkillRef {
	// Core skills are always-on for every user — always available to the designer.
	refs := make([]prompts.SkillRef, 0, 16)
	for _, s := range skilllibrary.LoadBundled() {
		refs = append(refs, prompts.SkillRef{Name: s.Name, Description: s.Description})
	}
	if f.db == nil {
		return refs
	}
	skills, _ := f.db.ListSkills(workspaceID)
	for _, s := range skills {
		refs = append(refs, prompts.SkillRef{Name: s.Name, Description: s.Description})
	}
	return refs
}

// parseSkillsLine reads a "# Skills: skill1, skill2" header comment from AGENT.md.
// It validates names against the installed set and returns only known names.
// Returns nil if the line is absent (caller falls back to all installed skills).
// parseSkillsLine extracts the skills the coder declared for an agent from its
// AGENT.md. It is deliberately tolerant of LLM formatting drift: the coder is
// asked to emit a `# Skills: a, b` header, but models often vary the spelling,
// the heading level, the delimiter, the casing, or wrap names in backticks/quotes
// — and sometimes put the line after a blank line or render it as a bullet list.
//
// It returns the validated canonical skill names (matched case-insensitively
// against the installed pool). It returns nil ONLY when no skills header is found
// at all (caller treats nil as "declared none"). When a header is found, it
// returns a non-nil slice (empty if the header said "none" or no names matched).
//
// Unknown names are dropped (with a warning log) rather than failing — a
// hallucinated or misspelled skill name attaches nothing instead of poisoning
// the agent.
func parseSkillsLine(agentMD string, installed []prompts.SkillRef) []string {
	byLower := make(map[string]string, len(installed))
	for _, s := range installed {
		byLower[strings.ToLower(s.Name)] = s.Name
	}
	if len(byLower) == 0 {
		return nil
	}

	lines := strings.Split(agentMD, "\n")
	for i, line := range lines {
		if rest, ok := skillsHeaderInline(line); ok {
			names := matchSkillNames(splitSkillCandidates(rest), byLower)
			if len(names) > 0 {
				return names
			}
			// Header present but no inline names (e.g. "# Skills:") — gather the
			// bullet/numbered list that usually follows it.
			return matchSkillNames(bulletCandidates(lines[i+1:]), byLower)
		}
		if skillsListHeading(line) {
			return matchSkillNames(bulletCandidates(lines[i+1:]), byLower)
		}
	}
	return nil
}

// skillsHeaderInline matches a line that declares skills inline, e.g.
// "# Skills: csv, pdf", "## skills - csv and pdf", "Required skills: csv",
// "Skill: csv". It returns the text after the separator and true, or "", false.
// Scanning is case-insensitive and tolerates 0-6 heading hashes, an optional
// qualifier (required/needed/uses/used), and a ':', '-', or '=' separator.
var skillsInlineRe = regexp.MustCompile(`(?i)^\s*#{0,6}\s*(?:required\s+|needed\s+|uses?\s+)?skills?\b\s*[:\-=]\s*(.*)$`)

func skillsHeaderInline(line string) (string, bool) {
	m := skillsInlineRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// skillsListHeading matches a bare heading that introduces a bullet list of
// skills, e.g. "## Skills" with no separator.
var skillsListHeadingRe = regexp.MustCompile(`(?i)^\s*#{0,6}\s*(?:required\s+|needed\s+|uses?\s+)?skills?\s*$`)

func skillsListHeading(line string) bool {
	return skillsListHeadingRe.MatchString(line)
}

// bulletCandidates collects the text of bullet/numbered list items from the lines
// immediately following a skills heading, stopping at the first non-list line.
var bulletItemRe = regexp.MustCompile(`^\s*(?:[-*+]|\d+\.)\s+(.*)$`)

func bulletCandidates(lines []string) []string {
	var out []string
	for _, line := range lines {
		m := bulletItemRe.FindStringSubmatch(line)
		if m == nil {
			break // blank line or prose ends the list
		}
		out = append(out, m[1])
	}
	return out
}

// splitSkillCandidates splits the inline skill list on the delimiters the LLM
// tends to use: comma, semicolon, pipe, newline, " and ", " or ", "&", "+", "/".
// Bare spaces are NOT separators — skill names can contain spaces
// (e.g. "Google Workspace") — so "csv pdf" with no comma is left as one
// (non-matching) token rather than mis-splitting a multi-word name.
var skillSepRe = regexp.MustCompile(`(?i)\s*,\s*|\s*;\s*|\s*\|\s*|\s*\+\s*|\s*&\s*|\s*/\s*|\s+and\s+|\s+or\s+|\n`)

func splitSkillCandidates(rest string) []string {
	return skillSepRe.Split(rest, -1)
}

// matchSkillNames normalises each candidate (strips markdown emphasis/quotes,
// truncates trailing prose like "(for …)" or "— used for …"), lowercases it, and
// returns the canonical installed names that match. Unknown names are dropped
// with a warning. Always returns a non-nil slice (empty if nothing matched).
func matchSkillNames(candidates []string, byLower map[string]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, c := range candidates {
		c = cutSkillProse(c)
		c = strings.Trim(c, " \t`'\"*._-")
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		low := strings.ToLower(c)
		if low == "none" || low == "n/a" || low == "na" {
			continue
		}
		canon, ok := byLower[low]
		if !ok {
			slog.Warn("agentdesigner: skill name in # Skills line did not match any installed skill", "name", c)
			continue
		}
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	return out
}

// cutSkillProse truncates a skill candidate at the first trailing prose marker
// so "pdf (for extracting text)" → "pdf" and "csv — used for parsing" → "csv".
func cutSkillProse(s string) string {
	for _, ch := range "([←—–" {
		if idx := strings.IndexRune(s, ch); idx >= 0 {
			s = s[:idx]
		}
	}
	if idx := strings.Index(s, " //"); idx >= 0 {
		s = s[:idx]
	}
	return s
}

func (f *Flow) loadConnectedPlatforms(workspaceID string) []string {
	if f.db == nil {
		return nil
	}
	conns, _ := f.db.ListWorkspacePlatformConnections(workspaceID)
	out := make([]string, 0, len(conns))
	for _, c := range conns {
		out = append(out, c.Platform)
	}
	return out
}

// loadUserProfile returns the rendered "[User profile]" context block for
// workspaceID, or "" if no db is attached or no profile fields are set.
func (f *Flow) loadUserProfile(workspaceID string) string {
	if f.db == nil {
		return ""
	}
	return profile.Load(f.db, workspaceID).ContextString()
}

// loadComposioEnabled returns true if the user has stored a COMPOSIO_API_KEY secret.
// Uses a presence-only check so no master password is required.
func (f *Flow) loadComposioEnabled(workspaceID string) bool {
	if f.db == nil {
		return false
	}
	ok, _ := f.db.SecretExists(workspaceID, "COMPOSIO_API_KEY")
	return ok
}

// loadUserMemory returns saved memory entries as a bullet list, or "" if none.
func (f *Flow) loadUserMemory(workspaceID string) string {
	if f.memStore == nil {
		return ""
	}
	mem, _ := f.memStore.ContextString(workspaceID)
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
