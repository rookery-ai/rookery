package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDriveCreateFolderParentsArray(t *testing.T) {
	r, _ := LoadBundled()
	a, ok := r.Action("google_drive", "drive_create_folder")
	if !ok {
		t.Fatal("drive_create_folder missing")
	}
	// With parent_id: parents must be a string array, not a string.
	_, _, body, _, err := renderRequest(a, map[string]any{"name": "Sub", "parent_id": "F1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	parents, ok := got["parents"].([]any)
	if !ok || len(parents) != 1 || parents[0] != "F1" {
		t.Fatalf("parents not a 1-element array: %s", body)
	}
	// Without parent_id: parents key must be omitted entirely.
	_, _, body2, _, err := renderRequest(a, map[string]any{"name": "Top"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got2 map[string]any
	if err := json.Unmarshal(body2, &got2); err != nil {
		t.Fatal(err)
	}
	if _, present := got2["parents"]; present {
		t.Fatalf("parents key should be omitted when parent_id is absent: %s", body2)
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

func TestGoogleDocsInsertText(t *testing.T) {
	r, _ := LoadBundled()
	if _, ok := r.OAuthProvider("google_docs"); !ok {
		t.Fatal("google_docs not loaded")
	}
	a, ok := r.Action("google_docs", "docs_insert_text")
	if !ok {
		t.Fatal("docs_insert_text missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"document_id": "D1", "text": "hello", "index": float64(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	reqs, ok := got["requests"].([]any)
	if !ok || len(reqs) != 1 {
		t.Fatalf("requests[] not built: %s", body)
	}
}

func TestTeamsSendMessageBody(t *testing.T) {
	r, _ := LoadBundled()
	oauth, ok := r.OAuthProvider("teams")
	if !ok || oauth.Name != "outlook" {
		t.Fatalf("teams parent = %q, want outlook", oauth.Name)
	}
	a, ok := r.Action("teams", "teams_send_channel_message")
	if !ok {
		t.Fatal("teams_send_channel_message missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"team_id": "T", "channel_id": "C", "content": "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	b, ok := got["body"].(map[string]any)
	if !ok || b["content"] != "hi" {
		t.Fatalf("nested body.content missing: %s", body)
	}
}

func TestExecuteChildProviderSendsBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Errorf("bearer missing on child call")
		}
		w.Write([]byte(`{"files":[{"id":"f1"}]}`))
	}))
	defer srv.Close()
	reg := testRegistry(t)
	a, _ := reg.Action("google_drive", "drive_list_files")
	a.Request.URL = srv.URL + "/files"
	reg.actions["google_drive"] = []Action{a}
	res, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c1", Provider: "google_drive"}, "drive_list_files", map[string]any{}, false)
	if err != nil {
		t.Fatalf("execute child: %v", err)
	}
	if !strings.Contains(string(res.Data), "f1") {
		t.Fatalf("extract failed: %s", res.Data)
	}
}
