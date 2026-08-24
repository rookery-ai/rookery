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
	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/agentstate"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/mcp"
	"github.com/rookery-ai/rookery/internal/profile"
	"github.com/rookery-ai/rookery/internal/prompts"
	"github.com/rookery-ai/rookery/internal/secrets"
	"github.com/rookery-ai/rookery/internal/skilllibrary"
	"github.com/rookery-ai/rookery/internal/skillstore"
	"github.com/rookery-ai/rookery/internal/vault"
)

const (
	maxCallDepth = 3 // maximum agent-to-agent call depth
	maxTurns     = 5 // maximum coder.Generate calls per top-level run
	// One limit, defined in agentstate and re-exported here so the existing
	// call sites read unchanged. Two constants would drift.
	maxStateSize = agentstate.MaxStateSize
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
	// tools, CLI coders via the loopback bridge (rookery connector exec).
	connReg    *connectors.Registry
	connStore  connectors.TokenStore
	connBridge *connectors.Bridge
	parkerFor  ParkerFactory

	// kbBridge, when set, lets a CLI coder's agent run reach the knowledge base's
	// conversion + search paths via `rookery kb convert|search` (the same
	// vault.ImportFile / Searcher code the API engine's save_to_kb/search_files
	// tools call in-process). nil for tests that don't wire one.
	kbBridge *vault.Bridge

	// stateBridge, when set, lets a CLI coder's agent run reach its own state.md
	// via `rookery state get|set` — the same agentstate.Get/Apply the API engine's
	// get_state/set_state tools call in-process. Without it a CLI coder's only way
	// to record memory is hand-editing the file, which is exactly how two live
	// agents stranded their state outside the json fence and went permanently
	// silent. nil for tests that don't wire one.
	stateBridge *agentstate.Bridge

	// MCP: when set, an agent's BOUND servers (agent_mcp_servers) are exposed to
	// both coder types — the API engine calls mcpClient in-process, a CLI coder
	// reaches the same mcp.Execute via mcpBridge (`rookery mcp exec`).
	mcpClient    *mcp.Client
	mcpBridge    *mcp.Bridge
	mcpParkerFor MCPParkerFactory
}

// MCPParkerFactory returns the approval gate for one agent's MCP calls, or nil when
// that agent has no gated binding. Separate from ParkerFactory because the two layers
// identify a call differently — (connection, action) versus (server, tool) — while
// meaning exactly the same thing to the owner.
type MCPParkerFactory func(ctx context.Context, workspaceID, agentID, agentName string) mcp.Parker

// WithMCP wires the MCP client + loopback bridge so an agent's bound MCP servers are
// usable by every coder type, exactly as WithConnectors does for service connections.
func (r *Runner) WithMCP(c *mcp.Client, bridge *mcp.Bridge) *Runner {
	r.mcpClient = c
	r.mcpBridge = bridge
	return r
}

// WithMCPApprovalGate installs the per-agent MCP approval gate.
func (r *Runner) WithMCPApprovalGate(f MCPParkerFactory) *Runner {
	r.mcpParkerFor = f
	return r
}

// WithConnectors wires the self-managed-OAuth connector registry + token store + loopback
// bridge so an agent's bound service connections are usable by every coder type: the API
// engine calls connectors.Execute in-process; a CLI coder shells out to
// `rookery connector exec`, which reaches the same Execute via the bridge.
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

// WithStateBridge wires the loopback agent-state bridge so a CLI coder's run can
// reach `rookery state get|set` — parity with the API engine's get_state/set_state
// host tools, so changing coder kind cannot change what an agent can remember.
func (r *Runner) WithStateBridge(b *agentstate.Bridge) *Runner {
	r.stateBridge = b
	return r
}

// WithKBBridge wires the loopback KB bridge so a CLI coder's agent run can reach
// `rookery kb convert|search` (parity with the API engine's built-in
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

// ─── Coder agent execution ────────────────────────────────────────────────────

// coderRunContext tracks mutable state across the turns of one top-level run.
type coderRunContext struct {
	turnsUsed      int
	chatLines      []string
	warnings       []string
	rawChunks      []string    // raw coder output per turn, joined into the run note
	lastRaw        string      // raw text of the most recent turn (fallback prose source)
	offeredTools   []string    // tools the most recent turn offered the model (API engine; empty for CLI)
	silentSignaled bool        // any turn emitted [SILENT] — run is intentionally quiet
	usage          coder.Usage // accumulated token usage (API coder); zero for CLI coders
	// toolTrace accumulates what the model actually DID across every turn. A run
	// that produces nothing records its cost and its outcome; without this it
	// records nothing about the path it took, which is the only thing that
	// explains either. Three diagnoses of one failing agent were made by
	// inferring the calls from token counts, and all three were wrong.
	toolTrace []coder.ToolCallStat
	// stopReason is the engine's account of WHY the last turn ended: "" (finished
	// normally), "truncated", "empty", "budget", "unproductive", "hard-ceiling".
	// Logged because a run that delivers a fallback message looks identical from
	// the outside whatever produced it — which is how a truncating reasoning model
	// was diagnosed as a large-file problem four times over.
	stopReason string
	// transcript interleaves progress milestones (tool calls, verification
	// nudges, delivered [CHAT] output) with the coder's own raw turns, in the
	// order they happened, and is persisted with the run.
	//
	// Distinct from toolTrace, which is a per-call SUMMARY (name, turn, bytes,
	// error) built for one log line and carrying neither arguments nor ordering
	// against the model's own replies. Both are kept: the summary answers "how
	// many calls, how big, how many failed", the transcript answers "what did it
	// do, in what order, and what did it say about it".
	transcript *transcriptCollector
}

// outcome assembles what FinishAgentRun records.
//
// Centralised because Run has THREE exit paths — coder error, produced-nothing,
// and success — and each writes its own row. The transcript and the silent flag
// were added long after those call sites existed, and a path that forgot one
// would produce a run with no debugging record and no sign that anything was
// missing, which is the exact failure this whole change set is about.
func (rctx *coderRunContext) outcome(exitCode int, stdout, stderr string) db.RunOutcome {
	out := db.RunOutcome{
		ExitCode:         exitCode,
		Stdout:           stdout,
		Stderr:           stderr,
		Silent:           rctx.silentSignaled,
		PromptTokens:     rctx.usage.PromptTokens,
		CompletionTokens: rctx.usage.CompletionTokens,
		TotalTokens:      rctx.usage.TotalTokens,
		CachedTokens:     rctx.usage.CachedTokens,
		CacheReported:    rctx.usage.CacheReported,
		CostUSD:          rctx.usage.Cost,
		CostReported:     rctx.usage.CostReported,
	}
	if rctx.transcript != nil {
		// The tool summary is appended as the transcript's LAST event rather
		// than kept in a column of its own: it is a closing note about the run,
		// it already has a rendering (SummarizeToolTrace, written for the log
		// line), and a second column would be a second thing to forget.
		if s := coder.SummarizeToolTrace(rctx.toolTrace); s != "" {
			rctx.transcript.add(EventSummary, s)
		}
		out.Transcript = rctx.transcript.encode()
	}
	return out
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
	// A scheduled run had no idea what day it was: nothing in prompt
	// construction called time.Now() except the reminder parser.
	var runtimeCtx string
	if r.db != nil {
		runtimeCtx = profile.RuntimeContextString(r.db, input.WorkspaceID, time.Now())
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
	// The exact tool names a CLI coder invokes via `rookery connector exec <tool>`.
	var connToolNames []string
	for _, d := range r.connReg.ToolDefs(boundConns) {
		connToolNames = append(connToolNames, d.Name)
	}

	prompt := prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD:         string(agentMD),
		StateJSON:       string(stateJSON),
		RuntimeContext:  runtimeCtx,
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

	// MCP tools are described in their own block rather than folded into the
	// connector one: a connector action is a curated call against a known API, while
	// an MCP tool is whatever a server the owner added chose to advertise, and the
	// model needs that distinction to choose between two tools that sound alike.
	if r.mcpClient != nil {
		if boundMCP, err := mcp.BoundServersForAgent(ctx, r.db, r.systemKey, agent.ID); err == nil && len(boundMCP) > 0 {
			var refs []prompts.MCPServerRef
			for _, b := range boundMCP {
				refs = append(refs, prompts.MCPServerRef{Name: b.Name})
			}
			prompt += "\n" + prompts.MCPToolsBlock(refs, mcp.ToolNames(boundMCP), backendTypeOf(baseCoder), connectorBinPath())
		}
	}

	if baseCoder == nil {
		return fmt.Errorf("no coder service configured")
	}
	// Run inside the agent's own directory (not the shared per-user home) so
	// tools/*.py and state.md resolve correctly and runs never see other
	// agents' files. Pre-approve the tools agents need so the subprocess never
	// blocks on interactive permission prompts (--setting-sources "" suppresses
	// all settings).
	// WithAgentName is what stops set_state creating a state.md headed "# State — "
	// with a blank name. A brand-new agent genuinely has no state file (nothing seeds
	// one — see agentdesigner/flow.go), so the first set_state call of its first run
	// creates it from the template, and every later write only splices the fence. A
	// blank heading written there is permanent.
	// Built here rather than just before runCoderTurns because the coder needs
	// its transcript collector as a progress sink, and the sink has to be in
	// place before the coder is constructed.
	//
	// The transcript is collected at THIS layer, not at the web layer, because
	// this is the only layer both triggers pass through: the scheduler wires
	// SendOutput and no OnProgress at all, so a collector attached where the
	// manual run is started would leave cron runs — the ones nobody watched, and
	// so the ones most in need of a record — with nothing captured.
	rctx := &coderRunContext{transcript: &transcriptCollector{}}

	coderSvc := baseCoder.WithDir(agentDir).WithAgentName(agent.Name).WithAllowedTools("Bash,WebFetch,Read,Write,Edit").WithProgress(rctx.transcript.wrap(input.OnProgress))

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
		// `rookery connector exec <tool>` reaches the same connectors.Execute.
		if r.connBridge != nil && r.connBridge.Addr() != "" {
			token := r.connBridge.RegisterGated(input.WorkspaceID, boundConns, false, parker)
			defer r.connBridge.Unregister(token)
			extraEnv["ROOKERY_CONNECTOR_URL"] = r.connBridge.Addr()
			extraEnv["ROOKERY_CONNECTOR_TOKEN"] = token
		}
	}
	// Expose the agent's BOUND MCP servers to both coder types. Only bound ones:
	// a build sees every enabled server, a run sees what the agent declared —
	// the same narrowing agent_connections applies.
	if r.mcpClient != nil {
		if boundMCP, err := mcp.BoundServersForAgent(ctx, r.db, r.systemKey, agent.ID); err == nil && len(boundMCP) > 0 {
			var mcpParker mcp.Parker
			if r.mcpParkerFor != nil {
				mcpParker = r.mcpParkerFor(ctx, input.WorkspaceID, agent.ID, agent.Name)
			}
			coderSvc = coderSvc.WithMCP(r.mcpClient, boundMCP).WithMCPParker(mcpParker)
			if r.mcpBridge != nil && r.mcpBridge.Addr() != "" {
				token := r.mcpBridge.RegisterGated(input.WorkspaceID, boundMCP, false, mcpParker)
				defer r.mcpBridge.Unregister(token)
				extraEnv["ROOKERY_MCP_URL"] = r.mcpBridge.Addr()
				extraEnv["ROOKERY_MCP_TOKEN"] = token
			}
		}
	}
	// CLI coders: register a run-scoped KB bridge token so `rookery kb
	// convert|search` reaches the same vault.ImportFile / Searcher code the API
	// engine's save_to_kb/search_files tools call in-process. Unregistered when
	// the run ends, alongside the connector-token cleanup above.
	if r.kbBridge != nil && r.kbBridge.URL() != "" {
		kbToken := r.kbBridge.Register(input.WorkspaceID, false)
		defer r.kbBridge.Unregister(kbToken)
		extraEnv["ROOKERY_KB_URL"] = r.kbBridge.URL()
		extraEnv["ROOKERY_KB_TOKEN"] = kbToken
	}
	// Scoped to THIS agent's directory and name, not to the workspace: state.md is
	// per-agent, so a token that reached the workspace would let one agent read and
	// overwrite another's memory.
	if r.stateBridge != nil && r.stateBridge.Addr() != "" {
		stateToken := r.stateBridge.Register(agentDir, agent.Name)
		defer r.stateBridge.Unregister(stateToken)
		extraEnv["ROOKERY_STATE_URL"] = r.stateBridge.Addr()
		extraEnv["ROOKERY_STATE_TOKEN"] = stateToken
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

	runErr := r.runCoderTurns(ctx, agent, input, agentDir, stateMap, stateReadOK, prompt, coderSvc, rctx)

	// A coder that returned zero bytes did not run quietly — it did not run. Treat
	// it as a failure so the run shows as failed, the message says what actually
	// happened, and the next scheduled run is not preceded by a green tick.
	producedNothing := runErr == nil && coderProducedNothing(rctx.chatLines, rctx.rawChunks, rctx.silentSignaled)

	exitCode := 0
	if runErr != nil || producedNothing {
		exitCode = -1
	}
	r.reflectRun(input, agent, runID, exitCode, startedAt, rctx)

	// One line per finished run. Agent runs previously logged NOTHING on the happy
	// path, so a run that produced no output at all left no trace anywhere except
	// an empty "Raw output" section in its own log note — the designer has had
	// build_id tracing for exactly this reason, and diagnosing a silent run
	// without it meant reading the database. Cheap, once per run.
	//
	// DEFERRED, and that is load-bearing. Two of the warnings this counts are
	// appended in the delivery phase far below — "no [CHAT] marker emitted" and
	// "no deliverable prose" — so emitting here inline read len(rctx.warnings)
	// before either existed and reported warnings=0 for exactly the silent runs
	// this line was added to explain. (The seven appends inside runCoderTurns are
	// unaffected: that call has already returned by this point, which is why the
	// field was right for a [CALL:] warning and wrong for a suppressed delivery.)
	//
	// A defer rather than moving the statement down: Run has three exit paths —
	// coder error, produced-nothing, and success — and only the last reaches the
	// delivery phase, so a relocated statement would silently stop logging the
	// other two. rctx is a pointer and exitCode/producedNothing are captured by
	// reference, so the closure sees final values on every path.
	defer func() {
		slog.Info("agentrunner: run finished",
			"run_id", runID, "agent_id", input.AgentID, "agent", agent.Name,
			"trigger", input.Trigger, "exit", exitCode,
			"raw_chunks", len(rctx.rawChunks), "chat_lines", len(rctx.chatLines),
			"silent", rctx.silentSignaled, "produced_nothing", producedNothing,
			"warnings", len(rctx.warnings), "total_tokens", rctx.usage.TotalTokens,
			// "n/a" rather than 0 when the provider reported nothing: a zero
			// would read as "caching is broken", which is a finding, when the
			// truth is "we cannot tell", which is not.
			"cached_tokens", cachedTokensField(rctx.usage),
			"cost", costField(rctx.usage),
			"stop_reason", rctx.stopReason,
			"tools", coder.SummarizeToolTrace(rctx.toolTrace))
	}()

	if runErr != nil {
		_ = r.db.FinishAgentRun(runID, rctx.outcome(-1,
			strings.Join(rctx.chatLines, "\n"),
			strings.Join(rctx.warnings, "\n")+"\n"+runErr.Error()))
		friendly := FriendlyRunError(runErr, coderSvc.Name())
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

	if producedNothing {
		// Deliberately NOT the "produced no notification" wording: that phrasing
		// describes an agent that ran and chose not to speak, which is the one thing
		// this is not. Naming the real event is what lets the owner tell a broken
		// model apart from a working agent with nothing to report.
		msg := "⚠️ Ran but the model returned no output at all — nothing was checked and no state was saved. See the run log."
		_ = r.db.FinishAgentRun(runID, rctx.outcome(-1, "",
			strings.Join(append(rctx.warnings, "coder returned no output"), "\n")))
		if input.SendOutput != nil {
			input.SendOutput(msg)
		}
		r.recordInbox(input, agent, runID, msg, "error")
		if input.OnProgress != nil {
			input.OnProgress(msg)
		}
		return errors.New("coder returned no output")
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
		if prose := deliverableProse(rctx.lastRaw, rctx.offeredTools); prose != "" {
			rctx.warnings = append(rctx.warnings, "no [CHAT] marker emitted; delivered prose as fallback")
			finalOutput = prose
		} else if strings.TrimSpace(rctx.lastRaw) != "" {
			// Both causes, because this fires on ANY empty prose: the reply may have been
			// tool-call scaffolding we refused to forward, or it may have been nothing but
			// protocol markers from a state-only agent that forgot [SILENT]. Naming only
			// the first asserted scaffolding that was never there, during triage, which is
			// the one moment anyone reads this line.
			rctx.warnings = append(rctx.warnings,
				"no deliverable prose (markers only, or tool-call scaffolding) — nothing sent")
		}
	}

	_ = r.db.FinishAgentRun(runID, rctx.outcome(0, finalOutput, strings.Join(rctx.warnings, "\n")))

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
		// No agent name here: the chat copy is labelled at the send site by
		// gateway.AgentPrefixed, and the inbox copy carries AgentName as its
		// own column — repeating it would read "🤖 weather … ⚠️ weather ran…".
		warn := "⚠️ Ran but produced no notification — see the run log."
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
	var activity []string
	if rctx.transcript != nil {
		activity = rctx.transcript.activityLines()
	}
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
		Activity:         activity,
		CachedTokens:     rctx.usage.CachedTokens,
		CacheReported:    rctx.usage.CacheReported,
		CostUSD:          rctx.usage.Cost,
		CostReported:     rctx.usage.CostReported,
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
//
// The row is the whole record. The notification is NOT reflected into the vault:
// ReflectAgentRun has already archived the exact delivered text in
// agents/<id>/logs/run_<ts>.md under "Output sent to user", so an inbox/<uuid>.md
// note was a third copy that cluttered the KB browser and fed designer retrieval
// with weather reports.
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
		rctx.toolTrace = append(rctx.toolTrace, result.ToolTrace...)
		// Keep the LAST non-empty stop reason. A multi-turn run's final turn is
		// the one that decided the outcome, and "" is the engine's explicit
		// statement that a turn finished of its own accord — so an ordinary last
		// turn must not erase the reason an earlier one was cut short.
		if result.StopReason != "" {
			rctx.stopReason = result.StopReason
		}

		parsed := parseCoderOutput(result.Text)
		rctx.chatLines = append(rctx.chatLines, parsed.chatLines...)
		rctx.warnings = append(rctx.warnings, parsed.warnings...)
		if parsed.silent {
			rctx.silentSignaled = true
		}
		rctx.lastRaw = result.Text
		rctx.offeredTools = result.OfferedTools

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
		// Recorded here rather than reconstructed from rawChunks at the end, so
		// the turn lands in the transcript BETWEEN the tool calls that preceded
		// it and those that follow. Joining two lists by position afterwards
		// would produce the same thing with a way to get the order wrong.
		if rctx.transcript != nil {
			rctx.transcript.add(EventCoder, result.Text)
		}

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

// FriendlyRunError converts a low-level run failure into a message safe to
// show the user directly (web UI error banner, or sent as a chat message for
// cron-triggered runs). Usage-limit hits are an expected, recurring condition
// — not an agent bug — so they get a distinct, reassuring message instead of
// a raw exit code. coderName identifies the underlying CLI binary (e.g.
// "claude") so the message stays accurate across different coder profiles;
// pass "" to fall back to a generic phrase.
//
// Exported because the KB assist endpoint (web/api_kb_assist.go) needs the same
// wording: a workspace out of quota must not get one sentence from a scheduled
// run and a different one from the note editor.
func FriendlyRunError(err error, coderName string) string {
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
	if errors.Is(err, coder.ErrProviderEmpty) {
		// The provider answered 2xx with nothing in it, on every retry. Nothing
		// about the agent or the request is wrong, and nothing partial survives —
		// so the only useful instruction is to run it again.
		//
		// This case used to fall through to the raw err.Error() below, which
		// showed someone whose run had just spent ten minutes retrying the string
		// "llm: empty response body (status 200)" — an accurate sentence that
		// tells them nothing they can act on, and reads like a bug in their agent.
		return fmt.Sprintf("⚠️ %s got no response from the provider, on every retry — a temporary problem at their end, not with this agent. Nothing ran, so nothing was lost. Try again.", who)
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
		if isSilentMarker(trimmed) {
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
// coderProducedNothing reports whether a run's coder returned no output at all —
// as distinct from a run that ran fine and merely forgot the [CHAT] marker.
//
// The two used to be conflated and both reported as exit 0 with "⚠️ Ran but
// produced no notification". They are not the same event. A forgotten marker
// leaves prose behind to deliver and means the agent did its work; an EMPTY
// response means nothing was fetched, nothing was decided, and no state was
// written — the agent's whole job was skipped. Calling that a successful quiet
// run is how a real agent sat at state.md = {} for two runs while telling its
// owner, twice, that it had simply produced no notification.
//
// Judged on RAW output rather than the parsed result, because that is the only
// place the difference survives: parsing an empty string and parsing a paragraph
// with no markers both yield zero chat lines.
//
// A [SILENT] run is never "nothing": the marker is a decision the agent made and
// stated, which is exactly the output we asked it for.
func coderProducedNothing(chatLines, rawChunks []string, silent bool) bool {
	if silent || len(chatLines) > 0 {
		return false
	}
	for _, chunk := range rawChunks {
		if strings.TrimSpace(chunk) != "" {
			return false
		}
	}
	return true
}

// isSilentMarker reports whether a line IS the [SILENT] marker, allowing for the
// ways a model decorates a token: **[SILENT]**, `[SILENT]`, [silent], [SILENT].,
// [/SILENT], or a bare SILENT.
//
// Lenient about DECORATION, strict about CONTEXT, and the asymmetry is the whole
// design. The two failures are not equally bad:
//
//   - A missed marker makes a correctly-behaving agent send "⚠️ Ran but produced
//     no notification" every time it had nothing to say. Noisy, but the user can
//     see something is wrong.
//   - A marker matched inside a sentence ("I stayed [SILENT] last night") would
//     suppress a real message the user was waiting for, and nothing would say so.
//
// So only a line that IS the marker once decoration is removed counts; a mention
// inside prose never does. A bare "silent" line is accepted because models write
// it, and the blast radius is bounded: `silent` only suppresses the prose
// fallback and the empty-run warning, so a run that produced actual [CHAT]
// content still delivers it.
func isSilentMarker(line string) bool {
	s := strings.TrimSpace(line)
	// Peel decoration from both ends: emphasis, code ticks, quotes.
	s = strings.Trim(s, "*_`\"' \t")
	// Trailing sentence punctuation a model may append.
	s = strings.TrimRight(s, ".!?,;:")
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "[silent]", "[/silent]", "silent":
		return true
	}
	return false
}

// deliverableProse is the floor under the prose fallback: the message to deliver, or
// "" when there is nothing safe to send.
//
// The fallback's job is to rescue a run whose model forgot the [CHAT] marker. It is
// NOT to forward whatever the model happened to emit — a distinction that cost a real
// user, who received DeepSeek's raw tool-call markup as a notification. Keyed on the
// tools the run itself offered, so it needs no knowledge of any provider's dialect.
func deliverableProse(raw string, offeredTools []string) string {
	prose := extractProseMessage(raw)
	if prose == "" {
		return ""
	}
	if coder.LooksLikeToolScaffolding(prose, offeredTools) {
		return ""
	}
	return prose
}

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
		// isSilentMarker rather than an equality check, so a decorated marker
		// (**[SILENT]**) is stripped here too — otherwise a run that fell back to
		// prose delivery would send the user the literal marker text.
		if isSilentMarker(t) || t == "[CHAT]" || t == "[/CHAT]" {
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

// mergeState delegates to agentstate.Merge so the semantics an agent sees are
// identical whichever door it used — the [STATE] marker here, the API engine's
// set_state tool, or the CLI bridge. Two copies of "nil deletes the key" is
// exactly how those doors would drift apart.
func mergeState(existing map[string]interface{}, update map[string]interface{}) {
	agentstate.Merge(existing, update)
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

// applyAndSaveState merges this turn's [STATE] updates (if any) into the state
// that is actually on disk, and persists the result.
//
// "On disk", not `currentState`: the agent may have written state.md during this
// turn through set_state or `rookery state set`, and currentState is only the
// run-start snapshot. Which of the two the patch lands on is decided below and
// is the subtlest thing in this file.
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
	if len(updates) == 0 && !stateReadOK {
		return nil
	}

	// Combine this turn's updates into ONE patch, then let agentstate.Apply merge
	// it into whatever the file currently holds.
	//
	// This must not write `currentState` back wholesale, and that distinction is
	// the whole point. currentState was read at RUN START; the agent may since
	// have called set_state (API engine) or `rookery state set` (CLI bridge),
	// which write the file directly. Replacing the file with the run-start map
	// plus this turn's markers would DISCARD those writes — re-creating, through
	// the new tools, the exact "the agent stored something and the next run saw
	// nothing" failure this whole change exists to remove.
	//
	// The updates are combined with a plain assignment rather than Merge: Merge
	// treats a nil value as "delete this key", so merging a deletion into an
	// empty patch would erase it from the patch instead of recording it, and the
	// key would quietly survive in the file.
	patch := map[string]interface{}{}
	for _, update := range updates {
		for k, v := range update {
			patch[k] = v
		}
	}

	// Which base the patch lands on depends on whether the FILE still makes sense,
	// and both answers are load-bearing:
	//
	//   understood — trust the file. It may hold a set_state / `rookery state set`
	//   write made during this turn, and the run-start snapshot does not.
	//
	//   not understood, or RECOVERED, or gone empty — all while the run started
	//   with state. Each means the agent mangled the file mid-run (a full-file
	//   write_file editing "## Notes" drops the fence entirely) without emitting
	//   [STATE], so the run-start snapshot is the best truth available and
	//   writing it back is the self-heal that stops a formatting slip costing the
	//   agent's memory.
	//
	// `understood` alone cannot decide this: a file with NO fence is both "a
	// fresh agent" and "an agent that just deleted its fence". Nor is "the file
	// went empty" enough — that misses the case a review reproduced: the rewrite
	// leaves a JSON snippet in its prose (a quoted API error), recovery adopts
	// the snippet, base is non-empty, and the real state is destroyed. `recovered`
	// is the missing signal: state found OUTSIDE the fence means the file is
	// damaged by construction, whatever it happens to contain.
	//
	// All three are gated on the run having STARTED with state, so a genuinely
	// fresh agent still adopts what it wrote, and an hn-watch-shaped file whose
	// data was always stranded still recovers on its next run.
	//
	// The accepted cost, stated rather than hidden: an agent that deliberately
	// clears its entire state through set_state has that clear undone — whether
	// or not it emits an unrelated [STATE] in the same turn. Restoring memory a
	// slip destroyed is the failure worth optimising for; wholesale deliberate
	// clearing is not a thing agents do.
	path := agentstate.StateFilePath(agentDir)
	base, understood, recovered, err := agentstate.GetDetail(path)
	if err != nil {
		return err
	}
	if !understood || ((len(base) == 0 || recovered) && len(currentState) > 0) {
		base = map[string]interface{}{}
		for k, v := range currentState {
			base[k] = v
		}
	}
	agentstate.Merge(base, patch)

	// Keep the in-run view in step, so a later turn of the SAME run sees what the
	// file now holds rather than the run-start snapshot.
	for k := range currentState {
		delete(currentState, k)
	}
	for k, v := range base {
		currentState[k] = v
	}
	return saveState(agentDir, agentName, base)
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
// addUsage delegates to llm.Usage.Add, the single definition.
//
// This function used to enumerate three fields, and that is exactly how it
// broke: the engine parsed CachedTokens and CacheReported correctly and carried
// them out, and this summing discarded both — so the run log reported "n/a" for
// a provider that reports cache statistics on every response. Do not reintroduce
// per-field copying here; a new field would be dropped the same way.
func addUsage(a, b coder.Usage) coder.Usage { return a.Add(b) }

// connectorBinPath is the absolute path to the running rookery binary, which a CLI
// coder invokes as `<bin> connector exec …`. Falls back to "" (bare name via PATH) if
// os.Executable() fails.
func connectorBinPath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return ""
}
