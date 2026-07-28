// Package agentrunner loads an agent from disk and executes it via the coder CLI.
// Output [CHAT] lines are routed back to the user.
package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
	"github.com/ilijad1/simple-agents/internal/skillstore"
	"github.com/ilijad1/simple-agents/internal/vault"
)

const (
	maxCallDepth = 3 // maximum agent-to-agent call depth
	maxTurns     = 5 // maximum coder.Generate calls per top-level run
	maxStateSize = 65536
)

// SendFunc delivers a message back to the user's chat platform.
type SendFunc func(msg string)

// RunInput describes one agent execution request.
type RunInput struct {
	AgentID     string
	WorkspaceID string
	Trigger     string // "chat", "cron", "manual"
	MasterPw    string // user's master password for secret decryption
	SendOutput  SendFunc
	// OnProgress, if set, is called once per coder turn with that turn's freshly
	// parsed [CHAT] lines as they arrive — enabling live streaming (e.g. SSE) while
	// the run is still in flight. SendOutput is still called once at the end with the
	// full joined output for durable delivery (Telegram, run history). Optional.
	OnProgress SendFunc
	depth      int             // internal: recursion depth (0 = top-level)
	visited    map[string]bool // internal: cycle detection for agent-to-agent calls
}

// memoryStore is satisfied by *memory.Store — kept local to avoid the import.
type memoryStore interface {
	ContextString(workspaceID string) (string, error)
}

// Runner executes agents.
type Runner struct {
	db        *db.DB
	systemKey []byte
	agentsDir string // vaults base: <data>/vaults/ (agent dirs at <base>/<workspaceID>/agents/<agentID>)
	homesDir  string // per-user home dirs root (for per-user sandbox binding)
	dataDir   string // root data dir (blacklisted inside sandbox)
	coderSvc  *coder.Coder
	// coderFactory, when set, builds the coder for a given workspace honoring the
	// workspace's inlined coder config. When nil (or it returns nil), coderSvc is used.
	coderFactory func(workspaceID string) *coder.Coder
	skillsDir    string           // vaults base: <data>/vaults (skills at <base>/<workspaceID>/skills/<name>)
	memStore     memoryStore      // optional; nil = no memory injected
	reflector    *vault.Reflector // optional; mirrors runs into the user's vault

	// Self-managed OAuth connectors: when set, an agent's bound connections
	// (agent_connections) are exposed to BOTH coder types — API engine via in-process
	// tools, CLI coders via the loopback bridge (simple-agents connector exec).
	connReg    *connectors.Registry
	connStore  connectors.TokenStore
	connBridge *connectors.Bridge
	parkerFor  ParkerFactory

	// kbBridge, when set, lets a CLI coder's agent run reach the knowledge base's
	// conversion + search paths via `simple-agents kb convert|search` (the same
	// vault.ImportFile / Searcher code the API engine's save_to_kb/search_files
	// tools call in-process). nil for tests that don't wire one.
	kbBridge *vault.Bridge
}

// WithConnectors wires the self-managed-OAuth connector registry + token store + loopback
// bridge so an agent's bound service connections are usable by every coder type: the API
// engine calls connectors.Execute in-process; a CLI coder shells out to
// `simple-agents connector exec`, which reaches the same Execute via the bridge.
func (r *Runner) WithConnectors(reg *connectors.Registry, store connectors.TokenStore, bridge *connectors.Bridge) *Runner {
	r.connReg = reg
	r.connStore = store
	r.connBridge = bridge
	return r
}

// ParkerFactory builds the approval gate for one run, or returns nil when this agent
// has no gated binding. A function rather than the service itself so agentrunner does
// not import internal/approval, which imports connectors + db and would make this
// package's tests drag the whole stack in.
type ParkerFactory func(ctx context.Context, workspaceID, agentID, agentName string) connectors.Parker

// WithApprovalGate wires the public_write approval gate. Without it, runs are ungated —
// today's behaviour, and the behaviour of any install that never turns approval on.
func (r *Runner) WithApprovalGate(f ParkerFactory) *Runner {
	r.parkerFor = f
	return r
}

// New creates a Runner.
func New(database *db.DB, systemKey []byte, agentsDir, homesDir, dataDir string, coderSvc *coder.Coder, skillsDir string) *Runner {
	return &Runner{
		db:        database,
		systemKey: systemKey,
		agentsDir: agentsDir,
		homesDir:  homesDir,
		dataDir:   dataDir,
		coderSvc:  coderSvc,
		skillsDir: skillsDir,
	}
}

// WithCoderFactory wires a per-workspace coder factory so each workspace's agent
// runs use that workspace's configured coder (binary + backend + timeout) instead
// of a single shared default. Without it, runs used the system default coder and
// ignored the assignment (the pre-refactor gap this fixes).
func (r *Runner) WithCoderFactory(f func(workspaceID string) *coder.Coder) *Runner {
	r.coderFactory = f
	return r
}

// coderForWorkspace returns the coder to use for a workspace: the factory's result
// when configured, otherwise the shared default.
func (r *Runner) coderForWorkspace(workspaceID string) *coder.Coder {
	if r.coderFactory != nil {
		if c := r.coderFactory(workspaceID); c != nil {
			return c
		}
	}
	return r.coderSvc
}

// WithMemory attaches a memory store so saved user facts are injected into agent run prompts.
func (r *Runner) WithMemory(m memoryStore) *Runner {
	r.memStore = m
	return r
}

// WithVault wires the knowledge base so runs are mirrored into the user's vault.
func (r *Runner) WithVault(v *vault.Vault) *Runner {
	r.reflector = v.Reflector()
	return r
}

// WithKBBridge wires the loopback KB bridge so a CLI coder's agent run can reach
// `simple-agents kb convert|search` (parity with the API engine's built-in
// save_to_kb/search_files host tools).
func (r *Runner) WithKBBridge(b *vault.Bridge) *Runner {
	r.kbBridge = b
	return r
}

// Run executes the agent identified by input.AgentID.
func (r *Runner) Run(ctx context.Context, input RunInput) error {
	if input.visited == nil {
		input.visited = make(map[string]bool)
	}

	agent, err := r.db.GetAgent(input.AgentID)
	if err != nil {
		return fmt.Errorf("load agent: %w", err)
	}
	if agent.WorkspaceID != input.WorkspaceID {
		return fmt.Errorf("agent not found")
	}

	return r.runCoderAgent(ctx, agent, input)
}

// RunByName looks up an agent by name and runs it.
func (r *Runner) RunByName(ctx context.Context, workspaceID, agentName, masterPw string, send SendFunc) error {
	agent, err := r.db.GetAgentByName(workspaceID, agentName)
	if err != nil {
		return fmt.Errorf("agent %q not found", agentName)
	}
	return r.Run(ctx, RunInput{
		AgentID:     agent.ID,
		WorkspaceID: workspaceID,
		Trigger:     "chat",
		MasterPw:    masterPw,
		SendOutput:  send,
	})
}

// TestRunFromContent executes agentMD content once from a temp directory and
// returns the joined [CHAT] lines. Used by the agent designer to verify an agent
// works before committing it to disk/DB. Returns ("", nil) if the agent produces
// no [CHAT] output; returns ("", err) if the coder subprocess fails.
func (r *Runner) TestRunFromContent(ctx context.Context, workspaceID, agentMD string, tools map[string]string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte(agentMD), 0o640); err != nil {
		return "", fmt.Errorf("write AGENT.md: %w", err)
	}
	if err := agentdesigner.WriteState(filepath.Join(tmpDir, "state.md"), "Test Agent", map[string]any{}); err != nil {
		return "", fmt.Errorf("write state.md: %w", err)
	}
	if len(tools) > 0 {
		// Reproduce the full nested project tree (helper modules, tests, …).
		if err := agentdesigner.WriteToolsTree(filepath.Join(tmpDir, "tools"), tools); err != nil {
			return "", err
		}
	}

	testCoder := r.coderForWorkspace(workspaceID)
	prompt := prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD:     agentMD,
		StateJSON:   "{}",
		ChatApps:    r.loadChatApps(workspaceID),
		BackendType: backendTypeOf(testCoder),
	})

	if testCoder == nil {
		return "", fmt.Errorf("no coder service configured")
	}
	result, err := testCoder.WithDir(tmpDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit").Generate(ctx, workspaceID, prompt)
	if err != nil {
		return "", err
	}

	parsed := parseCoderOutput(result.Text)
	if len(parsed.chatLines) == 0 {
		return "", nil
	}
	return strings.Join(parsed.chatLines, "\n"), nil
}

// ─── Coder agent execution ────────────────────────────────────────────────────

// coderRunContext tracks mutable state across the turns of one top-level run.
type coderRunContext struct {
	turnsUsed      int
	chatLines      []string
	warnings       []string
	rawChunks      []string    // raw coder output per turn, joined into the run note
	lastRaw        string      // raw text of the most recent turn (fallback prose source)
	silentSignaled bool        // any turn emitted [SILENT] — run is intentionally quiet
	usage          coder.Usage // accumulated token usage (API coder); zero for CLI coders
}

func (r *Runner) runCoderAgent(ctx context.Context, agent *db.Agent, input RunInput) error {
	agentDir := agentdesigner.AgentDir(r.agentsDir, input.WorkspaceID, input.AgentID)

	// Read AGENT.md instructions (fall back to CLAUDE.md for legacy agents).
	agentMD, err := os.ReadFile(agentdesigner.AgentDescPath(r.agentsDir, input.WorkspaceID, input.AgentID))
	if err != nil {
		legacyPath := filepath.Join(agentDir, "CLAUDE.md")
		agentMD, err = os.ReadFile(legacyPath)
		if err != nil {
			return fmt.Errorf("read AGENT.md (and CLAUDE.md fallback): %w", err)
		}
	}

	// Read state (default empty object). ReadState already degrades a
	// missing file/damaged fence to an empty map, but guard against a
	// genuine read failure too — either a real I/O error, or a well-formed
	// fence whose JSON body doesn't parse. stateReadOK tracks that outcome
	// so the end-of-turn self-heal write (below) never mistakes a synthetic
	// {} for "this agent's state really is empty" and overwrites
	// hand-recoverable bad content with nothing.
	stateMap, err := agentdesigner.ReadState(agentdesigner.StateFilePath(r.agentsDir, input.WorkspaceID, input.AgentID))
	stateReadOK := err == nil
	if err != nil {
		stateMap = map[string]interface{}{}
	}
	stateJSON, err := json.MarshalIndent(stateMap, "", "  ")
	if err != nil {
		stateJSON = []byte("{}")
	}

	// Load skills context. The available pool is core skills (always-on, embedded)
	// plus the user's own installed/created skills. The agent's DECLARED skills come
	// from the agent_skills DB table (the source of truth), not from AGENT.md.
	allSkills, _ := r.db.ListSkills(input.WorkspaceID)
	declaredSkills, _ := r.db.ListAgentSkillNames(agent.ID)
	declaredContent, _ := r.loadDeclaredSkillContent(input.WorkspaceID, declaredSkills)

	var userMemory string
	if r.memStore != nil {
		userMemory, _ = r.memStore.ContextString(input.WorkspaceID)
	}

	skillRefs := make([]prompts.SkillRef, 0, len(allSkills)+8)
	for _, s := range skilllibrary.LoadBundled() {
		skillRefs = append(skillRefs, prompts.SkillRef{Name: s.Name, Description: s.Description, Category: s.Category})
	}
	for _, sk := range allSkills {
		skillRefs = append(skillRefs, prompts.SkillRef{Name: sk.Name, Description: sk.Description, Category: "User skills"})
	}

	homeDir := filepath.Join(r.homesDir, input.WorkspaceID)
	vaultRoot := filepath.Join(r.agentsDir, input.WorkspaceID)
	skillEnv := prompts.SkillEnvBlock(r.resolveSkillBins(input.WorkspaceID, homeDir, declaredSkills, declaredContent), homeDir, vaultRoot)

	// Resolve the workspace's configured coder up front so both the prompt's backend
	// description and the actual execution use the same (correct) coder.
	baseCoder := r.coderForWorkspace(input.WorkspaceID)

	// Load the agent's bound service connections once: they feed both the runtime
	// prompt (so the agent knows it has native typed tools) and WithConnectors below
	// (which actually exposes those tools to the API engine).
	var boundConns []connectors.BoundConn
	var boundRefs []prompts.ConnectionRef
	if r.connReg != nil && r.connStore != nil {
		if conns, err := r.db.ListAgentConnections(ctx, agent.ID); err == nil {
			for _, c := range conns {
				boundConns = append(boundConns, connectors.BoundConn{
					ID: c.ID, Provider: c.Provider, AccountLabel: c.AccountLabel, AccountIdentity: c.AccountIdentity,
					Extra: connectors.ParseExtra(c.Extra),
				})
				boundRefs = append(boundRefs, prompts.ConnectionRef{
					Provider: c.Provider, Label: c.AccountLabel, Identity: c.AccountIdentity,
				})
			}
		}
	}
	// The exact tool names a CLI coder invokes via `simple-agents connector exec <tool>`.
	var connToolNames []string
	for _, d := range r.connReg.ToolDefs(boundConns) {
		connToolNames = append(connToolNames, d.Name)
	}

	prompt := prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD:         string(agentMD),
		StateJSON:       string(stateJSON),
		UserMemory:      userMemory,
		AllSkills:       skillRefs,
		DeclaredSkills:  declaredSkills,
		DeclaredContent: declaredContent,
		SkillEnv:        skillEnv,
		VaultRoot:       vaultRoot,
		AgentDir:        agentDir,
		ChatApps:        r.loadChatApps(input.WorkspaceID),
		BackendType:     backendTypeOf(baseCoder),
		Connections:     boundRefs,
		ConnectionTools: connToolNames,
		ConnectorBin:    connectorBinPath(),
	})

	if baseCoder == nil {
		return fmt.Errorf("no coder service configured")
	}
	// Run inside the agent's own directory (not the shared per-user home) so
	// tools/*.py and state.md resolve correctly and runs never see other
	// agents' files. Pre-approve the tools agents need so the subprocess never
	// blocks on interactive permission prompts (--setting-sources "" suppresses
	// all settings).
	coderSvc := baseCoder.WithDir(agentDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit").WithProgress(input.OnProgress)

	// Assemble the subprocess env once (WithExtraEnv replaces rather than merges): user
	// secrets + the connector-bridge vars. Injected for every coder type.
	extraEnv := map[string]string{}
	if input.MasterPw != "" {
		if user, err := r.db.GetWorkspaceByID(input.WorkspaceID); err == nil {
			svc := secrets.New(r.db, input.WorkspaceID, input.MasterPw, user.SecretsSalt)
			if allSecrets, err := svc.GetAll(ctx); err == nil {
				for k, v := range allSecrets {
					extraEnv[k] = v
				}
			}
		}
	}

	// Expose the agent's bound service connections to BOTH coder types.
	if len(boundConns) > 0 {
		// The approval gate for public_write actions. Resolved once per run: nil when
		// the agent has no binding set to 'approve', which keeps the ungated path free
		// of any per-call work. BOTH coder kinds get the same parker, so switching
		// coder kind cannot silently disable the user's approval setting.
		var parker connectors.Parker
		if r.parkerFor != nil {
			parker = r.parkerFor(ctx, input.WorkspaceID, agent.ID, agent.Name)
		}
		// API engine: native in-process typed tools.
		coderSvc = coderSvc.WithConnectors(r.connReg, r.connStore, boundConns).WithParker(parker)
		// CLI coders: register a run-scoped bridge token + inject the loopback URL so
		// `simple-agents connector exec <tool>` reaches the same connectors.Execute.
		if r.connBridge != nil && r.connBridge.Addr() != "" {
			token := r.connBridge.RegisterGated(input.WorkspaceID, boundConns, false, parker)
			defer r.connBridge.Unregister(token)
			extraEnv["SA_CONNECTOR_URL"] = r.connBridge.Addr()
			extraEnv["SA_CONNECTOR_TOKEN"] = token
		}
	}
	// CLI coders: register a run-scoped KB bridge token so `simple-agents kb
	// convert|search` reaches the same vault.ImportFile / Searcher code the API
	// engine's save_to_kb/search_files tools call in-process. Unregistered when
	// the run ends, alongside the connector-token cleanup above.
	if r.kbBridge != nil && r.kbBridge.URL() != "" {
		kbToken := r.kbBridge.Register(input.WorkspaceID, false)
		defer r.kbBridge.Unregister(kbToken)
		extraEnv["SA_KB_URL"] = r.kbBridge.URL()
		extraEnv["SA_KB_TOKEN"] = kbToken
	}
	if len(extraEnv) > 0 {
		coderSvc = coderSvc.WithExtraEnv(extraEnv)
	}

	runID := uuid.New().String()
	startedAt := time.Now().UTC()
	if err := r.db.CreateAgentRun(&db.AgentRun{
		ID: runID, AgentID: input.AgentID,
		WorkspaceID: input.WorkspaceID, Trigger: input.Trigger,
	}); err != nil {
		return fmt.Errorf("create run record: %w", err)
	}

	rctx := &coderRunContext{}
	runErr := r.runCoderTurns(ctx, agent, input, agentDir, stateMap, stateReadOK, prompt, coderSvc, rctx)

	exitCode := 0
	if runErr != nil {
		exitCode = -1
	}
	r.reflectRun(input, agent, runID, exitCode, startedAt, rctx)

	if runErr != nil {
		_ = r.db.FinishAgentRun(runID, -1, strings.Join(rctx.chatLines, "\n"), strings.Join(rctx.warnings, "\n")+"\n"+runErr.Error(), rctx.usage.PromptTokens, rctx.usage.CompletionTokens, rctx.usage.TotalTokens)
		friendly := friendlyRunError(runErr, coderSvc.Name())
		// A multi-turn run (e.g. one that made a [CALL: agent] to a child agent, or
		// completed some turns before the final one failed) may have already
		// accumulated real [CHAT] output before the error — don't let a late failure
		// silently swallow it. Show what was delivered so far, then the error.
		if partial := strings.Join(rctx.chatLines, "\n"); partial != "" {
			friendly = partial + "\n\n" + friendly
		}
		// Notify the user directly — for cron-triggered runs this is the ONLY
		// way they'd ever find out (the scheduler otherwise just logs to slog).
		if input.SendOutput != nil {
			input.SendOutput(friendly)
		}
		r.recordInbox(input, agent, runID, friendly, "error")
		return errors.New(friendly)
	}

	// ── Reliable delivery ────────────────────────────────────────────────────
	// Delivery does not depend solely on the coder emitting [CHAT]. If it forgot
	// the marker (the most common model mistake) and didn't signal [SILENT], fall
	// back to its prose output so the user still receives the message. A run that
	// produces nothing deliverable and isn't intentionally silent is surfaced as a
	// visible warning instead of a silent success.
	finalOutput := strings.Join(rctx.chatLines, "\n")
	streamedLive := finalOutput != "" // per-turn OnProgress already streamed [CHAT]

	if finalOutput == "" && !rctx.silentSignaled {
		if prose := extractProseMessage(rctx.lastRaw); prose != "" {
			rctx.warnings = append(rctx.warnings, "no [CHAT] marker emitted; delivered prose as fallback")
			finalOutput = prose
		}
	}

	_ = r.db.FinishAgentRun(runID, 0, finalOutput, strings.Join(rctx.warnings, "\n"), rctx.usage.PromptTokens, rctx.usage.CompletionTokens, rctx.usage.TotalTokens)

	switch {
	case finalOutput != "":
		if input.SendOutput != nil {
			input.SendOutput(finalOutput)
		}
		r.recordInbox(input, agent, runID, finalOutput, "ok")
		if !streamedLive && input.OnProgress != nil {
			// Fallback prose wasn't streamed per-turn; show it on the live view too.
			input.OnProgress(finalOutput)
		}
	case !rctx.silentSignaled:
		warn := fmt.Sprintf("⚠️ %s ran but produced no notification — see the run log.", agent.Name)
		if input.SendOutput != nil {
			input.SendOutput(warn)
		}
		r.recordInbox(input, agent, runID, warn, "ok")
		if input.OnProgress != nil {
			input.OnProgress(warn)
		}
	}

	return nil
}

// reflectRun mirrors the completed run into the user's vault as a markdown note.
func (r *Runner) reflectRun(input RunInput, agent *db.Agent, runID string, exitCode int, startedAt time.Time, rctx *coderRunContext) {
	if err := r.reflector.ReflectAgentRun(input.WorkspaceID, vault.RunNote{
		RunID:            runID,
		AgentID:          input.AgentID,
		AgentName:        agent.Name,
		Trigger:          input.Trigger,
		ExitCode:         exitCode,
		StartedAt:        startedAt,
		FinishedAt:       time.Now().UTC(),
		Output:           strings.Join(rctx.rawChunks, "\n\n———\n\n"),
		ChatLines:        rctx.chatLines,
		Warnings:         rctx.warnings,
		PromptTokens:     rctx.usage.PromptTokens,
		CompletionTokens: rctx.usage.CompletionTokens,
		TotalTokens:      rctx.usage.TotalTokens,
	}); err != nil {
		slog.Warn("agentrunner: reflect run to vault", "run_id", runID, "err", err)
	}
}

// recordInbox drops one inbox notification whose body is the actual message
// delivered to the user (the friendly error, the [CHAT] output, or the
// no-notification warning). Best-effort: a failure never affects the run.
// Silent ([SILENT]) runs never reach a SendOutput site, so they post nothing.
func (r *Runner) recordInbox(input RunInput, agent *db.Agent, runID, body, status string) {
	if body == "" || r.db == nil {
		return
	}
	id := uuid.New().String()
	now := time.Now().UTC()
	if err := r.db.CreateInboxMessage(&db.InboxMessage{
		ID: id, WorkspaceID: input.WorkspaceID, Source: "agent_run",
		AgentID: input.AgentID, AgentName: agent.Name, RefID: runID,
		Trigger: input.Trigger, Body: body, Status: status, CreatedAt: now,
	}); err != nil {
		slog.Warn("inbox: create agent_run", "run_id", runID, "err", err)
		return
	}
	if r.reflector != nil {
		if err := r.reflector.ReflectInbox(input.WorkspaceID, vault.InboxNote{
			ID: id, Source: "agent_run", AgentName: agent.Name,
			Trigger: input.Trigger, Body: body, Status: status, CreatedAt: now,
		}); err != nil {
			slog.Warn("inbox: reflect agent_run", "run_id", runID, "err", err)
		}
	}
}

// runCoderTurns drives the multi-turn coder loop for one agent invocation.
func (r *Runner) runCoderTurns(
	ctx context.Context,
	agent *db.Agent,
	input RunInput,
	agentDir string,
	currentState map[string]interface{},
	stateReadOK bool,
	initialPrompt string,
	coderSvc *coder.Coder,
	rctx *coderRunContext,
) error {
	if coderSvc == nil {
		return fmt.Errorf("no coder service configured for md/hybrid agents")
	}

	if currentState == nil {
		currentState = make(map[string]interface{})
	}

	prompt := initialPrompt

	for {
		if rctx.turnsUsed >= maxTurns {
			rctx.warnings = append(rctx.warnings, fmt.Sprintf("max turns (%d) reached", maxTurns))
			break
		}

		result, err := coderSvc.Generate(ctx, input.WorkspaceID, prompt)
		rctx.turnsUsed++
		if err != nil {
			return fmt.Errorf("coder generate: %w", err)
		}
		rctx.usage = addUsage(rctx.usage, result.Usage)

		parsed := parseCoderOutput(result.Text)
		rctx.chatLines = append(rctx.chatLines, parsed.chatLines...)
		rctx.warnings = append(rctx.warnings, parsed.warnings...)
		if parsed.silent {
			rctx.silentSignaled = true
		}
		rctx.lastRaw = result.Text

		// Stream this turn's chat lines live (SSE) while the run is still going.
		// Final durable delivery (Telegram/history) still happens once at the end.
		if input.OnProgress != nil && len(parsed.chatLines) > 0 {
			input.OnProgress(strings.Join(parsed.chatLines, "\n"))
		}

		// Merge and persist state — unconditionally, not just when this turn
		// emitted [STATE]. See applyAndSaveState for why: a turn's own file
		// tools can mangle or drop state.md's json fence (e.g. while making a
		// legitimate "## Notes" edit) without emitting [STATE] that same turn,
		// and skipping the save on a no-update turn would leave that damage
		// standing for the next run's ReadState to silently see as {}.
		if err := applyAndSaveState(agentDir, agent.Name, currentState, parsed.stateUpdates, stateReadOK); err != nil {
			rctx.warnings = append(rctx.warnings, "state save failed: "+err.Error())
		}

		// Accumulate raw output; the run note (markdown, in the vault) is written
		// once the run finishes — see reflectRun in runCoderAgent.
		rctx.rawChunks = append(rctx.rawChunks, result.Text)

		// Handle agent-to-agent calls.
		if len(parsed.callAgents) == 0 {
			break
		}

		var childOutputs []string
		for _, callName := range parsed.callAgents {
			if input.depth >= maxCallDepth {
				warn := fmt.Sprintf("skipping [CALL: %s]: max depth (%d) reached", callName, maxCallDepth)
				rctx.warnings = append(rctx.warnings, warn)
				slog.Warn("agentrunner: "+warn, "agent_id", agent.ID)
				continue
			}
			if input.visited[callName] {
				warn := fmt.Sprintf("skipping [CALL: %s]: cycle detected", callName)
				rctx.warnings = append(rctx.warnings, warn)
				slog.Warn("agentrunner: "+warn, "agent_id", agent.ID)
				continue
			}

			child, err := r.db.GetAgentByName(input.WorkspaceID, callName)
			if err != nil {
				warn := fmt.Sprintf("skipping [CALL: %s]: agent not found", callName)
				rctx.warnings = append(rctx.warnings, warn)
				slog.Warn("agentrunner: "+warn, "agent_id", agent.ID)
				continue
			}

			var childChat []string
			newVisited := make(map[string]bool)
			for k, v := range input.visited {
				newVisited[k] = v
			}
			newVisited[agent.Name] = true

			childInput := RunInput{
				AgentID:     child.ID,
				WorkspaceID: input.WorkspaceID,
				Trigger:     input.Trigger,
				MasterPw:    input.MasterPw,
				SendOutput: func(msg string) {
					childChat = append(childChat, msg)
				},
				depth:   input.depth + 1,
				visited: newVisited,
			}

			slog.Info("agentrunner: calling child agent", "parent", agent.Name, "child", callName, "depth", input.depth+1)
			if err := r.Run(ctx, childInput); err != nil {
				warn := fmt.Sprintf("child agent %s failed: %v", callName, err)
				rctx.warnings = append(rctx.warnings, warn)
			}

			if len(childChat) > 0 {
				childOutputs = append(childOutputs,
					fmt.Sprintf("=== Agent %q responded ===\n%s", callName, strings.Join(childChat, "\n")))
			}
		}

		if len(childOutputs) == 0 {
			break
		}

		// Build follow-up prompt injecting child results.
		prompt = prompts.BuildChildAgentFollowUpPrompt(childOutputs)
	}

	return nil
}

// friendlyRunError converts a low-level run failure into a message safe to
// show the user directly (web UI error banner, or sent as a chat message for
// cron-triggered runs). Usage-limit hits are an expected, recurring condition
// — not an agent bug — so they get a distinct, reassuring message instead of
// a raw exit code. coderName identifies the underlying CLI binary (e.g.
// "claude") so the message stays accurate across different coder profiles;
// pass "" to fall back to a generic phrase.
func friendlyRunError(err error, coderName string) string {
	who := "The coder"
	if coderName != "" {
		who = coderName
	}
	if errors.Is(err, coder.ErrRateLimited) {
		// Transient provider throttle (429 RPM/TPM window), not quota
		// exhaustion — chat still works because its requests are smaller. The
		// run would very likely succeed if retried shortly after.
		return fmt.Sprintf("⚠️ %s was rate-limited by the provider (too many requests just now). Try running the agent again in a minute — your quota is fine.", who)
	}
	if errors.Is(err, coder.ErrUsageLimit) {
		return fmt.Sprintf("⚠️ This agent run was skipped — %s hit its usage limit (quota/credits exhausted). It will retry automatically on the next scheduled run.", who)
	}
	if errors.Is(err, coder.ErrMaxTurns) {
		// Normally unreachable: the API-coder tool loop (api_engine.go's runToolLoop)
		// gives the model one final text-only turn to explain itself and stop before
		// returning this — so a real run only reaches this branch if that grace turn
		// ALSO failed. Any [CHAT] output from earlier turns in this same run is
		// prepended by the caller, so this is the fallback for when there's none.
		return fmt.Sprintf("⚠️ %s ran out of attempts partway through this run and couldn't finish or explain why. It will retry on the next scheduled run — if this keeps happening, the task likely needs to be simplified or split up.", who)
	}
	return "⚠️ This agent run failed: " + err.Error()
}

// ─── Output parsing ───────────────────────────────────────────────────────────

type parsedOutput struct {
	chatLines    []string
	stateUpdates []map[string]interface{}
	callAgents   []string
	warnings     []string
	// silent is true if the coder emitted a [SILENT] marker, signalling that
	// this run intentionally produces no user-facing message (note-only /
	// state-only agents). It suppresses the prose-delivery fallback in the
	// runner so silent agents are not noisified by stray prose.
	silent bool
}

// parseCoderOutput scans the coder's text response for protocol markers.
//
// [CHAT] blocks run until the next protocol marker ([STATE], [CALL], [SILENT],
// a new [CHAT]) or the end of output. All lines in between — including blank
// lines — are part of the message; leading/trailing blank lines are trimmed on
// flush. Blank lines do NOT end the block (they used to, which silently dropped
// real content when the runtime emitted a header, a blank line, then content).
// Empty/whitespace-only [CHAT] blocks are dropped (never deliver a blank msg).
//
// [SILENT] (alone on a line) marks the run as intentionally silent.
//
// [STATE] may appear as a multi-line block ([STATE]\n{...}\n[/STATE]) or
// as a single inline line ([STATE]{...}[/STATE]).
func parseCoderOutput(text string) parsedOutput {
	var out parsedOutput
	lines := strings.Split(text, "\n")

	var stateAcc strings.Builder
	inState := false
	var chatAcc strings.Builder
	inChat := false

	flushChat := func() {
		if inChat && chatAcc.Len() > 0 {
			if msg := strings.TrimSpace(chatAcc.String()); msg != "" {
				out.chatLines = append(out.chatLines, msg)
			}
			chatAcc.Reset()
			inChat = false
		}
	}

	parseStateJSON := func(raw string) {
		// UseNumber: this is the decode site a live [STATE] update from the
		// coder actually goes through. A coder emitting
		// [STATE]{"last_id": 9007199254740993}[/STATE] (a 64-bit Discord
		// snowflake, or any ID above 2^53) would otherwise be rounded here,
		// before mergeState/saveState ever run — fixing only ReadState and the
		// migration would leave this, the single most common live-run case,
		// still lossy.
		dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.UseNumber()
		var update map[string]interface{}
		if err := dec.Decode(&update); err != nil {
			out.warnings = append(out.warnings,
				fmt.Sprintf("state parse error: %s (json: %.200s)", err, raw))
		} else {
			out.stateUpdates = append(out.stateUpdates, update)
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// ── [STATE] block opener ──────────────────────────────────────────────
		if !inState && trimmed == "[STATE]" {
			flushChat()
			inState = true
			stateAcc.Reset()
			continue
		}

		// ── inline [STATE]{...}[/STATE] on a single line ─────────────────────
		if !inState && strings.HasPrefix(trimmed, "[STATE]") && strings.HasSuffix(trimmed, "[/STATE]") {
			flushChat()
			jsonPart := strings.TrimSuffix(strings.TrimPrefix(trimmed, "[STATE]"), "[/STATE]")
			parseStateJSON(jsonPart)
			continue
		}

		// ── inside a [STATE] block ────────────────────────────────────────────
		if inState {
			if trimmed == "[/STATE]" {
				inState = false
				parseStateJSON(stateAcc.String())
				stateAcc.Reset()
			} else {
				stateAcc.WriteString(line)
				stateAcc.WriteByte('\n')
			}
			continue
		}

		// ── [CHAT] — start a new chat block ──────────────────────────────────
		// Matches the marker whether the message is inline ("[CHAT] hello") or the
		// marker sits alone on its own line with the message on the FOLLOWING lines
		// ("[CHAT]\nhello\n…") — a common weak-model pattern (e.g. qwen). The earlier
		// "[CHAT] " (trailing-space) requirement silently dropped a bare-marker block:
		// inChat never turned on, the message lines were never captured, and a trailing
		// [SILENT] then suppressed both the prose fallback and the empty-run warning.
		if strings.HasPrefix(trimmed, "[CHAT]") {
			flushChat()
			inChat = true
			// The [CHAT] protocol has NO close tag, but weak models often emit a stray
			// "[/CHAT]" anyway (e.g. "[CHAT] msg [/CHAT]" on one line). Strip a trailing
			// close tag here so it never leaks into the delivered message.
			content := strings.TrimSpace(strings.TrimPrefix(trimmed, "[CHAT]"))
			content = strings.TrimSpace(strings.TrimSuffix(content, "[/CHAT]"))
			chatAcc.WriteString(content)
			continue
		}

		// ── [/CHAT] — stray close tag (weak models) ──────────────────────────
		// Treat a standalone "[/CHAT]" line, or a continuation line ending in
		// "[/CHAT]", as the end of the chat block and DROP the tag — never deliver it.
		if inChat && (trimmed == "[/CHAT]" || strings.HasSuffix(trimmed, "[/CHAT]")) {
			if trimmed != "[/CHAT]" {
				chatAcc.WriteByte('\n')
				chatAcc.WriteString(strings.TrimSpace(strings.TrimSuffix(trimmed, "[/CHAT]")))
			}
			flushChat()
			continue
		}

		// ── [CALL: <name>] ────────────────────────────────────────────────────
		if strings.HasPrefix(trimmed, "[CALL: ") && strings.HasSuffix(trimmed, "]") {
			flushChat()
			name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "[CALL: "), "]")
			name = strings.TrimSpace(name)
			if name != "" {
				out.callAgents = append(out.callAgents, name)
			}
			continue
		}

		// ── [SILENT] — run intentionally produces no user-facing message ────────
		// Ends any open [CHAT] block and marks the run silent so the runner does
		// not fall back to prose delivery.
		if trimmed == "[SILENT]" {
			flushChat()
			out.silent = true
			continue
		}

		// ── chat continuation ─────────────────────────────────────────────────
		// A [CHAT] block runs until the next protocol marker ([STATE], [CALL],
		// a new [CHAT]) or end of output. Blank lines are part of the message,
		// NOT a terminator. The previous rule (blank line ends the block) silently
		// dropped real content when the runtime emitted "header\n\ncontent" — a
		// common pattern when an AGENT.md example shows a blank line. Leading and
		// trailing blank lines are stripped on flush via TrimSpace.
		if inChat {
			chatAcc.WriteByte('\n')
			chatAcc.WriteString(trimmed)
		}
	}

	flushChat()

	if inState {
		out.warnings = append(out.warnings, "unclosed [STATE] block in coder output — discarded")
	}

	return out
}

// extractProseMessage strips protocol markers from the coder's raw text and
// returns the remaining prose. It is the reliable-delivery fallback for when the
// coder produced a user-facing message but forgot the [CHAT] marker: its prose
// output is delivered instead of being silently lost.
//
// Strips: [STATE] blocks (multi-line and inline), [CALL: …] lines, [SILENT],
// standalone [CHAT]/[/CHAT] marker lines, and [BLOCKED]…[/BLOCKED] blocks. The
// remaining lines are joined, edge-trimmed, and have runs of 3+ blank lines
// collapsed. Returns "" when nothing usable remains (only markers/whitespace).
func extractProseMessage(text string) string {
	lines := strings.Split(text, "\n")
	var kept []string
	inState := false
	inBlocked := false
	for _, line := range lines {
		t := strings.TrimSpace(line)

		// [BLOCKED]…[/BLOCKED] block (design-time marker; harmless if present).
		if inBlocked {
			if t == "[/BLOCKED]" {
				inBlocked = false
			}
			continue
		}
		if t == "[BLOCKED]" {
			inBlocked = true
			continue
		}

		// [STATE] block.
		if !inState && t == "[STATE]" {
			inState = true
			continue
		}
		if inState {
			if t == "[/STATE]" {
				inState = false
			}
			continue
		}
		// Inline [STATE]{…}[/STATE].
		if strings.HasPrefix(t, "[STATE]") && strings.HasSuffix(t, "[/STATE]") {
			continue
		}

		// [CALL: …].
		if strings.HasPrefix(t, "[CALL: ") && strings.HasSuffix(t, "]") {
			continue
		}
		// Explicit silence / standalone chat markers (no content to keep).
		if t == "[SILENT]" || t == "[CHAT]" || t == "[/CHAT]" {
			continue
		}
		// A malformed [CHAT] line (e.g. "[CHAT]" with content after on the same
		// line but no space) — keep the content, drop the marker.
		if strings.HasPrefix(t, "[CHAT]") {
			kept = append(kept, strings.TrimSpace(strings.TrimPrefix(t, "[CHAT]")))
			continue
		}

		kept = append(kept, line)
	}
	cleaned := strings.TrimSpace(strings.Join(kept, "\n"))
	for strings.Contains(cleaned, "\n\n\n") {
		cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	}
	return cleaned
}

// ─── State management ─────────────────────────────────────────────────────────

// mergeState shallowly merges update into existing. A null value deletes the key.
func mergeState(existing map[string]interface{}, update map[string]interface{}) {
	for k, v := range update {
		if v == nil {
			delete(existing, k)
		} else {
			existing[k] = v
		}
	}
}

// saveState writes state.md, replacing only the machine-state json fence and
// preserving any prose an agent (or the user) has written around it.
func saveState(agentDir, agentName string, state map[string]interface{}) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxStateSize {
		return fmt.Errorf("state too large (%d bytes > %d limit)", len(data), maxStateSize)
	}
	return agentdesigner.WriteState(filepath.Join(agentDir, "state.md"), agentName, state)
}

// applyAndSaveState merges this turn's [STATE] updates (if any) into
// currentState and persists it to state.md.
//
// When there ARE updates, the write always happens — the long-standing
// contract that an explicit [STATE] emission always wins is unchanged, even
// over an unreadable prior file (currentState degrades to {} and this turn's
// update becomes the new baseline).
//
// When there are NO updates this turn, the write STILL happens — unless the
// run's initial ReadState failed (stateReadOK == false). This is the
// self-heal fix for the state-loss vector: an agent's own file tools (the API
// engine's write_file is a full-file overwrite) can mangle or drop state.md's
// json fence — e.g. while making a legitimate edit to "## Notes" — without
// ever emitting [STATE] that same turn. Because currentState was read from
// disk before this run's coder turns could touch the file, writing it back
// here repairs any such damage; WriteState only ever splices the fence, so a
// legitimate prose edit made this turn survives untouched.
//
// The stateReadOK guard exists because ReadState can itself fail — either a
// genuine I/O error, or a well-formed fence whose JSON body doesn't parse. In
// both cases currentState is a synthetic {} standing in for content we could
// never make sense of. Writing that {} back unconditionally on a no-update
// turn would silently replace hand-recoverable bad state with nothing — the
// exact failure mode this fix exists to prevent, just moved one level up. So
// a no-update turn is a strict no-op when the initial read failed, leaving
// the malformed file for a human (or a later explicit [STATE]) to fix.
func applyAndSaveState(agentDir, agentName string, currentState map[string]interface{}, updates []map[string]interface{}, stateReadOK bool) error {
	for _, update := range updates {
		mergeState(currentState, update)
	}
	if len(updates) == 0 && !stateReadOK {
		return nil
	}
	return saveState(agentDir, agentName, currentState)
}

// ─── Skills loading ───────────────────────────────────────────────────────────

func (r *Runner) loadDeclaredSkillContent(workspaceID string, skillNames []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, name := range skillNames {
		// Core skills are always-on and embedded — read from the binary, not disk.
		if content, ok := skilllibrary.CoreSkillContent(name); ok {
			result[name] = content
			continue
		}
		skillMD := filepath.Join(skillstore.SkillDir(r.skillsDir, workspaceID, name), "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			slog.Warn("agentrunner: skill not found on disk", "user_id", workspaceID, "skill", name)
			continue
		}
		result[name] = string(data)
	}
	return result, nil
}

// resolveSkillBins resolves the absolute path of every CLI tool a declared skill
// requires (metadata.requires.bins / anyBins), so the runtime env block
// can tell the agent where to invoke each tool. A tool is "resolved" if it exists
// at $HOME/.local/bin/<bin> or on PATH; otherwise Path is empty (the env block
// instructs the agent to install it via the cli-tool-installer skill).
func (r *Runner) resolveSkillBins(workspaceID, homeDir string, declaredSkills []string, declaredContent map[string]string) []prompts.SkillBin {
	localBin := filepath.Join(homeDir, ".local", "bin")
	var out []prompts.SkillBin
	seen := map[string]bool{}
	add := func(skill, bin string) {
		if bin == "" || seen[bin] {
			return
		}
		seen[bin] = true
		cand := filepath.Join(localBin, bin)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			out = append(out, prompts.SkillBin{Skill: skill, Bin: bin, Path: cand})
			return
		}
		if p, err := exec.LookPath(bin); err == nil {
			out = append(out, prompts.SkillBin{Skill: skill, Bin: bin, Path: p})
			return
		}
		out = append(out, prompts.SkillBin{Skill: skill, Bin: bin, Path: ""})
	}
	for _, name := range declaredSkills {
		content, ok := declaredContent[name]
		if !ok {
			continue
		}
		meta, _ := skilllibrary.ParseMeta(content)
		for _, b := range meta.RequiresBins {
			add(name, b)
		}
		for _, b := range meta.AnyBins {
			add(name, b)
		}
	}
	return out
}

// loadChatApps returns the chat apps connected by this user as prompts.ChatAppInfo
// (name + commands), so the runtime coder prompt knows where [CHAT] output lands and
// what the user can type. Returns nil if the DB is unavailable or none are connected.
func (r *Runner) loadChatApps(workspaceID string) []prompts.ChatAppInfo {
	if r.db == nil {
		return nil
	}
	conns, err := r.db.ListWorkspacePlatformConnections(workspaceID)
	if err != nil {
		return nil
	}
	platforms := make([]string, 0, len(conns))
	for _, c := range conns {
		platforms = append(platforms, c.Platform)
	}
	return prompts.ChatAppsForPlatforms(platforms)
}

// backendTypeOf returns the prompts-level backend capability for a coder, so the
// runtime prompt can describe how the coder acts on files (full coder vs basic model).
func backendTypeOf(c *coder.Coder) string {
	if c == nil {
		return prompts.BackendFullCoder
	}
	return prompts.MapCoderBackend(c.BackendType())
}

// addUsage accumulates coder.Usage across turns. CLI coders report zero; the API
// coder reports provider-reported token counts per turn.
func addUsage(a, b coder.Usage) coder.Usage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.TotalTokens += b.TotalTokens
	return a
}

// connectorBinPath is the absolute path to the running simple-agents binary, which a CLI
// coder invokes as `<bin> connector exec …`. Falls back to "" (bare name via PATH) if
// os.Executable() fails.
func connectorBinPath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return ""
}
