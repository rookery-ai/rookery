package web

import (
	"net/http"
	"strings"
	"testing"
)

// WARNING for anyone adding a case to this file: TestKBAssistRejectsPathTraversal
// below clears the action and selection validation (a real action, a
// non-empty short selection) and is stopped ONLY by vault.Resolve rejecting
// "../../etc/passwd" — not by validateAssistRequest. A future test in this
// file that combines a valid action, a non-empty selection, AND a path that
// resolves to a real note would sail straight past every guard and reach the
// real coder — a paid, multi-second LLM call on every CI run (worse locally,
// where a `claude` binary on PATH with live credentials makes it a real API
// call, not just a slow one). Assert boundary/gating behaviour directly
// against validateAssistRequest instead, the way
// TestValidateAssistRequestSelectionCapBoundary below does, unless the test's
// entire point is the HTTP-level wiring.

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

// A selection exactly at the cap must be accepted, and one byte over must be
// rejected — the off-by-one boundary the reject-not-truncate contract lives
// on. This is asserted directly against validateAssistRequest rather than
// over HTTP: an HTTP round trip that clears validation reaches the real
// coder, and in a dev environment with a `claude` binary on PATH and live
// credentials available, that means a real (paid, multi-second) API call on
// every test run. validateAssistRequest is the exact gate that decides
// whether Generate is ever called, so testing it directly proves the
// boundary without paying that cost.
func TestValidateAssistRequestSelectionCapBoundary(t *testing.T) {
	base := apiKBAssistRequest{Action: "improve", Path: "notes/a.md"}

	atCap := base
	atCap.Selection = strings.Repeat("a", maxAssistSelectionBytes)
	if _, _, ok := validateAssistRequest(atCap); !ok {
		t.Fatal("a selection exactly at the cap was rejected")
	}

	overCap := base
	overCap.Selection = strings.Repeat("a", maxAssistSelectionBytes+1)
	if _, _, ok := validateAssistRequest(overCap); ok {
		t.Fatal("a selection one byte over the cap was accepted")
	}
}
