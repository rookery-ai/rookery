package agentdesigner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// newDraftFlow builds a Flow wired to a fresh migrated DB + temp agents dir.
func newDraftFlow(t *testing.T) (*Flow, *db.DB, string, string) {
	t.Helper()
	database, workspaceID := testDB(t)
	agentsDir := t.TempDir()
	flow := &Flow{
		sessions: make(map[string]*DesignSession),
		designer: NewDesigner(database, agentsDir),
		db:       database,
	}
	return flow, database, workspaceID, agentsDir
}

// TestDraftSaveAndResume_Designing verifies a saved designing-state draft restores
// the conversation history and reloads derived context on resume — without any
// coder subprocess involved.
func TestDraftSaveAndResume_Designing(t *testing.T) {
	flow, database, workspaceID, _ := newDraftFlow(t)

	sess := &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "price-tracker",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "track BTC price"},
			{Role: "assistant", Content: "Sure — how often?"},
			{Role: "user", Content: "every 10 minutes"},
			{Role: "assistant", Content: "Got it. Approve when ready."},
		},
	}
	flow.saveDraft(sess)

	if d := flow.HasDraft(workspaceID); d == nil || d.AgentName != "price-tracker" || d.State != "designing" {
		t.Fatalf("HasDraft = %+v, want a designing draft for price-tracker", d)
	}

	// Simulate session loss: no in-memory session exists.
	resp, err := flow.ResumeDraft(context.Background(), workspaceID, OriginWeb)
	if err != nil {
		t.Fatalf("ResumeDraft: %v", err)
	}
	if !strings.Contains(resp, "price-tracker") {
		t.Errorf("resume message = %q, want it to mention the agent name", resp)
	}

	got := flow.GetSession(workspaceID)
	if got == nil {
		t.Fatal("no session reconstructed after resume")
	}
	if got.State != StateDesigning {
		t.Errorf("state = %s, want designing", got.State)
	}
	if len(got.History) != 4 || got.History[3].Content != "Got it. Approve when ready." {
		t.Errorf("history not restored: %+v", got.History)
	}
	if got.AgentID != sess.AgentID {
		t.Errorf("AgentID = %q, want %q (must round-trip)", got.AgentID, sess.AgentID)
	}
	_ = database // keep ref; flow.db already wired
}

// TestDraftSaveAndResume_Verifying verifies a saved verifying-state draft restores
// the generated AGENT.md content and surfaces it in the resume message.
func TestDraftSaveAndResume_Verifying(t *testing.T) {
	flow, _, workspaceID, _ := newDraftFlow(t)

	pendingMD := "# Suggested schedule: */10 * * * *\nFetches BTC price and alerts."
	sess := &DesignSession{
		WorkspaceID:    workspaceID,
		AgentID:        uuid.New().String(),
		AgentName:      "price-tracker",
		State:          StateVerifying,
		PendingAgentMD: pendingMD,
		PendingTools:   map[string]string{"fetch_price.py": "print('ok')"},
	}
	flow.saveDraft(sess)

	resp, err := flow.ResumeDraft(context.Background(), workspaceID, OriginWeb)
	if err != nil {
		t.Fatalf("ResumeDraft: %v", err)
	}
	if !strings.Contains(resp, "approve") {
		t.Errorf("verifying resume message = %q, want an approve prompt", resp)
	}

	got := flow.GetSession(workspaceID)
	if got == nil || got.State != StateVerifying {
		t.Fatalf("state after resume = %+v, want verifying", got)
	}
	if got.PendingAgentMD != pendingMD {
		t.Errorf("PendingAgentMD = %q, want round-tripped content", got.PendingAgentMD)
	}
	if got.PendingTools["fetch_price.py"] != "print('ok')" {
		t.Errorf("PendingTools not restored: %+v", got.PendingTools)
	}
}

// TestDraftDismissedOnFinalize drives a verifying session through final approval
// (no coder — pending content is pre-set) and asserts the agent is persisted and
// the draft row is deleted so the resume prompt never reappears.
func TestDraftDismissedOnFinalize(t *testing.T) {
	flow, database, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	sess := &DesignSession{
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		AgentName:      "price-tracker",
		State:          StateVerifying,
		PendingAgentMD: "# Suggested schedule: none\nFetches BTC price and alerts.",
	}
	flow.sessions[workspaceID] = sess
	flow.saveDraft(sess)

	// Simulate the WIP draft_<name> dir left by generation — finalize must remove it
	// once the agent is reconstituted at its canonical AgentDir(<uuid>).
	draftDir := DraftAgentDir(agentsDir, workspaceID, "price-tracker")
	if err := os.MkdirAll(filepath.Join(draftDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}

	if flow.HasDraft(workspaceID) == nil {
		t.Fatal("draft should exist before finalize")
	}

	resp, done, gotID, err := flow.Step(context.Background(), workspaceID, "approve", OriginWeb)
	if err != nil {
		t.Fatalf("Step approve: %v", err)
	}
	if !done || gotID != agentID {
		t.Fatalf("Step = (%q, %v, %q, %v), want done with agentID %q", resp, done, gotID, err, agentID)
	}

	if flow.HasDraft(workspaceID) != nil {
		t.Error("draft should be deleted after a successful finalize")
	}
	if a, err := database.GetAgent(agentID); err != nil || a == nil {
		t.Errorf("agent not persisted after finalize: %v (err=%v)", a, err)
	}
	if _, err := os.Stat(draftDir); !os.IsNotExist(err) {
		t.Errorf("draft_<name> dir should be removed on finalize; stat err = %v", err)
	}
	// The finalized agent lives at its canonical UUID dir.
	if _, err := os.Stat(filepath.Join(AgentDir(agentsDir, workspaceID, agentID), "AGENT.md")); err != nil {
		t.Errorf("finalized agent AGENT.md should exist at AgentDir(<uuid>); stat err = %v", err)
	}
}

// TestDraftCleanupOrphanDir verifies DismissDraft removes the readable draft_<name>
// working directory left by runGeneration for a create-mode draft.
func TestDraftCleanupOrphanDir(t *testing.T) {
	flow, _, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	draftDir := DraftAgentDir(agentsDir, workspaceID, "price-tracker")
	if err := os.MkdirAll(filepath.Join(draftDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(draftDir, "AGENT.md"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	sess := &DesignSession{
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		AgentName:      "price-tracker",
		State:          StateVerifying,
		PendingAgentMD: "x",
	}
	flow.saveDraft(sess)

	if err := flow.DismissDraft(workspaceID); err != nil {
		t.Fatalf("DismissDraft: %v", err)
	}
	if _, err := os.Stat(draftDir); !os.IsNotExist(err) {
		t.Errorf("draft dir should be removed by DismissDraft; stat err = %v", err)
	}
	if flow.HasDraft(workspaceID) != nil {
		t.Error("draft should be deleted by DismissDraft")
	}
}

// TestDraftDismiss_RemovesDraftDirKeepsUnrelated verifies DismissDraft removes the
// draft_<name> working dir (in designing state — a blocked build leaves one) but
// never touches an unrelated finalized agent's AgentDir(<uuid>).
func TestDraftDismiss_RemovesDraftDirKeepsUnrelated(t *testing.T) {
	flow, _, workspaceID, agentsDir := newDraftFlow(t)

	// An unrelated finalized agent lives at its canonical UUID dir — must survive.
	otherID := uuid.New().String()
	finalizedDir := AgentDir(agentsDir, workspaceID, otherID)
	if err := os.MkdirAll(finalizedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// The WIP draft's readable dir (blocked build left files, still "designing").
	draftDir := DraftAgentDir(agentsDir, workspaceID, "price-tracker")
	if err := os.MkdirAll(filepath.Join(draftDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}

	sess := &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "price-tracker",
		State:       StateDesigning,
	}
	flow.saveDraft(sess)

	if err := flow.DismissDraft(workspaceID); err != nil {
		t.Fatalf("DismissDraft: %v", err)
	}
	if _, err := os.Stat(draftDir); !os.IsNotExist(err) {
		t.Errorf("draft dir should be removed even in designing state; stat err = %v", err)
	}
	if _, err := os.Stat(finalizedDir); err != nil {
		t.Errorf("unrelated finalized agent dir must not be touched; stat err = %v", err)
	}
}

// TestResumeDraft_RecoversInterruptedBuildFromDisk verifies that resuming a
// create draft whose DB row captured no build (state=designing, empty pending) but
// whose on-disk draft dir holds a valid AGENT.md + tools recovers that build and
// lands the user in StateVerifying — so an interrupted build continues from where it
// left off instead of rebuilding from scratch.
func TestResumeDraft_RecoversInterruptedBuildFromDisk(t *testing.T) {
	flow, _, workspaceID, agentsDir := newDraftFlow(t)

	// On-disk built files from a previous build that errored before the verifying save.
	draftDir := DraftAgentDir(agentsDir, workspaceID, "notion-porter")
	if err := os.MkdirAll(filepath.Join(draftDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(draftDir, "AGENT.md"), "# Suggested schedule: none\nFetches Notion pages.")
	mustWriteFile(t, filepath.Join(draftDir, "tools", "fetch.py"), "print('ok')")

	// Draft DB row: designing, empty pending (the build never reached the verifying save).
	flow.saveDraft(&DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "notion-porter",
		State:       StateDesigning,
		History:     []db.ChatMessage{{Role: "user", Content: "port notion"}, {Role: "assistant", Content: "proposal…"}},
	})

	resp, err := flow.ResumeDraft(context.Background(), workspaceID, OriginWeb)
	if err != nil {
		t.Fatalf("ResumeDraft: %v", err)
	}
	if !strings.Contains(resp, "recovered") {
		t.Errorf("resume message = %q, want it to mention recovering the interrupted build", resp)
	}

	got := flow.GetSession(workspaceID)
	if got == nil || got.State != StateVerifying {
		t.Fatalf("state after resume = %+v, want StateVerifying (recovered build)", got)
	}
	if !strings.Contains(got.PendingAgentMD, "Fetches Notion pages") {
		t.Errorf("PendingAgentMD not recovered from disk: %q", got.PendingAgentMD)
	}
	if got.PendingTools["fetch.py"] != "print('ok')" {
		t.Errorf("PendingTools not recovered from disk: %+v", got.PendingTools)
	}
}

// TestStepDesigning_KeepAsIsForceSaves verifies that after a weak-backend soft-fail
// (GenerationFailed), "keep it as-is" recovers the built agent from disk and saves it
// — honoring the block message's promise instead of dropping into the design chat.
func TestStepDesigning_KeepAsIsForceSaves(t *testing.T) {
	flow, database, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	// On-disk built files from a blocked build.
	draftDir := DraftAgentDir(agentsDir, workspaceID, "notion-porter")
	if err := os.MkdirAll(filepath.Join(draftDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(draftDir, "AGENT.md"), "# Suggested schedule: none\nFetches Notion pages.")
	mustWriteFile(t, filepath.Join(draftDir, "tools", "fetch.py"), "print('ok')")

	// Session in designing state, freshly soft-failed (weak-backend gate).
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID:      workspaceID,
		AgentID:          agentID,
		AgentName:        "notion-porter",
		State:            StateDesigning,
		GenerationFailed: true,
		History:          []db.ChatMessage{{Role: "user", Content: "port notion"}, {Role: "assistant", Content: "built but unverified"}},
	}
	flow.saveDraft(flow.sessions[workspaceID])

	resp, done, gotID, err := flow.Step(context.Background(), workspaceID, "keep it as-is", OriginWeb)
	if err != nil {
		t.Fatalf("Step keep-as-is: %v", err)
	}
	if !done || gotID != agentID {
		t.Fatalf("Step = (%q, done=%v, id=%q); want done with agentID %q", resp, done, gotID, agentID)
	}
	if a, err := database.GetAgent(agentID); err != nil || a == nil {
		t.Errorf("agent should be saved after keep-as-is: %v (err=%v)", a, err)
	}
	// Draft dir promoted to the canonical UUID dir; draft row gone.
	if _, err := os.Stat(filepath.Join(AgentDir(agentsDir, workspaceID, agentID), "tools", "fetch.py")); err != nil {
		t.Errorf("built tool should be saved at the canonical dir; err = %v", err)
	}
	if flow.HasDraft(workspaceID) != nil {
		t.Error("draft should be deleted after force-save")
	}
}

// TestResumeDraft_NoDiskFilesStaysDesigning verifies a fresh designing draft with no
// on-disk build stays in the design conversation (recovery is a no-op).
func TestResumeDraft_NoDiskFilesStaysDesigning(t *testing.T) {
	flow, _, workspaceID, _ := newDraftFlow(t)
	flow.saveDraft(&DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "no-build-yet",
		State:       StateDesigning,
		History:     []db.ChatMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok"}},
	})
	if _, err := flow.ResumeDraft(context.Background(), workspaceID, OriginWeb); err != nil {
		t.Fatalf("ResumeDraft: %v", err)
	}
	if got := flow.GetSession(workspaceID); got == nil || got.State != StateDesigning {
		t.Fatalf("state = %+v, want StateDesigning (nothing to recover)", got)
	}
}

// TestFinalize_RenamePreservesNonToolFiles verifies finalize promotes the draft
// dir by RENAME, so files a build wrote outside tools/ (notes/, root-level
// requirements.txt) survive into the finalized agent — not just the tools/ tree
// that PendingTools captures.
func TestFinalize_RenamePreservesNonToolFiles(t *testing.T) {
	flow, _, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	draftDir := DraftAgentDir(agentsDir, workspaceID, "greeter")
	if err := os.MkdirAll(filepath.Join(draftDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(draftDir, "notes"), 0o750); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(draftDir, "tools", "x.py"), "print('hi')")
	mustWriteFile(t, filepath.Join(draftDir, "notes", "plan.md"), "the plan")
	mustWriteFile(t, filepath.Join(draftDir, "requirements.txt"), "requests\n")

	sess := &DesignSession{
		WorkspaceID:    workspaceID,
		AgentID:        agentID,
		AgentName:      "greeter",
		State:          StateVerifying,
		PendingAgentMD: "# Suggested schedule: none\nDoes a thing.",
		PendingTools:   map[string]string{"x.py": "print('hi')"},
	}
	flow.sessions[workspaceID] = sess
	flow.saveDraft(sess)

	_, done, _, err := flow.Step(context.Background(), workspaceID, "approve", OriginWeb)
	if err != nil || !done {
		t.Fatalf("approve: done=%v err=%v", done, err)
	}

	live := AgentDir(agentsDir, workspaceID, agentID)
	for _, rel := range []string{"notes/plan.md", "requirements.txt", "tools/x.py", "AGENT.md"} {
		if _, err := os.Stat(filepath.Join(live, rel)); err != nil {
			t.Errorf("finalized agent missing %s (rename should preserve it); err = %v", rel, err)
		}
	}
	if _, err := os.Stat(draftDir); !os.IsNotExist(err) {
		t.Errorf("draft dir should be gone after finalize; stat err = %v", err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

// TestEditDraftResume_AgentDeleted verifies that resuming an edit draft whose
// agent has since been deleted dismisses the draft and returns an error.
func TestEditDraftResume_AgentDeleted(t *testing.T) {
	flow, database, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	seedAgent(t, database, agentsDir, workspaceID, agentID, "# Suggested schedule: none\nDoes a thing.", nil)

	sess := &DesignSession{
		WorkspaceID:     workspaceID,
		AgentID:         agentID,
		AgentName:       "test-agent",
		State:           StateDesigning,
		IsEdit:          true,
		ExistingAgentMD: "# Suggested schedule: none\nDoes a thing.",
	}
	flow.saveDraft(sess)

	// Agent is deleted out-of-band (user removed it while a draft sat unfinalized).
	if err := database.DeleteAgent(agentID); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(agentsDir, workspaceID, agentID))

	if _, err := flow.ResumeDraft(context.Background(), workspaceID, OriginWeb); err == nil {
		t.Fatal("ResumeDraft should error when the edited agent no longer exists")
	}
	if flow.HasDraft(workspaceID) != nil {
		t.Error("draft should be dismissed after a failed edit resume")
	}
}

// TestDraftExpiry verifies an expired draft is treated as absent by GetAgentDraft.
func TestDraftExpiry(t *testing.T) {
	_, database, workspaceID, _ := newDraftFlow(t)

	if err := database.UpsertAgentDraft(&db.AgentDraft{
		WorkspaceID: workspaceID,
		AgentName:   "stale",
		State:       "designing",
		ExpiresAt:   time.Now().Add(-24 * time.Hour), // already expired
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := database.GetAgentDraft(workspaceID); err != db.ErrNotFound {
		t.Errorf("GetAgentDraft err = %v, want ErrNotFound for expired draft", err)
	}
}

// TestAwaitingResume_NewBranch verifies the "new" path dismisses the draft and
// starts a fresh create session (reusing Start's context loading).
func TestAwaitingResume_NewBranch(t *testing.T) {
	flow, _, workspaceID, _ := newDraftFlow(t)

	agentID := uuid.New().String()
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		AgentName:   "old-draft-name",
		State:       StateAwaitingResume,
		pendingName: "fresh-name",
	}
	// Seed a draft so DismissDraft has something to clear.
	flow.saveDraft(&DesignSession{
		WorkspaceID: workspaceID, AgentID: agentID, AgentName: "old-draft-name", State: StateDesigning,
	})
	if flow.HasDraft(workspaceID) == nil {
		t.Fatal("draft should exist before 'new'")
	}

	resp, _, _, err := flow.Step(context.Background(), workspaceID, "new", OriginWeb)
	if err != nil {
		t.Fatalf("Step new: %v", err)
	}
	if !strings.Contains(resp, "fresh-name") {
		t.Errorf("new-branch response = %q, want it to mention the pending name", resp)
	}
	if flow.HasDraft(workspaceID) != nil {
		t.Error("draft should be dismissed on the 'new' branch")
	}
	sess := flow.GetSession(workspaceID)
	if sess == nil || sess.State != StateDescribing || sess.AgentName != "fresh-name" {
		t.Errorf("session after 'new' = %+v, want fresh StateDescribing session named fresh-name", sess)
	}
	if sess.AgentID == agentID {
		t.Error("fresh session should have a new AgentID, not the dismissed draft's")
	}
}
