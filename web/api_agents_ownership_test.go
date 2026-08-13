package web

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/db"
)

// A note turn is the coder's steering context and must never reach the browser.
// Rendering it is what showed a generic "it did not succeed" in the web UI while
// the real explanation went to chat alone.
func TestDesignHistoryDTODropsNoteTurns(t *testing.T) {
	got := designHistoryDTO([]db.ChatMessage{
		{Role: "user", Content: "approve"},
		{Role: "assistant", Content: "here is the real reason"},
		{Role: agentdesigner.RoleNote, Content: "internal steering"},
	})

	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	for _, e := range got {
		if strings.Contains(e.Content, "internal steering") {
			t.Errorf("note turn leaked to the browser: %+v", e)
		}
	}
	if got[1].Content != "here is the real reason" {
		t.Errorf("entry 1 = %q, want the user-facing message preserved", got[1].Content)
	}
}
