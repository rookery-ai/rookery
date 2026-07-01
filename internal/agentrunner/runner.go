// Package agentrunner loads an agent from disk and executes it via the coder CLI.
// Output [CHAT] lines are routed back to the user.
package agentrunner

import (
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
	AgentID    string
	UserID     string
	Trigger    string // "chat", "cron", "manual"
	MasterPw   string // user's master password for secret decryption
	SendOutput SendFunc
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
	ContextString(userID string) (string, error)
}

// Runner executes agents.
type Runner struct {
	db        *db.DB
	systemKey []byte
	agentsDir string // vaults base: <data>/vaults/ (agent dirs at <base>/<userID>/agents/<agentID>)
	homesDir  string // per-user home dirs root (for per-user sandbox binding)
	dataDir   string // root data dir (blacklisted inside sandbox)
	coderSvc  *coder.Coder
	skillsDir string           // vaults base: <data>/vaults (skills at <base>/<userID>/skills/<name>)
	memStore  memoryStore      // optional; nil = no memory injected
	reflector *vault.Reflector // optional; mirrors runs into the user's vault
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

// Run executes the agent identified by input.AgentID.
func (r *Runner) Run(ctx context.Context, input RunInput) error {
	if input.visited == nil {
		input.visited = make(map[string]bool)
	}

	agent, err := r.db.GetAgent(input.AgentID)
	if err != nil {
		return fmt.Errorf("load agent: %w", err)
	}
	if agent.UserID != input.UserID {
		return fmt.Errorf("agent not found")
	}

	manifest, _ := agentdesigner.LoadManifest(r.agentsDir, input.UserID, input.AgentID)
	if manifest == nil {
		manifest = &agentdesigner.AgentManifest{ID: agent.ID, Name: agent.Name}
	}

	return r.runCoderAgent(ctx, agent, manifest, input)
}

// RunByName looks up an agent by name and runs it.
func (r *Runner) RunByName(ctx context.Context, userID, agentName, masterPw string, send SendFunc) error {
	agent, err := r.db.GetAgentByName(userID, agentName)
	if err != nil {
		return fmt.Errorf("agent %q not found", agentName)
	}
	return r.Run(ctx, RunInput{
		AgentID:    agent.ID,
		UserID:     userID,
		Trigger:    "chat",
		MasterPw:   masterPw,
		SendOutput: send,
	})
}

// TestRunFromContent executes agentMD content once from a temp directory and
// returns the joined [CHAT] lines. Used by the agent designer to verify an agent
// works before committing it to disk/DB. Returns ("", nil) if the agent produces
// no [CHAT] output; returns ("", err) if the coder subprocess fails.
func (r *Runner) TestRunFromContent(ctx context.Context, userID, agentMD string, tools map[string]string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte(agentMD), 0o640); err != nil {
		return "", fmt.Errorf("write AGENT.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "state.json"), []byte("{}"), 0o640); err != nil {
		return "", fmt.Errorf("write state.json: %w", err)
	}
	if len(tools) > 0 {
		// Reproduce the full nested project tree (helper modules, tests, …).
		if err := agentdesigner.WriteToolsTree(filepath.Join(tmpDir, "tools"), tools); err != nil {
			return "", err
		}
	}

	prompt := prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD:     agentMD,
		StateJSON:   "{}",
		ChatApps:    r.loadChatApps(userID),
		BackendType: r.backendType(),
	})

	if r.coderSvc == nil {
		return "", fmt.Errorf("no coder service configured")
	}
	result, err := r.coderSvc.WithDir(tmpDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit").Generate(ctx, userID, prompt)
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
	turnsUsed       int
	chatLines       []string
	warnings        []string
	rawChunks       []string // raw coder output per turn, joined into the run note
	lastRaw         string   // raw text of the most recent turn (fallback prose source)
	silentSignaled  bool     // any turn emitted [SILENT] — run is intentionally quiet
}

func (r *Runner) runCoderAgent(ctx context.Context, agent *db.Agent, manifest *agentdesigner.AgentManifest, input RunInput) error {
	agentDir := agentdesigner.AgentDir(r.agentsDir, input.UserID, input.AgentID)

	// Read AGENT.md instructions (fall back to CLAUDE.md for legacy agents).
	agentMD, err := os.ReadFile(agentdesigner.AgentDescPath(r.agentsDir, input.UserID, input.AgentID))
	if err != nil {
		legacyPath := filepath.Join(agentDir, "CLAUDE.md")
		agentMD, err = os.ReadFile(legacyPath)
		if err != nil {
			return fmt.Errorf("read AGENT.md (and CLAUDE.md fallback): %w", err)
		}
	}

	// Read state (default empty object).
	stateRaw, err := os.ReadFile(filepath.Join(agentDir, "state.json"))
	if err != nil {
		stateRaw = []byte("{}")
	}

	// Load skills context. The available pool is core skills (always-on, embedded)
	// plus the user's own installed/created skills. The agent's DECLARED skills come
	// from the agent_skills DB table (the source of truth), not from AGENT.md.
	allSkills, _ := r.db.ListSkills(input.UserID)
	declaredSkills, _ := r.db.ListAgentSkillNames(agent.ID)
	declaredContent, _ := r.loadDeclaredSkillContent(input.UserID, declaredSkills)

	var userMemory string
	if r.memStore != nil {
		userMemory, _ = r.memStore.ContextString(input.UserID)
	}

	skillRefs := make([]prompts.SkillRef, 0, len(allSkills)+8)
	for _, s := range skilllibrary.LoadBundled() {
		skillRefs = append(skillRefs, prompts.SkillRef{Name: s.Name, Description: s.Description})
	}
	for _, sk := range allSkills {
		skillRefs = append(skillRefs, prompts.SkillRef{Name: sk.Name, Description: sk.Description})
	}

	homeDir := filepath.Join(r.homesDir, input.UserID)
	vaultRoot := filepath.Join(r.agentsDir, input.UserID)
	skillEnv := prompts.SkillEnvBlock(r.resolveSkillBins(input.UserID, homeDir, declaredSkills, declaredContent), homeDir, vaultRoot)

	prompt := prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD:         string(agentMD),
		StateJSON:       string(stateRaw),
		UserMemory:      userMemory,
		AllSkills:       skillRefs,
		DeclaredSkills:  declaredSkills,
		DeclaredContent: declaredContent,
		SkillEnv:        skillEnv,
		VaultRoot:       vaultRoot,
		AgentDir:        agentDir,
		ChatApps:        r.loadChatApps(input.UserID),
		BackendType:     r.backendType(),
	})

	// Run inside the agent's own directory (not the shared per-user home) so
	// tools/*.py and state.json resolve correctly and runs never see other
	// agents' files. Pre-approve the tools agents need so the subprocess never
	// blocks on interactive permission prompts (--setting-sources "" suppresses
	// all settings).
	coderSvc := r.coderSvc.WithDir(agentDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit")

	// Inject user secrets as env vars when master password is available.
	if input.MasterPw != "" {
		if user, err := r.db.GetUserByID(input.UserID); err == nil {
			svc := secrets.New(r.db, input.UserID, input.MasterPw, user.SecretsSalt)
			if allSecrets, err := svc.GetAll(ctx); err == nil && len(allSecrets) > 0 {
				coderSvc = coderSvc.WithExtraEnv(allSecrets)
			}
		}
	}

	runID := uuid.New().String()
	startedAt := time.Now().UTC()
	if err := r.db.CreateAgentRun(&db.AgentRun{
		ID: runID, AgentID: input.AgentID,
		UserID: input.UserID, Trigger: input.Trigger,
	}); err != nil {
		return fmt.Errorf("create run record: %w", err)
	}

	rctx := &coderRunContext{}
	runErr := r.runCoderTurns(ctx, agent, manifest, input, agentDir, stateRaw, prompt, coderSvc, rctx)

	exitCode := 0
	if runErr != nil {
		exitCode = -1
	}
	r.reflectRun(input, agent, runID, exitCode, startedAt, rctx)

	if runErr != nil {
		_ = r.db.FinishAgentRun(runID, -1, strings.Join(rctx.chatLines, "\n"), strings.Join(rctx.warnings, "\n")+"\n"+runErr.Error())
		friendly := friendlyRunError(runErr, coderSvc.Name())
		// Notify the user directly — for cron-triggered runs this is the ONLY
		// way they'd ever find out (the scheduler otherwise just logs to slog).
		if input.SendOutput != nil {
			input.SendOutput(friendly)
		}
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

	_ = r.db.FinishAgentRun(runID, 0, finalOutput, strings.Join(rctx.warnings, "\n"))

	switch {
	case finalOutput != "":
		if input.SendOutput != nil {
			input.SendOutput(finalOutput)
		}
		if !streamedLive && input.OnProgress != nil {
			// Fallback prose wasn't streamed per-turn; show it on the live view too.
			input.OnProgress(finalOutput)
		}
	case !rctx.silentSignaled:
		warn := fmt.Sprintf("⚠️ %s ran but produced no notification — see the run log.", agent.Name)
		if input.SendOutput != nil {
			input.SendOutput(warn)
		}
		if input.OnProgress != nil {
			input.OnProgress(warn)
		}
	}

	return nil
}

// reflectRun mirrors the completed run into the user's vault as a markdown note.
func (r *Runner) reflectRun(input RunInput, agent *db.Agent, runID string, exitCode int, startedAt time.Time, rctx *coderRunContext) {
	if err := r.reflector.ReflectAgentRun(input.UserID, vault.RunNote{
		RunID:      runID,
		AgentID:    input.AgentID,
		AgentName:  agent.Name,
		Trigger:    input.Trigger,
		ExitCode:   exitCode,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		Output:     strings.Join(rctx.rawChunks, "\n\n———\n\n"),
		ChatLines:  rctx.chatLines,
		Warnings:   rctx.warnings,
	}); err != nil {
		slog.Warn("agentrunner: reflect run to vault", "run_id", runID, "err", err)
	}
}

// runCoderTurns drives the multi-turn coder loop for one agent invocation.
func (r *Runner) runCoderTurns(
	ctx context.Context,
	agent *db.Agent,
	manifest *agentdesigner.AgentManifest,
	input RunInput,
	agentDir string,
	stateRaw []byte,
	initialPrompt string,
	coderSvc *coder.Coder,
	rctx *coderRunContext,
) error {
	if coderSvc == nil {
		return fmt.Errorf("no coder service configured for md/hybrid agents")
	}

	// Load current state so we can merge updates.
	var currentState map[string]interface{}
	_ = json.Unmarshal(stateRaw, &currentState)
	if currentState == nil {
		currentState = make(map[string]interface{})
	}

	prompt := initialPrompt

	for {
		if rctx.turnsUsed >= maxTurns {
			rctx.warnings = append(rctx.warnings, fmt.Sprintf("max turns (%d) reached", maxTurns))
			break
		}

		result, err := coderSvc.Generate(ctx, input.UserID, prompt)
		rctx.turnsUsed++
		if err != nil {
			return fmt.Errorf("coder generate: %w", err)
		}

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

		// Merge state updates.
		if len(parsed.stateUpdates) > 0 {
			for _, update := range parsed.stateUpdates {
				mergeState(currentState, update)
			}
			if err := saveState(agentDir, currentState); err != nil {
				rctx.warnings = append(rctx.warnings, "state save failed: "+err.Error())
			}
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

			child, err := r.db.GetAgentByName(input.UserID, callName)
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
				AgentID:  child.ID,
				UserID:   input.UserID,
				Trigger:  input.Trigger,
				MasterPw: input.MasterPw,
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
	if errors.Is(err, coder.ErrUsageLimit) {
		who := "The coder"
		if coderName != "" {
			who = coderName
		}
		return fmt.Sprintf("⚠️ This agent run was skipped — %s hit its usage limit. It will retry automatically on the next scheduled run.", who)
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
		var update map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &update); err != nil {
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
		if strings.HasPrefix(trimmed, "[CHAT] ") {
			flushChat()
			inChat = true
			chatAcc.WriteString(strings.TrimPrefix(trimmed, "[CHAT] "))
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

// saveState atomically writes state.json.
func saveState(agentDir string, state map[string]interface{}) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxStateSize {
		return fmt.Errorf("state too large (%d bytes > %d limit)", len(data), maxStateSize)
	}
	tmpPath := filepath.Join(agentDir, "state.json.tmp")
	if err := os.WriteFile(tmpPath, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Join(agentDir, "state.json"))
}

// ─── Skills loading ───────────────────────────────────────────────────────────

func (r *Runner) loadDeclaredSkillContent(userID string, skillNames []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, name := range skillNames {
		// Core skills are always-on and embedded — read from the binary, not disk.
		if content, ok := skilllibrary.CoreSkillContent(name); ok {
			result[name] = content
			continue
		}
		skillMD := filepath.Join(skillstore.SkillDir(r.skillsDir, userID, name), "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			slog.Warn("agentrunner: skill not found on disk", "user_id", userID, "skill", name)
			continue
		}
		result[name] = string(data)
	}
	return result, nil
}

// resolveSkillBins resolves the absolute path of every CLI tool a declared skill
// requires (metadata.openclaw.requires.bins / anyBins), so the runtime env block
// can tell the agent where to invoke each tool. A tool is "resolved" if it exists
// at $HOME/.local/bin/<bin> or on PATH; otherwise Path is empty (the env block
// instructs the agent to install it via the cli-tool-installer skill).
func (r *Runner) resolveSkillBins(userID, homeDir string, declaredSkills []string, declaredContent map[string]string) []prompts.SkillBin {
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
func (r *Runner) loadChatApps(userID string) []prompts.ChatAppInfo {
	if r.db == nil {
		return nil
	}
	conns, err := r.db.ListUserPlatformConnections(userID)
	if err != nil {
		return nil
	}
	platforms := make([]string, 0, len(conns))
	for _, c := range conns {
		platforms = append(platforms, c.Platform)
	}
	return prompts.ChatAppsForPlatforms(platforms)
}

// backendType returns the prompts-level backend capability for this runner's coder, so
// the runtime prompt can describe how the coder acts on files (full coder vs basic model).
func (r *Runner) backendType() string {
	if r.coderSvc == nil {
		return prompts.BackendFullCoder
	}
	return prompts.MapCoderBackend(r.coderSvc.BackendType())
}
