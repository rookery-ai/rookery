package connectors

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractDottedPath(t *testing.T) {
	raw := []byte(`{"data":{"budgets":[{"id":"b1"},{"id":"b2"}],"other":9},"top":1}`)

	got := extract("$.data.budgets", raw)
	var out []map[string]string
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("extract returned %s, which is not the budgets array: %v", got, err)
	}
	if len(out) != 2 || out[0]["id"] != "b1" {
		t.Errorf("extract = %s, want the two-element budgets array", got)
	}
}

func TestExtractSingleKeyStillWorks(t *testing.T) {
	raw := []byte(`{"items":[1,2,3],"next":"x"}`)
	if got := string(extract("$.items", raw)); got != "[1,2,3]" {
		t.Errorf("extract = %s, want [1,2,3]", got)
	}
}

func TestExtractWholeBody(t *testing.T) {
	raw := []byte(`{"a":1}`)
	for _, p := range []string{"", "$", "  "} {
		if got := string(extract(p, raw)); got != `{"a":1}` {
			t.Errorf("extract(%q) = %s, want the raw body", p, got)
		}
	}
}

// A path that does not resolve must return the raw body rather than nothing — a
// connector returning an empty result reads to the model as "there is no data",
// which is a different and worse claim than "I could not narrow this".
func TestExtractMissingPathFallsBackToRaw(t *testing.T) {
	raw := []byte(`{"a":1}`)
	if got := string(extract("$.nope.deeper", raw)); got != `{"a":1}` {
		t.Errorf("extract = %s, want the raw body on a miss", got)
	}
}

// An array element that is not an object, or an object missing the field, is simply
// not matched — filtering must never panic on real-world payloads.
func TestApplyResponseFilter(t *testing.T) {
	raw := []byte(`[
		{"entity_id":"sensor.kitchen_temp","state":"21"},
		{"entity_id":"light.kitchen","state":"on"},
		{"entity_id":"sensor.hall_temp","state":"19"},
		"not-an-object",
		{"state":"no entity_id here"}
	]`)

	got := applyResponseFilter(raw, ResponseFilter{Field: "entity_id"}, "sensor.")
	var out []map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("filter returned %s: %v", got, err)
	}
	if len(out) != 2 {
		t.Fatalf("filter kept %d elements, want 2: %s", len(out), got)
	}
	for _, e := range out {
		if !strings.HasPrefix(e["entity_id"].(string), "sensor.") {
			t.Errorf("kept %v, which is not a sensor", e)
		}
	}
}

// An empty prefix means "no filter requested" — return everything rather than nothing.
func TestApplyResponseFilterEmptyPrefixIsNoOp(t *testing.T) {
	raw := []byte(`[{"entity_id":"light.kitchen"}]`)
	if got := string(applyResponseFilter(raw, ResponseFilter{Field: "entity_id"}, "")); got != string(raw) {
		t.Errorf("filter = %s, want the input unchanged", got)
	}
}

// A non-array body cannot be filtered; pass it through rather than erroring.
func TestApplyResponseFilterNonArrayPassesThrough(t *testing.T) {
	raw := []byte(`{"entity_id":"light.kitchen"}`)
	if got := string(applyResponseFilter(raw, ResponseFilter{Field: "entity_id"}, "sensor.")); got != string(raw) {
		t.Errorf("filter = %s, want the input unchanged", got)
	}
}

// The prefix is read out of the args map at the END of Execute, long after
// renderRequest has run over the same map. A MISSING key must therefore yield an
// empty prefix — which the no-op case above turns into "return everything".
//
// This is the sharp edge: if a missing key stringified to anything non-empty, the
// filter would match nothing and ha_list_states would return [], which reads to the
// model as "you have no sensors" rather than "the filter broke". Silent, plausible
// emptiness is the worst possible failure here, so pin the contract.
func TestMissingFilterArgYieldsEmptyPrefix(t *testing.T) {
	args := map[string]any{"something_else": "x"}
	if got := asString(args["entity_prefix"]); got != "" {
		t.Fatalf("asString(missing key) = %q, want \"\" — a non-empty value would make the filter drop everything", got)
	}
	raw := []byte(`[{"entity_id":"sensor.a"},{"entity_id":"light.b"}]`)
	got := applyResponseFilter(raw, ResponseFilter{Field: "entity_id", PrefixArg: "entity_prefix"}, asString(args["entity_prefix"]))
	if string(got) != string(raw) {
		t.Errorf("filter = %s, want everything unchanged when the arg is absent", got)
	}
}
