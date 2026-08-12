package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/db"
)

func seedAgent(t *testing.T, s *Server, wsID string) *db.Agent {
	t.Helper()
	a := &db.Agent{ID: uuid.New().String(), WorkspaceID: wsID, Name: "Digest",
		Description: "daily digest", Active: true, CreatedAt: time.Now()}
	if err := s.db.CreateAgent(a); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return a
}

func TestAPIAgentsListDetailSchedule(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"name":"Digest"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"agent_md"`) {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}

	// Schedule: bad cron → 400; good cron → 200.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "not-a-cron"}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "invalid_cron") {
		t.Fatalf("bad cron: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "*/10 * * * *"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("good cron: %d %s", rec.Code, rec.Body.String())
	}

	// Foreign agent → 404.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+uuid.New().String(), nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d", rec.Code)
	}
}

// TestAPIAgentDetailFreshAgentNeverNullArrays is a regression test for a
// user-reported SPA crash: `TypeError: Cannot read properties of null
// (reading 'length')` on clicking any agent. A freshly-created agent (no
// runs, no attached skills, no agent-dir log files, no manifest-declared
// secrets) leaves several agentDetailData slice fields (Logs,
// AttachedSkills, MissingSecrets) as Go nil slices — `var x []T` that's
// only ever appended to. json.Marshal renders a nil slice as `null`, and the
// frontend unconditionally does `.length`/`.map` on every array field in the
// AgentDetail DTO, so `null` throws. Assert EVERY array key is `[]`, not
// just the three fields the fix touches — the rest were already safe
// (`make([]T, 0, ...)`) and this guards them against regressing back to nil.
func TestAPIAgentDetailFreshAgentNeverNullArrays(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// None of the array fields may ever serialize as JSON null — that's what
	// throws `.length`/`.map` client-side. core_skills is asserted separately
	// below (non-empty: 13 bundled core skills always populate it).
	for _, field := range []string{
		"runs", "logs", "attached_skills", "core_skills", "all_skills",
		"workspace_connections", "attached_connection_ids", "missing_secrets",
	} {
		if contains(body, `"`+field+`":null`) {
			t.Fatalf("field %q serialized as null, not an array: %s", field, body)
		}
	}
	// A fresh agent has none of these — assert the concretely-empty ones
	// render as "[]", not just "not null".
	for _, field := range []string{
		"runs", "logs", "attached_skills", "all_skills",
		"workspace_connections", "attached_connection_ids", "missing_secrets",
	} {
		if !contains(body, `"`+field+`":[]`) {
			t.Fatalf("field %q missing or not an empty array: %s", field, body)
		}
	}
	if !contains(body, `"core_skills":[{`) {
		t.Fatalf("core_skills missing the bundled core-skill catalog: %s", body)
	}
}

// TestAPIRunAgentAlreadyRunning verifies apiRunAgent honors startManualRun's
// bool: when a run for this agent is already in flight, the endpoint reports
// 202 {"status":"already_running"} instead of silently discarding the signal
// (previously the return value was ignored and the client had no way to tell
// a genuine new run from a no-op double-click).
func TestAPIRunAgentAlreadyRunning(t *testing.T) {
	s, _ := newAPITestServer(t)
	// apiRunAgent 503s ("not_configured") before ever reaching startManualRun
	// when s.runner is nil, and newAPITestServer wires no runner. Give it a
	// harmless non-nil Runner so the already-running branch is reachable — its
	// Run() is never actually invoked here: startManualRun's in-flight check
	// (primed below) returns false before any run goroutine is spawned, so no
	// real coder subprocess is started.
	s.runner = agentrunner.New(s.db, []byte(strings.Repeat("ab", 32)), t.TempDir(), t.TempDir(), t.TempDir(), nil, t.TempDir())

	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	// Prime the in-flight tracker directly (rather than firing a real run) so
	// the very next POST hits startManualRun's "already running" branch.
	s.runsMu.Lock()
	s.runs[a.ID] = &agentRunState{progressCh: make(chan string, 1)}
	s.runsMu.Unlock()

	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/"+a.ID+"/run", nil, cookies)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for an already-running run: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"status":"already_running"`) {
		t.Fatalf("expected already_running status: %s", rec.Body.String())
	}
}

// TestAPIAgentDetailReadsStateMarkdown verifies the agent-detail endpoint
// shows state.md verbatim (it is a document now, not a JSON blob to
// re-marshal) and no longer looks at state.json at all.
func TestAPIAgentDetailReadsStateMarkdown(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	path := agentdesigner.StateFilePath(s.agentsDir(), wsID, a.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	if err := agentdesigner.WriteState(path, a.Name, map[string]any{"cursor": "abc123"}); err != nil {
		t.Fatalf("seed state.md: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, "cursor") || !contains(body, "abc123") {
		t.Fatalf("state.md content not surfaced verbatim: %s", body)
	}
	if !contains(body, "State — "+a.Name) {
		t.Fatalf("expected the state.md heading to survive verbatim: %s", body)
	}
}

// TestAPIAgentDetailMissingSecretsFromAgentMD is the branch's merge-gate
// regression test. agent.json is gone (Task 3's startup migration deletes it
// from every agent dir), so the "missing secrets" warning must be derived
// straight from AGENT.md's "# Required secrets:" block, not a cached
// manifest — before the fix, loadAgentDetail called
// agentdesigner.LoadManifest, which returns nil when agent.json is absent,
// silently emptying missing_secrets for every migrated agent.
func TestAPIAgentDetailMissingSecretsFromAgentMD(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	path := agentdesigner.AgentDescPath(s.agentsDir(), wsID, a.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	md := "# Agent\n\n# Required secrets:\n# - SENDGRID_KEY: for sending\n"
	if err := os.WriteFile(path, []byte(md), 0o640); err != nil {
		t.Fatalf("write AGENT.md: %v", err)
	}
	// Deliberately no agent.json written — this is the whole point of the test.

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	// Check the missing_secrets array specifically, not just any substring
	// match — AGENT.md's raw text is echoed back in the agent_md field too,
	// which would contain "SENDGRID_KEY" regardless of whether the
	// missing-secrets computation works.
	if !contains(rec.Body.String(), `"missing_secrets":["SENDGRID_KEY"]`) {
		t.Fatalf("missing_secrets should come from AGENT.md, not agent.json: %s", rec.Body.String())
	}
}

// TestAPIAgentDetailNoStateFileIsEmptyNotError verifies a missing state.md
// (a brand-new agent) yields an empty state string, not a 500.
func TestAPIAgentDetailNoStateFileIsEmptyNotError(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"state":""`) {
		t.Fatalf(`expected empty "state":"" for a missing state.md: %s`, rec.Body.String())
	}
}

// TestAPIDesignStateExposesPendingBuild verifies GET /api/v1/agents/design/state
// carries the reviewable build artifacts (pending_agent_md, pending_tools) so
// the frontend can show the user what the coder actually produced before they
// approve it. Critically, pending_tools must serialize as `{}` — never `null`
// — when no build has run yet, because the frontend maps over it; a nil Go map
// would marshal to JSON null and crash that panel (the same class of bug fixed
// in TestAPIAgentDetailFreshAgentNeverNullArrays above).
func TestAPIDesignStateExposesPendingBuild(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent"); err != nil {
		t.Fatalf("start design session: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/design/state", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("design state: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"pending_agent_md":""`) {
		t.Fatalf(`expected empty "pending_agent_md":"" before any build: %s`, body)
	}
	if !contains(body, `"pending_tools":{}`) {
		t.Fatalf(`expected "pending_tools":{} (not null) before any build: %s`, body)
	}
	if contains(body, `"pending_tools":null`) {
		t.Fatalf("pending_tools must never serialize as null: %s", body)
	}
}

// TestAPIDesignStateExposesPopulatedPendingBuild is the other half of the case
// above: once a build HAS produced artifacts, they must actually reach the
// wire. The empty-case test would still pass if Snapshot() dropped the fields
// entirely, so without this one the endpoint's whole purpose is unpinned.
func TestAPIDesignStateExposesPopulatedPendingBuild(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent"); err != nil {
		t.Fatalf("start design session: %v", err)
	}
	sess := s.designFlow.GetSession(wsID)
	if sess == nil {
		t.Fatal("no design session after Start")
	}
	sess.PendingAgentMD = "# Daily digest\n\nSummarises your mail.\n"
	sess.PendingTools = map[string]string{"tools/main.py": "print('hi')\n"}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/design/state", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("design state: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, "Daily digest") {
		t.Fatalf("pending_agent_md did not reach the wire: %s", body)
	}
	if !contains(body, "tools/main.py") || !contains(body, `print('hi')`) {
		t.Fatalf("pending_tools content did not reach the wire: %s", body)
	}
}

// TestAPIDesignStateHistoryCarriesTimestamps pins that design-conversation turns
// reach the browser with a time, so DesignerSurface's bubbles can render the same
// `Day HH:MM` footer the chats page shows. A turn with no CreatedAt (a draft
// written before turns were timestamped) must OMIT the field rather than emit a
// zero time, which would render a bubble stamped year 1 — `omitempty` does
// nothing for a struct, which is why the DTO field is a preformatted string.
func TestAPIDesignStateHistoryCarriesTimestamps(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent"); err != nil {
		t.Fatalf("start design session: %v", err)
	}
	sess := s.designFlow.GetSession(wsID)
	if sess == nil {
		t.Fatal("no design session after Start")
	}
	stamped := time.Date(2026, 7, 28, 9, 30, 0, 0, time.UTC)
	sess.History = []db.ChatMessage{
		{Role: "user", Content: "stamped turn", CreatedAt: stamped},
		{Role: "assistant", Content: "legacy turn"}, // zero CreatedAt
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/design/state", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("design state: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"created_at":"2026-07-28T09:30:00Z"`) {
		t.Fatalf("stamped history turn lost its created_at: %s", body)
	}
	if contains(body, "0001-01-01") {
		t.Fatalf("zero CreatedAt must be omitted, not serialized: %s", body)
	}
}

// TestDesignTurnResponseCarriesState pins the shape every non-terminal design
// turn returns. handleStartEditDesign shares it with handleDesignChat: with the
// edit pre-screen gone, DesignerSurface no longer remounts after the first edit
// message, so this response is the ONLY thing that can move the stepper into
// "designing" and reveal the Build button.
func TestDesignTurnResponseCarriesState(t *testing.T) {
	snap := agentdesigner.DesignSnapshot{
		Active:           true,
		State:            "designing",
		GenerationFailed: true,
		CanKeepAsIs:      true,
	}
	out := designTurnResponse("here's my read on it", snap)

	if out["response"] != "here's my read on it" {
		t.Fatalf("response = %v", out["response"])
	}
	if out["done"] != false {
		t.Fatalf("done = %v, want false", out["done"])
	}
	if out["state"] != "designing" {
		t.Fatalf("state = %v, want designing", out["state"])
	}
	if out["generation_failed"] != true || out["can_keep_as_is"] != true {
		t.Fatalf("failure flags dropped: %#v", out)
	}
}
