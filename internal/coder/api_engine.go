package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/prompts"
)

// maxAPITurns is the BASE turn budget for agent runs and one-off chat. It is spent
// only by turns that achieved nothing (see turnbudget.go) — a run making real
// progress is not stopped by it. Exhausting it does NOT produce an error: the loop
// ends with an engine-composed account of the run (see exhaustionSummary).
const maxAPITurns = 30

// maxBuildAPITurns is the base budget during an agent BUILD, which shares its turns
// between the actual work, up to maxVerifyNudges finish-verification nudges, and the
// grace turn. It must stay larger than maxAPITurns for that reason.
const maxBuildAPITurns = 50

// maxHardTurns is runaway protection and is NEVER extended, however productive the
// loop claims to be.
//
// It is not reachable in practice on most models today: nothing trims req.Messages,
// so a 128k-context model exceeds its window somewhere around turn 45-50 and the
// provider errors first. History compaction is the prerequisite for this ceiling to
// mean what it says; see the design doc.
const maxHardTurns = 150

// maxUnproductiveStreak stops a model that is going nowhere long before the base
// budget would. Six consecutive turns with no successful tool call is not a slow
// run, it is a stuck one.
const maxUnproductiveStreak = 6

// buildMaxTokens raises the completion-token cap during a build so a large single
// write_file (a full AGENT.md plus a script in one call) is not truncated
// mid-content at the default 4096, which leaves partial, unparseable Python on disk.
const buildMaxTokens = 8192

// runAPI drives the in-process LLM tool-calling loop for a workspace whose
// coder_kind is "api". It resolves the provider key via the secrets lookup,
// builds the provider, then loops: Complete → execute tool calls against the
// vault → feed results back → Complete again, until the model emits a final
// answer (no tool calls) or the turn budget / timeout is hit. The model's final
// text carries the same agent output-protocol markers ([CHAT]/[STATE]/[SILENT])
// as a CLI coder, so the runner's parser works unchanged.
func (c *Coder) runAPI(ctx context.Context, workspaceID, prompt string) (*Result, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	prov, err := c.resolveProvider(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	tools := c.buildHostTools(workspaceID)
	// Clear any spill files left by a previous run of this agent so .rookery_out can't grow
	// unbounded across runs (the live agent dir, unlike a build dir, is never cleaned up).
	tools.clearSpillDir()
	var offeredTools []llm.Tool
	if !c.noTools {
		offeredTools = tools.tools()
	}
	// During a build, raise the completion-token cap so a large single write_file isn't
	// truncated (H2). tools.verifyBuild is set only when ROOKERY_BUILD_PHASE=generation.
	maxTokens := 0
	if tools.verifyBuild {
		maxTokens = buildMaxTokens
	}
	// A text-only call (WithNoTools) must NOT be told to emit the output protocol.
	// The protocol kickoff used to be sent unconditionally, so every one-shot
	// Generate — the KB rewrite panel, skill-metadata extraction, reminder parsing,
	// Ping — was instructed to wrap its answer in [CHAT], and a well-behaved model
	// did. The two JSON callers then failed to parse and fell back silently, which
	// is why it survived so long. Keyed on noTools because the two coincide exactly
	// today and every WithNoTools caller was audited; a future caller wanting
	// protocol markers WITHOUT tools needs an explicit opt-in rather than
	// inheriting one by accident.
	kickoff := prompts.APIEngineKickoffMessage
	if c.noTools {
		kickoff = prompts.APIEngineTextKickoffMessage
	}
	req := llm.Request{
		Model:     c.api.model,
		System:    prompt,
		Messages:  []llm.Message{{Role: "user", Content: kickoff}},
		Tools:     offeredTools,
		MaxTokens: maxTokens,
	}
	return c.runToolLoop(ctx, prov, tools, req, start)
}

// runToolLoop drives the Complete → execute host tools → feed results back →
// Complete loop shared by runAPI (single user kickoff) and chatToolsAPI (threaded
// chat history). It terminates when the model emits a final answer with no tool
// calls, when the turn budget is exhausted (base budget, unproductive streak or
// hard ceiling — see turnbudget.go), or when the context deadline passes. An
// exhausted budget is not an error: it degrades to a best-effort grace turn over
// an engine-composed summary of what the run actually did. A model that rejects
// the tools field degrades to a single no-tool reasoning turn rather than failing
// the call.
func (c *Coder) runToolLoop(ctx context.Context, prov llm.Provider, tools *hostToolSet, req llm.Request, start time.Time) (*Result, error) {
	var total llm.Usage
	toolsDisabled := false
	budget := newTurnBudget(tools.verifyBuild)
	offered := toolNames(req.Tools)
	var stopReason string
	for {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w after %s", ErrTimeout, c.timeout)
		}
		// Counted at the top so paths that `continue` without running a tool still
		// consume the hard ceiling — the loop is bounded by construction, not by the
		// two `continue` sites happening to have counters of their own.
		if stop, reason := budget.iterate(); stop {
			stopReason = reason
			slog.Info("coder: tool loop stopped", "reason", reason, "turns", budget.turns)
			break
		}
		resp, err := prov.Complete(ctx, req)
		if err != nil {
			// A model that rejects the tools field degrades to a single no-tool
			// reasoning turn rather than failing the run.
			if errors.Is(err, llm.ErrToolsUnsupported) && len(req.Tools) > 0 && !toolsDisabled {
				toolsDisabled = true
				req.Tools = nil
				continue
			}
			return nil, mapProviderErr(err)
		}
		total = addUsage(total, resp.Usage)

		if len(resp.ToolCalls) == 0 {
			// The model wants to finish. During a build, refuse to accept a "done" that
			// ships a helper script it never got real output from — nudge it to run and
			// fix the script instead (bounded; the last nudge tells it to report the
			// failure to the user in plain language). Returns "" once verification passes,
			// isn't applicable (a real run, or no script), or the nudge budget is spent.
			if nudge := tools.verifyFinishNudge(); nudge != "" {
				if c.progress != nil {
					c.progress("🔁 verifying " + tools.buildSpec().ProgressNoun + " actually works…")
				}
				req.Messages = append(req.Messages,
					llm.Message{Role: "assistant", Content: resp.Content},
					llm.Message{Role: "user", Content: nudge},
				)
				continue
			}
			// Final answer.
			res := &Result{Text: resp.Content, Duration: time.Since(start), Usage: total}
			res.ScriptVerified = tools.scriptVerified()
			res.ScriptOutput = tools.verifiedOutput()
			res.ScriptRan = tools.authoredScriptRan()
			res.UsedConnectionIDs = tools.usedConnectionIDs()
			res.UsedMCPServerIDs = tools.usedMCPServerIDList()
			res.UsedMCPServerIDs = tools.usedMCPServerIDList()
			return res, nil
		}

		// Append the assistant turn (text + tool calls), then execute each tool
		// call against the vault and append the results.
		req.Messages = append(req.Messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		productiveBefore := tools.callStats().Productive
		for _, tc := range resp.ToolCalls {
			if c.progress != nil {
				c.progress(toolMilestone(tc, tools.vaultRootPath(), tools.homeDirPath()))
			}
			result := tools.executeOrNudge(ctx, tc)
			req.Messages = append(req.Messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    result,
			})
		}
		// A turn counts as progress only if some tool actually succeeded on it.
		if stop, reason := budget.next(tools.callStats().Productive > productiveBefore); stop {
			stopReason = reason
			slog.Info("coder: tool loop stopped", "reason", reason, "turns", budget.turns)
			break
		}
	}
	res, err := c.graceTurnOnBudgetExhausted(ctx, prov, req, total, start, tools.verifyBuild, tools.callStats(), offered, stopReason)
	if res != nil {
		// A build whose script the engine already ran must surface that ground truth even
		// when it exhausted its turn budget — the review path uses it as the sample.
		res.ScriptVerified = tools.scriptVerified()
		res.ScriptOutput = tools.verifiedOutput()
		res.ScriptRan = tools.authoredScriptRan()
		res.UsedConnectionIDs = tools.usedConnectionIDs()
		res.UsedMCPServerIDs = tools.usedMCPServerIDList()
	}
	return res, err
}

// graceTurnBudgetNudge is appended as the final message when a BUILD's tool-calling loop
// exhausts its turn budget without producing a final answer. It asks for [BLOCKED] because
// the agent designer parses that marker (agentdesigner.parseBlockedOutput). It must NOT be
// used for a run or one-off chat, which don't understand [BLOCKED] and would surface it as
// stray text to the user (see graceTurnWrapUpNudge).
const graceTurnBudgetNudge = "You have used all available tool-call turns without finishing. " +
	"Stop — do not attempt any more tool calls (none are offered on this turn). " +
	"Immediately emit [BLOCKED] now: one line on what you were trying to do, one line on " +
	"what specifically failed or is still missing, and one or two concrete alternatives — " +
	"or, if there is truly no alternative, say plainly that this isn't currently possible " +
	"and state what you CAN do instead."

// graceTurnWrapUpNudge is the non-build equivalent (agent runs, one-off chat): the same
// turn-budget bail-out, but WITHOUT the build-only [BLOCKED] convention leaking into a
// chat/run reply. It asks the model to close out in plain language.
const graceTurnWrapUpNudge = "You have used all available tool-call turns. " +
	"Stop — do not attempt any more tool calls (none are offered on this turn). " +
	"In plain language, tell the user what you were able to do and what you could not " +
	"finish, and suggest a next step if there is one. Do not emit any special markers or " +
	"technical error text — just a normal, helpful message."

// exhaustionSummary composes the user-facing account of a run that ran out of steps,
// from facts the engine already holds.
//
// The alternative — asking the model to explain its own failure and trusting the
// reply — is what delivered raw tool-call markup to a user. A model that has just
// failed to finish is the last thing that should narrate the outcome, so the grace
// turn became optional garnish and this became the source of truth.
func exhaustionSummary(stats callStats, reason string) string {
	var b strings.Builder
	switch reason {
	case "unproductive":
		b.WriteString("⚠️ Stopped early: several tool calls in a row achieved nothing.")
	case "hard-ceiling":
		b.WriteString("⚠️ Stopped: the run hit its hard limit on tool calls.")
	default:
		b.WriteString("⚠️ Ran out of steps before finishing.")
	}
	if stats.Productive > 0 {
		b.WriteString(" Completed: ")
		b.WriteString(strconv.Itoa(stats.Productive))
		b.WriteString(" successful tool calls (")
		b.WriteString(strings.Join(stats.SucceededTools, ", "))
		b.WriteString(").")
	}
	if stats.Failed > 0 {
		b.WriteString(" ")
		b.WriteString(strconv.Itoa(stats.Failed))
		b.WriteString(" failed.")
	}
	b.WriteString(" See the run log for detail.")
	return b.String()
}

// toolNames lists the tools offered on this run, for the scaffolding predicate. It
// must be evaluated BEFORE graceTurnOnBudgetExhausted, which nils req.Tools.
func toolNames(tools []llm.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// graceTurnOnBudgetExhausted gives the model exactly one more turn to wrap up gracefully
// instead of failing opaquely when runToolLoop exhausts maxAPITurns. It strips Tools from
// the request (req.Tools = nil forces a text-only reply — the model literally cannot
// request another tool call) and appends a wrap-up nudge, then makes one final Complete
// call. For a BUILD (isBuild) it uses graceTurnBudgetNudge (the [BLOCKED] convention parsed
// by agentdesigner.parseBlockedOutput); for a run/chat it uses graceTurnWrapUpNudge (plain
// language, no [BLOCKED] marker) so the build-only convention never leaks into a chat/run
// reply. Either way a turn-budget exhaustion degrades to a useful message instead of the
// opaque bare ErrMaxTurns a caller previously had no way to present usefully. The reply is
// now GARNISH, not the account of record: exhaustionSummary is composed from facts the
// engine already holds, and is used whenever the grace call fails, returns nothing, or
// returns tool scaffolding. It must not be able to loop further itself (exactly one extra
// Complete call, ever).
func (c *Coder) graceTurnOnBudgetExhausted(ctx context.Context, prov llm.Provider, req llm.Request, total llm.Usage, start time.Time, isBuild bool, stats callStats, offeredTools []string, reason string) (*Result, error) {
	fallback := exhaustionSummary(stats, reason)
	req.Tools = nil
	nudge := graceTurnWrapUpNudge
	if isBuild {
		nudge = graceTurnBudgetNudge
	}
	req.Messages = append(req.Messages, llm.Message{Role: "user", Content: nudge})
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		return &Result{Text: fallback, Duration: time.Since(start), Usage: total}, nil
	}
	total = addUsage(total, resp.Usage)
	text := strings.TrimSpace(resp.Content)
	// Best-effort garnish. We removed the model's structured tool channel a moment
	// ago while it still had work queued, so a reply expressing that work as raw
	// markup is close to expected — and must never be forwarded.
	if text == "" || LooksLikeToolScaffolding(text, offeredTools) {
		text = fallback
	}
	return &Result{Text: text, Duration: time.Since(start), Usage: total}, nil
}

// resolveProvider resolves the workspace's provider API key via the secrets
// lookup and builds the llm.Provider. Shared by runAPI, chatAPI, chatToolsAPI,
// and pingAPI — every API-coder call site resolves the key the same way (lazily,
// on demand), so a path that doesn't pre-inject secrets via env still authenticates.
func (c *Coder) resolveProvider(ctx context.Context, workspaceID string) (llm.Provider, error) {
	if c.secretsLookup == nil {
		return nil, fmt.Errorf("%w: no secrets lookup configured", ErrAPIAuth)
	}
	if c.api.apiKeySecretName == "" {
		return nil, fmt.Errorf("%w: workspace has no coder_api_key_secret set", ErrAPIAuth)
	}
	apiKey, err := c.secretsLookup(ctx, workspaceID, c.api.apiKeySecretName)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve api key: %v", ErrAPIAuth, err)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%w: api key secret %q is empty", ErrAPIAuth, c.api.apiKeySecretName)
	}
	return llm.New(llm.Config{
		Provider: c.api.provider,
		APIKey:   apiKey,
		BaseURL:  c.api.baseURL,
		Model:    c.api.model,
		Timeout:  60 * time.Second,
	})
}

// threadHistory rebuilds a conversation as real alternating user/assistant
// message turns (prior history, then the current user message) and coalesces any
// consecutive same-role turns into one. Coalescing keeps strict-alternation
// providers (Anthropic) happy when history lands two user or two assistant
// messages back-to-back. Shared by chatAPI (text-only design turns) and
// chatToolsAPI (one-off chat with the KB tool loop).
func (c *Coder) threadHistory(history []db.ChatMessage, userMessage string) []llm.Message {
	msgs := make([]llm.Message, 0, len(history)+1)
	for _, m := range history {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		msgs = append(msgs, llm.Message{Role: role, Content: m.Content})
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: userMessage})

	var coalesced []llm.Message
	for _, m := range msgs {
		if n := len(coalesced); n > 0 && coalesced[n-1].Role == m.Role {
			coalesced[n-1].Content += "\n\n" + m.Content
		} else {
			coalesced = append(coalesced, m)
		}
	}
	return coalesced
}

// chatAPI is the API-coder path for the text-only design conversations (agent
// designer, skill designer, skill vetter) — Chat called with WithNoTools. It is a
// SINGLE completion call with no tool loop (design conversations are plain Q&A),
// and sends the conversation history as proper alternating user/assistant message
// turns with the design system prompt as the system message. Flattening the
// history into the system prompt (as the CLI Chat path does) makes the model
// re-ask the opening question every turn — the design loop. The model's reply is
// returned directly as the next assistant turn.
func (c *Coder) chatAPI(ctx context.Context, workspaceID string, history []db.ChatMessage, systemContext, userMessage string) (*Result, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	prov, err := c.resolveProvider(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	msgs := c.threadHistory(history, userMessage)
	resp, err := prov.Complete(ctx, llm.Request{
		Model:     c.api.model,
		System:    systemContext,
		Messages:  msgs,
		MaxTokens: 0,
	})
	if err != nil {
		return nil, mapProviderErr(err)
	}
	return &Result{Text: resp.Content, Duration: time.Since(start), Usage: resp.Usage}, nil
}

// chatToolsAPI is the API-coder path for one-off chat — Chat called WITHOUT
// WithNoTools, so the chat can retrieve and edit the user's knowledge base on
// demand. It combines chatAPI's history-threading (real alternating
// user/assistant turns with the chat system prompt as the system message) with
// runAPI's tool-calling loop: the model is offered the host file tools
// (read_file/write_file/edit_file/list_dir — run_script is excluded for chat
// because the chat workDir is the vault root, matching the chat "no shell"
// boundary) and can call them to read/write the user's notes before replying.
// The model's final answer (after any tool turns) is returned as the reply.
func (c *Coder) chatToolsAPI(ctx context.Context, workspaceID string, history []db.ChatMessage, systemContext, userMessage string) (*Result, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	prov, err := c.resolveProvider(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	tools := c.buildHostTools(workspaceID)
	var offeredTools []llm.Tool
	if !c.noTools {
		offeredTools = tools.tools()
	}
	req := llm.Request{
		Model:     c.api.model,
		System:    systemContext,
		Messages:  c.threadHistory(history, userMessage),
		Tools:     offeredTools,
		MaxTokens: 0,
	}
	return c.runToolLoop(ctx, prov, tools, req, start)
}

// pingAPI verifies the API coder is reachable by issuing a trivial completion.
// workspaceID resolves the provider API key via the secrets lookup — a ping
// against a synthetic/empty ID would never resolve a real workspace's secret.
func (c *Coder) pingAPI(ctx context.Context, workspaceID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if c.secretsLookup == nil || c.api == nil || c.api.apiKeySecretName == "" {
		return "", ErrAPIAuth
	}
	if workspaceID == "" {
		return "", fmt.Errorf("%w: ping requires a workspace id to resolve the api key", ErrAPIAuth)
	}
	apiKey, err := c.secretsLookup(ctx, workspaceID, c.api.apiKeySecretName)
	if err != nil || apiKey == "" {
		return "", fmt.Errorf("%w: cannot resolve api key for ping", ErrAPIAuth)
	}
	prov, err := llm.New(llm.Config{
		Provider: c.api.provider,
		APIKey:   apiKey,
		BaseURL:  c.api.baseURL,
		Model:    c.api.model,
		Timeout:  15 * time.Second,
	})
	if err != nil {
		return "", err
	}
	resp, err := prov.Complete(ctx, llm.Request{
		Model:     c.api.model,
		Messages:  []llm.Message{{Role: "user", Content: prompts.APIEnginePingMessage}},
		MaxTokens: 16,
	})
	if err != nil {
		return "", mapProviderErr(err)
	}
	return c.api.provider + "/" + c.api.model + " (" + resp.Content + ")", nil
}

// buildHostTools assembles the vault-scoped, sandboxed host tool set for this run.
// run_script is offered only for agent runs (workDir = the agent's own dir, a
// subdirectory of the vault) — not for chat (workDir = vault root) and not when
// tools are disabled (design conversation).
func (c *Coder) buildHostTools(workspaceID string) *hostToolSet {
	workDir := c.workDir
	vaultRoot := ""
	if c.vlt != nil {
		vaultRoot = c.vlt.Root(workspaceID)
	}
	if workDir == "" {
		workDir = vaultRoot
	}
	// The powerful/execution tools (run_script, bash, web_fetch) are offered only in an
	// agent execution context (workDir is the agent's own dir, not the vault root). That
	// excludes one-off chat (workDir == vault root), matching the CLI chat's file-only tool
	// set (Read,Write,Edit,Glob,Grep — no shell/web-fetch) so the two backends stay at parity.
	includeExecTools := false
	if !c.noTools && workDir != "" && vaultRoot != "" {
		includeExecTools = filepath.Clean(workDir) != filepath.Clean(vaultRoot)
	}
	return &hostToolSet{
		workspaceID:      workspaceID,
		vlt:              c.vlt,
		workDir:          workDir,
		subprocessEnv:    stripKey(c.extraEnv, c.api.apiKeySecretName),
		sandbox:          c.sandbox,
		selfExe:          c.selfExe,
		dataDir:          c.dataDir,
		homesDir:         c.homesDir,
		includeExecTools: includeExecTools,
		// Enforce script self-verification only during an agent BUILD (the caller sets
		// ROOKERY_BUILD_PHASE=generation). A real run must never block on this — an agent that
		// legitimately has nothing to report must be free to finish silently.
		verifyBuild: c.extraEnv[buildphase.EnvVar] == buildphase.Generation,
		spec:        c.buildSpec,

		connReg:    c.connReg,
		connStore:  c.connStore,
		boundConns: c.boundConns,
		connParker: c.connParker,

		mcpCaller: c.mcpCaller,
		mcpParker: c.mcpParker,
		boundMCP:  c.boundMCP,

		usedConnIDs:      map[string]bool{},
		usedMCPServerIDs: map[string]bool{},
	}
}

// mapProviderErr converts an llm transport error into the coder error taxonomy.
// A transient 429 (ErrRateLimit) becomes ErrRateLimited — "throttled, try again
// shortly" — while genuine quota exhaustion (ErrQuotaExhausted, 402) becomes
// ErrUsageLimit — "out of credits, retries next scheduled run". Keeping these
// distinct stops a transient throttle from being misreported as "you're out of
// quota" (which is exactly why a user whose chat still works gets confused).
func mapProviderErr(err error) error {
	if errors.Is(err, llm.ErrRateLimit) {
		return ErrRateLimited
	}
	if errors.Is(err, llm.ErrQuotaExhausted) {
		return ErrUsageLimit
	}
	if errors.Is(err, llm.ErrAuth) {
		return fmt.Errorf("%w: %v", ErrAPIAuth, err)
	}
	return fmt.Errorf("coder api error: %w", err)
}

func addUsage(a, b llm.Usage) llm.Usage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.TotalTokens += b.TotalTokens
	return a
}

// stripKey returns a copy of env with the named key removed, so the LLM provider
// key is never exposed to the agent's run_script subprocess (the agent's own
// secrets remain available for its API calls).
func stripKey(env map[string]string, key string) map[string]string {
	if key == "" {
		return env
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if k == key {
			continue
		}
		out[k] = v
	}
	return out
}

// toolMilestone renders a short, human-readable line for one tool call, shown on
// the live progress stream so the user can see the API coder act on files as it
// works. Shows the most useful identifier for the call: the path argument for
// file tools, the query for search_files/web_search, the pattern for glob, the
// url for web_fetch, or the command line for bash — falling back to a trimmed
// raw-arg blob when none apply.
//
// `command` is not optional politeness: without it a bash call matched no field
// and fell through to the raw-JSON fallback, so every shell step the agent took
// was shown to the user as a truncated `{"command": "cd /home/…` blob. bash is
// the tool an agent reaches for most, which made it the most visible line on the
// stream and the least readable.
//
// vaultRoot/homeDir drive shortenHostPaths, which strips host filesystem layout
// out of whichever detail is chosen — see its comment for why that has to apply
// to the detail string rather than just to the path-shaped arguments.
func toolMilestone(tc llm.ToolCall, vaultRoot, homeDir string) string {
	var args struct {
		Path    string `json:"path"`
		Query   string `json:"query"`
		Pattern string `json:"pattern"`
		URL     string `json:"url"`
		Command string `json:"command"`
		Source  string `json:"source"`
	}
	_ = json.Unmarshal(tc.Args, &args)
	// Order is by specificity, not by tool: each tool sets exactly one of these,
	// so the first non-empty field IS that tool's subject. `source` (save_to_kb)
	// and `command` (bash) are last only because they are the newest additions —
	// both previously matched nothing and fell through to the raw-JSON blob.
	detail := firstNonEmpty(args.Path, args.Query, args.Pattern, args.URL, args.Command, args.Source)
	if detail == "" {
		detail = strings.TrimSpace(string(tc.Args))
	}
	detail = shortenHostPaths(detail, vaultRoot, homeDir)
	// Truncate AFTER shortening: a bash command that opens with a long absolute
	// vault path would otherwise spend the whole 60-char budget on the prefix
	// and truncate away the part that says what the command actually does.
	detail = truncateRunes(detail, 60)
	if detail == "" {
		return "🔧 " + tc.Name
	}
	return "🔧 " + tc.Name + "(" + detail + ")"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// truncateRunes caps s at n RUNES, not bytes. The details rendered here routinely
// carry non-ASCII (a note title, a search query, an emoji in a filename), and
// slicing those by byte can cut a multi-byte rune in half and emit U+FFFD into
// the user's progress stream.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// shortenHostPaths rewrites absolute host paths in a progress detail into the
// vault-relative form the user actually recognises, so the live stream stops
// reading as a tour of the server's filesystem:
//
//	cd /home/user/.rookery/vaults/fd11c47e-…/notes  →  cd notes
//
// This operates on the whole detail string by substring replacement rather than
// on path-shaped arguments only, because the worst offender is a bash command
// line: the path is embedded mid-string, often more than once, and never arrives
// as a standalone argument we could clean structurally.
//
// The vault root is replaced before the home directory: a vault normally lives
// under the workspace home, so matching home first would leave a half-rewritten
// `~/vaults/<uuid>/notes` that still exposes the workspace UUID.
//
// Only these two known roots are rewritten. Absolute paths that are genuinely
// meaningful to the user — `/usr/bin/python3`, a skill's tool path — are left
// exactly as they are; there is deliberately no general "strip leading slash"
// step, which would corrupt them.
func shortenHostPaths(s, vaultRoot, homeDir string) string {
	// Vault root first, and with the separator included, so "<root>/notes"
	// becomes "notes" rather than a leading-slash "/notes" that would read as an
	// absolute path it no longer is.
	if root := cleanRoot(vaultRoot); root != "" {
		s = strings.ReplaceAll(s, root+string(filepath.Separator), "")
		// A bare reference to the root itself is the vault's "here".
		s = replaceWholePath(s, root, ".")
	}
	if root := cleanRoot(homeDir); root != "" {
		s = strings.ReplaceAll(s, root+string(filepath.Separator), "~"+string(filepath.Separator))
		s = replaceWholePath(s, root, "~")
	}
	return s
}

// cleanRoot normalises a root for prefix matching, rejecting the values that
// would match far too much. filepath.Clean("") returns ".", which as a substring
// would hit every relative path in the string, and "/" would hit every absolute
// one — neither is a usable prefix.
func cleanRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	c := strings.TrimSuffix(filepath.Clean(root), string(filepath.Separator))
	if c == "" || c == "." || c == string(filepath.Separator) {
		return ""
	}
	return c
}

// replaceWholePath substitutes `root` only where it is not followed by a path
// character, so a root of "/data/vault" rewrites a bare "/data/vault" but leaves
// a sibling directory like "/data/vault-backup" alone. Occurrences followed by a
// separator are already handled by the caller's prefix replacement.
func replaceWholePath(s, root, repl string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, root)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		rest := s[i+len(root):]
		b.WriteString(s[:i])
		if rest == "" || rest[0] == ' ' || rest[0] == '"' || rest[0] == '\'' || rest[0] == ':' || rest[0] == '\n' {
			b.WriteString(repl)
		} else {
			b.WriteString(root) // part of a longer name — leave it intact
		}
		s = rest
	}
}
