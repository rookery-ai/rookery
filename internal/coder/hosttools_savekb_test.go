package coder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/vault"
)

func newSaveKBToolset(t *testing.T) (*hostToolSet, *vault.Vault) {
	t.Helper()
	base := t.TempDir()
	v := vault.New(base)
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	return &hostToolSet{
		workspaceID:      "ws1",
		vlt:              v,
		workDir:          v.Root("ws1"),
		includeExecTools: false,
	}, v
}

func TestSaveToKBFromVaultPath(t *testing.T) {
	h, v := newSaveKBToolset(t)
	if err := os.WriteFile(filepath.Join(v.Root("ws1"), "raw.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	out := h.execute(context.Background(), llm.ToolCall{
		Name: "save_to_kb",
		Args: json.RawMessage(`{"source":"raw.csv"}`),
	})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("save_to_kb failed: %s", out)
	}
	if !strings.Contains(out, "notes/") {
		t.Errorf("result should name the created note, got %q", out)
	}
}

func TestSaveToKBOfferedInChat(t *testing.T) {
	h, _ := newSaveKBToolset(t)
	var found bool
	for _, tool := range h.tools() {
		if tool.Name == "save_to_kb" {
			found = true
		}
	}
	if !found {
		t.Error("save_to_kb must be available without exec tools: it converts and files, it does not execute")
	}
}

func TestSaveToKBMissingSourceIsError(t *testing.T) {
	h, _ := newSaveKBToolset(t)
	out := h.execute(context.Background(), llm.ToolCall{Name: "save_to_kb", Args: json.RawMessage(`{"source":"nope.csv"}`)})
	if !strings.HasPrefix(out, "error:") {
		t.Errorf("a missing source must error, got %q", out)
	}
}

// TestSaveToKBBlocksPrivateAddressByDefault proves save_to_kb fetching a URL uses the
// SAME guarded client web_fetch does: a loopback target (e.g. the connector/KB bridge
// itself) must be refused, not become a way around the private-address block.
func TestSaveToKBBlocksPrivateAddressByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bridge secrets"))
	}))
	defer srv.Close()

	h, _ := newSaveKBToolset(t) // allowPrivateHosts is false (the zero value) — guard ON
	out := h.execute(context.Background(), llm.ToolCall{
		Name: "save_to_kb",
		Args: json.RawMessage(`{"source":"` + srv.URL + `"}`),
	})
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("save_to_kb must not reach a loopback address, got %q", out)
	}
}

// TestSaveToKBBlockedDuringBuild proves save_to_kb refuses to write into the LIVE
// vault while an agent build is under verification — ImportFile always resolves
// against the vault root regardless of workDir, so (unlike write_file/edit_file,
// which stay inside the draft agent dir at build time) it would otherwise leave a
// real, uncleaned note in the user's knowledge base from a build-time test call.
func TestSaveToKBBlockedDuringBuild(t *testing.T) {
	h, v := newSaveKBToolset(t)
	h.verifyBuild = true
	if err := os.WriteFile(filepath.Join(v.Root("ws1"), "raw.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	out := h.execute(context.Background(), llm.ToolCall{
		Name: "save_to_kb",
		Args: json.RawMessage(`{"source":"raw.csv"}`),
	})
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("save_to_kb must be blocked during a build, got %q", out)
	}
	entries, _ := os.ReadDir(filepath.Join(v.Root("ws1"), "notes"))
	if len(entries) != 0 {
		t.Errorf("no note should have been written to the live vault during a build, found %d", len(entries))
	}
}

// TestSaveToKBFromURLOverLimitErrors pins Fix 2: an over-limit source must be
// refused with a clear error, never silently truncated into a shorter file
// that a false original_bytes frontmatter value then lies about.
func TestSaveToKBFromURLOverLimitErrors(t *testing.T) {
	big := make([]byte, maxImportBody+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(big)
	}))
	defer srv.Close()

	h, v := newSaveKBToolset(t)
	h.allowPrivateHosts = true
	h.webRetryBase = time.Millisecond
	out := h.execute(context.Background(), llm.ToolCall{
		Name: "save_to_kb",
		Args: json.RawMessage(`{"source":"` + srv.URL + `/big.csv"}`),
	})
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("an over-limit source must be refused, got %q", out)
	}
	// Confirm nothing was silently written — a partial import would be worse
	// than no import at all (a note claiming to be the whole document).
	entries, _ := os.ReadDir(filepath.Join(v.Root("ws1"), "notes"))
	if len(entries) != 0 {
		t.Errorf("no note should exist after a refused over-limit fetch, found %d", len(entries))
	}
}

// TestSaveToKBFromURLAcceptsAboveContextCap pins the asymmetry fix: a document
// larger than the web_fetch context cap (maxWebBody) but within the import cap
// must import successfully by URL — the same document that would upload fine
// through the browser must not be rejected only because it arrived by URL. This
// is why save_to_kb reads to maxImportBody, not maxWebBody.
func TestSaveToKBFromURLAcceptsAboveContextCap(t *testing.T) {
	if maxImportBody <= maxWebBody {
		t.Fatalf("import cap (%d) must exceed the context cap (%d) for this test to mean anything",
			maxImportBody, maxWebBody)
	}
	// A valid CSV comfortably above maxWebBody (2 MiB) but under maxImportBody (25 MiB).
	var b strings.Builder
	b.WriteString("id,value\n")
	for b.Len() < maxWebBody+(1<<20) { // ~3 MiB
		b.WriteString("1,some row of data here\n")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, b.String())
	}))
	defer srv.Close()

	h, v := newSaveKBToolset(t)
	h.allowPrivateHosts = true
	h.webRetryBase = time.Millisecond
	out := h.execute(context.Background(), llm.ToolCall{
		Name: "save_to_kb",
		Args: json.RawMessage(`{"source":"` + srv.URL + `/mid.csv"}`),
	})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("a document above the context cap but within the import cap must import, got %q", out)
	}
	entries, _ := os.ReadDir(filepath.Join(v.Root("ws1"), "notes"))
	if len(entries) == 0 {
		t.Error("expected a note to be written for an in-limit import")
	}
}

// TestSaveToKBFromURLUsesGuardedClient proves the happy path also goes through the
// same client selection as web_fetch (allowPrivateHosts opts a TEST ONLY into
// reaching the httptest server, mirroring newWebToolSet's convention).
func TestSaveToKBFromURLUsesGuardedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	}))
	defer srv.Close()

	h, _ := newSaveKBToolset(t)
	h.allowPrivateHosts = true
	h.webRetryBase = time.Millisecond
	out := h.execute(context.Background(), llm.ToolCall{
		Name: "save_to_kb",
		Args: json.RawMessage(`{"source":"` + srv.URL + `/report.csv"}`),
	})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("save_to_kb from URL failed: %s", out)
	}
	if !strings.Contains(out, "notes/") {
		t.Errorf("result should name the created note, got %q", out)
	}
}
