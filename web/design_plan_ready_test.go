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
