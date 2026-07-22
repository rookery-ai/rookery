package vault

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func startTestBridge(t *testing.T) (*Bridge, *Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b := NewBridge(v)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Close)
	return b, v, b.Register("ws1")
}

func post(t *testing.T, url, token string, payload any) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestBridgeConvertWritesNote(t *testing.T) {
	b, v, token := startTestBridge(t)
	resp, out := post(t, b.URL()+"/convert", token, map[string]any{
		"filename": "data.csv",
		"content":  "YSxiCjEsMgo=", // base64 of "a,b\n1,2\n"
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	notePath, _ := out["note_path"].(string)
	if notePath == "" {
		t.Fatalf("no note_path in %v", out)
	}
	if _, err := v.ReadNote("ws1", notePath); err != nil {
		t.Errorf("note not written: %v", err)
	}
}

func TestBridgeRejectsBadToken(t *testing.T) {
	b, _, _ := startTestBridge(t)
	resp, _ := post(t, b.URL()+"/convert", "not-a-real-token", map[string]any{"filename": "x.csv", "content": "YSxiCg=="})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBridgeSearchScopedToWorkspace(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/a.md", []byte("the dentist appointment is tuesday"))
	v.EnsureScaffold("ws2")
	v.WriteNote("ws2", "notes/b.md", []byte("another workspace dentist note"))

	resp, out := post(t, b.URL()+"/search", token, map[string]any{"query": "dentist"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	results, _ := out["results"].(string)
	if !bytes.Contains([]byte(results), []byte("notes/a.md")) {
		t.Errorf("own workspace note missing: %q", results)
	}
	if bytes.Contains([]byte(results), []byte("notes/b.md")) {
		t.Error("a token scoped to ws1 must never surface another workspace's notes")
	}
}

func TestBridgeUnregister(t *testing.T) {
	b, _, token := startTestBridge(t)
	b.Unregister(token)
	resp, _ := post(t, b.URL()+"/search", token, map[string]any{"query": "x"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked token must stop working, got %d", resp.StatusCode)
	}
}
