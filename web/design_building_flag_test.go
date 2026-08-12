package web

import (
	"encoding/json"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
)

// Generation is detached on BOTH surfaces now, so the turn that starts a build
// returns a placeholder instead of the build's result. The SPA only copes with
// that because it attaches the SSE stream — and refetches /state when the stream
// closes — on `building: true` (DesignerSurface.tsx; pinned by
// designer.test.tsx's "a building:true build refetches /state on SSE done").
//
// Drop the flag and the web UI renders the placeholder and waits forever, with
// every test still green: the failure is entirely in the browser. Hence this
// assertion on the raw response shape.
func TestDesignTurnResponseCarriesTheBuildingFlag(t *testing.T) {
	body := designTurnResponse("🤖 Building your agent…", agentdesigner.DesignSnapshot{
		State:      "designing",
		Generating: true,
	})

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	building, ok := got["building"]
	if !ok {
		t.Fatal("response has no `building` key — the SPA would never attach the SSE stream")
	}
	if building != true {
		t.Errorf("building = %v, want true while a build is running", building)
	}
}

// The flag must track the snapshot rather than being hardcoded, or an ordinary
// design turn would put the SPA into build-waiting mode with no build to wait for.
func TestDesignTurnResponseReportsNotBuildingWhenIdle(t *testing.T) {
	body := designTurnResponse("What should it monitor?", agentdesigner.DesignSnapshot{
		State:      "designing",
		Generating: false,
	})
	if body["building"] != false {
		t.Errorf("building = %v, want false on an ordinary turn", body["building"])
	}
}
