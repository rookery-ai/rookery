package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
)

func schedulesDashboardTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return database, workspaceID
}

// TestListWorkspaceSchedulesWithNames_Empty proves a workspace with no
// schedules gets a non-nil empty slice (never nil — callers marshal straight
// to JSON arrays).
func TestListWorkspaceSchedulesWithNames_Empty(t *testing.T) {
	database, workspaceID := schedulesDashboardTestDB(t)

	got, err := database.ListWorkspaceSchedulesWithNames(workspaceID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 schedules, got %d", len(got))
	}
}

// TestListWorkspaceSchedulesWithNames_ShapeAndFiltering seeds three agents:
// one with an enabled schedule (should appear, with its agent name joined
// in), one with a disabled schedule (should NOT appear), and one active
// agent whose schedule belongs to an inactive agent (should NOT appear —
// mirrors ListDueSchedules' `a.active=1` join semantics: a paused agent's
// schedule will never fire, so it's not "upcoming"). Also asserts ordering
// by next_run_at ascending.
func TestListWorkspaceSchedulesWithNames_ShapeAndFiltering(t *testing.T) {
	database, workspaceID := schedulesDashboardTestDB(t)

	mustAgent := func(id, name string, active bool) {
		t.Helper()
		if err := database.CreateAgent(&db.Agent{ID: id, WorkspaceID: workspaceID, Name: name, Active: active}); err != nil {
			t.Fatalf("create agent %s: %v", id, err)
		}
		// CreateAgent always inserts active=1 (Active param unused) — set the
		// real state explicitly via SetAgentActive.
		if !active {
			if err := database.SetAgentActive(id, false); err != nil {
				t.Fatalf("set agent %s inactive: %v", id, err)
			}
		}
	}
	mustAgent("agent-enabled-later", "Later Agent", true)
	mustAgent("agent-enabled-sooner", "Sooner Agent", true)
	mustAgent("agent-disabled", "Disabled Agent", true)
	mustAgent("agent-paused", "Paused Agent", false)

	now := time.Now().UTC()
	later := now.Add(2 * time.Hour)
	sooner := now.Add(1 * time.Hour)

	mustSchedule := func(id, agentID, cron string, next time.Time, enabled bool) {
		t.Helper()
		if err := database.UpsertAgentSchedule(&db.AgentSchedule{
			ID: id, AgentID: agentID, WorkspaceID: workspaceID, CronExpr: cron, NextRunAt: &next, Enabled: enabled,
		}); err != nil {
			t.Fatalf("upsert schedule %s: %v", id, err)
		}
		if !enabled {
			// UpsertAgentSchedule always sets enabled=1 on INSERT — disable
			// explicitly via a second call semantics isn't available, so
			// disable directly for the fixture.
			if _, err := database.Exec(`UPDATE agent_schedules SET enabled=0 WHERE id=?`, id); err != nil {
				t.Fatalf("disable schedule %s: %v", id, err)
			}
		}
	}
	mustSchedule("sched-later", "agent-enabled-later", "0 8 * * *", later, true)
	mustSchedule("sched-sooner", "agent-enabled-sooner", "0 9 * * *", sooner, true)
	mustSchedule("sched-disabled", "agent-disabled", "0 10 * * *", now.Add(30*time.Minute), false)
	mustSchedule("sched-paused", "agent-paused", "0 11 * * *", now.Add(15*time.Minute), true)

	got, err := database.ListWorkspaceSchedulesWithNames(workspaceID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		var ids []string
		for _, s := range got {
			ids = append(ids, s.AgentID)
		}
		t.Fatalf("expected 2 upcoming schedules, got %d: %v", len(got), ids)
	}
	if got[0].AgentID != "agent-enabled-sooner" || got[0].AgentName != "Sooner Agent" {
		t.Fatalf("expected sooner schedule first, got %+v", got[0])
	}
	if got[1].AgentID != "agent-enabled-later" || got[1].AgentName != "Later Agent" {
		t.Fatalf("expected later schedule second, got %+v", got[1])
	}
	if got[0].CronExpr != "0 9 * * *" {
		t.Fatalf("cron_expr not carried through: %+v", got[0])
	}
	if got[0].NextRunAt == nil {
		t.Fatalf("next_run_at should be set: %+v", got[0])
	}
}
