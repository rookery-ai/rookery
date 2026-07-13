package connectors

import (
	"encoding/json"
	"testing"
)

func TestSlackLoadedNonExpiring(t *testing.T) {
	r, _ := LoadBundled()
	p, ok := r.ProviderByName("slack")
	if !ok {
		t.Fatal("slack provider not loaded")
	}
	if !p.NonExpiring() {
		t.Fatal("slack tokens should be non-expiring")
	}
	if len(r.Actions("slack")) < 10 {
		t.Fatalf("expected >=10 slack actions, got %d", len(r.Actions("slack")))
	}
}

func TestSlackSendMessageBodyRenders(t *testing.T) {
	r, _ := LoadBundled()
	a, ok := r.Action("slack", "slack_send_message")
	if !ok {
		t.Fatal("slack_send_message missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"channel": "C1", "text": "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["channel"] != "C1" || got["text"] != "hello" {
		t.Fatalf("bad body: %s", body)
	}
}
