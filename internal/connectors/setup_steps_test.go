package connectors

import (
	"strings"
	"testing"
)

// A setup step that tells the user to register a redirect URI must name it via
// the {{redirect_uri}} placeholder. Prose like "the redirect URI shown above"
// is what made this feature unusable: it referred to something the page never
// rendered. This test stops that wording from creeping back.
func TestSetupStepsUsePlaceholderNotProse(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	banned := []string{"shown above", "uri shown", "url shown"}
	for _, name := range r.ProviderNames() {
		p, ok := r.ProviderByName(name)
		if !ok {
			continue
		}
		for i, step := range p.SetupSteps {
			low := strings.ToLower(step)
			for _, b := range banned {
				if strings.Contains(low, b) {
					t.Errorf("%s step %d refers to %q instead of using {{redirect_uri}}: %s",
						name, i+1, b, step)
				}
			}
		}
	}
}

// Every OAuth provider that ships its own setup steps must name the redirect URI
// in at least one of them — otherwise the guide is incomplete for the one field
// the user cannot guess.
func TestOAuthSetupStepsNameTheRedirectURI(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range r.ProviderNames() {
		p, ok := r.ProviderByName(name)
		if !ok || p.PastesCredential() || len(p.SetupSteps) == 0 {
			continue
		}
		// Aliased children inherit their parent's guidance and ship none of their own.
		if p.AuthParent != "" {
			continue
		}
		found := false
		for _, step := range p.SetupSteps {
			if strings.Contains(step, "{{redirect_uri}}") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: no setup step names {{redirect_uri}}", name)
		}
	}
}
