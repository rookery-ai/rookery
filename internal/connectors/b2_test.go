package connectors

import (
	"encoding/json"
	"testing"
)

// b2Reg loads the bundled registry for B2 connector tests.
func b2Reg(t *testing.T) *Registry {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	return r
}

// renderB2 renders a B2 connector action and returns the parsed body as a map.
func renderB2(t *testing.T, r *Registry, provider, action string, args map[string]any) map[string]any {
	a, ok := r.Action(provider, action)
	if !ok {
		t.Fatalf("action %s.%s not found", provider, action)
	}
	_, _, body, _, err := renderRequest(a, args, nil)
	if err != nil {
		t.Fatalf("renderRequest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("Unmarshal body: %v", err)
	}
	return m
}

func TestB2_HubSpot(t *testing.T) {
	r := b2Reg(t)
	p, ok := r.ProviderByName("hubspot")
	if !ok || !p.IsAPIKey() {
		t.Fatal("hubspot must load as api_key")
	}
	if len(r.Actions("hubspot")) < 8 {
		t.Fatalf("want >=8 hubspot actions, got %d", len(r.Actions("hubspot")))
	}
	m := renderB2(t, r, "hubspot", "hubspot_create_contact", map[string]any{"properties": map[string]any{"email": "a@b.com"}})
	props, ok := m["properties"].(map[string]any)
	if !ok || props["email"] != "a@b.com" {
		t.Fatalf("properties object not passed through: %v", m)
	}
}

func TestB2_Calendly(t *testing.T) {
	r := b2Reg(t)
	if p, ok := r.ProviderByName("calendly"); !ok || !p.IsAPIKey() {
		t.Fatal("calendly must load as api_key")
	}
	if len(r.Actions("calendly")) < 6 {
		t.Fatalf("want >=6 calendly actions, got %d", len(r.Actions("calendly")))
	}
	m := renderB2(t, r, "calendly", "calendly_cancel_event", map[string]any{"uuid": "U1", "reason": "x"})
	if m["reason"] != "x" {
		t.Fatalf("cancel body: %v", m)
	}
}
