package agentdesigner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/prompts"
)

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
// vaultRoot IS passed, and that reverses a deliberate earlier decision. This
// function used to withhold it on the grounds that BuildCoderPrompt gates its
// <agent_workspace> block on the field, so an agent never told where the vault
// is would not write there. That reasoning was wrong: the host tools resolve any
// path under the vault root, their own descriptions advertise it ("relative to
// the vault root, or absolute within the vault"), and a script's Landlock grant
// covers the whole vault regardless. The omission never contained anything — all
// it did was make the rehearsal less like the run it exists to predict, which is
// the one property a rehearsal cannot afford to lose. Containment now comes from
// vault.WriteJournal, which undoes the writes instead of pretending to prevent
// them; see dryRun below.
func dryRunPrompt(agentMD, backendType, runtimeContext, vaultRoot, agentDir string, chatApps []prompts.ChatAppInfo, browserAvailable bool) string {
	return prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD:        agentMD,
		BackendType:    backendType,
		RuntimeContext: runtimeContext,
		VaultRoot:      vaultRoot,
		AgentDir:       agentDir,
		ChatApps:       chatApps,
		// Reading only. BrowserActing is deliberately left false: a rehearsal
		// told how to click would plan around a capability CheckAct refuses it,
		// and then report a plan it never actually carried out.
		BrowserAvailable: browserAvailable,
	}) + prompts.DryRunSendProhibition
}

// dryRunResult carries what a rehearsal learned. The sample is what the review step
// shows; the id slices are evidence for auto-bind.
//
// The ids matter because auto-bind exists to catch the case a weak model omits the
// `# Connections:` header, and it binds exactly what the build's tool calls invoked.
// The dry run is a REAL agent run against the same bound surface, so a connection it
// exercised is evidence of the same kind — and it was being thrown away, because the
// caller consumed only res.Text. An agent whose build happened not to touch a
// connection its first real run needs would ship unbound.
type dryRunResult struct {
	Sample            string
	UsedConnectionIDs []string
	UsedMCPServerIDs  []string
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
func (f *Flow) dryRun(ctx context.Context, workspaceID, workDir, agentMD, backendType string, chatApps []prompts.ChatAppInfo, notify func(string)) (dryRunResult, bool) {
	if f.coderFor == nil {
		return dryRunResult{}, false
	}
	coderSvc := f.coderFor(workspaceID)
	if coderSvc == nil {
		return dryRunResult{}, false
	}

	// A rehearsal must not become the saved agent's memory. saveAndFinish promotes the
	// draft dir wholesale, writeAgentContent rewrites AGENT.md + tools/ but NOT state.md,
	// and cleanupTestArtifacts does not classify it as junk — so a state.md written here
	// would ship as the agent's state. A change-detection agent would then open its first
	// real run already believing it had seen everything, stay silent, and read as broken.
	// This undoes exactly what the rehearsal did: anything the BUILD wrote is left alone,
	// because that predates the dry run.
	defer restoreDryRunState(workDir)()

	// state.md is restored above; the user's knowledge base is restored here.
	// Both are the same idea applied to the two places a rehearsal leaves marks,
	// and they are separate calls because they want opposite things: state.md
	// must go back to what the BUILD wrote, while the knowledge base must go back
	// to what the USER had. Its own journal, not the build's — the build's is
	// already reverted by the time this runs.
	dryRunJournal, revertDryRunWrites := f.beginKBRehearsal(workspaceID, "dry-run", "")
	defer revertDryRunWrites()

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
		WithKBJournal(dryRunJournal).
		WithAllowedTools("Bash,WebFetch,Read,Write,Edit").
		WithExtraEnv(extraEnv)

	// Stream per-tool-call milestones the same way the build does. A dry run is a full
	// agent run and can take minutes; without this the user watches one static 🧪 line
	// and the build reads as frozen. No-op for the CLI engine, which never calls the sink.
	if notify != nil {
		run = run.WithProgress(notify)
	}

	// The browser, read-only. A rehearsal of an agent written to read a
	// JavaScript-rendered page has to be able to open it, or the sample shown at
	// review describes nothing that happened. Acting stays refused by
	// browser.CheckAct — the build marker is set in extraEnv above, and
	// api_engine re-derives BuildPhase from it, so this cannot click "Pay"
	// during a rehearsal of an agent nobody has approved yet.
	if f.browser != nil && f.browser.Available().OK {
		run = run.WithBrowser(f.browser, browser.Policy{})
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

	// The rehearsal gets the SAME prompt a real run does, vault and all, and its
	// knowledge-base writes are undone afterwards by the journal above. The
	// previous arrangement withheld VaultRoot and claimed that as containment; it
	// never was one (see dryRunPrompt), and it cost the rehearsal the fidelity
	// that is its entire purpose.
	res, err := run.Generate(ctx, workspaceID,
		dryRunPrompt(agentMD, backendType, f.loadRuntimeContext(workspaceID),
			f.vaultRootFor(workspaceID), workDir, chatApps,
			f.browser != nil && f.browser.Available().OK))
	if err != nil || res == nil {
		// One line, and a CLASS rather than the error text: a provider error can echo back
		// the request that produced it, and that dataflow reaches the workspace's API key
		// (see buildErrClass). Without this a systematically failing dry run is invisible to
		// an operator — the review simply never shows real output and nothing says why. A
		// nil error with a nil result classes as "", which is itself the useful signal.
		slog.Warn("agentdesigner: dry run produced no sample",
			"workspace_id", workspaceID, "err_class", buildErrClass(err))
		return dryRunResult{}, false
	}
	sample, ok := dryRunOutput(res.Text)
	if !ok {
		return dryRunResult{}, false
	}
	return dryRunResult{
		Sample:            sample,
		UsedConnectionIDs: res.UsedConnectionIDs,
		UsedMCPServerIDs:  res.UsedMCPServerIDs,
	}, true
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
		// A stray [/CHAT] close tag is dropped for the same reason
		// agentrunner.parseCoderOutput drops it: weak models emit one with no
		// opener. Without this the rehearsal displayed the tag verbatim while
		// the real run it rehearses would have stripped it — the review sample
		// has to look like what the agent will actually deliver.
		case t == "", strings.HasPrefix(t, "[CALL:"), isSilentLine(t), t == "[/CHAT]":
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
