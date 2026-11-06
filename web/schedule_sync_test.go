package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/db"
)

const scheduleTestMD = "# Suggested schedule: 0 9 * * *\n# Skills: none\n\nEvery day at 9am, fetch the report.\n\nThe schedule (`0 9 * * *`) is local. Not this: 10 9 * * *.\n"

func scheduleTestAgent(t *testing.T, s *Server, wsID, md string) (*db.Agent, string) {
	t.Helper()
	a := seedAgent(t, s, wsID)
	path := agentdesigner.AgentDescPath(s.agentsDir(), wsID, a.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(md), 0o640); err != nil {
		t.Fatal(err)
	}
	return a, path
}

func readAgentMD(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// waitScheduleSync polls until the pass leaves "running".
func waitScheduleSync(t *testing.T, s *Server, agentID string) apiScheduleSync {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.scheduleSyncStatus(agentID); st != nil && st.State != "running" {
			return *st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("schedule pass did not finish")
	return apiScheduleSync{}
}

func TestSavingAScheduleUpdatesTheFile(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, path := scheduleTestAgent(t, s, wsID, scheduleTestMD)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "5 8 * * 1"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	got := readAgentMD(t, path)
	for _, want := range []string{"# Suggested schedule: 5 8 * * 1\n", "`5 8 * * 1`", "Not this: 10 9 * * *", "# Skills: none", "Every day at 9am"} {
		if !strings.Contains(got, want) {
			t.Errorf("file missing %q:\n%s", want, got)
		}
	}
	if s.scheduleSyncStatus(a.ID) != nil {
		t.Error("a server without the model pass reported one")
	}
}

func TestRemovingAScheduleUpdatesTheFile(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, path := scheduleTestAgent(t, s, wsID, scheduleTestMD)
	if rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "0 9 * * *"}, cookies); rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/agents/"+a.ID+"/schedule", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	got := readAgentMD(t, path)
	if !strings.HasPrefix(got, "# Suggested schedule: none\n") {
		t.Errorf("header not cleared:\n%s", got)
	}
	// A quoted cron has no truthful replacement when there is no schedule. That
	// sentence is the model's to reword, not this code's to mangle.
	if !strings.Contains(got, "`0 9 * * *`") {
		t.Errorf("quoted cron was rewritten on removal:\n%s", got)
	}
	if sched, _ := s.db.GetScheduleForAgent(a.ID); sched != nil {
		t.Error("row still present")
	}
}

func TestScheduleSaveSurvivesAMissingFile(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	_ = os.Remove(agentdesigner.AgentDescPath(s.agentsDir(), wsID, a.ID))

	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "5 8 * * 1"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(agentdesigner.AgentDescPath(s.agentsDir(), wsID, a.ID)); err == nil {
		t.Error("a file was created where none existed")
	}
}

func TestSchedulePassAppliesGuardedEditsAndDescription(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, path := scheduleTestAgent(t, s, wsID, scheduleTestMD)
	if err := s.db.UpdateAgentDescription(a.ID, "Every day at 9am, fetch the report."); err != nil {
		t.Fatal(err)
	}

	var gotPrompt string
	s.scheduleProse = func(_ context.Context, _, prompt string) (string, error) {
		gotPrompt = prompt
		return `{"edits":[
			{"find":"Every day at 9am","replace":"Every Monday at 8:05am"},
			{"find":"# Skills: none","replace":"# Skills: csv"},
			{"find":"not in the file","replace":"x"}
		],"review":["compare with yesterday's run"]}`, nil
	}

	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "5 8 * * 1"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	st := waitScheduleSync(t, s, a.ID)
	if st.State != "done" || len(st.Changes) != 1 || st.Refused != 2 || len(st.Review) != 1 {
		t.Fatalf("status = %+v", st)
	}
	if !strings.Contains(gotPrompt, "`5 8 * * 1`") {
		t.Errorf("prompt does not state the new schedule:\n%s", gotPrompt)
	}
	got := readAgentMD(t, path)
	if !strings.Contains(got, "Every Monday at 8:05am") || !strings.Contains(got, "# Skills: none") {
		t.Errorf("file:\n%s", got)
	}
	fresh, _ := s.db.GetAgent(a.ID)
	if !strings.Contains(fresh.Description, "Every Monday") {
		t.Errorf("description not refreshed: %q", fresh.Description)
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	var detail struct {
		ScheduleSync *apiScheduleSync `json:"schedule_sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.ScheduleSync == nil || detail.ScheduleSync.State != "done" {
		t.Errorf("detail schedule_sync = %+v (%v)", detail.ScheduleSync, err)
	}
}

func TestSchedulePassFailureKeepsTheSaveAndTheFile(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		err    error
	}{
		{"coder failed", "", errors.New("the coder is out of quota")},
		{"unreadable answer", "I rewrote your file for you!", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newAPITestServer(t)
			cookies := bootstrapAndLogin(t, s)
			cookies, wsID := createAndEnterWorkspace(t, s, cookies)
			a, path := scheduleTestAgent(t, s, wsID, scheduleTestMD)
			s.scheduleProse = func(context.Context, string, string) (string, error) { return c.answer, c.err }

			rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
				map[string]string{"cron_expr": "5 8 * * 1"}, cookies)
			if rec.Code != 200 {
				t.Fatalf("save: %d", rec.Code)
			}
			st := waitScheduleSync(t, s, a.ID)
			if st.State != "failed" || st.Error == "" {
				t.Errorf("status = %+v", st)
			}
			if got := readAgentMD(t, path); !strings.HasPrefix(got, "# Suggested schedule: 5 8 * * 1\n") || !strings.Contains(got, "Every day at 9am") {
				t.Errorf("file:\n%s", got)
			}
		})
	}
}

func TestANewerSaveSupersedesAnEarlierPass(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, path := scheduleTestAgent(t, s, wsID, scheduleTestMD)

	var calls atomic.Int32
	firstStarted := make(chan struct{})
	s.scheduleProse = func(ctx context.Context, _, _ string) (string, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-ctx.Done()
			return `{"edits":[{"find":"Every day at 9am","replace":"FROM THE STALE PASS"}]}`, nil
		}
		return `{"edits":[{"find":"Every day at 9am","replace":"Every Tuesday"}]}`, nil
	}

	save := func(expr string) {
		if rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
			map[string]string{"cron_expr": expr}, cookies); rec.Code != 200 {
			t.Fatalf("save: %d", rec.Code)
		}
	}
	save("1 1 * * *")
	<-firstStarted
	save("2 2 * * 2")
	st := waitScheduleSync(t, s, a.ID)
	if st.State != "done" {
		t.Fatalf("status = %+v", st)
	}
	got := readAgentMD(t, path)
	if strings.Contains(got, "STALE") || !strings.Contains(got, "Every Tuesday") || !strings.HasPrefix(got, "# Suggested schedule: 2 2 * * 2\n") {
		t.Errorf("file:\n%s", got)
	}
}

func TestScheduleChangeUpdatesThisAgentsEditDraftOnly(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, _ := scheduleTestAgent(t, s, wsID, scheduleTestMD)

	upsert := func(agentID string) {
		if err := s.db.UpsertAgentDraft(&db.AgentDraft{
			WorkspaceID: wsID, AgentID: agentID, AgentName: "Digest", IsEdit: true, State: "verifying",
			HistoryJSON: "[]", PendingAgentMD: scheduleTestMD, PendingToolsJSON: "{}",
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	upsert(a.ID)
	if rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "5 8 * * 1"}, cookies); rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	draft, err := s.db.GetAgentDraft(wsID)
	if err != nil || draft == nil {
		t.Fatalf("draft: %v", err)
	}
	if !strings.HasPrefix(draft.PendingAgentMD, "# Suggested schedule: 5 8 * * 1\n") {
		t.Errorf("draft would restore the old schedule:\n%s", draft.PendingAgentMD)
	}

	upsert("some-other-agent")
	if rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "6 8 * * 1"}, cookies); rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	draft, _ = s.db.GetAgentDraft(wsID)
	if draft.PendingAgentMD != scheduleTestMD {
		t.Errorf("another agent's draft was changed:\n%s", draft.PendingAgentMD)
	}
}

func TestAgentMDCardSave(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, path := scheduleTestAgent(t, s, wsID, scheduleTestMD)
	if rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "7 7 * * *"}, cookies); rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	put := func(content string) int {
		return doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/agent-md",
			map[string]string{"content": content}, cookies).Code
	}
	cronNow := func() string { return s.currentCron(a.ID) }

	// A changed header is a schedule change.
	if code := put("# Suggested schedule: 3 3 * * *\nBody `7 7 * * *`.\n"); code != 200 {
		t.Fatalf("change: %d", code)
	}
	if cronNow() != "3 3 * * *" {
		t.Errorf("row = %q", cronNow())
	}
	if got := readAgentMD(t, path); !strings.Contains(got, "Body `3 3 * * *`.") {
		t.Errorf("quoted cron not followed:\n%s", got)
	}

	// An invalid one is refused and changes nothing.
	if code := put("# Suggested schedule: not a cron\nBody\n"); code != http.StatusBadRequest {
		t.Errorf("invalid: %d", code)
	}
	if cronNow() != "3 3 * * *" {
		t.Errorf("row moved on an invalid header: %q", cronNow())
	}

	// A body-only edit must not move the schedule, and neither must a missing header.
	if code := put("# Suggested schedule: 3 3 * * *\nNew body.\n"); code != 200 {
		t.Fatalf("body: %d", code)
	}
	if code := put("No header at all.\n"); code != 200 {
		t.Fatalf("no header: %d", code)
	}
	if cronNow() != "3 3 * * *" {
		t.Errorf("row moved: %q", cronNow())
	}
	if got := readAgentMD(t, path); !strings.HasPrefix(got, "# Suggested schedule: 3 3 * * *\nNo header at all.") {
		t.Errorf("header not restored:\n%s", got)
	}

	// A file that drifted before this feature must not re-time the agent on a body
	// edit: the header the owner left alone follows the row, not the other way round.
	if err := os.WriteFile(path, []byte("# Suggested schedule: 9 9 * * *\nOld.\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if code := put("# Suggested schedule: 9 9 * * *\nEdited.\n"); code != 200 {
		t.Fatalf("drift: %d", code)
	}
	if cronNow() != "3 3 * * *" {
		t.Errorf("drifted header re-timed the agent: %q", cronNow())
	}
	if got := readAgentMD(t, path); !strings.HasPrefix(got, "# Suggested schedule: 3 3 * * *\nEdited.") {
		t.Errorf("drift not repaired:\n%s", got)
	}

	// An explicit none removes the schedule.
	if code := put("# Suggested schedule: none\nEdited.\n"); code != 200 {
		t.Fatalf("none: %d", code)
	}
	if cronNow() != "" {
		t.Errorf("row = %q", cronNow())
	}
}

func TestKBSaveOfAgentMDNeverMovesTheSchedule(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a, path := scheduleTestAgent(t, s, wsID, scheduleTestMD)
	if rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "7 7 * * *"}, cookies); rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}

	rec := doJSON(t, s, http.MethodPut, "/api/v1/kb/note", map[string]string{
		"path": "agents/" + a.ID + "/AGENT.md", "content": "# Suggested schedule: 0 9 * * *\nFrom a stale tab.\n",
	}, cookies)
	if rec.Code != 200 {
		t.Fatalf("kb save: %d %s", rec.Code, rec.Body.String())
	}
	if s.currentCron(a.ID) != "7 7 * * *" {
		t.Errorf("an autosave re-timed the agent: %q", s.currentCron(a.ID))
	}
	if got := readAgentMD(t, path); !strings.HasPrefix(got, "# Suggested schedule: 7 7 * * *\nFrom a stale tab.") {
		t.Errorf("file:\n%s", got)
	}
}

func TestAgentIDFromAgentMDPath(t *testing.T) {
	for rel, want := range map[string]string{
		"agents/abc/AGENT.md": "abc", "agents/abc/agent.md": "abc",
		"agents/abc/notes/AGENT.md": "", "notes/AGENT.md": "", "agents/abc/state.md": "", "agents/../AGENT.md": "",
	} {
		got, ok := agentIDFromAgentMDPath(rel)
		if got != want || ok != (want != "") {
			t.Errorf("agentIDFromAgentMDPath(%q) = %q,%v", rel, got, ok)
		}
	}
}
