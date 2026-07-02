package agentdesigner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
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
		WorkspaceID:    workspaceID,
		AgentID:   uuid.New().String(),
		AgentName: "price-tracker",
		State:     StateDesigning,
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
	resp, err := flow.ResumeDraft(context.Background(), workspaceID)
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
		WorkspaceID:         workspaceID,
		AgentID:        uuid.New().String(),
		AgentName:      "price-tracker",
		State:          StateVerifying,
		PendingAgentMD: pendingMD,
		PendingTools:   map[string]string{"fetch_price.py": "print('ok')"},
	}
	flow.saveDraft(sess)

	resp, err := flow.ResumeDraft(context.Background(), workspaceID)
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
	flow, database, workspaceID, _ := newDraftFlow(t)

	agentID := uuid.New().String()
	sess := &DesignSession{
		WorkspaceID:         workspaceID,
		AgentID:        agentID,
		AgentName:      "price-tracker",
		State:          StateVerifying,
		PendingAgentMD: "# Suggested schedule: none\nFetches BTC price and alerts.",
	}
	flow.sessions[workspaceID] = sess
	flow.saveDraft(sess)

	if flow.HasDraft(workspaceID) == nil {
		t.Fatal("draft should exist before finalize")
	}

	resp, done, gotID, err := flow.Step(context.Background(), workspaceID, "approve")
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
}

// TestDraftCleanupOrphanDir verifies DismissDraft removes the pre-approved agent
// directory left by runGeneration for a create-mode verifying draft.
func TestDraftCleanupOrphanDir(t *testing.T) {
	flow, _, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	agentDir := AgentDir(agentsDir, workspaceID, agentID)
	if err := os.MkdirAll(filepath.Join(agentDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENT.md"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	sess := &DesignSession{
		WorkspaceID:         workspaceID,
		AgentID:        agentID,
		AgentName:      "price-tracker",
		State:          StateVerifying,
		PendingAgentMD: "x",
	}
	flow.saveDraft(sess)

	if err := flow.DismissDraft(workspaceID); err != nil {
		t.Fatalf("DismissDraft: %v", err)
	}
	if _, err := os.Stat(agentDir); !os.IsNotExist(err) {
		t.Errorf("orphan agent dir should be removed; stat err = %v", err)
	}
	if flow.HasDraft(workspaceID) != nil {
		t.Error("draft should be deleted by DismissDraft")
	}
}

// TestDraftDismiss_CreateDesigningKeepsDir verifies that dismissing a *designing*
// (not yet generated) create draft does NOT touch any directory — there is none
// to clean, and DismissDraft must never RemoveAll a path that wasn't created.
func TestDraftDismiss_CreateDesigningKeepsDir(t *testing.T) {
	flow, _, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	// A real sibling agent dir exists (e.g. a finalized agent); DismissDraft must
	// not touch it because this draft is in "designing" state (no generation ran).
	siblingDir := AgentDir(agentsDir, workspaceID, agentID)
	if err := os.MkdirAll(siblingDir, 0o750); err != nil {
		t.Fatal(err)
	}

	sess := &DesignSession{
		WorkspaceID:    workspaceID,
		AgentID:   agentID,
		AgentName: "price-tracker",
		State:     StateDesigning,
	}
	flow.saveDraft(sess)

	if err := flow.DismissDraft(workspaceID); err != nil {
		t.Fatalf("DismissDraft: %v", err)
	}
	if _, err := os.Stat(siblingDir); err != nil {
		t.Errorf("designing draft dismiss must not remove agent dir; stat err = %v", err)
	}
}

// TestEditDraftResume_AgentDeleted verifies that resuming an edit draft whose
// agent has since been deleted dismisses the draft and returns an error.
func TestEditDraftResume_AgentDeleted(t *testing.T) {
	flow, database, workspaceID, agentsDir := newDraftFlow(t)

	agentID := uuid.New().String()
	seedAgent(t, database, agentsDir, workspaceID, agentID, "# Suggested schedule: none\nDoes a thing.", nil)

	sess := &DesignSession{
		WorkspaceID:          workspaceID,
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

	if _, err := flow.ResumeDraft(context.Background(), workspaceID); err == nil {
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
		WorkspaceID:    workspaceID,
		AgentName: "stale",
		State:     "designing",
		ExpiresAt: time.Now().Add(-24 * time.Hour), // already expired
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
		WorkspaceID:      workspaceID,
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

	resp, _, _, err := flow.Step(context.Background(), workspaceID, "new")
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
