package connectors

import (
	"encoding/json"
	"testing"
)

// An optional argument that is absent must leave NO trace in the body. The subtle case
// is a nested one: a wrapper object whose only child came from the missing argument.
// Sending it empty says "set this field, to nothing", which is a different statement
// from "do not touch this field" — and providers read it that way.
//
// calendar_update_event is the action this was found on. Its body carries
// start: {dateTime: "{{start}}"}, so a PATCH that only changed the title still sent
// "start": {}, and Google rejects an event whose start is present but timeless. Partial
// updates — the entire point of a PATCH action — could not work.
func TestRenderBodyOmitsAContainerLeftEmptyByAbsentArgs(t *testing.T) {
	tmpl := map[string]any{
		"summary": "{{summary}}",
		"start":   map[string]any{"dateTime": "{{start}}"},
		"end":     map[string]any{"dateTime": "{{end}}"},
	}
	got, ok := renderBody(tmpl, map[string]any{"summary": "Standup"}, nil)
	if !ok {
		t.Fatal("body should render")
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"summary":"Standup"}` {
		t.Fatalf("absent start/end leaked into the body: %s", b)
	}

	// Present arguments still nest correctly.
	got, _ = renderBody(tmpl, map[string]any{"summary": "Standup", "start": "2026-08-11T09:00:00Z"}, nil)
	b, _ = json.Marshal(got)
	if string(b) != `{"start":{"dateTime":"2026-08-11T09:00:00Z"},"summary":"Standup"}` {
		t.Fatalf("present start did not render: %s", b)
	}
}

func TestRenderBodyOmitsAnArrayLeftEmptyByAbsentArgs(t *testing.T) {
	// drive_copy_file's parents: ["{{parent_id}}"] — an absent parent must not send
	// "parents": [], which reads as "this file has no parent folder" rather than
	// "leave the destination alone".
	tmpl := map[string]any{"name": "{{name}}", "parents": []any{"{{parent_id}}"}}
	got, _ := renderBody(tmpl, map[string]any{"name": "copy"}, nil)
	b, _ := json.Marshal(got)
	if string(b) != `{"name":"copy"}` {
		t.Fatalf("empty parents array leaked: %s", b)
	}
}

// A container written empty in the YAML is a deliberate statement and must survive, or
// an action that has to send a literal {} loses the ability to.
func TestRenderBodyKeepsADeliberatelyEmptyContainer(t *testing.T) {
	got, ok := renderBody(map[string]any{"filter": map[string]any{}}, nil, nil)
	if !ok {
		t.Fatal("body should render")
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"filter":{}}` {
		t.Fatalf("an intentional empty object was dropped: %s", b)
	}
}
