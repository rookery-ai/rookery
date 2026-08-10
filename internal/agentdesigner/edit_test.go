package agentdesigner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
)

// testDB opens a fresh migrated SQLite DB in a temp dir and registers a single
// user (FK target for agents/schedules).
func testDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{
		ID:   workspaceID,
		Name: "tester",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return database, workspaceID
}

func seedAgent(t *testing.T, database *db.DB, vaultsBase, workspaceID, agentID, agentMD string, tools map[string]string) {
	t.Helper()
	dir := AgentDir(vaultsBase, workspaceID, agentID)
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(agentMD), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(StateFilePath(vaultsBase, workspaceID, agentID), "test-agent", map[string]any{"counter": 42}); err != nil {
		t.Fatal(err)
	}
	for name, content := range tools {
		if err := os.WriteFile(filepath.Join(dir, "tools", name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	// CreatedAt lives only in the agents DB row now (agent.json/AgentManifest
	// are gone) — set at INSERT time by CreateAgent and never touched again.
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "test-agent", Description: "d", Active: true}); err != nil {
		t.Fatal(err)
	}
}

// ─── loadAgentForEdit: schedule reconciliation ────────────────────────────────

func TestLoadAgentForEdit_ReconcilesScheduleFromDB(t *testing.T) {
	database, workspaceID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	// AGENT.md on disk says */10 (stale — simulates drift from the schedule UI,
	// which writes the DB directly and never touches AGENT.md).
	seedAgent(t, database, agentsDir, workspaceID, agentID,
		"# Suggested schedule: */10 * * * *\nDoes a thing.", nil)

	// The real schedule (set via the web schedule-editor form) is */5.
	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID: uuid.New().String(), AgentID: agentID, WorkspaceID: workspaceID,
		CronExpr: "*/5 * * * *", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	flow := &Flow{designer: NewDesigner(database, agentsDir), db: database}
	_, reconciled, _, err := flow.loadAgentForEdit(workspaceID, agentID)
	if err != nil {
		t.Fatalf("loadAgentForEdit: %v", err)
	}

	got := parseSuggestedSchedule(reconciled)
	if got != "*/5 * * * *" {
		t.Errorf("reconciled schedule = %q, want DB's */5 * * * * (not AGENT.md's stale */10)", got)
	}
}

func TestLoadAgentForEdit_NoScheduleReconcilesToNone(t *testing.T) {
	database, workspaceID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	seedAgent(t, database, agentsDir, workspaceID, agentID,
		"# Suggested schedule: */10 * * * *\nDoes a thing.", nil)
	// No schedule row created — agent is on-demand only.

	flow := &Flow{designer: NewDesigner(database, agentsDir), db: database}
	_, reconciled, _, err := flow.loadAgentForEdit(workspaceID, agentID)
	if err != nil {
		t.Fatalf("loadAgentForEdit: %v", err)
	}

	if got := parseSuggestedSchedule(reconciled); got != "" {
		t.Errorf("reconciled schedule = %q, want empty (no DB schedule exists)", got)
	}
}

// ─── reconcileScheduleOnSave: the duplicate-row / double-fire guard ──────────

func TestReconcileScheduleOnSave_UpsertReusesExistingID(t *testing.T) {
	database, workspaceID := testDB(t)
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	existingID := uuid.New().String()
	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID: existingID, AgentID: agentID, WorkspaceID: workspaceID, CronExpr: "*/10 * * * *", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	reconcileScheduleOnSave(database, workspaceID, agentID, "# Suggested schedule: */5 * * * *\nbody")

	sched, err := database.GetScheduleForAgent(agentID)
	if err != nil || sched == nil {
		t.Fatalf("expected a schedule row, got %v, err=%v", sched, err)
	}
	if sched.ID != existingID {
		t.Errorf("schedule ID = %q, want reused existing ID %q (a fresh ID would duplicate the row and double-fire)", sched.ID, existingID)
	}
	if sched.CronExpr != "*/5 * * * *" {
		t.Errorf("cron = %q, want updated */5 * * * *", sched.CronExpr)
	}

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM agent_schedules WHERE agent_id=?`, agentID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("agent_schedules rows for agent = %d, want exactly 1", count)
	}
}

func TestReconcileScheduleOnSave_DeletesOnNone(t *testing.T) {
	database, workspaceID := testDB(t)
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID: uuid.New().String(), AgentID: agentID, WorkspaceID: workspaceID, CronExpr: "*/10 * * * *", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	reconcileScheduleOnSave(database, workspaceID, agentID, "# Suggested schedule: none\nbody")

	sched, err := database.GetScheduleForAgent(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if sched != nil {
		t.Errorf("expected schedule to be deleted, got %+v", sched)
	}
}

func TestReconcileScheduleOnSave_CreatesWhenNoneExisted(t *testing.T) {
	database, workspaceID := testDB(t)
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "a"}); err != nil {
		t.Fatal(err)
	}

	reconcileScheduleOnSave(database, workspaceID, agentID, "# Suggested schedule: */15 * * * *\nbody")

	sched, err := database.GetScheduleForAgent(agentID)
	if err != nil || sched == nil {
		t.Fatalf("expected a new schedule row, got %v, err=%v", sched, err)
	}
	if sched.CronExpr != "*/15 * * * *" {
		t.Errorf("cron = %q, want */15 * * * *", sched.CronExpr)
	}
}

// ─── copyAgentWorkspace: the live-dir isolation guarantee ────────────────────

// TestCopyAgentWorkspace_NeverTouchesLive proves the property the whole staging
// design depends on: generation only ever reads from the live agent dir and writes
// into a separate staging dir. liveDir must be byte-for-byte unchanged afterwards,
// and stagingDir must contain the *reconciled* AGENT.md (not the stale on-disk one).
func TestCopyAgentWorkspace_NeverTouchesLive(t *testing.T) {
	liveDir := t.TempDir()
	stagingDir := filepath.Join(t.TempDir(), "staging") // doesn't exist yet

	staleAgentMD := "# Suggested schedule: */10 * * * *\nOld stale body."
	if err := os.MkdirAll(filepath.Join(liveDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "AGENT.md"), []byte(staleAgentMD), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(filepath.Join(liveDir, "state.md"), "test-agent", map[string]any{"counter": 7}); err != nil {
		t.Fatal(err)
	}
	liveState, err := os.ReadFile(filepath.Join(liveDir, "state.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "tools", "a.py"), []byte("print('a')"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "tools", "b.py"), []byte("print('b')"), 0o640); err != nil {
		t.Fatal(err)
	}

	reconciledMD := "# Suggested schedule: */5 * * * *\nOld stale body."
	if err := copyAgentWorkspace(liveDir, stagingDir, reconciledMD); err != nil {
		t.Fatalf("copyAgentWorkspace: %v", err)
	}

	// Staging got the reconciled content, not the stale on-disk version.
	stagedMD, err := os.ReadFile(filepath.Join(stagingDir, "AGENT.md"))
	if err != nil || string(stagedMD) != reconciledMD {
		t.Errorf("staging AGENT.md = %q, err=%v, want reconciled %q", stagedMD, err, reconciledMD)
	}
	stagedState, err := os.ReadFile(filepath.Join(stagingDir, "state.md"))
	if err != nil || string(stagedState) != string(liveState) {
		t.Errorf("staging state.md = %q, err=%v, want %q", stagedState, err, liveState)
	}
	for _, name := range []string{"a.py", "b.py"} {
		data, err := os.ReadFile(filepath.Join(stagingDir, "tools", name))
		if err != nil {
			t.Errorf("staging tools/%s missing: %v", name, err)
		}
		live, _ := os.ReadFile(filepath.Join(liveDir, "tools", name))
		if string(data) != string(live) {
			t.Errorf("staging tools/%s = %q, want copy of live %q", name, data, live)
		}
	}

	// The live dir must be completely untouched — same content as before the call.
	liveMDAfter, err := os.ReadFile(filepath.Join(liveDir, "AGENT.md"))
	if err != nil || string(liveMDAfter) != staleAgentMD {
		t.Errorf("liveDir AGENT.md changed: got %q, want untouched stale %q", liveMDAfter, staleAgentMD)
	}
	liveStateAfter, err := os.ReadFile(filepath.Join(liveDir, "state.md"))
	if err != nil || string(liveStateAfter) != string(liveState) {
		t.Errorf("liveDir state.md changed: got %q, want untouched %q", liveStateAfter, liveState)
	}
}

// TestCopyAgentWorkspace_NoStateFileMeansNoSeed locks in the deliberate choice
// for a never-run live agent (no state.md yet): copyAgentWorkspace must not
// synthesize a placeholder state.md in staging. ReadState already treats a
// missing file as empty memory, so there is nothing useful to seed, and
// state.json (the old placeholder format) must never be written anywhere.
func TestCopyAgentWorkspace_NoStateFileMeansNoSeed(t *testing.T) {
	liveDir := t.TempDir()
	stagingDir := filepath.Join(t.TempDir(), "staging")

	if err := os.MkdirAll(filepath.Join(liveDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "AGENT.md"), []byte("body"), 0o640); err != nil {
		t.Fatal(err)
	}
	// Deliberately no state.md in liveDir.

	if err := copyAgentWorkspace(liveDir, stagingDir, "body"); err != nil {
		t.Fatalf("copyAgentWorkspace: %v", err)
	}

	if _, err := os.Stat(filepath.Join(stagingDir, "state.md")); !os.IsNotExist(err) {
		t.Errorf("staging state.md should not exist when live has none, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(stagingDir, "state.json")); !os.IsNotExist(err) {
		t.Errorf("staging state.json must never be created, stat err = %v", err)
	}
}

// ─── UpdateAgent: state preservation, CreatedAt preservation, tool wipe ──────

func TestUpdateAgent_PreservesStateAndCreatedAtAndWipesRemovedTools(t *testing.T) {
	database, workspaceID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	seedAgent(t, database, agentsDir, workspaceID, agentID,
		"# Suggested schedule: none\nOld body.",
		map[string]string{"keep.py": "print('keep')", "remove.py": "print('remove')"})

	before, err := database.GetAgent(agentID)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(StateFilePath(agentsDir, workspaceID, agentID))
	if err != nil {
		t.Fatal(err)
	}

	designer := NewDesigner(database, agentsDir)
	newTools := map[string]string{"keep.py": "print('keep updated')"}
	if err := designer.UpdateAgent(workspaceID, agentID, "test-agent", "new description",
		"# Suggested schedule: none\nNew body.", newTools, nil); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	dir := AgentDir(agentsDir, workspaceID, agentID)

	// state.md must be byte-identical to what was there before the edit —
	// writeAgentContent must never touch a live agent's persisted state.
	stateAfter, err := os.ReadFile(StateFilePath(agentsDir, workspaceID, agentID))
	if err != nil {
		t.Fatal(err)
	}
	if string(stateAfter) != string(stateBefore) {
		t.Errorf("state.md changed across UpdateAgent: got %q, want untouched %q", stateAfter, stateBefore)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Errorf("state.json must never be (re)created by UpdateAgent, stat err = %v", err)
	}

	// Removed tool must be gone; kept tool must reflect the new content.
	if _, err := os.Stat(filepath.Join(dir, "tools", "remove.py")); !os.IsNotExist(err) {
		t.Errorf("expected tools/remove.py to be deleted, stat err = %v", err)
	}
	keep, err := os.ReadFile(filepath.Join(dir, "tools", "keep.py"))
	if err != nil || string(keep) != "print('keep updated')" {
		t.Errorf("tools/keep.py = %q, err=%v, want updated content", keep, err)
	}

	// CreatedAt must survive the edit. There is no manifest any more to
	// duplicate this into — the agents DB row (set once at CreateAgent, never
	// touched by UpdateAgentDescription) is the only record of it.
	agent, err := database.GetAgent(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if !agent.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("agent.CreatedAt = %v, want preserved original %v", agent.CreatedAt, before.CreatedAt)
	}

	// DB description must be updated; this must be an UPDATE not an INSERT (a
	// second CreateAgent on this ID/name would violate the PK/unique constraint).
	if agent.Description != "new description" {
		t.Errorf("agent.Description = %q, want %q", agent.Description, "new description")
	}
}
