package skilldesigner

import (
	"context"
	"strings"
	"testing"
)

// TestRunGeneration_ReferenceDocProseNotBlocked is Finding 1's RED test at the
// generation-time call site (internal/skilldesigner/flow.go, the guardrail
// loop right after ReadSkillTree): a build whose references/guide.md contains
// destructive-command prose ("drop table") must reach StateVerifying, not be
// held back with a "failed an internal safety check" soft-fail. Before the
// fix, the loop ran every ReadSkillTree entry through
// agentdesigner.RunToolGuardrails regardless of extension, which applies the
// full code-context ethics keyword set (destructive commands included) even to
// a markdown reference doc.
func TestRunGeneration_ReferenceDocProseNotBlocked(t *testing.T) {
	fake := newFakeSkillCoder(t, `import os
if not os.path.exists('SKILL.md'):
    with open('SKILL.md', 'w') as f:
        f.write("---\nname: db-maintenance\ndescription: Helps run routine DB maintenance.\n---\n# DB Maintenance\nBody.\n")
    os.makedirs('references', exist_ok=True)
    with open('references/guide.md', 'w') as f:
        f.write("# Maintenance Guide\n\nAt the end of each run, drop table staging_tmp to free space.\n")
    print("[TEST_OUTPUT]Frontmatter parses cleanly.[/TEST_OUTPUT]")
else:
    # Second invocation is vetSkill's vetting call — report the skill as safe.
    print("Verdict: safe to save")
`)
	flow, workspaceID := newGenSkillFlow(t, fake)
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "db-maintenance")

	_, done, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if done {
		t.Fatal("verifying is not done yet")
	}

	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateVerifying {
		t.Fatalf("state = %+v, want StateVerifying (a references/*.md describing a destructive DB op in prose must not block the build)", sess)
	}
	if sess.GenerationFailed {
		t.Errorf("GenerationFailed should be false; a doc describing behaviour in prose is not executable code")
	}
	if strings.Contains(sess.PendingSkillMD, "failed an internal safety check") {
		t.Errorf("unexpected safety-check failure trace in pending skill: %q", sess.PendingSkillMD)
	}
}
