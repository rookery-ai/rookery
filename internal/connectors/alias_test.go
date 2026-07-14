package connectors

import (
	"encoding/json"
	"testing"
)

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

func TestGoogleDriveActions(t *testing.T) {
	r, _ := LoadBundled()
	if n := len(r.Actions("google_drive")); n < 8 {
		t.Fatalf("expected >=8 drive actions, got %d", n)
	}
	a, ok := r.Action("google_drive", "drive_share_file")
	if !ok {
		t.Fatal("drive_share_file missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"file_id": "F1", "role": "reader", "type": "user", "email": "x@y.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["role"] != "reader" || got["type"] != "user" {
		t.Fatalf("bad share body: %s", body)
	}
}

func TestGoogleSheetsAppendArrayBody(t *testing.T) {
	r, _ := LoadBundled()
	if _, ok := r.OAuthProvider("google_sheets"); !ok {
		t.Fatal("google_sheets not loaded / parent unresolved")
	}
	a, ok := r.Action("google_sheets", "sheets_append_values")
	if !ok {
		t.Fatal("sheets_append_values missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{
		"spreadsheet_id": "S1", "range": "Sheet1!A1",
		"values": []any{[]any{"a", "b"}, []any{"c", "d"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	rows, ok := got["values"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("values not a 2-row array: %s", body)
	}
}
