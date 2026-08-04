package connectors

import (
	"strings"
	"testing"
)

// The labels are DERIVED from each provider's own setup_steps, which name the console
// field verbatim ("Copy the App key (client id) and App secret"). This ties them back to
// that source: rename a label without touching the prose and the card says "App ID" above
// a step telling the user to copy the "Client ID". That divergence is invisible in either
// file alone, which is why it needs a test rather than a review convention.
func TestOAuthCredLabelsMatchSetupSteps(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range r.ProviderNames() {
		p, _ := r.ProviderByName(name)
		steps := strings.ToLower(strings.Join(p.SetupSteps, " "))
		for _, f := range []struct{ what, label string }{
			{"id_label", p.OAuthCreds.IDLabel},
			{"secret_label", p.OAuthCreds.SecretLabel},
		} {
			if f.label == "" {
				continue
			}
			if !strings.Contains(steps, strings.ToLower(f.label)) {
				t.Errorf("%s %s = %q but no setup step mentions it — the connect form and the instructions disagree",
					name, f.what, f.label)
			}
		}
	}
}
