package coder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// ── ranked passages (BM25 index) ────────────────────────────────────────────

func TestSearchFilesReturnsRankedChunks(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	v.WriteNote(ws, "notes/health.md", []byte("# Health\n\n## Appointments\n\nBooked an orthodontist visit for Tuesday.\n"))
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), "dentist appointment")
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if !strings.Contains(out, "notes/health.md") {
		t.Errorf("ranked retrieval should find the note, got:\n%s", out)
	}
	if !strings.Contains(out, "orthodontist") {
		t.Errorf("the result should carry the passage text, not just a path, got:\n%s", out)
	}
	if !strings.Contains(out, "Appointments") {
		t.Errorf("the heading trail should be shown, got:\n%s", out)
	}
}

// Exact matching must not regress: BM25 is worse than literal search for a UUID
// or an error string, so both run and exact hits come first.
func TestSearchFilesKeepsExactMatching(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)
	const id = "7f3a91e2-4c8b-4d2e-9a11-6b0f5c2d8e41"
	v.WriteNote(ws, "notes/ids.md", []byte("# Ids\n\nrun id "+id+" failed\n"))
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), id)
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if !strings.Contains(out, "notes/ids.md") {
		t.Errorf("an exact identifier must still match, got:\n%s", out)
	}
	if !strings.Contains(out, "Exact matches") {
		t.Errorf("exact hits should be labelled and listed first, got:\n%s", out)
	}
}

func TestSearchFilesNoMatchesIsNonError(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), "zzz-nothing-matches-this")
	if err != nil {
		t.Fatalf("no matches must not be an error: %v", err)
	}
	if !strings.Contains(out, "no matches") {
		t.Errorf("unexpected output: %q", out)
	}
}

// ── exact-section budget (crowding-out fix) ─────────────────────────────────

// exactCrowdingQuery is deliberately SHORT (two tokens) — used both as the
// search_files query and, verbatim, as the literal substring embedded into
// many junk notes for the exact/ripgrep pass. Its second word ("appointment")
// is the one that also gives health.md (below) a real BM25/heading signal, so
// this single query drives both signals at once. Kept to two tokens on
// purpose: search_files tokenizes the SAME query for BM25, and every token
// present in the query necessarily has to appear verbatim in the junk notes
// for the literal match to fire — a longer query would hand the junk notes
// more overlapping BM25 terms and let them out-rank health.md on ranked
// score too, which would defeat the fixture (see exactCrowdingFiller below
// for how the exact-match LINE is still made long without adding more query
// terms).
const exactCrowdingQuery = "zzqcrowdmarker9182 appointment"

// exactCrowdingFiller is unrelated padding placed AROUND exactCrowdingQuery on
// each junk note's line, so the ripgrep snippet (the whole matching LINE,
// trimmed to 200 bytes — see trimSnippet) approaches that cap and the exact
// section is large, WITHOUT adding any of these words to the query itself
// (bm25Score only scores query terms present in a document — unrelated filler
// words in the same line contribute nothing to the score).
const exactCrowdingFiller = "unrelated padding content repeated here purely to push this " +
	"single matching line closer to the two hundred byte ripgrep snippet cap " +
	"without contributing any additional query terms to the BM25 ranking pass"

// writeExactCrowdingFixture seeds a vault with n junk notes that each embed
// exactCrowdingQuery verbatim inside a long padded line (producing n
// exact/ripgrep hits, each near the 200-byte snippet cap) plus ONE
// strong-BM25 note (health.md, matching via its "Appointments" heading —
// never containing exactCrowdingQuery or "zzqcrowdmarker9182") — the "dentist
// finds orthodontist" case this whole feature exists for. Reproduces the
// review finding: a vault with dozens of notes containing a common literal
// phrase produced enough exact lines to consume the entire byte cap on their
// own, with zero ranked passages (including strong BM25 matches) reaching the
// model.
func writeExactCrowdingFixture(t *testing.T, v *vault.Vault, ws string, n int) {
	t.Helper()
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	for i := 0; i < n; i++ {
		rel := fmt.Sprintf("notes/junk%03d.md", i)
		body := fmt.Sprintf("# Ops log %d\n\nEntry %d: %s %s %s\n", i, i, exactCrowdingFiller, exactCrowdingQuery, exactCrowdingFiller)
		if err := v.WriteNote(ws, rel, []byte(body)); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := v.WriteNote(ws, "notes/health.md",
		[]byte("# Health\n\n## Appointments\n\nBooked an orthodontist visit for Tuesday.\n")); err != nil {
		t.Fatalf("write health.md: %v", err)
	}
}

// TestSearchFilesExactBudgetLeavesRoomForRanked is the Finding-1 repro: with
// the crowding fixture (40 junk notes, each an exact hit on a long literal
// phrase, PLUS one strong ranked-only match), the ranked passage must still
// reach the model, the exact section must be bounded (not all 40 hits), and
// the omission must be explicit rather than silent.
//
// MUTATION CHECK: reverting the budget split (writing the whole unbounded
// exact-match section first, then appending ranked passages, then truncating
// the combined string at maxToolResult) makes this test FAIL — the 40 exact
// lines alone exceed maxToolResult, so "Related passages"/"orthodontist" never
// appear in the truncated output. Confirmed by hand during review; restored
// afterward. If a future refactor makes this test pass again with the split
// removed, the fixture below is no longer big enough — see the doc comment on
// exactCrowdingQuery and grow n or the phrase length.
func TestSearchFilesExactBudgetLeavesRoomForRanked(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	writeExactCrowdingFixture(t, v, ws, 40)
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), exactCrowdingQuery)
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if len(out) > maxToolResult {
		t.Fatalf("result must stay within the tool result cap (%d bytes); got %d", maxToolResult, len(out))
	}

	// The exact section is present and bounded — not all 40 hits crammed in.
	if !strings.Contains(out, "Exact matches:") {
		t.Fatalf("expected an exact-matches section, got:\n%s", out)
	}
	gotExactLines := strings.Count(out, "junk")
	if gotExactLines >= 40 {
		t.Errorf("exact section should be budget-bounded, not all 40 hits; counted %d occurrences of 'junk' in:\n%s", gotExactLines, out)
	}

	// The omission is explicit, not silent.
	if !strings.Contains(out, "more exact match") {
		t.Errorf("expected a visible omission notice for the exact matches dropped by the budget, got:\n%s", out)
	}

	// The ranked/BM25 section — the whole reason this tool does two passes —
	// must still reach the model.
	if !strings.Contains(out, "Related passages:") {
		t.Errorf("ranked passages must not be crowded out by the exact section, got:\n%s", out)
	}
	if !strings.Contains(out, "orthodontist") {
		t.Errorf("the strong ranked match (health.md) must survive, got:\n%s", out)
	}
	if !strings.Contains(out, "notes/health.md") {
		t.Errorf("expected notes/health.md in the ranked section, got:\n%s", out)
	}
}

// TestSearchFilesEmptySectionGetsFullBudget: when only ONE section has
// content, it is not artificially capped at its 40% share — it may use the
// whole budget.
func TestSearchFilesEmptySectionGetsFullBudget(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	// Only exact hits: no BM25-scoring content elsewhere for this query
	// (the crowding phrase's tokens all live inside the junk notes themselves,
	// so BM25 will also score them — but with no OTHER note in the vault, the
	// "ranked" section is effectively the same junk notes; what matters here
	// is simply that the exact section is not truncated to 40% when the
	// overall output is still within budget).
	writeExactCrowdingFixture(t, v, ws, 5) // small n: fits well inside 8 KiB unsplit
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), exactCrowdingQuery)
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if strings.Contains(out, "more exact match") {
		t.Errorf("small exact section should not be truncated at all, got:\n%s", out)
	}
	if got := strings.Count(out, "junk"); got < 5 {
		t.Errorf("all 5 exact hits should be present, counted %d occurrences of 'junk' in:\n%s", got, out)
	}
}

// TestSearchFilesRankedGetsTrueRemainderNotFixedShare: when the exact section
// is SMALL (well under its 40% ceiling), the ranked section must get the
// actual leftover budget, not be capped at a fixed 60% share — otherwise a
// query with just a couple of short exact hits would still waste a big chunk
// of the cap even though ranked passages exist to fill it.
func TestSearchFilesRankedGetsTrueRemainderNotFixedShare(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	const query = "xqshort7 widgetreport"

	// Two SHORT exact hits: the exact section this produces is tiny (well
	// under its 40% ceiling of maxToolResult).
	for i := 0; i < 2; i++ {
		rel := fmt.Sprintf("notes/short%d.md", i)
		body := fmt.Sprintf("# Ref %d\n\nRef: %s done.\n", i, query)
		if err := v.WriteNote(ws, rel, []byte(body)); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// Six LARGE, distinct ranked-only notes (never contain "xqshort7", so
	// they're not exact hits) — each padded to near the 1500-byte hard chunk
	// bound, with "widgetreport" in the heading for a strong BM25 signal.
	// Combined (~8400 bytes) comfortably exceeds BOTH a fixed 60% share
	// (~4916 bytes, ~3 passages) and the true remainder after two tiny exact
	// hits (~8000+ bytes, ~5-6 passages) — so this only distinguishes the two
	// schemes if the true-remainder one lets noticeably more through.
	filler := strings.Repeat("padding content unrelated to the query terms themselves and here only to reach the target chunk size for this fixture note. ", 10)
	for i := 0; i < 6; i++ {
		rel := fmt.Sprintf("notes/big%d.md", i)
		body := fmt.Sprintf("# Widgetreport %d\n\n%s widgetreport widgetreport widgetreport\n", i, filler)
		if err := v.WriteNote(ws, rel, []byte(body)); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}
	out, err := h.searchFiles(context.Background(), query)
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if len(out) > maxToolResult {
		t.Fatalf("result must stay within the tool result cap (%d bytes); got %d", maxToolResult, len(out))
	}

	rankedIdx := strings.Index(out, "Related passages:")
	if rankedIdx < 0 {
		t.Fatalf("expected a ranked section, got:\n%s", out)
	}
	rankedBytes := len(out) - rankedIdx
	fixedShare := maxToolResult * 3 / 5 // the old (rejected) fixed-60% scheme
	if rankedBytes <= fixedShare {
		t.Errorf("ranked section (%d bytes) should exceed the old fixed 60%% share (%d bytes) "+
			"when the exact section is small — got:\n%s", rankedBytes, fixedShare, out)
	}
}

// ── exact-search error degradation (no longer silently swallowed) ──────────

// erroringSearcher always fails, simulating a broken ripgrep/subprocess.
type erroringSearcher struct{ err error }

func (e erroringSearcher) Search(ctx context.Context, workspaceID, query string) ([]vault.SearchHit, error) {
	return nil, e.err
}

// TestSearchFilesExactErrorDegradesToRankedOnly: a failing exact-match search
// must NOT fail the whole tool call (BM25 can still answer usefully), but the
// failure must be logged, not silently swallowed.
func TestSearchFilesExactErrorDegradesToRankedOnly(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := v.WriteNote(ws, "notes/health.md",
		[]byte("# Health\n\n## Appointments\n\nBooked an orthodontist visit for Tuesday.\n")); err != nil {
		t.Fatalf("write health.md: %v", err)
	}

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prevLogger)

	simulated := fmt.Errorf("simulated ripgrep subprocess failure")
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws), searcher: erroringSearcher{err: simulated}}

	out, err := h.searchFiles(context.Background(), "dentist appointment")
	if err != nil {
		t.Fatalf("a broken exact search must degrade, not fail the tool: %v", err)
	}
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("degraded result must not read as an error:; got %q", out)
	}
	if !strings.Contains(out, "Related passages:") || !strings.Contains(out, "orthodontist") {
		t.Errorf("ranked results must still be returned when the exact search fails, got:\n%s", out)
	}
	if strings.Contains(out, "Exact matches:") {
		t.Errorf("no exact section should be emitted when the exact search errored, got:\n%s", out)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, ws) {
		t.Errorf("the exact-search failure must be logged with the workspace, got log: %q", logged)
	}
	if !strings.Contains(logged, "simulated ripgrep subprocess failure") {
		t.Errorf("the exact-search failure must be logged with the underlying error, got log: %q", logged)
	}
}

// TestSearchFilesFindsOversizedFileByName pins the search_files side of Fix 1:
// a file over the index's size cap (see index.go's maxIndexFileBytes) is
// indexed name-only (empty body, never read into memory) — search_files must
// still report it by filename rather than silently omitting it, the same
// guarantee TestBuildKBContextFindsOversizedFileByName pins for the designer.
// The filler body deliberately never contains the query words, so the ONLY
// way this file can surface is via the name-only path (a literal exact-match
// hit would also be legitimate, but would make the assertion less precise
// about which mechanism is actually being exercised).
func TestSearchFilesFindsOversizedFileByName(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	// 4 MiB mirrors vault's unexported maxIndexFileBytes (internal/vault/index.go)
	// — not importable from this package, so the threshold is duplicated here as
	// a literal, same as index_test.go's own oversized-file fixtures do within
	// the vault package itself.
	const overIndexCap = 4 << 20
	body := strings.Repeat("unrelated filler content repeated many times over and over again. ", 90000)
	if len(body) <= overIndexCap {
		t.Fatalf("test fixture body (%d bytes) must exceed the index size cap (%d)", len(body), overIndexCap)
	}
	if err := v.WriteNote(ws, "notes/annual-budget-summary.md", []byte(body)); err != nil {
		t.Fatalf("write oversized note: %v", err)
	}
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), "annual budget summary")
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if !strings.Contains(out, "annual-budget-summary.md") {
		t.Errorf("oversized file must still be found by name, got:\n%s", out)
	}
	if !strings.Contains(out, "Also found by filename") {
		t.Errorf("expected the name-only-match section, got:\n%s", out)
	}
}
