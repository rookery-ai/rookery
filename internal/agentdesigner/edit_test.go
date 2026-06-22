package agentdesigner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

// testDB opens a fresh migrated SQLite DB in a temp dir and registers a single
// user (FK target for agents/schedules).
func testDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"), "../../migrations")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	userID := uuid.New().String()
	if err := database.CreateUser(&db.User{
		ID:           userID,
		Username:     "tester",
		PasswordHash: "x",
		Role:         "user",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return database, userID
}

func seedAgent(t *testing.T, database *db.DB, vaultsBase, userID, agentID, agentMD string, tools map[string]string) {
	t.Helper()
	dir := AgentDir(vaultsBase, userID, agentID)
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(agentMD), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"counter":42}`), 0o640); err != nil {
		t.Fatal(err)
	}
	for name, content := range tools {
		if err := os.WriteFile(filepath.Join(dir, "tools", name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &AgentManifest{ID: agentID, Name: "test-agent", CreatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := SaveManifest(vaultsBase, userID, agentID, manifest); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgent(&db.Agent{ID: agentID, UserID: userID, Name: "test-agent", Description: "d", Active: true}); err != nil {
		t.Fatal(err)
	}
}

// ─── loadAgentForEdit: schedule reconciliation ────────────────────────────────

func TestLoadAgentForEdit_ReconcilesScheduleFromDB(t *testing.T) {
	database, userID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	// AGENT.md on disk says */10 (stale — simulates drift from the schedule UI,
	// which writes the DB directly and never touches AGENT.md).
	seedAgent(t, database, agentsDir, userID, agentID,
		"# Suggested schedule: */10 * * * *\nDoes a thing.", nil)

	// The real schedule (set via the web schedule-editor form) is */5.
	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID: uuid.New().String(), AgentID: agentID, UserID: userID,
		CronExpr: "*/5 * * * *", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	flow := &Flow{designer: NewDesigner(database, agentsDir), db: database}
	_, reconciled, err := flow.loadAgentForEdit(userID, agentID)
	if err != nil {
		t.Fatalf("loadAgentForEdit: %v", err)
	}

	got := parseSuggestedSchedule(reconciled)
	if got != "*/5 * * * *" {
		t.Errorf("reconciled schedule = %q, want DB's */5 * * * * (not AGENT.md's stale */10)", got)
	}
}

func TestLoadAgentForEdit_NoScheduleReconcilesToNone(t *testing.T) {
	database, userID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	seedAgent(t, database, agentsDir, userID, agentID,
		"# Suggested schedule: */10 * * * *\nDoes a thing.", nil)
	// No schedule row created — agent is on-demand only.

	flow := &Flow{designer: NewDesigner(database, agentsDir), db: database}
	_, reconciled, err := flow.loadAgentForEdit(userID, agentID)
	if err != nil {
		t.Fatalf("loadAgentForEdit: %v", err)
	}

	if got := parseSuggestedSchedule(reconciled); got != "" {
		t.Errorf("reconciled schedule = %q, want empty (no DB schedule exists)", got)
	}
}

// ─── reconcileScheduleOnSave: the duplicate-row / double-fire guard ──────────

func TestReconcileScheduleOnSave_UpsertReusesExistingID(t *testing.T) {
	database, userID := testDB(t)
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{ID: agentID, UserID: userID, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	existingID := uuid.New().String()
	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID: existingID, AgentID: agentID, UserID: userID, CronExpr: "*/10 * * * *", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	reconcileScheduleOnSave(database, userID, agentID, "# Suggested schedule: */5 * * * *\nbody")

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
	database, userID := testDB(t)
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{ID: agentID, UserID: userID, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID: uuid.New().String(), AgentID: agentID, UserID: userID, CronExpr: "*/10 * * * *", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	reconcileScheduleOnSave(database, userID, agentID, "# Suggested schedule: none\nbody")

	sched, err := database.GetScheduleForAgent(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if sched != nil {
		t.Errorf("expected schedule to be deleted, got %+v", sched)
	}
}

func TestReconcileScheduleOnSave_CreatesWhenNoneExisted(t *testing.T) {
	database, userID := testDB(t)
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{ID: agentID, UserID: userID, Name: "a"}); err != nil {
		t.Fatal(err)
	}

	reconcileScheduleOnSave(database, userID, agentID, "# Suggested schedule: */15 * * * *\nbody")

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
	liveState := `{"counter":7}`
	if err := os.MkdirAll(filepath.Join(liveDir, "tools"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "AGENT.md"), []byte(staleAgentMD), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "state.json"), []byte(liveState), 0o640); err != nil {
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
	stagedState, err := os.ReadFile(filepath.Join(stagingDir, "state.json"))
	if err != nil || string(stagedState) != liveState {
		t.Errorf("staging state.json = %q, err=%v, want %q", stagedState, err, liveState)
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
	liveStateAfter, err := os.ReadFile(filepath.Join(liveDir, "state.json"))
	if err != nil || string(liveStateAfter) != liveState {
		t.Errorf("liveDir state.json changed: got %q, want untouched %q", liveStateAfter, liveState)
	}
}

// ─── UpdateAgent: state preservation, CreatedAt preservation, tool wipe ──────

func TestUpdateAgent_PreservesStateAndCreatedAtAndWipesRemovedTools(t *testing.T) {
	database, userID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	seedAgent(t, database, agentsDir, userID, agentID,
		"# Suggested schedule: none\nOld body.",
		map[string]string{"keep.py": "print('keep')", "remove.py": "print('remove')"})

	designer := NewDesigner(database, agentsDir)
	newTools := map[string]string{"keep.py": "print('keep updated')"}
	if err := designer.UpdateAgent(userID, agentID, "test-agent", "new description",
		"# Suggested schedule: none\nNew body.", newTools, nil, nil); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	dir := AgentDir(agentsDir, userID, agentID)

	// state.json must be byte-identical to what was there before the edit.
	state, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(state) != `{"counter":42}` {
		t.Errorf("state.json = %q, want untouched original", state)
	}

	// Removed tool must be gone; kept tool must reflect the new content.
	if _, err := os.Stat(filepath.Join(dir, "tools", "remove.py")); !os.IsNotExist(err) {
		t.Errorf("expected tools/remove.py to be deleted, stat err = %v", err)
	}
	keep, err := os.ReadFile(filepath.Join(dir, "tools", "keep.py"))
	if err != nil || string(keep) != "print('keep updated')" {
		t.Errorf("tools/keep.py = %q, err=%v, want updated content", keep, err)
	}

	// Manifest CreatedAt must survive the edit.
	manifest, err := LoadManifest(agentsDir, userID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.CreatedAt.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("manifest.CreatedAt = %v, want preserved original 2020-01-01", manifest.CreatedAt)
	}

	// DB description must be updated; this must be an UPDATE not an INSERT (a
	// second CreateAgent on this ID/name would violate the PK/unique constraint).
	agent, err := database.GetAgent(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Description != "new description" {
		t.Errorf("agent.Description = %q, want %q", agent.Description, "new description")
	}
}
