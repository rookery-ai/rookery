package vault

import (
	"fmt"
	"strings"
	"testing"
)

// bigTable builds a markdown table with `rows` data rows, wide enough that
// ChunkMarkdown must split it. Shaped after the real note that prompted this
// work: a 155 KB CSV import whose rows run to ~1774 characters.
func bigTable(rows int) string {
	var b strings.Builder
	b.WriteString("| Date | Merchant | Amount |\n|---|---|---|\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "| 2026-08-%02d | Merchant number %d with a long descriptive name | %d.00 |\n",
			i%28+1, i, i)
	}
	return b.String()
}

// A chunk of bare table rows is uninterpretable — nothing in it says which
// column is the amount and which is the date. Before this, a converted CSV
// became ~100 chunks of unlabelled rows, which is why asking about the table
// returned nothing useful while asking about prose worked fine.
func TestChunkMarkdownRepeatsTableHeaders(t *testing.T) {
	chunks := ChunkMarkdown("notes/tx.md", "# Transactions\n\n"+bigTable(400))
	if len(chunks) < 2 {
		t.Fatalf("expected the table to split, got %d chunk(s)", len(chunks))
	}
	for i, c := range chunks {
		if !strings.Contains(c.Text, "| Date | Merchant | Amount |") {
			t.Errorf("chunk %d has no column headers:\n%s", i, c.Text)
		}
	}
}

// The repeated header must come out of the per-chunk budget, not be added on
// top of it: targetChunkChars is a HARD bound the byte-capped tool result
// depends on, not an aspiration.
func TestRepeatedHeadersRespectTheChunkBound(t *testing.T) {
	for _, c := range ChunkMarkdown("notes/tx.md", "# Transactions\n\n"+bigTable(400)) {
		if len(c.Text) > targetChunkChars {
			t.Errorf("chunk exceeds the hard bound: %d > %d", len(c.Text), targetChunkChars)
		}
	}
}

// Prose must be untouched. The header logic may only fire on a real table, or
// every split document grows a spurious pipe line.
func TestSplitProseGainsNoTableHeader(t *testing.T) {
	prose := strings.Repeat("This is an ordinary paragraph of prose. ", 300)
	for i, c := range ChunkMarkdown("notes/p.md", "# Notes\n\n"+prose) {
		if strings.Contains(c.Text, "|---") {
			t.Errorf("chunk %d gained a spurious table header:\n%s", i, c.Text)
		}
	}
}

// The delimiter row is what identifies a table, not the pipes: prose
// containing a pipe is common, `|---|---|` is not.
func TestTableHeaderDetection(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain table", "| A | B |\n|---|---|\n| 1 | 2 |", true},
		{"aligned table", "| A | B |\n|:--|--:|\n| 1 | 2 |", true},
		{"centred", "| A | B |\n| :---: | :---: |\n| 1 | 2 |", true},
		{"prose with a pipe", "use a | b to pipe\nand then some more text", false},
		{"one line only", "| A | B |", false},
		{"no delimiter", "| A | B |\n| 1 | 2 |", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if _, ok := tableHeader(tc.in); ok != tc.want {
			t.Errorf("%s: tableHeader ok = %v, want %v", tc.name, ok, tc.want)
		}
	}
}

// A header wide enough to starve the budget must degrade to unlabelled rows
// rather than to one row per chunk, which would be worse on both counts.
func TestPathologicallyWideHeaderDegradesGracefully(t *testing.T) {
	wide := "| " + strings.Repeat("a very long column name | ", 80) + "|\n" +
		"|" + strings.Repeat("---|", 81) + "\n"
	var b strings.Builder
	b.WriteString(wide)
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "| row %d |\n", i)
	}

	chunks := ChunkMarkdown("notes/wide.md", "# Wide\n\n"+b.String())
	for _, c := range chunks {
		if len(c.Text) > targetChunkChars {
			t.Fatalf("chunk exceeds the hard bound: %d > %d", len(c.Text), targetChunkChars)
		}
	}
	if len(chunks) > 200 {
		t.Errorf("degraded into one chunk per row: %d chunks", len(chunks))
	}
}
