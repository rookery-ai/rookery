package web

import (
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/config"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/skillstore"
	"github.com/labstack/echo/v4"
)

// newAPITestServerWithSkills is like newAPITestServer but wires a real
// skillstore.Store (cheap to construct — no external deps) so the store-backed
// save/create/delete paths can be exercised, instead of only the nil-store
// 503/degraded paths newAPITestServer's harness produces.
func newAPITestServerWithSkills(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	t.Setenv("ROOKERY_SYSTEM_KEY", strings.Repeat("ab", 32))
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"), "../migrations")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cfg := &config.Config{}
	cfg.Data.Dir = dir
	store := skillstore.New(database, filepath.Join(dir, "skills"))
	s, err := NewServer(cfg, database, nil, nil, nil, filepath.Join(dir, "homes"), store, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s, database
}

const testSkillMD = `---
name: my-test-skill
description: A test skill used for API testing.
---

# My Test Skill

Body content.
`

func TestAPISkillsList(t *testing.T) {
	s, _ := newAPITestServer(t) // nil skillStore, nil skillFlow — list must degrade gracefully
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/skills", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"skills":[]`) {
		t.Fatalf("expected empty user skills, got: %s", body)
	}
	// A known bundled core skill must appear even with no store/flow configured.
	if !contains(body, `"slug":"csv"`) {
		t.Fatalf("expected core skill 'csv' in response, got: %s", body)
	}
	if !contains(body, `"draft":null`) {
		t.Fatalf("expected draft:null with nil skillFlow, got: %s", body)
	}
}

func TestAPISkillsCoreDetail(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/skills/core/csv", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("core detail: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"slug":"csv"`) || !contains(rec.Body.String(), `"content"`) {
		t.Fatalf("expected slug+content, got: %s", rec.Body.String())
	}

	// Unknown slug -> 404 not_found, and must NOT be captured by the /:id route.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/skills/core/not-a-real-skill", nil, cookies)
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not_found") {
		t.Fatalf("unknown core slug: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPISkillsDetailForeign404(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/skills/"+uuid.New().String(), nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign skill: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPISkillsCreateNilStore documents the nil-skillstore harness behavior:
// POST /api/v1/skills must return 503 not_configured rather than panicking.
func TestAPISkillsCreateNilStore(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/skills", map[string]string{"content": testSkillMD}, cookies)
	if rec.Code != http.StatusServiceUnavailable || !contains(rec.Body.String(), "not_configured") {
		t.Fatalf("expected 503 not_configured with nil store, got: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPISkillsCreateSaveDeleteWithStore exercises the store-backed paths
// (create via JSON, get detail, save, delete + dangling-attachment cleanup)
// using a real skillstore.Store.
func TestAPISkillsCreateSaveDeleteWithStore(t *testing.T) {
	s, _ := newAPITestServerWithSkills(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Create via JSON {content}.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/skills", map[string]string{"content": testSkillMD}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"name":"my-test-skill"`) {
		t.Fatalf("create response missing name: %s", rec.Body.String())
	}

	skills, err := s.db.ListSkills(wsID)
	if err != nil || len(skills) != 1 {
		t.Fatalf("expected 1 skill in db, got %v err=%v", skills, err)
	}
	sk := skills[0]

	// List now shows it.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/skills", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"name":"my-test-skill"`) {
		t.Fatalf("list after create: %d %s", rec.Code, rec.Body.String())
	}

	// Detail.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/skills/"+sk.ID, nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), "Body content.") {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}

	// Save (update content/description).
	updated := strings.Replace(testSkillMD, "A test skill used for API testing.", "Updated description.", 1)
	rec = doJSON(t, s, http.MethodPut, "/api/v1/skills/"+sk.ID, map[string]string{"content": updated}, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), "Updated description.") {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	// Seed a dangling agent_skills attachment, then delete the skill and verify
	// the attachment is cleaned up (db.DeleteAgentSkillsByName parity).
	agent := &db.Agent{ID: uuid.New().String(), WorkspaceID: wsID, Name: "A1", Description: "d"}
	if err := s.db.CreateAgent(agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := s.db.SetAgentSkills(agent.ID, []string{"my-test-skill"}); err != nil {
		t.Fatalf("set agent skills: %v", err)
	}
	names, _ := s.db.ListAgentSkillNames(agent.ID)
	if len(names) != 1 {
		t.Fatalf("expected 1 attached skill before delete, got %v", names)
	}

	rec = doJSON(t, s, http.MethodDelete, "/api/v1/skills/"+sk.ID, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	names, _ = s.db.ListAgentSkillNames(agent.ID)
	if len(names) != 0 {
		t.Fatalf("expected dangling attachment cleaned up, got %v", names)
	}
	if _, err := s.db.GetSkill(sk.ID); err == nil {
		t.Fatalf("expected skill to be gone after delete")
	}
}

// TestAPISkillsCreateReservedName verifies the core-skill-name guard added in
// the SP4 final review (mirroring skilldesigner.SkillSaver.SaveSkill's check):
// a paste/import whose frontmatter name collides with a bundled core skill
// (e.g. "pdf") must be rejected with 400 reserved_name, not silently shadow
// the core skill. A non-reserved name in the same store must still succeed.
func TestAPISkillsCreateReservedName(t *testing.T) {
	s, _ := newAPITestServerWithSkills(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	reservedMD := `---
name: pdf
description: A skill that collides with the bundled "pdf" core skill.
---

# PDF

Body content.
`
	rec := doJSON(t, s, http.MethodPost, "/api/v1/skills", map[string]string{"content": reservedMD}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "reserved_name") {
		t.Fatalf("expected 400 reserved_name, got: %d %s", rec.Code, rec.Body.String())
	}
	skills, err := s.db.ListSkills(wsID)
	if err != nil || len(skills) != 0 {
		t.Fatalf("expected no skill persisted for reserved name, got %v err=%v", skills, err)
	}

	// A non-reserved name in the same store still succeeds (guard isn't
	// over-broad).
	rec = doJSON(t, s, http.MethodPost, "/api/v1/skills", map[string]string{"content": testSkillMD}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected non-reserved create to succeed, got: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPISkillsCreateReservedNameNilStore documents that the reserved-name
// check runs before the nil-store 503 short-circuit is even reachable in
// practice — with a nil store, apiCreateSkill's own not_configured guard
// fires first (name parsing never runs), so this pins that ordering rather
// than the reserved-name message itself.
func TestAPISkillsCreateReservedNameNilStore(t *testing.T) {
	s, _ := newAPITestServer(t) // nil skillStore
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/skills", map[string]string{"content": `---
name: pdf
description: collides with core.
---
Body.
`}, cookies)
	if rec.Code != http.StatusServiceUnavailable || !contains(rec.Body.String(), "not_configured") {
		t.Fatalf("expected 503 not_configured (store check precedes name parsing), got: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPISkillsCreateMultipart exercises the multipart/form-data "content"
// field path (the same field the dashboard's paste-import form posts).
func TestAPISkillsCreateMultipart(t *testing.T) {
	s, _ := newAPITestServerWithSkills(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("content", testSkillMD); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills", strings.NewReader(buf.String()))
	req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("multipart create: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"name":"my-test-skill"`) {
		t.Fatalf("multipart create response: %s", rec.Body.String())
	}
}
