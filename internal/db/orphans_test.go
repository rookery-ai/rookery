package db_test

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/migrations"
)

// orphanFixture builds a database holding one SURVIVING agent with dependent
// rows and one DELETED agent whose dependent rows were left behind.
//
// The orphans are created the way the real ones were: by deleting the agent
// over a connection with foreign_keys OFF. That is the whole mechanism of the
// bug #214 fixed — pragmas were executed after Open rather than declared in the
// DSN, so `database/sql` handed the delete whichever pooled connection it liked
// and the cascade fired only sometimes. Reproducing it faithfully is better
// than hand-inserting rows that could never exist, because it also proves the
// cascade DOES fire on a properly-configured connection: the surviving agent's
// rows are removed by the FK when we delete it, not by the sweep.
func orphanFixture(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, a := range []struct{ id, name string }{{"live", "keeper"}, {"ghost", "doomed"}} {
		if err := database.CreateAgent(&db.Agent{
			ID: a.id, WorkspaceID: workspaceID, Name: a.name, Active: true,
		}); err != nil {
			t.Fatalf("create agent %s: %v", a.id, err)
		}
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	for _, id := range []string{"live", "ghost"} {
		exec(`INSERT INTO agent_runs (id,agent_id,workspace_id,trigger) VALUES (?,?,?,'cron')`,
			"run-"+id, id, workspaceID)
		exec(`INSERT INTO agent_skills (agent_id,skill_name) VALUES (?,'csv')`, id)
		exec(`INSERT INTO agent_schedules (id,agent_id,workspace_id,cron_expr) VALUES (?,?,?,'0 * * * *')`,
			"sched-"+id, id, workspaceID)
	}
	// A binding grants live credentials, so an orphaned one is the row with a
	// security dimension rather than merely an untidy one.
	exec(`INSERT INTO service_connections (id,workspace_id,provider,account_label,status)
		VALUES ('conn1',?,'github','personal','ACTIVE')`, workspaceID)
	exec(`INSERT INTO agent_connections (agent_id,connection_id) VALUES ('ghost','conn1')`)

	// Notification history for the doomed agent. Its denormalized name is the
	// reason the row survives the sweep while its dangling id does not.
	if err := database.CreateInboxMessage(&db.InboxMessage{
		ID: "inbox-ghost", WorkspaceID: workspaceID, Source: "agent_run",
		AgentID: "ghost", AgentName: "doomed", RefID: "run-ghost",
		Trigger: "cron", Body: "25C, clear sky", Status: "ok",
	}); err != nil {
		t.Fatalf("create inbox message: %v", err)
	}

	// Delete the agent over an FK-OFF connection, stranding its dependents.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DELETE FROM agents WHERE id='ghost'`); err != nil {
		t.Fatalf("strand rows: %v", err)
	}

	return database, workspaceID, path
}

// applyOrphanSweep runs migration 015's own SQL, read from the embedded
// migration set rather than retyped here. A test that asserts against a second
// copy of the statements would only prove the two copies agree.
func applyOrphanSweep(t *testing.T, database *db.DB) {
	t.Helper()
	raw, err := fs.ReadFile(migrations.FS, "015_orphaned_agent_rows.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	// Line-based, mirroring internal/db.splitStatements: comment lines are
	// dropped BEFORE splitting on ";". Splitting the raw text on ";" first
	// would cut a statement in half at any semicolon inside a comment, which
	// this file's own prose contains.
	var buf strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		buf.WriteString(line + "\n")
		if !strings.HasSuffix(trimmed, ";") {
			continue
		}
		stmt := strings.TrimSpace(buf.String())
		buf.Reset()
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("apply %q: %v", stmt, err)
		}
	}
}

func countRows(t *testing.T, database *db.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := database.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// The CASCADE tables lose their orphans and keep everything belonging to an
// agent that still exists — the sweep has to be selective, not a truncate.
func TestMigration015DeletesCascadeOrphans(t *testing.T) {
	database, _, _ := orphanFixture(t)
	applyOrphanSweep(t, database)

	for _, tc := range []struct{ table, col string }{
		{"agent_runs", "agent_id"},
		{"agent_skills", "agent_id"},
		{"agent_schedules", "agent_id"},
		{"agent_connections", "agent_id"},
	} {
		if n := countRows(t, database,
			`SELECT COUNT(*) FROM `+tc.table+` WHERE `+tc.col+`='ghost'`); n != 0 {
			t.Errorf("%s: %d orphaned row(s) survived the sweep", tc.table, n)
		}
	}
	for _, table := range []string{"agent_runs", "agent_skills", "agent_schedules"} {
		if n := countRows(t, database,
			`SELECT COUNT(*) FROM `+table+` WHERE agent_id='live'`); n != 1 {
			t.Errorf("%s: swept a surviving agent's row (got %d, want 1)", table, n)
		}
	}
}

// The inbox row is history and must survive with its denormalized name intact.
// Only the dangling id goes — that is what kills Home's link to a dead agent.
func TestMigration015PreservesInboxHistoryAndNullsTheDanglingID(t *testing.T) {
	database, _, _ := orphanFixture(t)
	applyOrphanSweep(t, database)

	var agentID sql.NullString
	var name, body string
	err := database.QueryRow(
		`SELECT agent_id, agent_name, body FROM inbox_messages WHERE id='inbox-ghost'`).
		Scan(&agentID, &name, &body)
	if err != nil {
		t.Fatalf("inbox row was deleted; it should have been preserved: %v", err)
	}
	if agentID.Valid {
		t.Errorf("dangling agent_id not nulled: %q", agentID.String)
	}
	if name != "doomed" {
		t.Errorf("denormalized agent_name lost: %q", name)
	}
	if body != "25C, clear sky" {
		t.Errorf("notification body lost: %q", body)
	}
}

// Migrations run on every boot, so a second application must change nothing.
func TestMigration015IsIdempotent(t *testing.T) {
	database, _, _ := orphanFixture(t)
	applyOrphanSweep(t, database)
	before := countRows(t, database, `SELECT COUNT(*) FROM agent_runs`)
	applyOrphanSweep(t, database)
	if after := countRows(t, database, `SELECT COUNT(*) FROM agent_runs`); after != before {
		t.Errorf("second sweep changed the row count: %d → %d", before, after)
	}
}

// The premise the whole migration rests on: with foreign keys enforced on every
// pooled connection, deleting an agent cascades and creates no orphans at all.
// If this ever fails, the sweep is treating a live leak as historical residue.
func TestDeletingAnAgentNowCascades(t *testing.T) {
	database, _, _ := orphanFixture(t)
	if err := database.DeleteAgent("live"); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	for _, table := range []string{"agent_runs", "agent_skills", "agent_schedules"} {
		if n := countRows(t, database,
			`SELECT COUNT(*) FROM `+table+` WHERE agent_id='live'`); n != 0 {
			t.Errorf("%s: cascade did not fire, %d row(s) left behind", table, n)
		}
	}
}
