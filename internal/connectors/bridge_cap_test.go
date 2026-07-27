package connectors

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapBridgeDataUnderCapIsUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"rows":[1,2,3]}`)
	out := capBridgeData(raw)

	got, ok := out["data"]
	if !ok {
		t.Fatal(`under-cap response must keep the "data" key`)
	}
	rm, ok := got.(json.RawMessage)
	if !ok {
		t.Fatalf("under-cap data must stay a raw JSON value, got %T", got)
	}
	if string(rm) != `{"rows":[1,2,3]}` {
		t.Errorf("under-cap data was modified: %s", rm)
	}
	if _, truncated := out["truncated"]; truncated {
		t.Error("under-cap response must not be marked truncated")
	}
}

// A response exactly at the cap is NOT truncated — the boundary belongs to the
// unchanged path, matching iolimit's read-cap+1 convention elsewhere.
func TestCapBridgeDataAtExactlyCapIsUnchanged(t *testing.T) {
	raw := json.RawMessage(strings.Repeat("x", maxBridgeResult))
	out := capBridgeData(raw)
	if _, truncated := out["truncated"]; truncated {
		t.Error("a response exactly at the cap must not be truncated")
	}
}

func TestCapBridgeDataOverCapTruncatesWithNotice(t *testing.T) {
	raw := json.RawMessage(`{"rows":"` + strings.Repeat("x", maxBridgeResult+100) + `"}`)
	out := capBridgeData(raw)

	if out["truncated"] != true {
		t.Fatal("over-cap response must be marked truncated")
	}
	s, ok := out["data"].(string)
	if !ok {
		t.Fatalf("over-cap data must be a string, got %T", out["data"])
	}
	if len(s) > maxBridgeResult+8 { // +8 allows the multi-byte ellipsis
		t.Errorf("truncated data is %d bytes, cap is %d", len(s), maxBridgeResult)
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "narrower") {
		t.Errorf("note must tell the model how to recover, got %q", note)
	}
}

// The whole envelope must still marshal — a truncated payload that breaks
// json.Marshal would turn a large result into a bridge 500.
func TestCapBridgeDataAlwaysMarshals(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"a":1}`),
		json.RawMessage(strings.Repeat("y", maxBridgeResult*2)),
	} {
		if _, err := json.Marshal(capBridgeData(raw)); err != nil {
			t.Errorf("capBridgeData output failed to marshal: %v", err)
		}
	}
}
