package connectors

import "testing"

func TestRedirectPolicyVerifiedProviders(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	cases := []struct {
		provider          string
		scheme            string
		allowRawIP        string
		requirePublicHost bool
	}{
		{"google", "https_or_loopback", "loopback_only", true},
		{"github", "", "", false},
		{"notion", "https_or_loopback", "", false},
		{"slack", "https", "no", false},
	}
	for _, tc := range cases {
		p := r.RedirectPolicy(tc.provider)
		if !p.Verified {
			t.Fatalf("%s: policy must be marked verified", tc.provider)
		}
		if p.Scheme != tc.scheme || p.AllowRawIP != tc.allowRawIP || p.RequirePublicHost != tc.requirePublicHost {
			t.Fatalf("%s: got %+v", tc.provider, p)
		}
	}
}

// A google-aliased child must inherit the parent's policy: the redirect URI is
// registered against the PARENT's OAuth app, so the parent's rules are the ones
// that apply.
func TestRedirectPolicyInheritsFromOAuthParent(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	parent := r.RedirectPolicy("google")
	for _, child := range []string{"google_drive", "google_sheets", "google_docs",
		"google_adsense", "google_analytics", "google_searchconsole", "youtube"} {
		if got := r.RedirectPolicy(child); got != parent {
			t.Fatalf("%s: got %+v, want parent policy %+v", child, got, parent)
		}
	}
}

// An unknown or unannotated provider yields the zero policy, which Check treats
// as fully permissive and unverified.
func TestRedirectPolicyDefaultsPermissive(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range []string{"dropbox", "no_such_provider"} {
		if p := r.RedirectPolicy(name); p.Verified {
			t.Fatalf("%s: an unannotated provider must not be verified: %+v", name, p)
		}
	}
}
