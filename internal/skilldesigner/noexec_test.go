package skilldesigner

import (
	"context"
	"strings"
	"testing"
)

// A CLI coder's skill build gets research and file tools, no shell: running and
// installing during the build is what made skill builds take 14-20 minutes.
func TestSkillBuildGrantsNoShell(t *testing.T) {
	for _, tool := range strings.Split(skillBuildAllowedTools, ",") {
		if tool == "Bash" {
			t.Fatal("skill build grants Bash")
		}
	}
	for _, want := range []string{"Read", "Write", "Edit", "WebFetch", "WebSearch"} {
		if !strings.Contains(","+skillBuildAllowedTools+",", ","+want+",") {
			t.Errorf("skill build lacks %s", want)
		}
	}
}

// The step after the build only syntax-checks scripts (py_compile / bash -n); calling
// it "Testing scripts" told the owner a test was running when nothing was executed.
func TestRunGeneration_ProgressSaysSyntaxCheckNotTesting(t *testing.T) {
	script, _ := vetFake(t, "Verdict: ✅ safe to save")
	flow, workspaceID := newGenSkillFlow(t, newFakeSkillCoder(t, script))
	sess := newDesigningSession(workspaceID, "my-skill")
	var lines []string
	sess.progressFunc = func(m string) { lines = append(lines, m) }
	flow.sessions[workspaceID] = sess

	if _, _, _, err := flow.runGeneration(context.Background(), workspaceID); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(lines, "\n")
	if strings.Contains(all, "Testing scripts") {
		t.Errorf("progress still says scripts are tested:\n%s", all)
	}
	if !strings.Contains(all, "Checking script syntax") {
		t.Errorf("progress does not name the syntax check:\n%s", all)
	}
}

// The review used to say "Here's the generated skill and how it tested". Nothing is
// run now, so it must say so — and say where the testing happens instead.
func TestRunGeneration_ReviewSaysTheSkillWasNotRun(t *testing.T) {
	script, _ := vetFake(t, "SKILL VETTING REPORT\nRisk level: 🟢 LOW\nVerdict: ✅ safe to save")
	flow, workspaceID := newGenSkillFlow(t, newFakeSkillCoder(t, script))
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "my-skill")

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "how it tested") {
		t.Errorf("review still claims the skill was tested: %q", msg)
	}
	if !strings.Contains(msg, "tested when an agent that uses it is built") {
		t.Errorf("review does not say where testing happens: %q", msg)
	}
}
