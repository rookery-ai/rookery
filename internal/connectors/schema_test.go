package connectors

import (
	"encoding/json"
	"testing"
)

func TestValidateArgs(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"to":{"type":"string"},"max":{"type":"integer"}},"required":["to"]}`)
	if err := validateArgs(schema, map[string]any{"to": "x@y.com", "max": 3}); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if err := validateArgs(schema, map[string]any{"max": 3}); err == nil {
		t.Fatal("missing required 'to' must fail")
	}
	if err := validateArgs(schema, map[string]any{"to": 5}); err == nil {
		t.Fatal("wrong type for 'to' must fail")
	}
}

func TestValidateArgsArray(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string"}}},"required":["ids"]}`)
	if err := validateArgs(schema, map[string]any{"ids": []any{"a", "b"}}); err != nil {
		t.Fatalf("valid string array rejected: %v", err)
	}
	if err := validateArgs(schema, map[string]any{"ids": "notarray"}); err == nil {
		t.Fatal("string given for array param must fail")
	}
	if err := validateArgs(schema, map[string]any{"ids": []any{"a", 3}}); err == nil {
		t.Fatal("wrong element type must fail")
	}
}
