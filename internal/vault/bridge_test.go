package vault

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	return b, v, b.Register("ws1", false)
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

// TestBridgeConvertRejectsOversizedBody proves /convert (which carries whole
// base64-inflated documents) is capped: an over-limit body must be rejected
// with a clear message, not silently truncated into a confusing JSON parse
// failure.
func TestBridgeConvertRejectsOversizedBody(t *testing.T) {
	b, _, token := startTestBridge(t)
	oversized := bytes.Repeat([]byte("a"), maxConvertBody+1)
	req, _ := http.NewRequest("POST", b.URL()+"/convert", bytes.NewReader(oversized))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	out, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(out, []byte("exceeds")) {
		t.Errorf("expected a clear size-limit message, got %q", out)
	}
}

// TestBridgeSearchRejectsOversizedBody mirrors the convert case for /search,
// which only ever needs to carry a short query string.
func TestBridgeSearchRejectsOversizedBody(t *testing.T) {
	b, _, token := startTestBridge(t)
	oversized := bytes.Repeat([]byte("a"), maxSearchBody+1)
	req, _ := http.NewRequest("POST", b.URL()+"/search", bytes.NewReader(oversized))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	out, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(out, []byte("exceeds")) {
		t.Errorf("expected a clear size-limit message, got %q", out)
	}
}

// TestBridgeRejectsNonPostMethod proves a stray GET is refused by design
// (405), not incidentally by falling through to "empty body → 400".
func TestBridgeRejectsNonPostMethod(t *testing.T) {
	b, _, token := startTestBridge(t)
	req, _ := http.NewRequest(http.MethodGet, b.URL()+"/convert", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// TestBridgeConvertRefusesDuringBuild proves the build-phase guard lives at
// the ImportFile choke point: a token registered with buildPhase=true must
// never be able to write a real note into the live vault, regardless of what
// the caller remembers to check.
func TestBridgeConvertRefusesDuringBuild(t *testing.T) {
	v := New(t.TempDir())
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b := NewBridge(v)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(b.Close)
	token := b.Register("ws1", true)

	resp, out := post(t, b.URL()+"/convert", token, map[string]any{
		"filename": "data.csv",
		"content":  "YSxiCjEsMgo=",
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a build-phase token must not write to the live vault, got 200: %v", out)
	}
	entries, _ := os.ReadDir(filepath.Join(v.Root("ws1"), "notes"))
	if len(entries) != 0 {
		t.Errorf("no note should exist after a refused build-phase import, found %d", len(entries))
	}
	files, _ := os.ReadDir(filepath.Join(v.Root("ws1"), FilesDir))
	if len(files) != 0 {
		t.Errorf("no preserved original should exist after a refused build-phase import, found %d", len(files))
	}
}
