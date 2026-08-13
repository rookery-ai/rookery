package agentdesigner

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// The failure path used to write a DIFFERENT message to History than the one it
// returned: chat got the real explanation, the web got a generic "it did not
// succeed". Both must now see the real one.
func TestRecordGenerationFailureStoresTheUserFacingMessage(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	flow.recordGenerationFailure(workspaceID,
		"I couldn't finish building this: the weather API rejected the key.",
		"the API key was rejected; ask the user to re-check it",
		false)

	hist := flow.GetSession(workspaceID).History
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2 (user-facing + steering note)", len(hist))
	}
	if hist[0].Role != "assistant" || !strings.Contains(hist[0].Content, "weather API rejected") {
		t.Errorf("turn 0 = %+v, want the user-facing message as assistant", hist[0])
	}
	if hist[1].Role != roleNote || !strings.Contains(hist[1].Content, "re-check it") {
		t.Errorf("turn 1 = %+v, want the steering note under the note role", hist[1])
	}
}

// An empty user-facing message must not produce a blank bubble.
func TestRecordGenerationFailureSkipsAnEmptyUserMessage(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	flow.recordGenerationFailure(workspaceID, "   ", "steering only", false)

	hist := flow.GetSession(workspaceID).History
	if len(hist) != 1 || hist[0].Role != roleNote {
		t.Errorf("history = %+v, want just the note turn", hist)
	}
}

// The coder must still receive the steering note — that is what stops the retry
// being context-blind. Mapping note->assistant and coalescing adjacent turns
// keeps the prompt shape identical to what it saw before the note role existed.
func TestNoteRoleReachesTheCoderAsAssistant(t *testing.T) {
	got := dbMessagesToPrompt([]db.ChatMessage{
		{Role: "user", Content: "approve"},
		{Role: "assistant", Content: "I couldn't finish this."},
		{Role: roleNote, Content: "drop the script and reason directly"},
	})

	if len(got) != 2 {
		t.Fatalf("prompt turns = %d, want 2 (the note coalesced into the assistant turn)", len(got))
	}
	if got[1].Role != "assistant" {
		t.Errorf("role = %q, want assistant", got[1].Role)
	}
	if !strings.Contains(got[1].Content, "couldn't finish") ||
		!strings.Contains(got[1].Content, "drop the script") {
		t.Errorf("content = %q, want BOTH the message and the steering note", got[1].Content)
	}
}

// A lone note with no preceding assistant turn still reaches the coder.
func TestLoneNoteBecomesAnAssistantTurn(t *testing.T) {
	got := dbMessagesToPrompt([]db.ChatMessage{
		{Role: "user", Content: "approve"},
		{Role: roleNote, Content: "steering"},
	})
	if len(got) != 2 || got[1].Role != "assistant" || got[1].Content != "steering" {
		t.Errorf("got %+v, want the note as its own assistant turn", got)
	}
}

// Coalescing must not merge across a user turn — that would reorder the
// conversation the coder reads.
func TestCoalescingStopsAtAUserTurn(t *testing.T) {
	got := dbMessagesToPrompt([]db.ChatMessage{
		{Role: "assistant", Content: "first"},
		{Role: "user", Content: "middle"},
		{Role: "assistant", Content: "second"},
	})
	if len(got) != 3 {
		t.Fatalf("turns = %d, want 3 — a user turn separates the assistants", len(got))
	}
	if got[0].Content != "first" || got[2].Content != "second" {
		t.Errorf("got %+v, want the two assistant turns kept apart", got)
	}
}
