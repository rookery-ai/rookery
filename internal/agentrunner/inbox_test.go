package agentrunner

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/vault"
)

// inboxTestDB opens a fresh migrated DB with one workspace + one agent.
func inboxTestDB(t *testing.T) (*db.DB, *db.Agent, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agent := &db.Agent{ID: "agent-1", WorkspaceID: workspaceID, Name: "Price Tracker", Active: true}
	if err := database.CreateAgent(agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return database, agent, workspaceID
}

// TestRecordInboxWritesRowOnly proves the runner's delivery-hook seam writes a
// DB row for ok and error notifications, skips empty bodies, and writes NOTHING
// to the vault. The delivered text is already archived by ReflectAgentRun in
// agents/<id>/logs/run_<ts>.md; a second copy under inbox/ only cluttered the KB
// browser and polluted designer retrieval.
func TestRecordInboxWritesRowOnly(t *testing.T) {
	database, agent, workspaceID := inboxTestDB(t)
	vlt := vault.New(t.TempDir())
	r := &Runner{db: database, reflector: vlt.Reflector()}
	in := RunInput{AgentID: agent.ID, WorkspaceID: workspaceID, Trigger: "manual"}

	// ok path
	r.recordInbox(in, agent, "run-ok", "BTC is $66k", "ok")
	// error path
	r.recordInbox(in, agent, "run-err", "\u26a0\ufe0f Price Tracker failed: out of quota", "error")
	// empty body is skipped (no notification)
	r.recordInbox(in, agent, "run-empty", "", "ok")

	msgs, err := database.ListInboxMessages(workspaceID, 100, 0)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("inbox rows = %d (%v), want 2 (empty body skipped)", len(msgs), err)
	}
	// Newest first: err then ok.
	if msgs[0].Status != "error" || msgs[1].Status != "ok" {
		t.Fatalf("status order = %s,%s; want error,ok", msgs[0].Status, msgs[1].Status)
	}
	if msgs[0].AgentName != agent.Name || msgs[0].Trigger != "manual" {
		t.Fatalf("err row missing denormalized fields: %+v", msgs[0])
	}
	if msgs[1].Body != "BTC is $66k" {
		t.Fatalf("ok row body = %q", msgs[1].Body)
	}

	// Nothing reflected into the vault.
	if nodes, err := vlt.List(workspaceID, "inbox"); err == nil && len(nodes) > 0 {
		t.Fatalf("inbox/ notes written = %d, want none", len(nodes))
	}
}

// TestRecordInboxNilReflector proves the DB row still lands when no vault is
// wired — the scheduler/manual-run paths that don't set WithVault must not panic.
func TestRecordInboxNilReflector(t *testing.T) {
	database, agent, workspaceID := inboxTestDB(t)
	r := &Runner{db: database, reflector: nil}
	in := RunInput{AgentID: agent.ID, WorkspaceID: workspaceID, Trigger: "cron"}

	r.recordInbox(in, agent, "run-1", "hello", "ok")
	msgs, _ := database.ListInboxMessages(workspaceID, 100, 0)
	if len(msgs) != 1 || msgs[0].Body != "hello" {
		t.Fatalf("nil-reflector row = %+v, want 1 row body=hello", msgs)
	}
}
