package llm

import (
	"bytes"
	"encoding/json"
)

// OpenObjectSchemas marks every free-form object in a tool's parameter schema as
// explicitly open: an object that declares neither properties, patternProperties
// nor additionalProperties gets "additionalProperties": true.
//
// JSON Schema already defaults additionalProperties to true, so this changes
// nothing about what the schema means. It changes what a provider DOES with it.
// Several hosts behind OpenRouter grammar-constrain tool-call arguments, and their
// schema-to-grammar compilers treat an object with no declared properties as
// closed — the grammar then admits exactly {}. That is how set_state lost every
// agent's memory on DeepSeek Flash: the model meant {"last_price": 159}, the
// constrained decoder could only emit {"patch": {}}, and the call "succeeded"
// 149 times in one run. Replaying the captured request with only this one field
// added produced the intended keys on every attempt.
//
// It is applied here, where every tool schema leaves the process, rather than in
// the one schema that was observed failing, because the same shape is declared by
// kb_table_query's `where`, by connector actions that take a free-form body, and by
// whatever an MCP server advertises — none of which this package can see.
//
// A schema that does not parse is returned unchanged: an unreadable schema is the
// provider's to reject, not ours to silently rewrite.
func OpenObjectSchemas(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // an enum or default above 2^53 must survive byte-for-byte
	var root map[string]any
	if err := dec.Decode(&root); err != nil || root == nil {
		return raw
	}
	if !openObjects(root) {
		return raw
	}
	out, err := json.Marshal(root)
	if err != nil {
		return raw
	}
	return out
}

// openObjects walks one schema node and reports whether it changed anything, so an
// untouched schema keeps its original bytes (and key order) on the wire.
func openObjects(node map[string]any) bool {
	changed := false
	if isObjectType(node["type"]) {
		_, hasProps := node["properties"]
		_, hasPattern := node["patternProperties"]
		_, hasAdditional := node["additionalProperties"]
		if !hasProps && !hasPattern && !hasAdditional {
			node["additionalProperties"] = true
			changed = true
		}
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions"} {
		if m, ok := node[key].(map[string]any); ok {
			for _, v := range m {
				if child, ok := v.(map[string]any); ok && openObjects(child) {
					changed = true
				}
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "not"} {
		if child, ok := node[key].(map[string]any); ok && openObjects(child) {
			changed = true
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if list, ok := node[key].([]any); ok {
			for _, v := range list {
				if child, ok := v.(map[string]any); ok && openObjects(child) {
					changed = true
				}
			}
		}
	}
	return changed
}

// isObjectType accepts both spellings JSON Schema allows: "object" and a type list
// containing it (["object","null"], which is how an optional object is declared).
func isObjectType(t any) bool {
	switch v := t.(type) {
	case string:
		return v == "object"
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s == "object" {
				return true
			}
		}
	}
	return false
}
