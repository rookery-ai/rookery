package skilldesigner

import (
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/vault"
)

// TestLoadKBManifest_NoVaultIsEmpty confirms loadKBManifest is safe to call
// before WithVault is attached — mirrors agentdesigner's version of this test.
func TestLoadKBManifest_NoVaultIsEmpty(t *testing.T) {
	f := NewSkillFlow(nil, nil)
	if got := f.loadKBManifest("any-workspace", "anything"); got != "" {
		t.Fatalf("expected empty string with no vault attached, got %q", got)
	}
}

// TestLoadKBManifest_UsesCurrentMessageOnFirstTurn proves the wiring works end
// to end against a real vault, and specifically that the CURRENT user message
// drives retrieval — not just History, which is empty on the session's first
// turn (Start/StartDesign).
func TestLoadKBManifest_UsesCurrentMessageOnFirstTurn(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := v.WriteNote(ws, "notes/health.md", []byte("# Health\n\n## Appointments\n\nOrthodontist visit booked for Tuesday.\n")); err != nil {
		t.Fatalf("write note: %v", err)
	}

	f := NewSkillFlow(nil, nil).WithVault(v)
	f.mu.Lock()
	f.sessions[ws] = &DesignSession{WorkspaceID: ws} // no History yet
	f.mu.Unlock()

	got := f.loadKBManifest(ws, "remind me about my dentist appointments")
	if !strings.Contains(got, "notes/health.md") {
		t.Fatalf("expected the relevant note surfaced from the current message alone, got:\n%s", got)
	}
	if !strings.Contains(got, "Orthodontist") {
		t.Fatalf("expected passage text, got:\n%s", got)
	}
}

// TestLoadKBManifest_IncludesRecentHistory proves prior user turns also feed
// retrieval, not just the current message.
func TestLoadKBManifest_IncludesRecentHistory(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws2"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := v.WriteNote(ws, "notes/expenses.md", []byte("# Expenses\n\nRent is 900 per month.\n")); err != nil {
		t.Fatalf("write note: %v", err)
	}

	f := NewSkillFlow(nil, nil).WithVault(v)
	f.mu.Lock()
	f.sessions[ws] = &DesignSession{
		WorkspaceID: ws,
		History: []db.ChatMessage{
			{Role: "user", Content: "track my monthly rent and expenses"},
			{Role: "assistant", Content: "sure, tell me more"},
		},
	}
	f.mu.Unlock()

	got := f.loadKBManifest(ws, "ok what else do you need")
	if !strings.Contains(got, "notes/expenses.md") {
		t.Fatalf("expected History to contribute to the retrieval query, got:\n%s", got)
	}
}
