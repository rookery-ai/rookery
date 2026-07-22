package vault

import (
	"fmt"
	"os"
	"path/filepath"
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
