package agentdesigner

import (
	"context"
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
	"github.com/robfig/cron/v3"
)

// DesignState is the current step in the conversational agent creation wizard.
type DesignState int

const (
	StateIdle       DesignState = iota
	StateDescribing             // Telegram: waiting for description after /agent create <name>
	StateDesigning              // free-form Q&A until user says "approve"
	StateVerifying              // test run shown; waiting for user to confirm or request changes
	StateDone
)

func (s DesignState) String() string {
	switch s {
	case StateIdle:
		return "idle"
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
	CreatedAt          time.Time

	// IsEdit distinguishes an edit-of-existing-agent session from a fresh create.
	// AgentID is the *existing* agent's ID (not a freshly minted one) when true.
	IsEdit bool

	// ExistingAgentMD holds the live agent's AGENT.md, reconciled with the real DB
	// schedule, as of session start. Used to seed the staging workspace during
	// generation. Empty for create sessions.
	ExistingAgentMD string

	// Set after generation; cleared on finalize or when user requests changes.
	PendingAgentMD string
	PendingTools   map[string]string
}

type dbDesignStore interface {
	ListSkills(userID string) ([]*db.Skill, error)
	ListUserPlatformConnections(userID string) ([]*db.PlatformConnection, error)
	UpsertAgentSchedule(s *db.AgentSchedule) error
	GetAgent(id string) (*db.Agent, error)
	GetScheduleForAgent(agentID string) (*db.AgentSchedule, error)
	DeleteAgentSchedule(agentID string) error
	GetSetting(userID, key string) (string, error)
}

// Flow manages per-user design sessions and drives the FSM.
// It is safe for concurrent use.
type Flow struct {
	mu       sync.Mutex
	sessions map[string]*DesignSession // keyed by userID

	coderFor func(userID string) *coder.Coder
	designer *AgentDesigner
	db       dbDesignStore
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
	f.sessions[userID] = &DesignSession{
		UserID:             userID,
		AgentID:            uuid.New().String(),
		AgentName:          agentName,
		State:              StateDescribing,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
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
	sess := &DesignSession{
		UserID:             userID,
		AgentID:            uuid.New().String(),
		AgentName:          agentName,
		State:              StateDesigning,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
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

	agentName, reconciledMD, err := f.loadAgentForEdit(userID, agentID)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	userProfile := f.loadUserProfile(userID)
	f.sessions[userID] = &DesignSession{
		UserID:             userID,
		AgentID:            agentID,
		AgentName:          agentName,
		State:              StateDescribing,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
		CreatedAt:          time.Now(),
		IsEdit:             true,
		ExistingAgentMD:    reconciledMD,
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

	agentName, reconciledMD, err := f.loadAgentForEdit(userID, agentID)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	userProfile := f.loadUserProfile(userID)
	sess := &DesignSession{
		UserID:             userID,
		AgentID:            agentID,
		AgentName:          agentName,
		State:              StateDesigning,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		UserProfile:        userProfile,
		CreatedAt:          time.Now(),
		IsEdit:             true,
		ExistingAgentMD:    reconciledMD,
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
func (f *Flow) loadAgentForEdit(userID, agentID string) (agentName, reconciledMD string, err error) {
	if f.db == nil {
		return "", "", fmt.Errorf("no database configured")
	}

	agent, err := f.db.GetAgent(agentID)
	if err != nil {
		return "", "", fmt.Errorf("agent not found: %w", err)
	}

	raw, err := os.ReadFile(AgentDescPath(f.designer.agentsDir, userID, agentID))
	if err != nil {
		return "", "", fmt.Errorf("read AGENT.md: %w", err)
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

	return agent.Name, agentMD, nil
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

// Cancel removes the user's active session without saving.
func (f *Flow) Cancel(userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, userID)
}

// GetSession returns the user's active session, or nil.
func (f *Flow) GetSession(userID string) *DesignSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[userID]
}

// ─── FSM step handlers ────────────────────────────────────────────────────────

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

	systemPrompt := f.buildSystemPrompt(sess)

	// Use WithNoTools so the design conversation outputs plain text and never
	// attempts to write files or request permissions.
	result, err := coderSvc.WithNoTools().Chat(ctx, userID, sess.History, systemPrompt, userMessage)
	if err != nil {
		return "", fmt.Errorf("coder: %w", err)
	}

	f.mu.Lock()
	sess.History = append(sess.History,
		db.ChatMessage{Role: "user", Content: userMessage},
		db.ChatMessage{Role: "assistant", Content: result.Text},
	)
	f.mu.Unlock()

	return result.Text, nil
}

func (f *Flow) buildSystemPrompt(sess *DesignSession) string {
	var sb strings.Builder
	if sess.IsEdit {
		sb.WriteString("You are a friendly agent design assistant helping the user EDIT an existing autonomous AI agent called \"")
		sb.WriteString(sess.AgentName)
		sb.WriteString("\".\n\nHere is its current AGENT.md:\n```\n")
		sb.WriteString(sess.ExistingAgentMD)
		sb.WriteString("\n```\n\nFind out exactly what the user wants to change, then propose an updated plan (same format as a new build: behavior, secrets, schedule). Only ask about parts that are actually changing — don't re-litigate things the user didn't mention.\n\n")
	} else {
		sb.WriteString("You are a friendly agent design assistant helping build an autonomous AI agent called \"")
		sb.WriteString(sess.AgentName)
		sb.WriteString("\".\n\n")
	}

	// Connected platform context.
	if len(sess.ConnectedPlatforms) > 0 {
		sb.WriteString("CONNECTED PLATFORMS:\n")
		sb.WriteString("The user has connected: ")
		sb.WriteString(strings.Join(sess.ConnectedPlatforms, ", "))
		sb.WriteString(`
When the user says "send to Telegram", "notify me", "post a message", or similar, they mean: emit [CHAT] text in your output — the system automatically routes it to their connected platform. No bot token, chat ID, or messaging credentials of any kind are needed or should be requested.

`)
	} else {
		sb.WriteString(`CONNECTED PLATFORMS:
No chat platform is currently connected. If the agent needs to send notifications, mention that the user can connect Telegram from Settings → Connectors in the web dashboard. Agents still use [CHAT] output for this — no credentials needed from the user.

`)
	}

	if len(sess.Skills) > 0 {
		sb.WriteString("INSTALLED SKILLS (can be used by this agent): ")
		sb.WriteString(strings.Join(sess.Skills, ", "))
		sb.WriteString("\n\n")
	}

	if sess.UserProfile != "" {
		sb.WriteString(sess.UserProfile)
		sb.WriteString("\n")
	}

	sb.WriteString(`SECRETS (API keys and credentials):
When the agent needs an API key or credential:
- Tell the user to add it to the Secrets store in the web dashboard (Settings → Secrets) using a clear name like COINGECKO_API_KEY.
- Do NOT ask the user to paste secret values in this chat — that would expose them.
- Explain in plain language what the credential is and where to get it (e.g. "You'll need a free CoinGecko API key — sign up at coingecko.com/en/api and generate one under Developer Dashboard").
- Secrets are injected automatically as environment variables when the agent runs. Reference them by name only (e.g. $COINGECKO_API_KEY). Never display values.

SCHEDULING:
- If the user mentions a frequency ("every 10 minutes", "daily at 8am", "once a week"), note it and include the schedule in your proposal: "This agent will run every 10 minutes (cron: */10 * * * *)."
- If no frequency is mentioned and the agent seems like it should recur (e.g. a price monitor), ask the user how often it should run before proposing.
- Do NOT suggest cron jobs, systemd timers, or any external scheduling — the system has a built-in scheduler.

YOUR JOB:
Have a focused conversation to fully understand what the agent should do. Then propose what you will build.
1. Ask focused questions: data sources, any APIs needed, what to do with the data, frequency.
2. When you have a clear picture, propose your implementation plan and explicitly list:
   - What the agent will do each run
   - Any required secrets (name + plain-language explanation + where to get it)
   - The run schedule (frequency and cron expression)
3. Tell the user to type "approve" when they're happy with the proposal.

STYLE:
- Assume the user may not be technical. Explain API keys, environment variables, and cron expressions in plain language when they come up. Do not use jargon without explanation.
- Ask one or two focused questions per turn — not a list of ten.
- Guide the user through setup steps where needed (e.g. "go to coingecko.com/en/api, click 'Get Free API Key'...").
- Do not write code or generate files yet — that happens after approval.

HARD CONSTRAINTS (never violate):
- Never ask for Telegram bot token, chat ID, or any messaging credentials.
- Never suggest writing files to the home directory or any disk path.
- Never suggest cron job setup or any external scheduling tool.
- Always use [CHAT] as the only way to send messages to the user.
`)

	return sb.String()
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
	f.mu.Unlock()

	if coderSvc == nil {
		return "", false, "", fmt.Errorf("no coder configured for this user")
	}

	var workDir, prompt string
	var cleanupOnFail, cleanupOnSuccess func()

	if isEdit {
		// Edit mode: never let the coder touch the live agent dir before approval —
		// it may be scheduled and running unattended. Generate against a sibling
		// staging copy instead; the live dir is only overwritten in finalizeAgent.
		liveDir := filepath.Join(f.designer.agentsDir, userID, agentIDSnap)
		stagingDir := liveDir + "-edit-staging"
		if err := copyAgentWorkspace(liveDir, stagingDir, existingAgentMD); err != nil {
			return "", false, "", fmt.Errorf("prepare staging workspace: %w", err)
		}
		workDir = stagingDir
		prompt = buildEditImplementationPrompt(agentNameSnap, historySnap)
		remove := func() { _ = os.RemoveAll(stagingDir) }
		cleanupOnFail, cleanupOnSuccess = remove, remove
	} else {
		// Create the agent directory structure on disk before the coder runs so it
		// has a clean workspace to write into.
		agentDir := filepath.Join(f.designer.agentsDir, userID, agentIDSnap)
		for _, sub := range []string{".", "tools", "logs"} {
			if err := os.MkdirAll(filepath.Join(agentDir, sub), 0o750); err != nil {
				return "", false, "", fmt.Errorf("create agent dir: %w", err)
			}
		}
		if err := os.WriteFile(filepath.Join(agentDir, "state.json"), []byte("{}"), 0o640); err != nil {
			return "", false, "", fmt.Errorf("write state.json: %w", err)
		}
		workDir = agentDir
		prompt = buildImplementationPrompt(agentNameSnap, historySnap)
		cleanupOnFail = func() { _ = os.RemoveAll(agentDir) }
		cleanupOnSuccess = func() {} // the dir IS the pending agent; keep it until finalize/iterate
	}

	// Run the coder WITH full tools so it can write files, execute them, debug
	// errors, and confirm the implementation works — all in one session.
	// WithAllowedTools pre-approves the specific tools needed so the subprocess
	// never blocks on interactive permission prompts.
	result, err := coderSvc.WithDir(workDir).WithAllowedTools("Bash,Write,Edit,Read").Generate(ctx, userID, prompt)
	if err != nil {
		cleanupOnFail()
		return "", false, "", fmt.Errorf("coder: %w", err)
	}

	// Ground truth: read what the coder actually wrote to disk.
	agentMDBytes, err := os.ReadFile(filepath.Join(workDir, "AGENT.md"))
	if err != nil {
		cleanupOnFail()
		return "The coder didn't create AGENT.md. Tell me what to change and I'll try again.", false, "", nil
	}
	agentMD := strings.TrimSpace(string(agentMDBytes))

	tools, err := readToolsFromDisk(workDir)
	if err != nil {
		cleanupOnFail()
		return "", false, "", fmt.Errorf("read tools: %w", err)
	}

	// Guardrails on the actual content the coder wrote.
	if err := CheckEthics(agentMD, ""); err != nil {
		cleanupOnFail()
		return fmt.Sprintf("Agent failed safety checks: %s\n\nPlease rephrase.", err.Error()), false, "", nil
	}
	for filename, code := range tools {
		if err := RunFullGuardrails(code, ""); err != nil {
			cleanupOnFail()
			return fmt.Sprintf("Tool %s failed safety checks: %s\n\nPlease rephrase.", filename, err.Error()), false, "", nil
		}
	}

	// Content is captured in memory now — discard the workspace (staging dir for
	// edits; create mode keeps its pending dir on disk until finalize/iterate).
	cleanupOnSuccess()

	// Store verified content in session and wait for user confirmation.
	testOut := parseTestOutput(result.Text)

	f.mu.Lock()
	sess = f.sessions[userID]
	sess.State = StateVerifying
	sess.PendingAgentMD = agentMD
	sess.PendingTools = tools
	f.mu.Unlock()

	if testOut == "" {
		return "The agent was built and tested successfully — no output messages were sent, which is expected if the agent only updates its internal state.\n\n" +
			"Type **approve** to save it, or tell me what to change.", false, "", nil
	}
	return fmt.Sprintf(
		"Here's what a test run produces:\n\n---\n%s\n---\n\nDoes this look right? Type **approve** to save the agent, or tell me what to change.",
		testOut,
	), false, "", nil
}

// copyAgentWorkspace creates a fresh staging directory containing the editable
// surface of a live agent: AGENT.md (the reconciled version, not the raw on-disk
// one), state.json, and tools/*.py. Used so an edit's test generation never touches
// the live agent. liveDir's logs/ and agent.json are intentionally not copied —
// the coder doesn't need them to make or test changes.
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

	entries, err := os.ReadDir(filepath.Join(liveDir, "tools"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(liveDir, "tools", e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stagingDir, "tools", e.Name()), data, 0o640); err != nil {
			return err
		}
	}
	return nil
}

// buildImplementationPrompt creates the prompt that tells Claude Code to write
// the agent files, test them, fix errors, and report the verified output.
func buildImplementationPrompt(agentName string, history []db.ChatMessage) string {
	var sb strings.Builder
	sb.WriteString("You are implementing an AI agent called \"")
	sb.WriteString(agentName)
	sb.WriteString("\".\n\n")
	sb.WriteString("DESIGN CONVERSATION:\n")
	for _, m := range history {
		if m.Role == "user" {
			sb.WriteString("User: ")
		} else {
			sb.WriteString("Designer: ")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString(`
YOUR TASK — follow these steps in order:

STEP 1 — CREATE THE AGENT FILES in the current directory.

Write AGENT.md:
- Line 1 MUST be exactly: # Suggested schedule: <5-part cron expression or "none">
- Optional secrets block (omit if no secrets needed):
  # Required secrets:
  # - SECRET_NAME: plain-language description
- Describe what the agent does each run
- Output protocol:
    [CHAT] <text>        — send a message to the user (the ONLY way to send output)
    [STATE]...[/STATE]   — JSON block merged into state.json
- Reference Python helpers as: python3 tools/filename.py

Write tools/<name>.py for data fetching / processing (if needed):
- Allowed: os, json, re, datetime, requests
- Forbidden: subprocess, eval, exec, socket, open() for writing files
- Read secrets via: os.environ.get('SECRET_NAME', '')

Do NOT create or modify state.json — it already exists.

STEP 2 — TEST THE IMPLEMENTATION.

Run each Python script using Bash and confirm it produces real, non-empty output.
If a script errors or returns None/empty, fix it and re-run until it works.

SECRETS: If a required secret is missing from the environment, substitute a
realistic mock value FOR THIS TEST ONLY (e.g. use a public test endpoint or
hard-code an example response). Do NOT abort — show the output format.

STEP 3 — REPORT THE VERIFIED RESULT.

Once everything works, end your final response with:
[TEST_OUTPUT]
<paste the actual terminal output from your test run>
[/TEST_OUTPUT]

HARD CONSTRAINTS — never violate:
- [CHAT] is the ONLY output channel. No Telegram API, no requests to messaging services.
- Never hardcode real credentials — always os.environ.get('NAME', '').
- Never create files outside the agent directory.
- Never set up cron jobs or external schedulers.
- No non-standard Python libraries (requests is fine; pandas, numpy, etc. are not).
`)
	return sb.String()
}

// buildEditImplementationPrompt creates the prompt that tells Claude Code to read
// the existing agent files in the current directory (a staging copy), apply the
// requested changes, test them, fix errors, and report the verified output.
func buildEditImplementationPrompt(agentName string, history []db.ChatMessage) string {
	var sb strings.Builder
	sb.WriteString("You are EDITING an existing AI agent called \"")
	sb.WriteString(agentName)
	sb.WriteString("\". The current directory contains its existing AGENT.md and tools/*.py — this is a safe working copy, not the live agent.\n\n")
	sb.WriteString("EDIT CONVERSATION (what the user wants changed):\n")
	for _, m := range history {
		if m.Role == "user" {
			sb.WriteString("User: ")
		} else {
			sb.WriteString("Designer: ")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString(`
YOUR TASK — follow these steps in order:

STEP 0 — READ THE EXISTING FILES.

Read AGENT.md and every file in tools/ in the current directory to understand what
the agent currently does before changing anything.

STEP 1 — APPLY THE REQUESTED CHANGES.

Edit AGENT.md and tools/*.py to implement what the user asked for in the conversation
above. Keep everything that wasn't asked to change. Delete any tool script that's no
longer needed as a result of the change.

- Line 1 of AGENT.md MUST remain exactly: # Suggested schedule: <5-part cron expression or "none">
  (update it only if the user asked to change how often the agent runs)
- Optional secrets block (omit if no secrets needed):
  # Required secrets:
  # - SECRET_NAME: plain-language description
- Output protocol unchanged:
    [CHAT] <text>        — send a message to the user (the ONLY way to send output)
    [STATE]...[/STATE]   — JSON block merged into state.json
- Reference Python helpers as: python3 tools/filename.py
- Allowed in tools/*.py: os, json, re, datetime, requests
- Forbidden: subprocess, eval, exec, socket, open() for writing files
- Read secrets via: os.environ.get('SECRET_NAME', '')

Do NOT create or modify state.json directly — it already exists and reflects the
agent's real persisted state; let the output protocol's [STATE] block manage it.

STEP 2 — TEST THE IMPLEMENTATION.

Run each Python script using Bash and confirm it produces real, non-empty output.
If a script errors or returns None/empty, fix it and re-run until it works.

SECRETS: If a required secret is missing from the environment, substitute a
realistic mock value FOR THIS TEST ONLY (e.g. use a public test endpoint or
hard-code an example response). Do NOT abort — show the output format.

STEP 3 — REPORT THE VERIFIED RESULT.

Once everything works, end your final response with:
[TEST_OUTPUT]
<paste the actual terminal output from your test run>
[/TEST_OUTPUT]

HARD CONSTRAINTS — never violate:
- [CHAT] is the ONLY output channel. No Telegram API, no requests to messaging services.
- Never hardcode real credentials — always os.environ.get('NAME', '').
- Never create files outside the current directory.
- Never set up cron jobs or external schedulers.
- No non-standard Python libraries (requests is fine; pandas, numpy, etc. are not).
`)
	return sb.String()
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

// readToolsFromDisk reads all .py files from agentDir/tools/ and returns them
// as a filename→content map.
func readToolsFromDisk(agentDir string) (map[string]string, error) {
	toolsDir := filepath.Join(agentDir, "tools")
	entries, err := os.ReadDir(toolsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	result := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(toolsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		result[e.Name()] = string(data)
	}
	return result, nil
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

	if isEdit {
		return f.updateAndFinish(ctx, userID, agentMD, tools)
	}
	return f.saveAndFinish(ctx, userID, agentMD, tools)
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
