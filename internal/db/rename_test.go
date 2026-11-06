package db_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
)

func renameAgentFixture(t *testing.T, database *db.DB, workspaceID, id, name string) {
	t.Helper()
	if err := database.CreateAgent(&db.Agent{ID: id, WorkspaceID: workspaceID, Name: name}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
}

func TestRenameAgentUpdatesDenormalisedLabels(t *testing.T) {
	database, ws := draftTestDB(t)
	ctx := context.Background()
	renameAgentFixture(t, database, ws, "a1", "Price watch")
	renameAgentFixture(t, database, ws, "a2", "Other")

	for id, agent := range map[string]string{"m1": "a1", "m2": "a2"} {
		name := map[string]string{"a1": "Price watch", "a2": "Other"}[agent]
		if err := database.CreateInboxMessage(&db.InboxMessage{ID: id, WorkspaceID: ws, Source: "agent_run", AgentID: agent, AgentName: name, Body: "x", Status: "ok"}); err != nil {
			t.Fatalf("inbox: %v", err)
		}
	}
	if err := database.InsertServiceConnection(ctx, db.ServiceConnection{ID: "c1", WorkspaceID: ws, Provider: "x", AccountLabel: "l", Status: "ACTIVE"}); err != nil {
		t.Fatalf("connection: %v", err)
	}
	if err := database.CreatePendingAction(ctx, &db.PendingAction{
		ID: "p1", WorkspaceID: ws, AgentID: "a1", AgentName: "Price watch", ConnectionID: "c1", Provider: "x", Action: "y", ArgsJSON: "{}",
		Status: db.PendingStatusPending,
	}); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := database.UpsertAgentDraft(&db.AgentDraft{
		WorkspaceID: ws, AgentID: "a1", AgentName: "Price watch", IsEdit: true, State: "designing",
		HistoryJSON: "[]", PendingAgentMD: "[CALL: Price watch]", PendingToolsJSON: "{}",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("draft: %v", err)
	}

	if err := database.RenameAgent("a1", "Price tracker", &db.DraftEdit{AgentMD: "[CALL: Price tracker]", ToolsJSON: "{}"}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, _ := database.GetAgent("a1")
	if got.Name != "Price tracker" {
		t.Fatalf("agent name = %q", got.Name)
	}
	msgs, _ := database.ListInboxMessages(ws, 10, 0)
	for _, m := range msgs {
		want := map[string]string{"a1": "Price tracker", "a2": "Other"}[m.AgentID]
		if m.AgentName != want {
			t.Errorf("inbox for %s = %q, want %q", m.AgentID, m.AgentName, want)
		}
	}
	p, err := database.GetPendingAction(ctx, ws, "p1")
	if err != nil || p.AgentName != "Price tracker" {
		t.Fatalf("pending action = %+v, %v", p, err)
	}
	dr, err := database.GetAgentDraft(ws)
	if err != nil || dr.AgentName != "Price tracker" || dr.PendingAgentMD != "[CALL: Price tracker]" {
		t.Fatalf("draft = %+v, %v", dr, err)
	}
}

func TestRenameAgentLeavesAnotherAgentsDraftLabel(t *testing.T) {
	database, ws := draftTestDB(t)
	renameAgentFixture(t, database, ws, "a1", "Price watch")
	if err := database.UpsertAgentDraft(&db.AgentDraft{
		WorkspaceID: ws, AgentID: "fresh", AgentName: "New idea", State: "designing",
		HistoryJSON: "[]", PendingToolsJSON: "{}", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("draft: %v", err)
	}
	if err := database.RenameAgent("a1", "Price tracker", nil); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if dr, _ := database.GetAgentDraft(ws); dr.AgentName != "New idea" {
		t.Fatalf("draft label = %q", dr.AgentName)
	}
}

func TestRenameAgentCollisionIsErrNameTakenAndChangesNothing(t *testing.T) {
	database, ws := draftTestDB(t)
	renameAgentFixture(t, database, ws, "a1", "One")
	renameAgentFixture(t, database, ws, "a2", "Two")
	if err := database.RenameAgent("a1", "Two", nil); !errors.Is(err, db.ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	if got, _ := database.GetAgent("a1"); got.Name != "One" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestRenameAgentMissingIsNotFound(t *testing.T) {
	database, _ := draftTestDB(t)
	if err := database.RenameAgent("nope", "X", nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestRenameSkillMovesAttachments(t *testing.T) {
	database, ws := draftTestDB(t)
	renameAgentFixture(t, database, ws, "a1", "One")
	if err := database.CreateSkill(&db.Skill{ID: "s1", WorkspaceID: ws, Name: "report-helper", Description: "old"}); err != nil {
		t.Fatalf("skill: %v", err)
	}
	if err := database.SetAgentSkills("a1", []string{"csv", "report-helper"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := database.RenameSkill(ws, "s1", "report-helper", "summary-helper", "new", nil); err != nil {
		t.Fatalf("rename: %v", err)
	}
	sk, _ := database.GetSkill("s1")
	if sk.Name != "summary-helper" || sk.Description != "new" {
		t.Fatalf("skill = %+v", sk)
	}
	names, _ := database.ListAgentSkillNames("a1")
	slices.Sort(names)
	if !slices.Equal(names, []string{"csv", "summary-helper"}) {
		t.Fatalf("attachments = %v", names)
	}
}

// agent_skills is keyed by name with no foreign key, so a row naming the new name can
// already exist, left behind by a skill deleted long ago.
func TestRenameSkillToleratesADanglingAttachment(t *testing.T) {
	database, ws := draftTestDB(t)
	renameAgentFixture(t, database, ws, "a1", "One")
	if err := database.CreateSkill(&db.Skill{ID: "s1", WorkspaceID: ws, Name: "report-helper"}); err != nil {
		t.Fatalf("skill: %v", err)
	}
	if err := database.SetAgentSkills("a1", []string{"report-helper", "summary-helper"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := database.RenameSkill(ws, "s1", "report-helper", "summary-helper", "", nil); err != nil {
		t.Fatalf("rename: %v", err)
	}
	names, _ := database.ListAgentSkillNames("a1")
	if !slices.Equal(names, []string{"summary-helper"}) {
		t.Fatalf("attachments = %v", names)
	}
}

func TestRenameSkillLeavesOtherWorkspacesAttachments(t *testing.T) {
	database, ws := draftTestDB(t)
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws2", Name: "other"}); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	renameAgentFixture(t, database, ws, "a1", "One")
	renameAgentFixture(t, database, "ws2", "a2", "Two")
	if err := database.CreateSkill(&db.Skill{ID: "s1", WorkspaceID: ws, Name: "report-helper"}); err != nil {
		t.Fatalf("skill: %v", err)
	}
	_ = database.SetAgentSkills("a1", []string{"report-helper"})
	_ = database.SetAgentSkills("a2", []string{"report-helper"})
	if err := database.RenameSkill(ws, "s1", "report-helper", "summary-helper", "", nil); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if names, _ := database.ListAgentSkillNames("a2"); !slices.Equal(names, []string{"report-helper"}) {
		t.Fatalf("other workspace changed: %v", names)
	}
}

func TestRenameSkillCollisionRollsBackAttachments(t *testing.T) {
	database, ws := draftTestDB(t)
	renameAgentFixture(t, database, ws, "a1", "One")
	for id, name := range map[string]string{"s1": "report-helper", "s2": "summary-helper"} {
		if err := database.CreateSkill(&db.Skill{ID: id, WorkspaceID: ws, Name: name}); err != nil {
			t.Fatalf("skill: %v", err)
		}
	}
	_ = database.SetAgentSkills("a1", []string{"report-helper"})
	if err := database.RenameSkill(ws, "s1", "report-helper", "summary-helper", "", nil); !errors.Is(err, db.ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	if names, _ := database.ListAgentSkillNames("a1"); !slices.Equal(names, []string{"report-helper"}) {
		t.Fatalf("attachments changed: %v", names)
	}
}
