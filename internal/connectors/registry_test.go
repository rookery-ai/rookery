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

func TestAllProvidersLoad(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{"google", "github", "notion", "outlook", "jira"} {
		p, ok := r.ProviderByName(name)
		if !ok || p.AuthorizeURL == "" || p.TokenURL == "" {
			t.Fatalf("provider %s not loaded: %+v", name, p)
		}
		if len(r.Actions(name)) == 0 {
			t.Fatalf("provider %s has no actions", name)
		}
		// Every action's params schema must compile to JSON.
		for _, a := range r.Actions(name) {
			if len(a.Params) == 0 {
				t.Fatalf("%s.%s has no compiled params", name, a.Name)
			}
			// Any body_builder referenced must be registered.
			if a.Request.BodyBuilder != "" {
				if _, ok := bodyBuilders[a.Request.BodyBuilder]; !ok {
					t.Fatalf("%s.%s references unknown body_builder %q", name, a.Name, a.Request.BodyBuilder)
				}
			}
		}
	}
	// Provider-specific config sanity.
	if gh, _ := r.ProviderByName("github"); !gh.NonExpiring() {
		t.Fatal("github tokens must be non-expiring")
	}
	if no, _ := r.ProviderByName("notion"); no.TokenAuth != "basic" {
		t.Fatal("notion must use basic token auth")
	}
	if jira, _ := r.ProviderByName("jira"); jira.PostConnect != "atlassian_cloudid" {
		t.Fatal("jira must resolve cloudid post-connect")
	}
}
