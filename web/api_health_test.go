package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ilijad1/rookery/internal/config"
	"github.com/labstack/echo/v4"
)

// /healthz must answer without a session: the operator debugging a broken
// install is exactly the person who cannot authenticate.
func TestHealthzNeedsNoSession(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	s := &Server{cfg: &config.Config{
		Coder:   config.CoderConfig{Mode: config.ModeSlim},
		Sandbox: config.SandboxConfig{Enabled: true},
	}}
	if err := s.apiHealthz(c); err != nil {
		t.Fatalf("apiHealthz returned %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if body["coder_mode"] != "slim" {
		t.Errorf("coder_mode = %v, want slim", body["coder_mode"])
	}
	if _, ok := body["sandbox"]; !ok {
		t.Error("response is missing the sandbox block")
	}
	if _, ok := body["tools"]; !ok {
		t.Error("response is missing the tools block")
	}
}

// A bare Server (as tests construct) must not panic on the accessors.
func TestCoderModeDefaultsWithoutConfig(t *testing.T) {
	s := &Server{}
	if got := s.coderMode(); got != config.ModeFull {
		t.Errorf("coderMode() = %q, want %q", got, config.ModeFull)
	}
	if s.sandboxEnabled() {
		t.Error("sandboxEnabled() must be false with no config")
	}
}
