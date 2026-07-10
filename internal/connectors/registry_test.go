package connectors

import "testing"

func TestLoadBundledGoogle(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p, ok := r.ProviderByName("google")
	if !ok || p.TokenURL == "" || len(p.DefaultScopes) == 0 {
		t.Fatalf("google provider not loaded: %+v", p)
	}
	acts := r.Actions("google")
	if len(acts) != 4 {
		t.Fatalf("want 4 gmail actions, got %d", len(acts))
	}
	send, ok := r.Action("google", "gmail_send_email")
	if !ok || !send.Mutating {
		t.Fatalf("gmail_send_email must be mutating: %+v", send)
	}
	if len(send.Params) == 0 {
		t.Fatal("params schema must be compiled to JSON")
	}
	if draft, _ := r.Action("google", "gmail_create_draft"); draft.Mutating {
		t.Fatal("gmail_create_draft must NOT be mutating")
	}
}
