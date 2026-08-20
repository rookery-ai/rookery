package web

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// The read path is what repairs conversations that leaked before this shipped —
// 30 of one install's 192 assistant rows carried a marker, and those rows are
// still on disk. Cleaning on display fixes them with no migration.
func TestToAPIChatMessageCleansStoredAssistantTurns(t *testing.T) {
	got := toAPIChatMessage(db.ChatMessage{
		Role:    "assistant",
		Content: "      [CHAT]\n7 + 10 = 17\n[/CHAT]",
	})
	if got.Content != "7 + 10 = 17" {
		t.Errorf("stored assistant turn not cleaned: %q", got.Content)
	}
}

// A reply that was nothing but [SILENT] must not come back as the raw marker —
// that is the leak — nor as an empty bubble.
func TestToAPIChatMessageReplacesAMarkerOnlyTurn(t *testing.T) {
	got := toAPIChatMessage(db.ChatMessage{Role: "assistant", Content: "[SILENT]"})
	if strings.Contains(got.Content, "[SILENT]") {
		t.Errorf("the marker survived to the wire: %q", got.Content)
	}
	if strings.TrimSpace(got.Content) == "" {
		t.Error("an empty bubble reads as being ignored")
	}
}

// The owner's own words are never rewritten. Someone who pasted a marker to ask
// what it was must still see their message quoted back intact.
func TestToAPIChatMessageLeavesUserTurnsAlone(t *testing.T) {
	in := "why do I keep seeing [CHAT] in my messages?"
	got := toAPIChatMessage(db.ChatMessage{Role: "user", Content: in})
	if got.Content != in {
		t.Errorf("a user turn was rewritten\n got %q\nwant %q", got.Content, in)
	}
}
