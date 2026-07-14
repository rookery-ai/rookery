package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/buildphase"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/llm"
	"github.com/ilijad1/simple-agents/internal/prompts"
)

// maxAPITurns bounds the tool-calling loop so a misbehaving model can't loop
// forever requesting tool executions. Surfaced as ErrMaxTurns (a normal run error,
// not a usage limit). Agent runs and one-off chat use this.
const maxAPITurns = 25

// maxBuildAPITurns is the (larger) bound during an agent BUILD. A build's budget is
// shared between the actual work, up to maxVerifyNudges finish-verification nudges, and
// the grace turn — 25 was routinely insufficient for a multi-action agent (work
// ~11 + drift + up to 5 verify nudges), so a weak model exhausted the budget before
// finishing. Builds get more headroom; runs/chat keep the tighter bound.
const maxBuildAPITurns = 40

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
	// Clear any spill files left by a previous run of this agent so .sa_out can't grow
	// unbounded across runs (the live agent dir, unlike a build dir, is never cleaned up).
	tools.clearSpillDir()
	var offeredTools []llm.Tool
	if !c.noTools {
		offeredTools = tools.tools()
	}
	// During a build, raise the completion-token cap so a large single write_file isn't
	// truncated (H2). tools.verifyBuild is set only when SA_BUILD_PHASE=generation.
	maxTokens := 0
	if tools.verifyBuild {
		maxTokens = buildMaxTokens
	}
	req := llm.Request{
		Model:     c.api.model,
		System:    prompt,
		Messages:  []llm.Message{{Role: "user", Content: prompts.APIEngineKickoffMessage}},
		Tools:     offeredTools,
		MaxTokens: maxTokens,
	}
	return c.runToolLoop(ctx, prov, tools, req, start)
}

// runToolLoop drives the Complete → execute host tools → feed results back →
// Complete loop shared by runAPI (single user kickoff) and chatToolsAPI (threaded
// chat history). It terminates when the model emits a final answer with no tool
// calls, when the turn budget is exhausted (ErrMaxTurns), or when the context
// deadline passes. A model that rejects the tools field degrades to a single
// no-tool reasoning turn rather than failing the call.
func (c *Coder) runToolLoop(ctx context.Context, prov llm.Provider, tools *hostToolSet, req llm.Request, start time.Time) (*Result, error) {
	var total llm.Usage
	toolsDisabled := false
	// A build gets more headroom (work + verify nudges + grace); runs/chat keep the
	// tighter bound.
	turnBudget := maxAPITurns
	if tools.verifyBuild {
		turnBudget = maxBuildAPITurns
	}
	for turn := 0; turn < turnBudget; turn++ {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("coder timed out after %s", c.timeout)
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
					c.progress("🔁 verifying the agent's script actually works…")
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
			return res, nil
		}

		// Append the assistant turn (text + tool calls), then execute each tool
		// call against the vault and append the results.
		req.Messages = append(req.Messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		for _, tc := range resp.ToolCalls {
			if c.progress != nil {
				c.progress(toolMilestone(tc))
			}
			result := tools.executeOrNudge(ctx, tc)
			req.Messages = append(req.Messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    result,
			})
		}
	}
	res, err := c.graceTurnOnBudgetExhausted(ctx, prov, req, total, start, tools.verifyBuild)
	if res != nil {
		// A build whose script the engine already ran must surface that ground truth even
		// when it exhausted its turn budget — the review path uses it as the sample.
		res.ScriptVerified = tools.scriptVerified()
		res.ScriptOutput = tools.verifiedOutput()
		res.ScriptRan = tools.authoredScriptRan()
		res.UsedConnectionIDs = tools.usedConnectionIDs()
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

// graceTurnOnBudgetExhausted gives the model exactly one more turn to wrap up gracefully
// instead of failing opaquely when runToolLoop exhausts maxAPITurns. It strips Tools from
// the request (req.Tools = nil forces a text-only reply — the model literally cannot
// request another tool call) and appends a wrap-up nudge, then makes one final Complete
// call. For a BUILD (isBuild) it uses graceTurnBudgetNudge (the [BLOCKED] convention parsed
// by agentdesigner.parseBlockedOutput); for a run/chat it uses graceTurnWrapUpNudge (plain
// language, no [BLOCKED] marker) so the build-only convention never leaks into a chat/run
// reply. Either way a turn-budget exhaustion degrades to a useful message instead of the
// opaque bare ErrMaxTurns a caller previously had no way to present usefully. Falls
// back to bare ErrMaxTurns only if this grace call itself fails or returns no text — it
// must not be able to loop further itself (exactly one extra Complete call, ever).
func (c *Coder) graceTurnOnBudgetExhausted(ctx context.Context, prov llm.Provider, req llm.Request, total llm.Usage, start time.Time, isBuild bool) (*Result, error) {
	req.Tools = nil
	nudge := graceTurnWrapUpNudge
	if isBuild {
		nudge = graceTurnBudgetNudge
	}
	req.Messages = append(req.Messages, llm.Message{Role: "user", Content: nudge})
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		return nil, ErrMaxTurns
	}
	total = addUsage(total, resp.Usage)
	text := strings.TrimSpace(resp.Content)
	if text == "" {
		return nil, ErrMaxTurns
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
		// SA_BUILD_PHASE=generation). A real run must never block on this — an agent that
		// legitimately has nothing to report must be free to finish silently.
		verifyBuild: c.extraEnv[buildphase.EnvVar] == buildphase.Generation,

		connReg:    c.connReg,
		connStore:  c.connStore,
		boundConns: c.boundConns,

		usedConnIDs: map[string]bool{},
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
// file tools, the query for search_files/web_search, the pattern for glob, or the
// url for web_fetch — falling back to a trimmed raw-arg blob when none apply.
func toolMilestone(tc llm.ToolCall) string {
	var args struct {
		Path    string `json:"path"`
		Query   string `json:"query"`
		Pattern string `json:"pattern"`
		URL     string `json:"url"`
	}
	_ = json.Unmarshal(tc.Args, &args)
	detail := args.Path
	if detail == "" {
		detail = args.Query
	}
	if detail == "" {
		detail = args.Pattern
	}
	if detail == "" {
		detail = args.URL
	}
	if detail == "" {
		detail = strings.TrimSpace(string(tc.Args))
	}
	if len(detail) > 60 {
		detail = detail[:60] + "…"
	}
	if detail == "" {
		return "🔧 " + tc.Name
	}
	return "🔧 " + tc.Name + "(" + detail + ")"
}
