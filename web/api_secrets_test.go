package web

import (
	"net/http"
	"testing"
)

func TestAPISecretsWriteOnlyAndDelete(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/secrets",
		map[string]string{"name": "API_KEY", "value": "sekrit"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/secrets", nil, cookies)
	if !contains(rec.Body.String(), `"API_KEY"`) || contains(rec.Body.String(), "sekrit") {
		t.Fatalf("list must have name, never value: %s", rec.Body.String())
	}
	// Delete with wrong master password → 401.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/secrets/API_KEY",
		map[string]string{"master_password": "wrong"}, cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong pw: %d %s", rec.Code, rec.Body.String())
	}
	// Correct master password ("master-pw-1" from the helper) → 200.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/secrets/API_KEY",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
}
