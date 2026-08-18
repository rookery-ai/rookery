package agentdesigner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
)

// newFakeCoder builds a Coder that shells out to a tiny python "binary" running
// the given script with CWD = the agent workDir. The generic backend passes the
// prompt via -p and reads plain stdout, so the script controls both what lands on
// disk and what Generate() returns. Sandbox off (unit test, no isolation needed).
func newFakeCoder(t *testing.T, script string) *coder.Coder {
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
	// Generic backend auto-mapping ("generic") → BackendFullCoder, so the
	// weak-backend verification gate never fires for these fakes.
	return coder.New(binPath, time.Minute, homes, dir).WithSandbox(false).WithBackendType("generic")
}

func newGenFlow(t *testing.T, fake *coder.Coder) (*Flow, string, string) {
	t.Helper()
	database, workspaceID := testDB(t)
	agentsDir := t.TempDir()
	flow := &Flow{
		sessions: make(map[string]*DesignSession),
		designer: NewDesigner(database, agentsDir),
		db:       database,
		coderFor: func(string) *coder.Coder { return fake },
	}
	return flow, workspaceID, agentsDir
}

// TestRunGeneration_SoftFailKeepsCreateDir is the regression guard for the
// "keep the files even when blocked" fix: a build that isn't presentable (here,
// the coder wrote no AGENT.md) must LEAVE the create-mode agent directory on disk
// so the user can request a change and finish it — the old code RemoveAll'd it.
func TestRunGeneration_SoftFailKeepsCreateDir(t *testing.T) {
	// Fake coder prints prose but never writes AGENT.md → not presentable.
	fake := newFakeCoder(t, "print('I could not figure out how to build this.')\n")
	flow, workspaceID, agentsDir := newGenFlow(t, fake)

	agentID := uuid.New().String()
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		AgentName:   "greeter",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "send a daily greeting"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}

	_, done, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if done {
		t.Fatal("soft-fail must not finish the flow")
	}

	// The readable draft_<name> WIP dir (created before the coder ran) must survive
	// the block so the user can iterate and finish it.
	draftDir := DraftAgentDir(agentsDir, workspaceID, "greeter")
	if _, err := os.Stat(draftDir); err != nil {
		t.Fatalf("draft dir must be KEPT after a blocked build; stat err = %v", err)
	}

	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateDesigning {
		t.Fatalf("state after block = %+v, want StateDesigning", sess)
	}
	if !sess.GenerationFailed {
		t.Error("GenerationFailed should be set so a forgiving retry re-runs generation")
	}
}

// successFakeScript writes a valid AGENT.md into the coder's CWD (the agent
// workDir) and emits a [TEST_OUTPUT] sample → decideBuildOutcome deems it
// presentable → runGeneration advances to StateVerifying.
const successFakeScript = `import os
with open(os.path.join(os.getcwd(), 'AGENT.md'), 'w') as f:
    f.write("# Suggested schedule: none\n# Skills: none\nSends a daily greeting.\n")
print("[TEST_OUTPUT]Greeting sent successfully.[/TEST_OUTPUT]")
`

// TestRunGeneration_CoderErrorKeepsDraftDir is the regression guard for the most
// likely branch behind the reported "navigated away and it left me at the design
// phase" symptom: the coder wrote a COMPLETE, guardrail-passing build to disk and
// then FAILED on a later call (non-zero exit / transient provider error), so
// Generate returned an error. The old code treated every coder error as a hard
// failure: cleanupOnFail RemoveAll'd the (complete) on-disk build and returned a
// 500, stranding the user at the design phase despite a finished build.
//
// The fix SALVAGES a saveable on-disk build: the disk is ground truth, not the
// exit code, so a complete build that hit a transient late-call error advances
// to StateVerifying (no Go error, no wiped dir) — the user reviews the agent they
// actually got, instead of being bounced back to describing.
func TestRunGeneration_CoderErrorKeepsDraftDir(t *testing.T) {
	fake := newFakeCoder(t, `import os, sys
os.makedirs('tools', exist_ok=True)
with open('AGENT.md','w') as f: f.write("# Suggested schedule: none\nDoes a thing.\n")
with open('tools/x.py','w') as f: f.write("print('hi')\n")
sys.stderr.write("boom\n")
sys.exit(1)
`)
	flow, workspaceID, agentsDir := newGenFlow(t, fake)

	agentID := uuid.New().String()
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		AgentName:   "greeter",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "do a thing"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}

	// The complete on-disk build is SALVAGED: no Go error is returned (the transient
	// late-call failure is logged, not surfaced as a 500), and the session advances.
	if _, _, _, err := flow.runGeneration(context.Background(), workspaceID); err != nil {
		t.Fatalf("a saveable build must be salvaged, not surfaced as an error; got %v", err)
	}
	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateVerifying {
		t.Fatalf("state after salvaged build = %+v, want StateVerifying (disk is ground truth)", sess)
	}
	if _, err := os.Stat(DraftAgentDir(agentsDir, workspaceID, "greeter")); err != nil {
		t.Errorf("draft dir must be KEPT after a salvaged build; stat err = %v", err)
	}
}

// TestRunGeneration_CoderErrorNoBuildReturnsError locks in the other side of the
// salvage gate: when the coder errors AND there is nothing saveable on disk (no
// AGENT.md), the failure is NOT swallowed and does NOT advance the session — but
// it is now a SOFT failure (nil error, a user-facing message), not a raw Go
// error. A raw error here used to reach the user as nothing at all: this exact
// branch called neither recordGenerationFailure nor saveDraft, so an eight-minute
// provider drop left the draft untouched and the user with no explanation. See
// hardFailureMessage / TestHardFailureMessageIsActionableAndLeaksNothing.
func TestRunGeneration_CoderErrorNoBuildReturnsError(t *testing.T) {
	// Errors immediately, writes nothing.
	fake := newFakeCoder(t, `import sys
sys.stderr.write("boom\n")
sys.exit(1)
`)
	flow, workspaceID, agentsDir := newGenFlow(t, fake)

	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "greeter",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "do a thing"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("a coder error with no build on disk must be a soft failure, not a Go error; got %v", err)
	}
	if msg == "" {
		t.Fatal("an empty failed build must still produce a user-facing message — silence is the bug")
	}
	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateDesigning {
		t.Fatalf("state = %+v, want StateDesigning (an empty failed build must not advance)", sess)
	}
	if !sess.GenerationFailed {
		t.Error("GenerationFailed should be set so a forgiving retry re-runs generation")
	}
	// Create mode never wipes the draft dir (keep-files policy: the user finishes it
	// later, the nightly GC sweeps expired ones) — so it survives even a hard error.
	if _, err := os.Stat(DraftAgentDir(agentsDir, workspaceID, "greeter")); err != nil {
		t.Errorf("draft dir is kept by the keep-files policy even on a hard error; stat err = %v", err)
	}
}

// TestRunGeneration_SuccessAppendsReviewToHistory is the regression guard for the
// replayable-result fix: on a presentable build the review message must be
// appended to History (so a page reload / draft resume replays it) and the
// session must reach StateVerifying with the pending AGENT.md captured.
func TestRunGeneration_SuccessAppendsReviewToHistory(t *testing.T) {
	fake := newFakeCoder(t, successFakeScript)
	flow, workspaceID, _ := newGenFlow(t, fake)

	agentID := uuid.New().String()
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		AgentName:   "greeter",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "send a daily greeting"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}
	histBefore := len(flow.sessions[workspaceID].History)

	resp, done, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if done {
		t.Fatal("verifying is not done yet")
	}

	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateVerifying {
		t.Fatalf("state after success = %+v, want StateVerifying", sess)
	}
	if sess.PendingAgentMD == "" {
		t.Error("PendingAgentMD should be captured on a presentable build")
	}
	if len(sess.History) != histBefore+1 {
		t.Fatalf("History grew by %d, want 1 (the review message)", len(sess.History)-histBefore)
	}
	last := sess.History[len(sess.History)-1]
	if last.Role != "assistant" || last.Content != resp {
		t.Errorf("last History entry = %+v, want assistant turn equal to the returned review message %q", last, resp)
	}
}

// TestSnapshot_ExposesLastProgress verifies the reconnect path can read the most
// recent build milestone, so a page returning mid-build shows the current action
// immediately instead of the generic placeholder.
func TestSnapshot_ExposesLastProgress(t *testing.T) {
	database, workspaceID := testDB(t)
	flow := &Flow{sessions: make(map[string]*DesignSession), db: database}

	if snap := flow.Snapshot(workspaceID); snap.Active {
		t.Fatal("no session yet, Snapshot should be inactive")
	}

	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID:  workspaceID,
		State:        StateDesigning,
		progressCh:   make(chan string, 8),
		lastProgress: "🔧 run_script(fetch_price.py)",
	}
	snap := flow.Snapshot(workspaceID)
	if !snap.Active || !snap.Generating {
		t.Fatalf("snapshot = %+v, want active+generating", snap)
	}
	if snap.LastProgress != "🔧 run_script(fetch_price.py)" {
		t.Errorf("LastProgress = %q, want the recorded milestone", snap.LastProgress)
	}
}

// TestSnapshotExposesPendingBuild verifies the reviewable-build artifacts
// (PendingAgentMD, PendingTools) set on a session after generation are exposed
// on the snapshot, so the design-state endpoint (and Task 2's review panel) can
// show the user what the coder actually produced before they approve it.
func TestSnapshotExposesPendingBuild(t *testing.T) {
	database, workspaceID := testDB(t)
	flow := &Flow{sessions: make(map[string]*DesignSession), db: database}

	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID:    workspaceID,
		State:          StateVerifying,
		PendingAgentMD: "# Test agent\n",
		PendingTools: map[string]string{
			"tools/main.py": "print('hi')\n",
		},
	}

	snap := flow.Snapshot(workspaceID)
	if snap.PendingAgentMD != "# Test agent\n" {
		t.Fatalf("PendingAgentMD not exposed: %q", snap.PendingAgentMD)
	}
	if snap.PendingTools["tools/main.py"] != "print('hi')\n" {
		t.Fatalf("PendingTools not exposed: %#v", snap.PendingTools)
	}

	// Mutating the session's own map after the snapshot was taken must not
	// affect the copy handed to the caller — Snapshot runs under the flow's
	// mutex but the returned map escapes it, so it must be a defensive copy,
	// the same way History already is.
	flow.sessions[workspaceID].PendingTools["tools/extra.py"] = "added later\n"
	if _, ok := snap.PendingTools["tools/extra.py"]; ok {
		t.Fatal("snapshot's PendingTools must be a copy, not the session's live map")
	}
}

// TestSnapshotPendingEmptyBeforeBuild verifies that a session which has not
// generated yet reports empty pending fields, and critically that PendingTools
// is a non-nil empty map — not nil — so it serialises to JSON `{}` rather than
// `null` (the frontend maps over it; a nil map would marshal to null and crash
// Task 2's panel).
func TestSnapshotPendingEmptyBeforeBuild(t *testing.T) {
	database, workspaceID := testDB(t)
	flow := &Flow{sessions: make(map[string]*DesignSession), db: database}

	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		State:       StateDesigning,
	}

	snap := flow.Snapshot(workspaceID)
	if snap.PendingAgentMD != "" || len(snap.PendingTools) != 0 {
		t.Fatal("pending fields must be empty before a build")
	}
	if snap.PendingTools == nil {
		t.Fatal("PendingTools must be a non-nil empty map, not nil (would marshal to JSON null)")
	}
}

// TestRunGeneration_DetachedFromCallerContext is the discriminating guard for the
// navigation-survival fix: generation must run to completion even when the CALLER's
// context is already cancelled (which is what a client disconnect does to the HTTP
// request context). Because runGeneration derives genCtx from context.Background()
// — not the passed ctx — a dead caller context no longer kills the build.
//
// Under the OLD code (genCtx := context.WithCancel(ctx)) the pre-cancelled ctx
// propagated into Generate(), the coder failed with context.Canceled, and the
// create dir was removed — this test would then see StateDesigning + an "cancelled"
// message. Under the fix it reaches StateVerifying.
func TestRunGeneration_DetachedFromCallerContext(t *testing.T) {
	fake := newFakeCoder(t, successFakeScript)
	flow, workspaceID, agentsDir := newGenFlow(t, fake)

	agentID := uuid.New().String()
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		AgentName:   "greeter",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "send a daily greeting"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}

	// Simulate the browser navigating away mid-build: the request context is dead
	// before generation even runs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, done, _, err := flow.runGeneration(ctx, workspaceID)
	if err != nil {
		t.Fatalf("runGeneration must ignore the dead caller ctx; got err = %v", err)
	}
	if done {
		t.Fatal("verifying is not done yet")
	}

	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateVerifying {
		t.Fatalf("state = %+v, want StateVerifying (build completed despite cancelled caller ctx)", sess)
	}
	if _, err := os.Stat(DraftAgentDir(agentsDir, workspaceID, "greeter")); err != nil {
		t.Errorf("draft dir should exist after a successful detached build; stat err = %v", err)
	}
}
