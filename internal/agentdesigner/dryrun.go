package agentdesigner

import (
	"context"
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/prompts"
)

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
func (f *Flow) dryRun(ctx context.Context, workspaceID, workDir, agentMD string) (string, bool) {
	if f.coderFor == nil {
		return "", false
	}
	coderSvc := f.coderFor(workspaceID)
	if coderSvc == nil {
		return "", false
	}

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

	// Without connectors the agent has no tools and the dry run proves nothing — the
	// whole point is to exercise the same surface a real run gets. The build-time guard
	// above is what keeps that safe.
	if bound := f.buildBoundConns(ctx, workspaceID); len(bound) > 0 {
		run = run.WithConnectors(f.connReg, f.connStore, bound)
	}
	if boundMCP := f.buildBoundMCP(ctx, workspaceID); len(boundMCP) > 0 {
		run = run.WithMCP(f.mcpCaller, boundMCP)
	}

	res, err := run.Generate(ctx, workspaceID, prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD: agentMD,
	}))
	if err != nil || res == nil {
		return "", false
	}
	return dryRunOutput(res.Text)
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
	if isDryRunSilent(s) {
		// A silent run is a CORRECT outcome for an agent built to stay quiet, and the
		// review must say so — an empty box reads as a broken build.
		return "(The agent ran and had nothing to report — it would stay silent.)", true
	}
	s = dryRunStateRE.ReplaceAllString(s, "")

	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "", strings.HasPrefix(t, "[CALL:"):
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
	if joined == "" {
		return "", false
	}
	return joined, true
}

// isDryRunSilent recognises the [SILENT] marker with the decoration models add.
func isDryRunSilent(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		t := strings.Trim(strings.TrimSpace(line), "*_`\"' \t")
		t = strings.TrimRight(t, ".!?,;:")
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "[silent]", "[/silent]", "silent":
			return true
		}
	}
	return false
}
