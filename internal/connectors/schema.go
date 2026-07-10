package connectors

import (
	"encoding/json"
	"fmt"
)

type propSchema struct {
	Type string `json:"type"`
}

type objSchema struct {
	Properties map[string]propSchema `json:"properties"`
	Required   []string              `json:"required"`
}

// validateArgs checks that args satisfy a flat object JSON schema: all `required`
// keys present (non-nil) and each supplied top-level property matches its declared
// type. The action manifests only use flat object schemas, so no external JSON-schema
// library is needed.
func validateArgs(schema json.RawMessage, args map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	var s objSchema
	if err := json.Unmarshal(schema, &s); err != nil {
		return fmt.Errorf("bad action schema: %w", err)
	}
	for _, req := range s.Required {
		if v, ok := args[req]; !ok || v == nil {
			return fmt.Errorf("missing required argument %q", req)
		}
	}
	for name, val := range args {
		p, ok := s.Properties[name]
		if !ok || val == nil {
			continue
		}
		if !typeOK(p.Type, val) {
			return fmt.Errorf("argument %q must be %s", name, p.Type)
		}
	}
	return nil
}

func typeOK(t string, v any) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer", "number":
		switch v.(type) {
		case float64, int, int64, json.Number:
			return true
		}
		return false
	default:
		return true
	}
}
