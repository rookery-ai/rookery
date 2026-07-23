package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func seedVault(t *testing.T) (*Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := v.WriteNote(ws, rel, []byte(body)); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("notes/health.md", "# Health\n\n## Appointments\n\nBooked an orthodontist visit for Tuesday morning.\n")
	write("notes/travel.md", "# Travel\n\nFlights to Lisbon in September. Booked with Wizz.\n")
	write("memory/USER.md", "# User\n\nIlija lives in Skopje and works on self-hosted infrastructure.\n")
	write("notes/ids.md", "# Ids\n\nrun id 7f3a91e2-4c8b-4d2e-9a11-6b0f5c2d8e41 failed\n")
	write("files/expenses.csv", "item,cost\nrent,900\ngroceries,240\n")
	return v, ws
}

// The headline case: literal search cannot find "dentist" in a note that says
// "orthodontist". Ranked retrieval must.
func TestIndexFindsWhatLiteralSearchMisses(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "dentist appointment", 5)
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if !strings.Contains(got[0].Path, "health.md") {
		t.Errorf("top result = %q, want notes/health.md", got[0].Path)
	}
}

func TestIndexSearchesWholeVaultNotJustNotes(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "Skopje infrastructure", 5)
	var found bool
	for _, s := range got {
		if strings.Contains(s.Path, "memory/USER.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("memory/ must be searchable, got %+v", paths(got))
	}
}

func TestIndexSearchesNonMarkdownContent(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "groceries", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "expenses.csv") {
		t.Errorf("csv body must be searchable, got %+v", paths(got))
	}
}

func TestIndexMatchesFilename(t *testing.T) {
	v, ws := seedVault(t)
	// "expenses" appears in the FILENAME only, never in the body.
	got := v.Indexer().Search(ws, "expenses", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "expenses") {
		t.Errorf("a filename match must rank, got %+v", paths(got))
	}
}

func TestIndexCarriesHeadingTrail(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "orthodontist", 3)
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if !strings.Contains(got[0].Heading, "Appointments") {
		t.Errorf("Heading = %q, want the section trail", got[0].Heading)
	}
}

func TestIndexDeterministicOrder(t *testing.T) {
	v, ws := seedVault(t)
	first := paths(v.Indexer().Search(ws, "booked", 5))
	for i := 0; i < 5; i++ {
		if got := paths(v.Indexer().Search(ws, "booked", 5)); strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("order changed between runs: %v vs %v", first, got)
		}
	}
}

func TestIndexPicksUpChanges(t *testing.T) {
	v, ws := seedVault(t)
	idx := v.Indexer()
	if got := idx.Search(ws, "kayaking", 5); len(got) != 0 {
		t.Fatalf("unexpected pre-existing match: %+v", paths(got))
	}
	// mtime resolution can be coarse; make the change unambiguous.
	time.Sleep(10 * time.Millisecond)
	if err := v.WriteNote(ws, "notes/new.md", []byte("# New\n\nWent kayaking on the Vardar.\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := idx.Search(ws, "kayaking", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "new.md") {
		t.Errorf("a new note must be found without a restart, got %+v", paths(got))
	}
}

func TestIndexSkipsInternalDir(t *testing.T) {
	v, ws := seedVault(t)
	sidecar := filepath.Join(v.Root(ws), InternalDir, "db-export", "x.json")
	os.MkdirAll(filepath.Dir(sidecar), 0o755)
	os.WriteFile(sidecar, []byte(`{"secret":"orthodontist"}`), 0o600)

	for _, s := range v.Indexer().Search(ws, "orthodontist", 10) {
		if strings.Contains(s.Path, InternalDir) {
			t.Errorf("internal sidecars must never be retrievable, got %q", s.Path)
		}
	}
}

func TestIndexEmptyQuery(t *testing.T) {
	v, ws := seedVault(t)
	if got := v.Indexer().Search(ws, "   ", 5); len(got) != 0 {
		t.Errorf("an empty query should return nothing, got %+v", paths(got))
	}
}

// TestIndexNonsenseQueryAgainstScaffoldVaultFindsNothing is the Finding-4
// repro: a scaffold-only vault (nothing but README.md + placeholder memory
// files created by EnsureScaffold — no user content at all) must return NO
// matches for a made-up query. The literal review repro was the query
// "zzz-nothing-here" matching README.md purely because "here" (an ordinary
// function word, in "Everything you and your agents create lives here")
// wasn't a stopword — any nonzero term overlap earned a nonzero BM25 score.
// This test would have passed by luck before the fix too (this exact query
// happens to dodge the specific missing stopword) — that's exactly Finding
// 4's point, so the case is reproduced verbatim, not paraphrased.
func TestIndexNonsenseQueryAgainstScaffoldVaultFindsNothing(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws-scaffold-only"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if got := v.Indexer().Search(ws, "zzz-nothing-here", 5); len(got) != 0 {
		t.Errorf("a nonsense query against a scaffold-only vault must find nothing, got %+v", paths(got))
	}
	// A genuine query must still find real content — the fix must not have
	// turned into a blanket "never match anything" regression.
	if err := v.WriteNote(ws, "notes/health.md", []byte("# Health\n\n## Appointments\n\nBooked an orthodontist visit for Tuesday.\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := v.Indexer().Search(ws, "dentist appointment", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "health.md") {
		t.Errorf("a genuine query must still match real content, got %+v", paths(got))
	}
}

// TestApplyScoreFloorDropsTrivialOverlap exercises applyScoreFloor directly
// (deterministic, not dependent on tuning a real corpus to land on a
// particular BM25 ratio): a result scoring well below scoreFloorFrac of the
// top result is noise and must be dropped, while one within the floor is
// kept.
func TestApplyScoreFloorDropsTrivialOverlap(t *testing.T) {
	in := []Scored{
		{Chunk: Chunk{Path: "top.md"}, Score: 10.0},
		{Chunk: Chunk{Path: "strong.md"}, Score: 4.0}, // 40% of top: kept
		{Chunk: Chunk{Path: "weak.md"}, Score: 0.5},   // 5% of top: below the 10% floor, dropped
	}
	out := applyScoreFloor(in)
	var gotPaths []string
	for _, s := range out {
		gotPaths = append(gotPaths, s.Path)
	}
	want := []string{"top.md", "strong.md"}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Errorf("applyScoreFloor(%v) = %v, want %v", in, gotPaths, want)
	}
}

// TestApplyScoreFloorAlwaysKeepsTopResult: the top result must survive even
// when it is the ONLY candidate, and even when its own absolute score is
// tiny — the floor is a fraction OF the top result, so it can never exclude
// the very result it is computed from.
func TestApplyScoreFloorAlwaysKeepsTopResult(t *testing.T) {
	in := []Scored{{Chunk: Chunk{Path: "only.md"}, Score: 0.0001}}
	out := applyScoreFloor(in)
	if len(out) != 1 || out[0].Path != "only.md" {
		t.Errorf("the sole top result must always survive filtering, got %+v", out)
	}
}

// TestApplyScoreFloorEmptyInput: filtering an empty slice is a no-op, not a panic.
func TestApplyScoreFloorEmptyInput(t *testing.T) {
	if out := applyScoreFloor(nil); len(out) != 0 {
		t.Errorf("expected no results from an empty input, got %+v", out)
	}
}

func paths(in []Scored) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Path)
	}
	return out
}

// The designer retrieves on every turn while a scheduled run can call
// search_files concurrently — same workspace, same Vault, same Indexer.
func TestIndexConcurrentSearchAndWrite(t *testing.T) {
	v, ws := seedVault(t)
	idx := v.Indexer()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); idx.Search(ws, "booked flights", 5) }()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v.WriteNote(ws, fmt.Sprintf("notes/conc%d.md", n), []byte("# C\n\nbooked something\n"))
		}(i)
	}
	wg.Wait()
}

// Two workspaces must never block each other: searching one while the other
// is being written/refreshed concurrently must not deadlock or race.
func TestIndexTwoWorkspacesConcurrent(t *testing.T) {
	root := t.TempDir()
	v := New(root)
	const wsA, wsB = "ws-a", "ws-b"
	for _, ws := range []string{wsA, wsB} {
		if err := v.EnsureScaffold(ws); err != nil {
			t.Fatalf("scaffold %s: %v", ws, err)
		}
	}
	if err := v.WriteNote(wsA, "notes/a.md", []byte("# A\n\nfacts about kayaking on the Vardar.\n")); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := v.WriteNote(wsB, "notes/b.md", []byte("# B\n\nfacts about sailing on the Adriatic.\n")); err != nil {
		t.Fatalf("write b: %v", err)
	}

	idx := v.Indexer()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Search(wsA, "kayaking", 5)
			v.WriteNote(wsA, fmt.Sprintf("notes/a%d.md", n), []byte("# A\n\nmore kayaking notes\n"))
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Search(wsB, "sailing", 5)
			v.WriteNote(wsB, fmt.Sprintf("notes/b%d.md", n), []byte("# B\n\nmore sailing notes\n"))
		}(i)
	}
	wg.Wait()

	gotA := paths(idx.Search(wsA, "kayaking", 5))
	if len(gotA) == 0 || !strings.Contains(gotA[0], "a") {
		t.Errorf("workspace A search corrupted, got %+v", gotA)
	}
	gotB := paths(idx.Search(wsB, "sailing", 5))
	if len(gotB) == 0 || !strings.Contains(gotB[0], "b") {
		t.Errorf("workspace B search corrupted, got %+v", gotB)
	}
	// Cross-contamination check: workspace A's index must never surface
	// workspace B's files and vice versa.
	for _, s := range idx.Search(wsA, "sailing", 5) {
		t.Errorf("workspace A must not see workspace B content, got %q", s.Path)
	}
}

// foldPlural must not conflate ordinary English words that end in "s" but
// aren't plurals, while still folding real plurals ("appointments" onto
// "appointment") — the entire reason the fold exists.
func TestFoldPluralDoesNotCollideOnOrdinaryWords(t *testing.T) {
	v, ws := seedVault(t)

	// "news" must not fold onto "new": a note about "today's news" must not
	// be a top hit for someone searching for a "new roof".
	if err := v.WriteNote(ws, "notes/news.md", []byte("# Market\n\nRead today's news about the market.\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := v.WriteNote(ws, "notes/roof.md", []byte("# Home\n\nGetting a new roof installed next month.\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := v.Indexer().Search(ws, "new", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "roof.md") {
		t.Errorf("query %q top hit = %+v, want roof.md (not news.md via a bad fold)", "new", paths(got))
	}

	// "lens" must not fold onto "len".
	if err := v.WriteNote(ws, "notes/camera.md", []byte("# Camera\n\nBought a new 50mm lens.\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := v.Indexer().Search(ws, "len", 5); len(got) != 0 {
		t.Errorf("query %q must not match lens.md via a bad plural fold, got %+v", "len", paths(got))
	}

	// The fold must still work for a real plural: this is the whole reason
	// foldPlural exists (see TestIndexFindsWhatLiteralSearchMisses's sibling
	// case in seedVault: notes/health.md has "## Appointments").
	got = v.Indexer().Search(ws, "appointment", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "health.md") {
		t.Errorf("query %q must still match the Appointments heading via the plural fold, got %+v", "appointment", paths(got))
	}
}

// A vault is Obsidian-style and can live inside a git working tree: neither
// .git/ nor .obsidian/ content should ever be retrievable, only the .kb case
// that was already covered.
func TestIndexSkipsAllDotDirectories(t *testing.T) {
	v, ws := seedVault(t)
	root := v.Root(ws)

	git := filepath.Join(root, ".git")
	if err := os.MkdirAll(git, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(git, "COMMIT_EDITMSG"), []byte("orthodontist secret commit message"), 0o600); err != nil {
		t.Fatalf("write COMMIT_EDITMSG: %v", err)
	}

	obsidian := filepath.Join(root, ".obsidian")
	if err := os.MkdirAll(obsidian, 0o755); err != nil {
		t.Fatalf("mkdir .obsidian: %v", err)
	}
	if err := os.WriteFile(filepath.Join(obsidian, "workspace.json"), []byte(`{"orthodontist":"config"}`), 0o600); err != nil {
		t.Fatalf("write workspace.json: %v", err)
	}

	for _, s := range v.Indexer().Search(ws, "orthodontist", 10) {
		if strings.Contains(s.Path, ".git") || strings.Contains(s.Path, ".obsidian") {
			t.Errorf("dot-directory content must never be retrievable, got %q", s.Path)
		}
	}

	// Ordinary content must still be found — the guard must not be so broad
	// it starts skipping real content.
	got := v.Indexer().Search(ws, "dentist appointment", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "health.md") {
		t.Errorf("ordinary content must still be searchable, got %+v", paths(got))
	}
}

// TestIndexOversizedFileStaysFindableByName pins Fix 1: a file over
// maxIndexFileBytes must never vanish from the index. Its content is never
// read (that's the whole point of the cap), but it must still be findable by
// filename — the "no existing notes matched this request" designer message
// is a false statement whenever this regresses.
func TestIndexOversizedFileStaysFindableByName(t *testing.T) {
	v, ws := seedVault(t)

	// A body well over the cap, containing a unique token that would ONLY be
	// findable via content indexing — it must NOT be what makes this file
	// findable, since an oversized file's body is never read into the index.
	body := "expenses-report-quarterly unique-content-token-zzqx\n" +
		strings.Repeat("filler filler filler filler filler filler filler filler\n", 90000)
	if len(body) <= maxIndexFileBytes {
		t.Fatalf("test fixture body (%d bytes) must exceed maxIndexFileBytes (%d)", len(body), maxIndexFileBytes)
	}
	if err := v.WriteNote(ws, "notes/expenses-report-quarterly.md", []byte(body)); err != nil {
		t.Fatalf("write oversized note: %v", err)
	}

	// Findable BY NAME (path/filename field boost) even though it's oversized.
	got := v.Indexer().Search(ws, "expenses report quarterly", 5)
	var found bool
	for _, s := range got {
		if strings.Contains(s.Path, "expenses-report-quarterly.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("oversized file must still be findable by name, got %+v", paths(got))
	}

	// NOT findable by a body-only token, proving the content genuinely was never
	// read (distinguishing this from a lucky path-boost false positive).
	got = v.Indexer().Search(ws, "unique-content-token-zzqx", 5)
	for _, s := range got {
		if strings.Contains(s.Path, "expenses-report-quarterly.md") {
			t.Errorf("oversized file's body must never be read into the index, but a body-only token matched it: %+v", paths(got))
		}
	}

	// A normal, well-under-cap file must still match by content exactly as before.
	got = v.Indexer().Search(ws, "dentist appointment", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "health.md") {
		t.Errorf("normal file content matching must be unaffected, got %+v", paths(got))
	}
}

func BenchmarkIndexSearchWarm(b *testing.B) {
	v := New(b.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)
	for i := 0; i < 200; i++ {
		v.WriteNote(ws, filepath.Join("notes", strings.Repeat("n", 3)+string(rune('a'+i%26))+".md"),
			[]byte("# Note\n\nsome content about budgets and travel and health\n"))
	}
	idx := v.Indexer()
	idx.Search(ws, "budgets", 5) // warm
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Search(ws, "budgets", 5)
	}
}
