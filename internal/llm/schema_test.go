package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schema is not JSON: %v\n%s", err, raw)
	}
	return m
}

// TestOpenObjectSchemasOpensAFreeFormObject is the set_state case that lost every
// agent's memory: a property typed "object" with no properties of its own. Under a
// provider that grammar-constrains tool calls, that schema admits exactly {} — the
// model cannot emit a key however hard it tries.
func TestOpenObjectSchemasOpensAFreeFormObject(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{"patch":{"type":"object","description":"keys to merge"}},"required":["patch"]}`)
	got := decodeSchema(t, OpenObjectSchemas(in))
	patch := got["properties"].(map[string]any)["patch"].(map[string]any)
	if patch["additionalProperties"] != true {
		t.Fatalf("free-form object was not opened: %v", patch)
	}
	if patch["description"] != "keys to merge" {
		t.Fatalf("description was lost: %v", patch)
	}
}

// The ROOT of a tool schema declares its parameters, so it must be left exactly as
// the tool wrote it: opening it would invite arguments the tool never reads.
func TestOpenObjectSchemasLeavesDeclaredObjectsAlone(t *testing.T) {
	for _, in := range []string{
		`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
		`{"type":"object","properties":{}}`,
		`{"type":"object","additionalProperties":false}`,
		`{"type":"object","additionalProperties":{"type":"string"}}`,
		`{"type":"object","patternProperties":{"^x":{"type":"string"}}}`,
	} {
		got := decodeSchema(t, OpenObjectSchemas(json.RawMessage(in)))
		want := decodeSchema(t, json.RawMessage(in))
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if string(gb) != string(wb) {
			t.Errorf("declared object changed:\n in: %s\nout: %s", wb, gb)
		}
	}
}

// Free-form objects nest — a connector body_arg inside an array, an MCP tool's
// option bag under anyOf — and each one is closed by the same decoder.
func TestOpenObjectSchemasRecurses(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{
		"rows":{"type":"array","items":{"type":"object"}},
		"opt":{"anyOf":[{"type":"object"},{"type":"null"}]},
		"where":{"type":"object","properties":{"inner":{"type":["object","null"]}}}
	}}`)
	got := decodeSchema(t, OpenObjectSchemas(in))
	props := got["properties"].(map[string]any)
	if props["rows"].(map[string]any)["items"].(map[string]any)["additionalProperties"] != true {
		t.Errorf("array items not opened: %v", props["rows"])
	}
	if props["opt"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)["additionalProperties"] != true {
		t.Errorf("anyOf branch not opened: %v", props["opt"])
	}
	inner := props["where"].(map[string]any)["properties"].(map[string]any)["inner"].(map[string]any)
	if inner["additionalProperties"] != true {
		t.Errorf("object in a type list not opened: %v", inner)
	}
	if _, ok := props["where"].(map[string]any)["additionalProperties"]; ok {
		t.Errorf("an object WITH properties was opened: %v", props["where"])
	}
}

// Numbers must survive byte-for-byte: a schema's enum or default may hold an
// integer above 2^53, and a float64 round trip would silently change it.
func TestOpenObjectSchemasPreservesNumbersAndBadInput(t *testing.T) {
	in := json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","enum":[1234567890123456789]}}}`)
	if out := string(OpenObjectSchemas(in)); !strings.Contains(out, "1234567890123456789") {
		t.Fatalf("large integer changed: %s", out)
	}
	for _, bad := range []string{``, `not json`, `[1,2]`} {
		if got := string(OpenObjectSchemas(json.RawMessage(bad))); got != bad {
			t.Errorf("unparseable schema %q was rewritten to %q", bad, got)
		}
	}
}

// Both wire formats must carry the opened schema — the bug was reproduced on the
// OpenAI-compatible path, and the Anthropic path builds its payload separately.
func TestBothProvidersSendOpenedSchemas(t *testing.T) {
	tools := []Tool{{Name: "set_state", Description: "d",
		Parameters: json.RawMessage(`{"type":"object","properties":{"patch":{"type":"object"}},"required":["patch"]}`)}}
	req := Request{Messages: []Message{{Role: "user", Content: "hi"}}, Tools: tools}
	for name, body := range map[string][]byte{
		"openai":    (&openaiProvider{}).buildBody("m", req),
		"anthropic": (&anthropicProvider{}).buildBody("m", req),
	} {
		if !strings.Contains(string(body), `"additionalProperties":true`) {
			t.Errorf("%s payload does not open the free-form object: %s", name, body)
		}
	}
}
