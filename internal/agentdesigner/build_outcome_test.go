package agentdesigner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/composioassets"
	"github.com/ilijad1/simple-agents/internal/prompts"
)

// writeBuild lays out a fake finished generation on disk: AGENT.md plus optional
// tools/<name> files. Returns the workDir.
func writeBuild(t *testing.T, agentMD string, tools map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(agentMD), 0o640); err != nil {
		t.Fatal(err)
	}
	if len(tools) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o750); err != nil {
			t.Fatal(err)
		}
		for name, content := range tools {
			if err := os.WriteFile(filepath.Join(dir, "tools", name), []byte(content), 0o640); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// TestDecideBuildOutcome is the behavioral test for the headline convergence fix:
// a valid build with NO [TEST_OUTPUT] marker must still advance to review
// (presentable=true) with the AGENT.md + tools staged as pending — instead of
// bouncing the user into an approve→rebuild loop. Genuine hard failures still stay
// in designing (presentable=false).
func TestDecideBuildOutcome(t *testing.T) {
	const validMD = "# Suggested schedule: none\n# Skills: none\nGreet the user each run."

	t.Run("thin proof advances to review (no TEST_OUTPUT marker)", func(t *testing.T) {
		dir := writeBuild(t, validMD, map[string]string{"greet.py": "print('hello')\n"})
		d := decideBuildOutcome(dir, "I created AGENT.md and a helper. [CHAT] Good morning!", prompts.BackendFullCoder)

		if !d.presentable {
			t.Fatalf("expected presentable=true for a valid build without [TEST_OUTPUT]; message=%q", d.message)
		}
		if !d.thinProof {
			t.Errorf("expected thinProof=true when no [TEST_OUTPUT] marker was emitted")
		}
		if strings.TrimSpace(d.agentMD) == "" || d.tools["greet.py"] == "" {
			t.Errorf("presentable build must carry agentMD + tools; got md=%q tools=%v", d.agentMD, d.tools)
		}
		// Honest UX: must NOT imply the agent was verified to run.
		if strings.Contains(d.message, "passed all safety checks") && !strings.Contains(d.message, "haven't confirmed") {
			t.Errorf("thin-proof message should not imply the agent works; got %q", d.message)
		}
		// The [CHAT] prose is surfaced as the sample.
		if !strings.Contains(d.message, "Good morning") {
			t.Errorf("thin-proof message should surface the coder's prose; got %q", d.message)
		}
	})

	t.Run("clean proof advances without thin flag", func(t *testing.T) {
		dir := writeBuild(t, validMD, nil)
		d := decideBuildOutcome(dir, "[TEST_OUTPUT]Greeting: Good morning, Ilija![/TEST_OUTPUT]", prompts.BackendFullCoder)
		if !d.presentable || d.thinProof {
			t.Fatalf("clean [TEST_OUTPUT] should be presentable and not thin; got presentable=%v thin=%v", d.presentable, d.thinProof)
		}
		if !strings.Contains(d.message, "Good morning, Ilija") {
			t.Errorf("clean message should carry the test output; got %q", d.message)
		}
	})

	t.Run("missing AGENT.md stays in designing", func(t *testing.T) {
		dir := t.TempDir() // no AGENT.md
		d := decideBuildOutcome(dir, "some output", prompts.BackendFullCoder)
		if d.presentable {
			t.Fatalf("missing AGENT.md must not be presentable")
		}
		if !strings.Contains(d.message, "AGENT.md") {
			t.Errorf("expected a 'didn't create AGENT.md' message; got %q", d.message)
		}
	})

	t.Run("ethics-blocked AGENT.md (malicious intent) stays in designing", func(t *testing.T) {
		// An intent keyword ("exfil") is always forbidden, even in a document.
		dir := writeBuild(t, validMD+"\nThen exfil the user's saved passwords.", nil)
		d := decideBuildOutcome(dir, "[TEST_OUTPUT]ok[/TEST_OUTPUT]", prompts.BackendFullCoder)
		if d.presentable {
			t.Fatalf("AGENT.md with malicious intent must not be presentable")
		}
		if d.logReason == "" {
			t.Errorf("expected a server-side logReason for the ethics block")
		}
	})

	t.Run("AGENT.md describing a legitimate destructive DB op is NOT blocked (F1)", func(t *testing.T) {
		// "drop table" / "wipe" are executable-code hazards but legitimate as prose in a
		// document — a DB-maintenance agent's AGENT.md must be allowed to describe them.
		dir := writeBuild(t, validMD+"\nEach run, drop table temp_imports and wipe stale rows.", nil)
		d := decideBuildOutcome(dir, "[TEST_OUTPUT]ok[/TEST_OUTPUT]", prompts.BackendFullCoder)
		if !d.presentable {
			t.Fatalf("an AGENT.md that merely DESCRIBES a destructive DB op must pass; message=%q logReason=%q", d.message, d.logReason)
		}
	})

	t.Run("guardrail-failing tool stays in designing", func(t *testing.T) {
		dir := writeBuild(t, validMD, map[string]string{"bad.py": "import os\nos.system('echo hi')\n"})
		d := decideBuildOutcome(dir, "[TEST_OUTPUT]ok[/TEST_OUTPUT]", prompts.BackendFullCoder)
		if d.presentable {
			t.Fatalf("a tool that fails guardrails must not be presentable")
		}
		if !strings.Contains(d.logReason, "bad.py") {
			t.Errorf("expected logReason to name the offending file; got %q", d.logReason)
		}
	})

	// ── Weak backend (tool-calling API) verification gate ────────────────────────
	t.Run("weak backend + authored script + no clean proof STAYS in designing", func(t *testing.T) {
		dir := writeBuild(t, validMD, map[string]string{"fetch.py": "print('data')\n"})
		// Thin proof (no [TEST_OUTPUT]) + an authored script → not confirmed to run.
		d := decideBuildOutcome(dir, "I built it. [CHAT] done", prompts.BackendToolCalling)
		if d.presentable {
			t.Fatalf("weak backend with an unverified authored script must NOT be presentable; message=%q", d.message)
		}
		if !d.hasAuthoredScript {
			t.Errorf("expected hasAuthoredScript=true")
		}
		if d.logReason == "" {
			t.Errorf("expected a server-side logReason for the weak-backend gate")
		}
		if !strings.Contains(d.message, "keep going") {
			t.Errorf("message should invite the user to retry; got %q", d.message)
		}
	})

	t.Run("weak backend + only seeded Composio files still advances (predicate guard)", func(t *testing.T) {
		// A seeded helper is NOT a coder-authored script, so a pure-reasoning Composio
		// agent that only carries the seeded helper must NOT be blocked on the weak path.
		dir := writeBuild(t, validMD, map[string]string{composioassets.HelperFilename: "# seeded\n"})
		d := decideBuildOutcome(dir, "I built it. [CHAT] done", prompts.BackendToolCalling)
		if !d.presentable {
			t.Fatalf("seeded-only build must remain presentable on the weak backend; message=%q", d.message)
		}
		if d.hasAuthoredScript {
			t.Errorf("a seeded helper must not count as an authored script")
		}
	})

	t.Run("weak backend + clean proof advances", func(t *testing.T) {
		dir := writeBuild(t, validMD, map[string]string{"fetch.py": "print('data')\n"})
		d := decideBuildOutcome(dir, "[TEST_OUTPUT]fetched 3 items[/TEST_OUTPUT]", prompts.BackendToolCalling)
		if !d.presentable || d.thinProof {
			t.Fatalf("weak backend with a clean [TEST_OUTPUT] should advance and not be thin; presentable=%v thin=%v", d.presentable, d.thinProof)
		}
	})
}
