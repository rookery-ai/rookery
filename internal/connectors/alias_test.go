package connectors

import "testing"

func TestOAuthProviderResolvesParent(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	// child identity is preserved
	child, ok := r.ProviderByName("google_drive")
	if !ok {
		t.Fatal("google_drive provider not loaded")
	}
	if child.AuthParent != "google" {
		t.Fatalf("auth_parent = %q, want google", child.AuthParent)
	}
	// OAuth mechanics resolve to the parent
	oauth, ok := r.OAuthProvider("google_drive")
	if !ok || oauth.Name != "google" {
		t.Fatalf("OAuthProvider(google_drive) = %q, want google", oauth.Name)
	}
	if oauth.AuthorizeURL == "" || oauth.TokenURL == "" {
		t.Fatal("resolved parent missing OAuth endpoints")
	}
	// a normal provider resolves to itself
	self, ok := r.OAuthProvider("google")
	if !ok || self.Name != "google" {
		t.Fatalf("OAuthProvider(google) = %q, want google", self.Name)
	}
}
