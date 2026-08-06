package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestKBAssistRejectsUnknownAction(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "translate", "path": "notes/a.md", "selection": "x"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Errorf("body = %s, want invalid_request", rec.Body.String())
	}
}

func TestKBAssistRejectsEmptySelection(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "notes/a.md", "selection": "   "}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestKBAssistRejectsOversizeSelection(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// cap+1 is rejected, not truncated: a silently shortened passage would
	// come back as a rewrite of something the user did not select.
	big := strings.Repeat("a", maxAssistSelectionBytes+1)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "notes/a.md", "selection": big}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestKBAssistRejectsPathTraversal(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "../../etc/passwd", "selection": "x"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_path") {
		t.Errorf("body = %s, want invalid_path", rec.Body.String())
	}
}

func TestKBAssistAcceptsSelectionAtTheCap(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	atCap := strings.Repeat("a", maxAssistSelectionBytes)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "notes/a.md", "selection": atCap}, cookies)
	// The coder is not configured in tests, so this must NOT be a 400 — it
	// fails later, at the coder call. Anything in the 4xx range other than a
	// coder-unavailable 503 means the cap is off by one.
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("a selection exactly at the cap was rejected: %s", rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
}
