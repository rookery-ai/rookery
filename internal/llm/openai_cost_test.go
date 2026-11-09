package llm

import (
	"encoding/json"
	"math"
	"testing"
)

func TestXAICostTicks(t *testing.T) {
	var r openAIResponse
	if err := json.Unmarshal([]byte(`{"usage":{"total_tokens":10,"cost_in_usd_ticks":25000000}}`), &r); err != nil {
		t.Fatal(err)
	}
	u := r.usage()
	if !u.CostReported || math.Abs(u.Cost-0.0025) > 1e-12 {
		t.Fatalf("got cost %v reported %v, want 0.0025 reported", u.Cost, u.CostReported)
	}
	// An explicit cost wins over ticks.
	if err := json.Unmarshal([]byte(`{"usage":{"cost":0.5,"cost_in_usd_ticks":25000000}}`), &r); err != nil {
		t.Fatal(err)
	}
	if u := r.usage(); u.Cost != 0.5 {
		t.Fatalf("cost = %v, want 0.5", u.Cost)
	}
}
