package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

func tzTestDB(t *testing.T) (*db.DB, string, string) {
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
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{
		ID: agentID, WorkspaceID: workspaceID, Name: "a", Active: true,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return database, workspaceID, agentID
}

// The column is only useful if it survives the write/read round trip. A SELECT
// that forgets to list it, or an INSERT that forgets to bind it, would leave
// every schedule silently host-local while the settings appeared to be saved —
// the failure mode this whole change exists to remove, reintroduced one layer
// down.
func TestScheduleTimezoneRoundTrips(t *testing.T) {
	database, workspaceID, agentID := tzTestDB(t)
	past := time.Now().Add(-time.Hour)

	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID:          uuid.New().String(),
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		CronExpr:    "0 8 * * *",
		NextRunAt:   &past,
		Enabled:     true,
		Timezone:    "Europe/Skopje",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	due, err := database.ListDueSchedules(time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due schedule, got %d", len(due))
	}
	if due[0].Timezone != "Europe/Skopje" {
		t.Fatalf("timezone did not round-trip: got %q — the scheduler would silently "+
			"evaluate this expression in the host's zone", due[0].Timezone)
	}
}

// The default must be the empty string, which the scheduler reads as
// "host-local". A NULL or a literal "UTC" default would re-time every
// pre-existing schedule the moment the migration ran.
func TestScheduleTimezoneDefaultsToEmpty(t *testing.T) {
	database, workspaceID, agentID := tzTestDB(t)
	past := time.Now().Add(-time.Hour)

	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID:          uuid.New().String(),
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		CronExpr:    "0 8 * * *",
		NextRunAt:   &past,
		Enabled:     true,
		// Timezone deliberately unset — the pre-migration shape.
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	due, err := database.ListDueSchedules(time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due schedule, got %d", len(due))
	}
	if due[0].Timezone != "" {
		t.Fatalf("an unset timezone must read back as the empty string (host-local), got %q",
			due[0].Timezone)
	}
}

// Updating an existing schedule must carry the zone too — the upsert path is
// what a settings save goes through.
func TestScheduleTimezoneUpdatesOnConflict(t *testing.T) {
	database, workspaceID, agentID := tzTestDB(t)
	past := time.Now().Add(-time.Hour)
	id := uuid.New().String()

	row := &db.AgentSchedule{
		ID: id, AgentID: agentID, WorkspaceID: workspaceID,
		CronExpr: "0 8 * * *", NextRunAt: &past, Enabled: true, Timezone: "",
	}
	if err := database.UpsertAgentSchedule(row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	row.Timezone = "America/New_York"
	if err := database.UpsertAgentSchedule(row); err != nil {
		t.Fatalf("update: %v", err)
	}

	due, err := database.ListDueSchedules(time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 || due[0].Timezone != "America/New_York" {
		t.Fatalf("ON CONFLICT must update the timezone; got %+v", due)
	}
}
