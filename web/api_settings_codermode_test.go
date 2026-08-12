package web

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/config"
)

func slimServer() *Server {
	return &Server{cfg: &config.Config{Coder: config.CoderConfig{Mode: config.ModeSlim}}}
}

func fullServer() *Server {
	return &Server{cfg: &config.Config{Coder: config.CoderConfig{Mode: config.ModeFull}}}
}

// In slim mode the host probe is pointless work AND misleading output: a coder
// binary that happens to be installed still cannot be used.
func TestSlimModeSkipsCoderDetection(t *testing.T) {
	if got := slimServer().detectedCoders(); len(got) != 0 {
		t.Errorf("detectedCoders() returned %d entries in slim mode, want 0", len(got))
	}
}

// Must return an empty slice, never nil — the JSON field has to marshal as []
// rather than null, which the SPA would have to special-case.
func TestDetectedCodersNeverNil(t *testing.T) {
	if slimServer().detectedCoders() == nil {
		t.Error("detectedCoders() returned nil in slim mode, want an empty slice")
	}
	if fullServer().detectedCoders() == nil {
		t.Error("detectedCoders() returned nil in full mode, want an empty slice")
	}
}

func TestSlimModeRejectsLocalKind(t *testing.T) {
	s := slimServer()
	if err := s.rejectLocalInSlim("local"); err == nil {
		t.Fatal("slim mode accepted coder kind local")
	}
	if err := s.rejectLocalInSlim("api"); err != nil {
		t.Fatalf("slim mode rejected coder kind api: %v", err)
	}
}

func TestFullModeAcceptsLocalKind(t *testing.T) {
	if err := fullServer().rejectLocalInSlim("local"); err != nil {
		t.Fatalf("full mode rejected coder kind local: %v", err)
	}
}
