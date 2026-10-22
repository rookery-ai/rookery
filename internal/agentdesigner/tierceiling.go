package agentdesigner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rookery-ai/rookery/internal/prompts"
)

// apiTierCeilingViolation reports the helper-code files a build authored that the
// workspace's coder is not allowed to author. On the tool-calling (API engine) backend
// agents are built at TIER 1 only — no files under tools/ — because the small models this
// backend usually runs could not write a helper script and prove it works: builds either
// shipped a script that never returned real data, or spent the whole turn budget trying.
// TIER 2/3 are reserved for CLI coders, which have their own shell and test loop.
//
// The prompt states the same ceiling (prompts.toolCallingTierCeiling); this is the half
// that binds, since a prompt steers and a weak model is exactly the one that ignores it.
//
// On an EDIT, `existing` is the live agent's tools — usually written by a CLI coder — and
// they are allowed through untouched: that is the "build on a CLI coder, run and tweak on
// a cheap API coder" workflow, and refusing those files would make every such agent
// uneditable. Only files the build ADDED or CHANGED count. A deleted file is not reported;
// dropping code is a move toward TIER 1, not away from it.
//
// Returns nil on any other backend, so CLI coders are byte-identical to before.
func apiTierCeilingViolation(backendType string, existing, built map[string]string) []string {
	if backendType != prompts.BackendToolCalling {
		return nil
	}
	var out []string
	for rel, code := range built {
		if prev, ok := existing[rel]; ok && prev == code {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// tierCeilingMessage is what the user reads when a build on the API engine needed helper
// code. It names the way through — build on a CLI coder, then switch back — because the
// agent itself is fine to RUN on the API engine; only building it is out of reach.
func tierCeilingMessage(isEdit bool) string {
	what := "This agent needs helper code (a script) to do its job"
	if isEdit {
		what = "This change needs the agent's helper code (its scripts) rewritten"
	}
	return "⚠️ " + what + ", and your workspace's coder — a direct model API — builds " +
		"agents without code only, because these models can't reliably write and test " +
		"scripts. Nothing was saved.\n\n" +
		"To build it: switch **Settings → Coder** to a CLI coder (for example Claude Code), " +
		"type **approve**, and once it's saved switch back — the agent will keep running on " +
		"your API coder. Or say **keep going** and I'll try again without any code."
}

// tierCeilingFailNote steers the retry. It is recorded with forceTier1, so the next
// attempt's prompt also carries the binding zero-code override.
func tierCeilingFailNote(isEdit bool) string {
	if isEdit {
		return "the edit added or changed files under tools/, which this coder may not do. " +
			"Next attempt: leave the existing tools/ files exactly as they are and make the " +
			"change in AGENT.md; if it cannot be done without changing the code, say so plainly."
	}
	return "the build wrote helper code under tools/, which this coder may not do (TIER 1 " +
		"only). Next attempt: create no files under tools/ — do every step with the direct " +
		"tools and reasoning; if a step truly needs code, say so plainly."
}

// removeAuthoredTools deletes the files apiTierCeilingViolation reported from a CREATE
// build's draft dir. They will never ship, and leaving them would make the retry fail the
// same check even when the model obeys the ceiling but forgets to clean up. Never used on
// an edit: there the working copy is rebuilt from the live agent on every attempt.
func removeAuthoredTools(workDir string, rels []string) {
	toolsDir := filepath.Join(workDir, "tools")
	for _, rel := range rels {
		p := filepath.Join(toolsDir, filepath.FromSlash(rel))
		// rel comes from ReadToolsTree, which walks toolsDir, so it cannot escape it;
		// the check is belt and braces before a delete.
		if r, err := filepath.Rel(toolsDir, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			continue
		}
		_ = os.Remove(p)
	}
}
