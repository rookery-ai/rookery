package coder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/llm"
	"github.com/ilijad1/simple-agents/internal/vault"
)

// newSearchToolSet builds a hostToolSet wired against a real (temp) vault so the
// search_files/glob tools — which operate on h.vlt.Root(h.workspaceID) — can be
// exercised. It scaffolds a notes/ dir so the vault root exists for walks.
func newSearchToolSet(t *testing.T) *hostToolSet {
	t.Helper()
	dir := t.TempDir()
	vlt := vault.New(dir)
	const ws = "wsSearch"
	root := vlt.Root(ws)
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o750); err != nil {
		t.Fatalf("scaffold vault: %v", err)
	}
	return &hostToolSet{
		workspaceID: ws,
		vlt:         vlt,
		workDir:     root,
	}
}

// writeVaultNote writes a file at a vault-relative slash path under the toolset's vault.
func writeVaultNote(t *testing.T, h *hostToolSet, rel, content string) {
	t.Helper()
	abs := filepath.Join(h.vlt.Root(h.workspaceID), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o640); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func searchCall(query string) llm.ToolCall {
	b, _ := json.Marshal(map[string]string{"query": query})
	return llm.ToolCall{Name: "search_files", Args: b}
}

func globCall(pattern string) llm.ToolCall {
	b, _ := json.Marshal(map[string]string{"pattern": pattern})
	return llm.ToolCall{Name: "glob", Args: b}
}

// ── search_files ──────────────────────────────────────────────────────────────

// TestSearchFilesFindsContent: a literal, case-insensitive match returns the
// vault-relative path, line number, and the matching snippet. Works whether
// ripgrep is present (greps all files) or the pure-Go fallback runs (.md only),
// because the test only uses .md notes.
func TestSearchFilesFindsContent(t *testing.T) {
	h := newSearchToolSet(t)
	writeVaultNote(t, h, "notes/dentist.md", "Dentist appointment on Tuesday.\nNothing else here.")
	res := h.execute(context.Background(), searchCall("dentist"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("search should succeed; got %q", res)
	}
	if !strings.Contains(res, "notes/dentist.md") {
		t.Fatalf("result should name the vault-relative path; got %q", res)
	}
	if !strings.Contains(res, "Dentist appointment") {
		t.Fatalf("result should include the matching snippet; got %q", res)
	}
}

// TestSearchFilesNoMatchesNonError: a query with no matches is a VALID empty
// result, not a failure — so it must NOT start with "error:" (which would trip
// the oscillation guard). It returns an explicit "no matches" notice so the
// model knows the search ran and found nothing.
func TestSearchFilesNoMatchesNonError(t *testing.T) {
	h := newSearchToolSet(t)
	writeVaultNote(t, h, "notes/x.md", "nothing relevant here")
	res := h.execute(context.Background(), searchCall("xyzzy-not-present"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("no matches must not surface as error:; got %q", res)
	}
	if !strings.Contains(res, "no matches") {
		t.Fatalf("expected a no-matches notice; got %q", res)
	}
}

// TestSearchFilesRequiresQuery: an empty query is a hard (non-retryable) error.
func TestSearchFilesRequiresQuery(t *testing.T) {
	h := newSearchToolSet(t)
	res := h.execute(context.Background(), searchCall(""))
	if !strings.HasPrefix(res, "error:") || !strings.Contains(res, "query") {
		t.Fatalf("empty query must be a hard error mentioning 'query'; got %q", res)
	}
}

// TestSearchFilesSchemaIsSimple: web_fetch taught us weak/OpenAI-compatible models
// handle free-form maps (additionalProperties) unevenly, which can drop a whole
// tool. search_files keeps a flat single-property schema.
func TestSearchFilesSchemaIsSimple(t *testing.T) {
	h := &hostToolSet{workspaceID: "w", vlt: vault.New(t.TempDir())}
	tl, ok := findTool(h.tools(), "search_files")
	if !ok {
		t.Fatal("search_files not offered")
	}
	schema := string(tl.Parameters)
	if strings.Contains(schema, "additionalProperties") {
		t.Errorf("search_files schema must avoid additionalProperties for weak-model interop; got %s", schema)
	}
	if !strings.Contains(schema, `"query"`) {
		t.Errorf("search_files must require a query; got %s", schema)
	}
}

// TestSearchFilesOfferedWhenExecDisabled: search_files is a read-only file tool,
// not an exec tool, so it is offered even when includeExecTools is off (chat).
func TestSearchFilesOfferedWhenExecDisabled(t *testing.T) {
	h := &hostToolSet{includeExecTools: false, workspaceID: "w", vlt: vault.New(t.TempDir())}
	if _, ok := findTool(h.tools(), "search_files"); !ok {
		t.Fatal("search_files must be offered even when exec tools are off (it is a read tool, used in chat)")
	}
}

// ── glob ──────────────────────────────────────────────────────────────────────

// TestGlobMatchesPattern: a single-segment * glob matches files in one folder.
func TestGlobMatchesPattern(t *testing.T) {
	h := newSearchToolSet(t)
	writeVaultNote(t, h, "notes/team-meeting.md", "x")
	writeVaultNote(t, h, "notes/standup-meeting.md", "x")
	writeVaultNote(t, h, "notes/random.md", "x")
	res := h.execute(context.Background(), globCall("notes/*-meeting.md"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("glob should succeed; got %q", res)
	}
	if !strings.Contains(res, "team-meeting.md") || !strings.Contains(res, "standup-meeting.md") {
		t.Fatalf("glob should match both *-meeting.md files; got %q", res)
	}
	if strings.Contains(res, "random.md") {
		t.Fatalf("glob should not match random.md; got %q", res)
	}
}

// TestGlobStarStarRecursive: ** crosses directory separators, matching nested files.
func TestGlobStarStarRecursive(t *testing.T) {
	h := newSearchToolSet(t)
	writeVaultNote(t, h, "notes/2024/jan.md", "x")
	writeVaultNote(t, h, "notes/2024/sub/feb.md", "x")
	res := h.execute(context.Background(), globCall("notes/**/*.md"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("glob should succeed; got %q", res)
	}
	if !strings.Contains(res, "2024/jan.md") || !strings.Contains(res, "2024/sub/feb.md") {
		t.Fatalf("** should match recursively across folders; got %q", res)
	}
}

// TestGlobNoMatchesNonError: no matching files is a valid empty result, not an error.
func TestGlobNoMatchesNonError(t *testing.T) {
	h := newSearchToolSet(t)
	res := h.execute(context.Background(), globCall("notes/no-such-*.md"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("no matches must not surface as error:; got %q", res)
	}
	if !strings.Contains(res, "no files matched") {
		t.Fatalf("expected a no-files-matched notice; got %q", res)
	}
}

// TestGlobHidesDotfilesAndInternalDir: glob skips dotfiles (like list_dir) and the
// internal .kb sidecar dir, so the model never sees or touches internal data.
func TestGlobHidesDotfilesAndInternalDir(t *testing.T) {
	h := newSearchToolSet(t)
	writeVaultNote(t, h, "notes/.secret.md", "x") // dotfile note
	writeVaultNote(t, h, "notes/visible.md", "x")
	root := h.vlt.Root(h.workspaceID)
	if err := os.MkdirAll(filepath.Join(root, ".kb", "db-export"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kb", "db-export", "x.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := h.execute(context.Background(), globCall("notes/*.md"))
	if strings.Contains(res, ".secret.md") {
		t.Fatalf("glob must hide dotfiles; got %q", res)
	}
	if !strings.Contains(res, "visible.md") {
		t.Fatalf("glob should match visible.md; got %q", res)
	}

	all := h.execute(context.Background(), globCall("**/*"))
	if strings.Contains(all, ".kb/") {
		t.Fatalf("glob must skip the internal .kb dir; got %q", all)
	}
	if strings.Contains(all, ".secret.md") {
		t.Fatalf("glob must skip dotfiles even in a recursive ** glob; got %q", all)
	}
}

// TestGlobRequiresPattern: an empty pattern is a hard (non-retryable) error.
func TestGlobRequiresPattern(t *testing.T) {
	h := newSearchToolSet(t)
	res := h.execute(context.Background(), globCall(""))
	if !strings.HasPrefix(res, "error:") || !strings.Contains(res, "pattern") {
		t.Fatalf("empty pattern must be a hard error mentioning 'pattern'; got %q", res)
	}
}

// TestGlobAcceptsAbsolutePath: a weak model sometimes passes an absolute vault
// path as the pattern instead of a vault-relative glob. glob must relativize it
// (mirror read_file/resolveVault) and still match — not no-op. This is the fix
// for the Mistral run where glob("/home/.../vaults/<ws>/…") matched nothing.
func TestGlobAcceptsAbsolutePath(t *testing.T) {
	h := newSearchToolSet(t)
	writeVaultNote(t, h, "notes/skopje-weather-diary.md", "x")
	abs := filepath.Join(h.vlt.Root(h.workspaceID), "notes", "skopje-weather-diary.md")
	res := h.execute(context.Background(), globCall(abs))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("absolute-within-vault pattern should match, not error; got %q", res)
	}
	if !strings.Contains(res, "notes/skopje-weather-diary.md") {
		t.Fatalf("absolute path pattern should be relativized and match the file; got %q", res)
	}
}

// TestGlobRejectsAbsolutePathOutsideVault: an absolute path that escapes the
// vault root is rejected (error), not silently matched against nothing.
func TestGlobRejectsAbsolutePathOutsideVault(t *testing.T) {
	h := newSearchToolSet(t)
	res := h.execute(context.Background(), globCall(filepath.Join(t.TempDir(), "notes", "x.md")))
	if !strings.HasPrefix(res, "error:") {
		t.Fatalf("an absolute path outside the vault must be a hard error; got %q", res)
	}
}

// TestGlobSchemaIsSimple: flat single-property schema (no additionalProperties).
func TestGlobSchemaIsSimple(t *testing.T) {
	h := &hostToolSet{workspaceID: "w", vlt: vault.New(t.TempDir())}
	tl, ok := findTool(h.tools(), "glob")
	if !ok {
		t.Fatal("glob not offered")
	}
	schema := string(tl.Parameters)
	if strings.Contains(schema, "additionalProperties") {
		t.Errorf("glob schema must avoid additionalProperties for weak-model interop; got %s", schema)
	}
	if !strings.Contains(schema, `"pattern"`) {
		t.Errorf("glob must require a pattern; got %s", schema)
	}
}