package vault

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// scopeFixture puts the query term in two files, with 20 hits in the target so
// the per-file cap is observable rather than incidental.
func scopeFixture(t *testing.T) (*Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	const ws = "u1"
	var b strings.Builder
	b.WriteString("# Target\n\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "line %d mentions dentist here\n", i)
	}
	mustWrite(t, v, ws, "notes/target.md", b.String())
	mustWrite(t, v, ws, "notes/other.md", "# Other\n\ndentist appears here too\n")
	return v, ws
}

// A search scoped to one file must not answer from another. This is the whole
// point of the scope, and the failure is silent: the caller gets plausible
// passages from a file it did not ask about.
func TestSearchInIsScopedToOneFile(t *testing.T) {
	v, ws := scopeFixture(t)
	hits, err := v.NewSearcher().SearchIn(context.Background(), ws, "dentist", "notes/target.md")
	if err != nil {
		t.Fatalf("SearchIn: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits at all")
	}
	for _, h := range hits {
		if h.Path != "notes/target.md" {
			t.Errorf("hit leaked from %s", h.Path)
		}
	}
}

// Both exact searchers hardcode 5 matches per file (--max-count 5 in ripgrep,
// count >= 5 in the Go walk). That is right for a vault-wide search — one file
// must not dominate — and exactly wrong when the caller named a single file,
// where five hits is not a search. The whole-vault cap must stay.
func TestSearchInRaisesThePerFileCap(t *testing.T) {
	v, ws := scopeFixture(t)
	s := v.NewSearcher()

	scoped, err := s.SearchIn(context.Background(), ws, "dentist", "notes/target.md")
	if err != nil {
		t.Fatalf("SearchIn: %v", err)
	}
	if len(scoped) <= 5 {
		t.Errorf("scoped search returned %d hits, want more than the whole-vault cap of 5", len(scoped))
	}

	wide, err := s.Search(context.Background(), ws, "dentist")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	inTarget := 0
	for _, h := range wide {
		if h.Path == "notes/target.md" {
			inTarget++
		}
	}
	if inTarget > 5 {
		t.Errorf("whole-vault search returned %d hits from one file, cap is 5", inTarget)
	}
}

// Search tries ripgrep and silently falls through to searchGo on ANY rg error,
// so these are not two different hosts — they are the same host on consecutive
// calls. Divergence therefore surfaces as nondeterminism, which is far harder
// to diagnose than a host-level difference. Nothing compared them before this.
func TestScopedSearchAgreesAcrossBothImplementations(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed on this host")
	}
	v, ws := scopeFixture(t)
	s := &ripgrepSearcher{v: v}

	rgHits, err := s.searchRipgrepIn(context.Background(), v.Root(ws), ws, "dentist", "notes/target.md")
	if err != nil {
		t.Fatalf("searchRipgrepIn: %v", err)
	}
	goHits, err := s.searchGoIn(ws, "dentist", "notes/target.md")
	if err != nil {
		t.Fatalf("searchGoIn: %v", err)
	}
	if len(rgHits) != len(goHits) {
		t.Fatalf("hit counts differ: ripgrep=%d go=%d", len(rgHits), len(goHits))
	}
	for i := range rgHits {
		if rgHits[i].Path != goHits[i].Path || rgHits[i].Line != goHits[i].Line {
			t.Errorf("hit %d differs: rg=%+v go=%+v", i, rgHits[i], goHits[i])
		}
	}
}

// The ranked pass filters prefixes OUT and has no include. Scoping only the
// exact pass would leave SearchKB's ranked half answering from the whole vault.
func TestSearchWithinScopesTheRankedPass(t *testing.T) {
	v, ws := scopeFixture(t)
	scored := v.Indexer().SearchWithin(ws, "dentist", 10, "notes/target.md")
	if len(scored) == 0 {
		t.Fatal("no ranked passages for a file that plainly matches")
	}
	for _, s := range scored {
		if s.Chunk.Path != "notes/target.md" {
			t.Errorf("ranked hit leaked from %s", s.Chunk.Path)
		}
	}
}

// The two-pass renderer must carry the scope through BOTH passes.
func TestSearchKBInMentionsOnlyTheScopedFile(t *testing.T) {
	v, ws := scopeFixture(t)
	out := SearchKBIn(context.Background(), v, v.NewSearcher(), ws, "dentist", "notes/target.md", 8192)
	if strings.Contains(out, "notes/other.md") {
		t.Errorf("scoped search named another file:\n%s", out)
	}
	if !strings.Contains(out, "notes/target.md") {
		t.Errorf("scoped search did not name the file it searched:\n%s", out)
	}
}
