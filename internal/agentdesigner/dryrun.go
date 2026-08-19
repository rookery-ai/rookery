package agentdesigner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/prompts"
)

// dryRunSendProhibition is appended to the RUNTIME prompt for a dry run, and it is the
// half of the safety story the build-phase marker cannot cover.
//
// buildphase.EnvVar gates connectors.Execute and mcp.Execute — nothing else. At build time
// the rest of the restraint is carried by the prompt: testingRulesBlock's "never send real
// outbound messages on the user's behalf", which reaches only the IMPLEMENTATION prompts.
// A dry run uses prompts.BuildCoderPrompt — the RUNTIME prompt — whose execution block says
// the opposite ("DO the task", "RUN that script"). So a TIER 2 agent holding an SMTP or bot
// token in a secret, with its own tools/send.py, would really send during a rehearsal of an
// agent the user has not yet approved. Connector- and MCP-mediated sends stay blocked by the
// marker; this closes the script/Bash path.
//
// It lives here rather than in internal/prompts because it is specific to this rehearsal,
// not to the runtime protocol every real run shares.
const dryRunSendProhibition = `

[DRY RUN — REHEARSAL ONLY]
You are being run once as a rehearsal so this agent's owner can see real output before they
approve it. Nothing you produce here is delivered to anyone.

Read, fetch, compute and inspect freely — that is the point of this run.

Do NOT send, post, publish, message, email, comment on, upload or otherwise transmit
anything to anyone or to any external service, and do NOT create, change or delete anything
there. If this agent's job ends in sending something, do all the work up to that point and
then describe exactly what it WOULD have sent, in your [CHAT] block, instead of sending it.`

// dryRunPrompt builds the prompt a dry run is given: the ordinary runtime prompt the agent
// will see on every real run, plus the send prohibition above.
//
// backendType is threaded in rather than left empty because coderCapabilitiesBlock falls
// through to the full-CLI branch when it is — telling an api-engine workspace it has direct
// shell access when it actually has function tools. That workspace is precisely the
// weak-model case this whole feature exists for: it answers with prose instead of running,
// and the caller then labels that prose as executed, reintroducing the bug the dry run was
// built to remove. runtimeContext is threaded for the same class of reason: an agent with no
// clock cannot behave correctly on a time-sensitive task.
func dryRunPrompt(agentMD, backendType, runtimeContext string, chatApps []prompts.ChatAppInfo) string {
	return prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD:        agentMD,
		BackendType:    backendType,
		RuntimeContext: runtimeContext,
		ChatApps:       chatApps,
	}) + dryRunSendProhibition
}

// dryRun executes a freshly built agent ONCE so the review step can show what it
// actually does, rather than what the model says it will do.
//
// Why this exists: decideBuildOutcome only has executed evidence when the build
// authored a script and the engine confirmed it ran. A TIER 1 agent has no script —
// the right tier for "call an API, compare, notify", and therefore the common case —
// so the review sample fell back to a preview of the model's own prose, presented as
// "here's what a test run produces". Nothing had run.
//
// Deliberately NOT agentrunner.Run: that requires a db.Agent row (a draft has none)
// and writes an agent_runs row, an inbox message and a vault reflection. A build must
// produce none of those. So this borrows only the runtime PROMPT and the output
// protocol, and throws the rest away.
//
// Best-effort by contract: ok=false on any failure, and the caller keeps the sample it
// already had. A dry run must never fail a build that is already on disk and has
// passed its guardrails.
//
// Cost is real: this is one extra agent run per create build, and a real one measured
// over 1.5M tokens. That is the price of the review step showing something true, and
// it is why the caller invokes this for CREATE builds only.
func (f *Flow) dryRun(ctx context.Context, workspaceID, workDir, agentMD, backendType string, chatApps []prompts.ChatAppInfo, notify func(string)) (string, bool) {
	if f.coderFor == nil {
		return "", false
	}
	coderSvc := f.coderFor(workspaceID)
	if coderSvc == nil {
		return "", false
	}

	// A rehearsal must not become the saved agent's memory. saveAndFinish promotes the
	// draft dir wholesale, writeAgentContent rewrites AGENT.md + tools/ but NOT state.md,
	// and cleanupTestArtifacts does not classify it as junk — so a state.md written here
	// would ship as the agent's state. A change-detection agent would then open its first
	// real run already believing it had seen everything, stay silent, and read as broken.
	// This undoes exactly what the rehearsal did: anything the BUILD wrote is left alone,
	// because that predates the dry run.
	defer restoreDryRunState(workDir)()

	// The build-phase marker is NOT optional here, and it is the single most important
	// line in this function. It is what makes connectors.Execute refuse mutating actions.
	// Without it a "dry run" would really post to the user's spreadsheet, really send the
	// email, really publish — a test run with live side effects. WithExtraEnv REPLACES
	// rather than merges, so the whole map is built once, exactly as the build call does.
	extraEnv := map[string]string{buildphase.EnvVar: buildphase.Generation}
	if f.secretsLoader != nil {
		if env, err := f.secretsLoader(ctx, workspaceID); err == nil {
			for k, v := range env {
				extraEnv[k] = v
			}
		}
	}

	run := coderSvc.
		WithDir(workDir).
		WithAllowedTools("Bash,WebFetch,Read,Write,Edit").
		WithExtraEnv(extraEnv)

	// Stream per-tool-call milestones the same way the build does. A dry run is a full
	// agent run and can take minutes; without this the user watches one static 🧪 line
	// and the build reads as frozen. No-op for the CLI engine, which never calls the sink.
	if notify != nil {
		run = run.WithProgress(notify)
	}

	// Without connectors the agent has no tools and the dry run proves nothing — the
	// whole point is to exercise the same surface a real run gets. The build-time guard
	// above is what keeps that safe.
	if bound := f.buildBoundConns(ctx, workspaceID); len(bound) > 0 {
		run = run.WithConnectors(f.connReg, f.connStore, bound)
	}
	if boundMCP := f.buildBoundMCP(ctx, workspaceID); len(boundMCP) > 0 {
		run = run.WithMCP(f.mcpCaller, boundMCP)
	}

	// dryRunPrompt deliberately passes NO VaultRoot, and that omission is the only thing
	// keeping a rehearsal out of the user's live knowledge base: BuildCoderPrompt gates its
	// <agent_workspace> block on that field, so without it the agent is never told where the
	// vault is or that it may write there. dryRunSendProhibition covers sending to external
	// services and says nothing about vault writes. Passing VaultRoot here to "make the
	// rehearsal match a real run" would silently convert rehearsals of an unapproved agent
	// into real notes and real memory edits — do not add it without building the containment
	// that would make it safe.
	res, err := run.Generate(ctx, workspaceID,
		dryRunPrompt(agentMD, backendType, f.loadRuntimeContext(workspaceID), chatApps))
	if err != nil || res == nil {
		// One line, and a CLASS rather than the error text: a provider error can echo back
		// the request that produced it, and that dataflow reaches the workspace's API key
		// (see buildErrClass). Without this a systematically failing dry run is invisible to
		// an operator — the review simply never shows real output and nothing says why. A
		// nil error with a nil result classes as "", which is itself the useful signal.
		slog.Warn("agentdesigner: dry run produced no sample",
			"workspace_id", workspaceID, "err_class", buildErrClass(err))
		return "", false
	}
	return dryRunOutput(res.Text)
}

// restoreDryRunState snapshots the agent's state.md and returns the function that puts it
// back — removing it when the rehearsal created it, rewriting it when the rehearsal changed
// one the BUILD had written, and re-creating it when the rehearsal deleted one the BUILD had
// written. Any other read error degrades to leaving the file alone: this is a tidy-up, and a
// dry run must never be able to fail a build.
func restoreDryRunState(workDir string) func() {
	path := filepath.Join(workDir, "state.md")
	before, err := os.ReadFile(path)
	existed := err == nil
	return func() {
		after, err := os.ReadFile(path)
		switch {
		case err != nil && !existed:
			return // nothing there before, nothing there now — nothing to undo
		case err != nil:
			// the rehearsal deleted a file the BUILD wrote — put it back
			_ = os.WriteFile(path, before, 0o640)
		case !existed:
			_ = os.Remove(path)
		case string(after) != string(before):
			_ = os.WriteFile(path, before, 0o640)
		}
	}
}

var dryRunStateRE = regexp.MustCompile(`(?s)\[STATE\].*?\[/STATE\]`)

// dryRunOutput turns a coder reply into the sample shown in the review, using the
// same output protocol a real run reads. Returns ok=false when there is nothing
// worth showing.
func dryRunOutput(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	// Chat wins over a later [SILENT], and the order of these two checks is the
	// whole reason why. agentrunner.parseCoderOutput treats [SILENT] as
	// suppressing the PROSE FALLBACK, not as cancelling real [CHAT] content —
	// so a run that reports news and then adds the marker still delivers the
	// news. Testing the marker first made the dry run disagree with the run it
	// is a rehearsal of: the review said "nothing to report" about the very
	// thing the agent had just found.
	silent := isDryRunSilent(s)
	s = dryRunStateRE.ReplaceAllString(s, "")

	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "", strings.HasPrefix(t, "[CALL:"), isSilentLine(t):
			continue
		case strings.HasPrefix(t, "[CHAT]"):
			t = strings.TrimSpace(strings.TrimPrefix(t, "[CHAT]"))
			if t == "" {
				continue
			}
		}
		out = append(out, t)
	}
	joined := strings.TrimSpace(strings.Join(out, "\n"))
	if joined != "" {
		return joined, true
	}
	if silent {
		// Nothing deliverable AND the marker: a silent run is a CORRECT outcome
		// for an agent built to stay quiet, and the review must say so — an
		// empty box reads as a broken build.
		return "(The agent ran and had nothing to report — it would stay silent.)", true
	}
	return "", false
}

// isDryRunSilent recognises the [SILENT] marker with the decoration models add.
func isDryRunSilent(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if isSilentLine(line) {
			return true
		}
	}
	return false
}

// isSilentLine reports whether one line is the [SILENT] marker, tolerating the
// decoration models wrap it in (**[SILENT]**, `[SILENT]`, a trailing full stop).
//
// Shared by isDryRunSilent and by dryRunOutput's extraction loop, which must
// DROP these lines: now that chat takes precedence over the marker, a lone
// [SILENT] would otherwise survive extraction as ordinary prose and be shown to
// the user as the agent's output — the literal marker text presented as a
// result.
func isSilentLine(line string) bool {
	t := strings.Trim(strings.TrimSpace(line), "*_`\"' \t")
	t = strings.TrimRight(t, ".!?,;:")
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "[silent]", "[/silent]", "silent":
		return true
	}
	return false
}
