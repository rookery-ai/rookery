package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/db"
)

// plan_ready is what decides whether the browser offers "Approve & build" at
// all — fsmState cannot, because a clarifying question and a finished proposal
// are both StateDesigning.
//
// Asserted on the raw JSON, not on the map, for the same reason
// flattenRequires' null-slice bug had to be: the SPA coerces a MISSING field to
// false, so a key that silently stops being emitted disables the button with
// every Go test still green. This codebase has already shipped one bug of
// exactly that shape (a hand-rolled mid-build body omitting `state` and
// `generation_failed`).
func TestDesignTurnResponseCarriesPlanReady(t *testing.T) {
	for _, tt := range []struct {
		name string
		snap agentdesigner.DesignSnapshot
		want bool
	}{
		{
			name: "settled plan",
			snap: agentdesigner.DesignSnapshot{State: "designing", PlanReady: true, PendingSpec: "Tier: 1"},
			want: true,
		},
		{
			name: "still asking questions",
			snap: agentdesigner.DesignSnapshot{State: "designing"},
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(designTurnResponse("…", tt.snap))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if _, ok := got["plan_ready"]; !ok {
				t.Fatal("response has no `plan_ready` key — the SPA coerces that to false and the build button never appears")
			}
			if got["plan_ready"] != tt.want {
				t.Errorf("plan_ready = %v, want %v", got["plan_ready"], tt.want)
			}
			if _, ok := got["pending_spec"]; !ok {
				t.Error("response has no `pending_spec` key — the Spec view has nothing to show before a build")
			}
		})
	}
}

// The transcript the browser replays must agree with the live turn about what
// the user was shown. History deliberately stores the raw designer text so the
// code generator's brief survives; this is the edge that hides it again.
func TestDesignHistoryDTOHidesTheTechnicalSpec(t *testing.T) {
	hist := []db.ChatMessage{
		{Role: "user", Content: "watch this page", CreatedAt: time.Now()},
		{
			Role: "assistant",
			Content: "Here's the plan.\n\n- Check each morning\n\n" +
				"[TECHNICAL SPEC]\nTier: 1\nSchedule: 0 8 * * *\n[/TECHNICAL SPEC]",
			CreatedAt: time.Now(),
		},
		{Role: agentdesigner.RoleNote, Content: "steering note for the coder"},
	}

	out := designHistoryDTO(hist)
	if len(out) != 2 {
		t.Fatalf("dto = %d entries, want 2 (the note turn is coder-facing)", len(out))
	}
	if strings.Contains(out[1].Content, "TECHNICAL SPEC") || strings.Contains(out[1].Content, "Tier: 1") {
		t.Fatalf("the replayed transcript leaked the machine-facing spec block:\n%s", out[1].Content)
	}
	if !strings.Contains(out[1].Content, "Check each morning") {
		t.Fatalf("stripping ate the plan prose:\n%s", out[1].Content)
	}
}

// A stored turn that was ENTIRELY the machine-facing block strips to "" and
// would replay as a blank bubble on every reload — worse than the live case,
// which at least only happened once. The replay path runs the same helper.
func TestDesignHistoryDTONeverReplaysABlankAssistantTurn(t *testing.T) {
	hist := []db.ChatMessage{
		{Role: "user", Content: "just make it silent", CreatedAt: time.Now()},
		{Role: "assistant", Content: "[TECHNICAL SPEC]\nTier: 1\n[/TECHNICAL SPEC]", CreatedAt: time.Now()},
	}
	out := designHistoryDTO(hist)
	if len(out) != 2 {
		t.Fatalf("dto = %d entries, want 2", len(out))
	}
	if strings.TrimSpace(out[1].Content) == "" {
		t.Fatal("replayed a blank assistant turn")
	}
	if strings.Contains(out[1].Content, "TECHNICAL SPEC") {
		t.Fatalf("the machine-facing block leaked into the replay: %q", out[1].Content)
	}
}

// The helper substitutes a recovery sentence when a turn empties out. Putting
// those words in the USER's mouth would be worse than the blank it replaces, so
// their own messages are echoed verbatim.
func TestDesignHistoryDTOLeavesUserTurnsVerbatim(t *testing.T) {
	hist := []db.ChatMessage{
		{Role: "user", Content: "Important is anything work related", CreatedAt: time.Now()},
	}
	out := designHistoryDTO(hist)
	if out[0].Content != "Important is anything work related" {
		t.Errorf("user turn was rewritten: %q", out[0].Content)
	}
}
