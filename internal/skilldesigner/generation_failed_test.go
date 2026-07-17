package skilldesigner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
)

// newFakeSkillCoder builds a Coder that shells out to a tiny python "binary"
// running the given script, mirroring agentdesigner's newFakeCoder helper
// (internal/agentdesigner/generation_keepfiles_test.go) — the generic backend
// passes the prompt via -p and reads plain stdout, so the script controls both
// what lands on disk and what Generate()/Chat() return. Sandbox off (unit
// test, no isolation needed).
func newFakeSkillCoder(t *testing.T, script string) *coder.Coder {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	binPath := filepath.Join(dir, "fakecoder")
	if err := os.WriteFile(binPath, []byte("#!/usr/bin/env python3\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatal(err)
	}
	homes := filepath.Join(dir, "homes")
	if err := os.MkdirAll(homes, 0o755); err != nil {
		t.Fatal(err)
	}
	return coder.New(binPath, time.Minute, homes, dir).WithSandbox(false).WithBackendType("generic")
}

// newGenSkillFlow builds a minimal Flow with an in-memory session map, no DB
// (saveDraft/vetSkill's slog on failure both tolerate f.db == nil / a coder
// error), and a SkillSaver pointed at a scratch vault dir (only SkillsDir() is
// exercised by runGeneration itself — finalizeSkill/SaveSkill are not reached
// by these tests).
func newGenSkillFlow(t *testing.T, fake *coder.Coder) (*Flow, string) {
	t.Helper()
	skillsBase := t.TempDir()
	workspaceID := "ws-" + t.Name()
	flow := &Flow{
		sessions: make(map[string]*DesignSession),
		saver:    NewSaver(nil, skillsBase),
		coderFor: func(string) *coder.Coder { return fake },
	}
	return flow, workspaceID
}

func newDesigningSession(workspaceID, skillName string) *DesignSession {
	return &DesignSession{
		WorkspaceID: workspaceID,
		SkillName:   skillName,
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "build me a skill"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}
}

// TestRunGeneration_BlockedMarkerSetsGenerationFailed is the regression guard
// for the SP4 final review fix: web/handlers_skill_design.go now surfaces
// DesignSession.GenerationFailed to the client so the new skill creator's
// soft-fail banner isn't permanently dead. A coder that emits [BLOCKED] (and
// never writes SKILL.md) must leave the session in StateDesigning with
// GenerationFailed set.
func TestRunGeneration_BlockedMarkerSetsGenerationFailed(t *testing.T) {
	fake := newFakeSkillCoder(t, `print("[BLOCKED]I couldn't figure out how to build this.[/BLOCKED]")
`)
	flow, workspaceID := newGenSkillFlow(t, fake)
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "my-skill")

	_, done, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if done {
		t.Fatal("a blocked build must not finish the flow")
	}

	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateDesigning {
		t.Fatalf("state after block = %+v, want StateDesigning", sess)
	}
	if !sess.GenerationFailed {
		t.Error("GenerationFailed should be set so the web handler reports it and the UI can show the soft-fail banner")
	}
}

// TestRunGeneration_EthicsFailureSetsGenerationFailed covers the other soft-fail
// branch inside runGeneration (a SKILL.md that trips CheckEthics) reaching the
// same GenerationFailed=true outcome.
func TestRunGeneration_EthicsFailureSetsGenerationFailed(t *testing.T) {
	fake := newFakeSkillCoder(t, `with open('SKILL.md', 'w') as f:
    f.write("---\nname: bad-skill\ndescription: will steal credit card numbers.\n---\nBody.\n")
`)
	flow, workspaceID := newGenSkillFlow(t, fake)
	flow.sessions[workspaceID] = newDesigningSession(workspaceID, "bad-skill")

	_, done, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if done {
		t.Fatal("an ethics-blocked build must not finish the flow")
	}

	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateDesigning {
		t.Fatalf("state after ethics failure = %+v, want StateDesigning", sess)
	}
	if !sess.GenerationFailed {
		t.Error("GenerationFailed should be set after a failed safety check")
	}
}

// TestRunGeneration_SuccessLeavesGenerationFailedFalse is the positive-path
// counterpart: a clean build must reach StateVerifying with GenerationFailed
// false (reset at the top of every runGeneration attempt), so the UI banner
// does not linger after a successful retry.
func TestRunGeneration_SuccessLeavesGenerationFailedFalse(t *testing.T) {
	fake := newFakeSkillCoder(t, `with open('SKILL.md', 'w') as f:
    f.write("---\nname: greeting\ndescription: Sends a daily greeting.\n---\n# Greeting\nBody.\n")
print("[TEST_OUTPUT]Frontmatter parses cleanly.[/TEST_OUTPUT]")
`)
	flow, workspaceID := newGenSkillFlow(t, fake)
	sess := newDesigningSession(workspaceID, "greeting")
	sess.GenerationFailed = true // a prior attempt failed — must be cleared by this success.
	flow.sessions[workspaceID] = sess

	_, done, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if done {
		t.Fatal("verifying is not done yet")
	}

	got := flow.GetSession(workspaceID)
	if got == nil || got.State != StateVerifying {
		t.Fatalf("state after success = %+v, want StateVerifying", got)
	}
	if got.GenerationFailed {
		t.Error("GenerationFailed must be reset to false on a successful build")
	}
}
