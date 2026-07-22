package vault

import (
	"strings"
	"testing"
)

func seedDesignerVault(t *testing.T) (*Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	v.WriteNote(ws, "notes/health.md", []byte("# Health\n\n## Appointments\n\nOrthodontist visit booked for Tuesday.\n"))
	v.WriteNote(ws, "files/expenses.csv", []byte("item,cost\nrent,900\n"))
	for i := 0; i < 80; i++ {
		v.WriteNote(ws, "notes/bulk/n"+string(rune('a'+i%26))+string(rune('a'+i/26))+".md", []byte("# Filler\n\nnothing relevant\n"))
	}
	return v, ws
}

func TestBuildKBContextRetrievesRelevantPassages(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "remind me about my dentist appointments")

	if !strings.Contains(got, "notes/health.md") {
		t.Errorf("expected the relevant note, got:\n%s", got)
	}
	if !strings.Contains(got, "Orthodontist") {
		t.Errorf("expected passage TEXT, not just a path, got:\n%s", got)
	}
}

func TestBuildKBContextFindsNonMarkdownByName(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "summarize my expenses spreadsheet each month")
	if !strings.Contains(got, "expenses.csv") {
		t.Errorf("a non-markdown file must be reachable, got:\n%s", got)
	}
}

func TestBuildKBContextIsBounded(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "filler")
	if len(got) > maxKBContextBytes {
		t.Errorf("context is %d bytes, over the %d cap", len(got), maxKBContextBytes)
	}
	// 80+ notes must NOT appear as 80 individual paths.
	if strings.Count(got, "notes/bulk/") > 8 {
		t.Errorf("the folder summary should replace an exhaustive path list, got:\n%s", got)
	}
}

func TestBuildKBContextStatesWhenNothingMatched(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "quantum chromodynamics lattice simulation")
	if !strings.Contains(strings.ToLower(got), "no existing notes matched") {
		t.Errorf("an empty retrieval must be stated explicitly so the designer asks instead of inventing a path, got:\n%s", got)
	}
}

func TestBuildKBContextEmptyVault(t *testing.T) {
	v := New(t.TempDir())
	v.EnsureScaffold("empty")
	if got := BuildKBContext(v, "empty", "anything"); got == "" {
		t.Error("an empty vault should still describe itself rather than returning nothing")
	}
}
