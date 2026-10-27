package skilldesigner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vetFake writes a valid skill on the generation call and answers each later (vetting)
// call with the next entry of answers, recording how many vetting calls were made.
func vetFake(t *testing.T, answers ...string) (string, string) {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "vet-calls")
	quoted := make([]string, len(answers))
	for i, a := range answers {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	// The vetting call is recognised by its prompt (the generic backend passes it as -p),
	// not by whether SKILL.md exists: vetting runs in a different working directory.
	script := fmt.Sprintf(`import os, sys
if 'auditing a newly generated skill' not in ' '.join(sys.argv):
    with open('SKILL.md', 'w') as f:
        f.write("---\nname: my-skill\ndescription: Does a thing. Use when asked to do the thing.\n---\nBody.\n")
    print("[TEST_OUTPUT]ok[/TEST_OUTPUT]")
else:
    n = int(open(%[1]q).read()) if os.path.exists(%[1]q) else 0
    open(%[1]q, 'w').write(str(n + 1))
    answers = [%[2]s]
    print(answers[min(n, len(answers) - 1)])
`, counter, strings.Join(quoted, ", "))
	return script, counter
}

func vetCalls(t *testing.T, counter string) string {
	data, _ := os.ReadFile(counter)
	return string(data)
}

// The trading-consultant skill reached review with a 0-byte vetting report: the vetter
// returned no text, and "no verdict" counted as "not blocked". Vetting is the safety gate
// for skills that install code, so a review that did not happen must not pass as one that
// found nothing. One retry first — an empty reply is often a one-off.
func TestRunGeneration_EmptyVettingReportBlocksSave(t *testing.T) {
	script, counter := vetFake(t, "", "")
	flow, workspaceID := newGenSkillFlow(t, newFakeSkillCoder(t, script))
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "my-skill")

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	sess := flow.GetSession(workspaceID)
	if sess.State != StateDesigning {
		t.Fatalf("state = %v, want StateDesigning (not saved)", sess.State)
	}
	if !strings.Contains(msg, "security review did not complete") {
		t.Errorf("message must say the review did not complete, got %q", msg)
	}
	if got := vetCalls(t, counter); got != "2" {
		t.Errorf("vetting calls = %q, want 2 (one retry)", got)
	}
}

func TestRunGeneration_VettingRetryThatSucceedsReachesReview(t *testing.T) {
	script, counter := vetFake(t, "", "SKILL VETTING REPORT\nRisk level: 🟢 LOW\nVerdict: ✅ safe to save")
	flow, workspaceID := newGenSkillFlow(t, newFakeSkillCoder(t, script))
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "my-skill")

	if _, _, _, err := flow.runGeneration(context.Background(), workspaceID); err != nil {
		t.Fatal(err)
	}
	if s := flow.GetSession(workspaceID).State; s != StateVerifying {
		t.Fatalf("state = %v, want StateVerifying", s)
	}
	if got := vetCalls(t, counter); got != "2" {
		t.Errorf("vetting calls = %q, want 2", got)
	}
}

func TestVettingComplete(t *testing.T) {
	cases := map[string]bool{
		"":                   false,
		"Risk level: 🔴 HIGH": false,
		"⚠️ Security vetting could not run automatically. Review the skill manually before approving.": false,
		"Verdict: ✅ safe to save":         true,
		"Verdict: ❌ do not save":          true,
		"  verdict: ⚠️ save with caution": true,
	}
	for in, want := range cases {
		if got := vettingComplete(in); got != want {
			t.Errorf("vettingComplete(%q) = %v, want %v", in, got, want)
		}
	}
}
