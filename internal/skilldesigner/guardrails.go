package skilldesigner

import (
	"strings"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
)

// guardrailsForGeneratedFile decides which ethics check a single generated skill
// file goes through, so the generation-time loop (flow.go) and the save-time
// loop (designer.go, SaveSkill) can't drift apart on the decision.
//
// A markdown file (references/*.md, or any other .md the coder wrote outside
// SKILL.md — SKILL.md itself is checked separately, before this loop even
// starts) is prose, not executable code: it goes through
// agentdesigner.CheckEthics, the document profile that applies only the
// always-forbidden intent keywords ("steal", "exfil", …) and NOT the
// destructive-command keywords ("rm -rf", "drop table", …), which are
// legitimate as descriptive text in a document. Before this split,
// references/*.md traveled through ReadSkillTree for the first time (Task 5)
// and immediately hit RunToolGuardrails — the full code-context ethics check —
// reintroducing the exact false-positive failure mode
// agentdesigner/guardrails.go already fixed once for AGENT.md prose (see the
// comment on destructiveCodeKeywords there).
//
// Everything else (scripts/*.py, scripts/*.sh, and any other non-.md file) is
// executable or config content and goes through the full
// agentdesigner.RunToolGuardrails check (destructive-command keywords + Python
// AST for .py). A .sh helper that actually contains `rm -rf` must still be
// rejected — it is code, not prose.
//
// Do not confuse this with agentdesigner.RunToolGuardrails itself, which is
// deliberately NOT extension-gated on ethics (it applies the full keyword set
// to every file, pinned by agentdesigner/toolstree_test.go) — that contract is
// shared with agent-authored files and out of scope here. This helper only
// changes which of the two agentdesigner entry points a skill FILE is routed
// through, based on its own extension.
func guardrailsForGeneratedFile(filename, code string) error {
	if strings.HasSuffix(filename, ".md") {
		return agentdesigner.CheckEthics(code, "")
	}
	return agentdesigner.RunToolGuardrails(filename, code, agentdesigner.ProfileSkillScript)
}
