package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/auth"
	"github.com/rookery-ai/rookery/internal/db"
)

func renameAPIFixture(t *testing.T) (*Server, []*http.Cookie, string) {
	t.Helper()
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	return s, cookies, wsID
}

func renameTestAgent(t *testing.T, s *Server, wsID, id, name string) {
	t.Helper()
	if err := s.db.CreateAgent(&db.Agent{ID: id, WorkspaceID: wsID, Name: name}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
}

func renameTestSkill(t *testing.T, s *Server, wsID, id, name string) {
	t.Helper()
	if err := s.db.CreateSkill(&db.Skill{ID: id, WorkspaceID: wsID, Name: name}); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	dir := filepath.Join(s.vault.SkillsDir(wsID), name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o640); err != nil {
		t.Fatal(err)
	}
}

func unfinishedRun(t *testing.T, s *Server, wsID, agentID string) {
	t.Helper()
	if err := s.db.CreateAgentRun(&db.AgentRun{ID: "run-" + agentID, AgentID: agentID, WorkspaceID: wsID, Trigger: "cron", StartedAt: time.Now()}); err != nil {
		t.Fatalf("create run: %v", err)
	}
}

func wantCode(t *testing.T, got int, body string, want int, code string) {
	t.Helper()
	if got != want || !strings.Contains(body, `"`+code+`"`) {
		t.Fatalf("got %d %s, want %d %s", got, body, want, code)
	}
}

func TestAPIRenameAgent(t *testing.T) {
	s, cookies, wsID := renameAPIFixture(t)
	renameTestAgent(t, s, wsID, "a1", "Price watch")

	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/a1/name", map[string]any{"name": "  Price tracker "}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"Price tracker"`, `"still_mentions":[]`, `"warnings":[]`, `"env_removed":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("response %s lacks %s", body, want)
		}
	}
	if a, _ := s.db.GetAgent("a1"); a.Name != "Price tracker" {
		t.Fatalf("name = %q", a.Name)
	}
	var detail string
	if err := s.db.QueryRow(`SELECT detail FROM audit_logs WHERE action='rename_agent'`).Scan(&detail); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if detail != "Price watch → Price tracker" {
		t.Errorf("audit detail = %q", detail)
	}
}

func TestAPIRenameAgentRefusals(t *testing.T) {
	s, cookies, wsID := renameAPIFixture(t)
	renameTestAgent(t, s, wsID, "a1", "One")
	renameTestAgent(t, s, wsID, "a2", "Two")
	other, err := auth.CreateWorkspace(s.db, "ws2", "")
	if err != nil {
		t.Fatal(err)
	}
	renameTestAgent(t, s, other.ID, "foreign", "Foreign")

	put := func(id, name string) (int, string) {
		rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+id+"/name", map[string]any{"name": name}, cookies)
		return rec.Code, rec.Body.String()
	}

	code, body := put("foreign", "Mine now")
	wantCode(t, code, body, http.StatusNotFound, "not_found")
	if a, _ := s.db.GetAgent("foreign"); a.Name != "Foreign" {
		t.Fatalf("another workspace's agent renamed: %q", a.Name)
	}
	code, body = put("a1", "bad|name")
	wantCode(t, code, body, http.StatusBadRequest, "invalid_name")
	code, body = put("a1", "two")
	wantCode(t, code, body, http.StatusConflict, "name_taken")

	// An unchanged name is a success, even when a refusal would otherwise apply.
	unfinishedRun(t, s, wsID, "a1")
	if code, body = put("a1", "One"); code != http.StatusOK {
		t.Fatalf("unchanged: %d %s", code, body)
	}
	code, body = put("a1", "Uno")
	wantCode(t, code, body, http.StatusConflict, "agent_running")
}

// A caller that is mid-run holds [CALL: old] in its prompt.
func TestAPIRenameAgentRefusedWhileACallerRuns(t *testing.T) {
	s, cookies, wsID := renameAPIFixture(t)
	renameTestAgent(t, s, wsID, "a1", "Callee")
	renameTestAgent(t, s, wsID, "a2", "Caller")
	dir := s.vault.AgentDir(wsID, "a2")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("[CALL: Callee]\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	unfinishedRun(t, s, wsID, "a2")

	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/a1/name", map[string]any{"name": "Renamed"}, cookies)
	wantCode(t, rec.Code, rec.Body.String(), http.StatusConflict, "agent_running")
	if !strings.Contains(rec.Body.String(), "Caller") {
		t.Errorf("message should name the running agent: %s", rec.Body.String())
	}
}

func TestAPIRenameRefusedWhileAnAgentDesignIsOpen(t *testing.T) {
	s, cookies, wsID := renameAPIFixture(t)
	renameTestAgent(t, s, wsID, "a1", "One")
	renameTestSkill(t, s, wsID, "s1", "report-helper")
	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "Draft", agentdesigner.OriginWeb); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/a1/name", map[string]any{"name": "Uno"}, cookies)
	wantCode(t, rec.Code, rec.Body.String(), http.StatusConflict, "design_in_progress")
	rec = doJSON(t, s, http.MethodPut, "/api/v1/skills/s1/name", map[string]any{"name": "summary-helper"}, cookies)
	wantCode(t, rec.Code, rec.Body.String(), http.StatusConflict, "design_in_progress")
}

func TestAPIRenameSkill(t *testing.T) {
	s, cookies, wsID := renameAPIFixture(t)
	renameTestSkill(t, s, wsID, "s1", "report-helper")

	rec := doJSON(t, s, http.MethodPut, "/api/v1/skills/s1/name", map[string]any{"name": "Summary Helper"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"summary-helper"`) {
		t.Errorf("response = %s", rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(s.vault.SkillsDir(wsID), "summary-helper", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "name: summary-helper") {
		t.Fatalf("SKILL.md = %q, %v", data, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='rename_skill'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit rows = %d, %v", n, err)
	}
}

func TestAPIRenameSkillRefusals(t *testing.T) {
	s, cookies, wsID := renameAPIFixture(t)
	renameTestSkill(t, s, wsID, "s1", "report-helper")
	renameTestSkill(t, s, wsID, "s2", "other-skill")
	renameTestAgent(t, s, wsID, "a1", "User")

	put := func(id, name string) (int, string) {
		rec := doJSON(t, s, http.MethodPut, "/api/v1/skills/"+id+"/name", map[string]any{"name": name}, cookies)
		return rec.Code, rec.Body.String()
	}
	code, body := put("csv", "anything-else")
	wantCode(t, code, body, http.StatusNotFound, "not_found")
	code, body = put("s1", "../evil")
	wantCode(t, code, body, http.StatusBadRequest, "invalid_name")
	code, body = put("s1", "other-skill")
	wantCode(t, code, body, http.StatusConflict, "name_taken")
	code, body = put("s1", "csv")
	wantCode(t, code, body, http.StatusConflict, "name_taken")

	if err := s.db.UpsertSkillDraft(&db.SkillDraft{
		WorkspaceID: wsID, SkillName: "summary-helper", State: "designing",
		HistoryJSON: "[]", PendingScriptsJSON: "{}", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	code, body = put("s1", "summary-helper")
	wantCode(t, code, body, http.StatusConflict, "design_in_progress")
	if err := s.db.DeleteSkillDraft(wsID); err != nil {
		t.Fatal(err)
	}

	if err := s.db.SetAgentSkills("a1", []string{"report-helper"}); err != nil {
		t.Fatal(err)
	}
	unfinishedRun(t, s, wsID, "a1")
	code, body = put("s1", "summary-helper")
	wantCode(t, code, body, http.StatusConflict, "agent_running")
}
