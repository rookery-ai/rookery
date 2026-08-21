package vault

import (
	"fmt"
	"strings"
	"testing"
)

// lopsidedTable is shaped after the real note that prompted this work: a table
// whose bulk is ONE junk column. A fixture with evenly-sized columns would pass
// a warning that never fires, which is precisely the mistake that let the
// original bug through the previous round of table work.
func lopsidedTable(rows int) string {
	var b strings.Builder
	b.WriteString("# Transactions\n\n*Converted from card-transactions.csv.*\n\n")
	b.WriteString("| date | merchantName | USDAmount | apiTransaction |\n")
	b.WriteString("|---|---|---|---|\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "| 2026-08-%02d | Merchant %d | %d.00 | %s |\n",
			i%28+1, i, i, strings.Repeat("x", 1200))
	}
	return b.String()
}

func TestMapFileFlagsADisproportionateColumn(t *testing.T) {
	v := New(t.TempDir())
	const ws = "u1"
	mustWrite(t, v, ws, "notes/tx.md", lopsidedTable(98))

	shape, err := MapFile(v, ws, "notes/tx.md")
	if err != nil {
		t.Fatalf("MapFile: %v", err)
	}
	if shape.Kind != "table" {
		t.Errorf("Kind = %q, want table", shape.Kind)
	}
	if shape.Rows != 98 {
		t.Errorf("Rows = %d, want 98", shape.Rows)
	}
	if len(shape.Columns) != 4 {
		t.Fatalf("Columns = %d, want 4: %+v", len(shape.Columns), shape.Columns)
	}

	warn := strings.Join(shape.Warnings, "\n")
	if !strings.Contains(warn, "apiTransaction") {
		t.Errorf("the dominant column was not flagged: %v", shape.Warnings)
	}
	// A warning that fires on everything tells the reader nothing.
	if strings.Contains(warn, "USDAmount") {
		t.Errorf("a small column was wrongly flagged: %v", shape.Warnings)
	}
}

// Prose gets an outline instead of columns — one call, shape decided by content.
func TestMapFileOutlinesProse(t *testing.T) {
	v := New(t.TempDir())
	const ws = "u1"
	doc := "# Trip\n\nintro\n\n## Flights\n\n" + strings.Repeat("detail. ", 400) +
		"\n\n## Hotels\n\nshort\n"
	mustWrite(t, v, ws, "notes/trip.md", doc)

	shape, err := MapFile(v, ws, "notes/trip.md")
	if err != nil {
		t.Fatalf("MapFile: %v", err)
	}
	if shape.Kind != "prose" {
		t.Errorf("Kind = %q, want prose", shape.Kind)
	}
	if len(shape.Sections) < 3 {
		t.Fatalf("outline too thin: %+v", shape.Sections)
	}
	var found bool
	for _, s := range shape.Sections {
		if strings.Contains(s.Heading, "Flights") {
			found = true
		}
	}
	if !found {
		t.Errorf("Flights section missing from the outline: %+v", shape.Sections)
	}
}

// The rendered map is itself a tool result and must respect the cap. A
// 500-heading document must not produce 500 lines, and the truncation must say
// so — a silently short outline reads as a short document.
func TestRenderedMapRespectsTheByteCap(t *testing.T) {
	v := New(t.TempDir())
	const ws = "u1"
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "## Section %d\n\nsome text here\n\n", i)
	}
	mustWrite(t, v, ws, "notes/many.md", b.String())

	shape, err := MapFile(v, ws, "notes/many.md")
	if err != nil {
		t.Fatalf("MapFile: %v", err)
	}
	out := shape.Render(8192)
	if len(out) > 8192 {
		t.Errorf("rendered map is %d bytes, over the 8192 cap", len(out))
	}
	if !strings.Contains(out, "more") {
		t.Errorf("truncated without saying how many were omitted:\n%s", out)
	}
}

// The rendered map is what the model actually reads, so the numbers that drive
// its strategy have to be in it.
func TestRenderedMapNamesTheCostAndTheWarning(t *testing.T) {
	v := New(t.TempDir())
	const ws = "u1"
	mustWrite(t, v, ws, "notes/tx.md", lopsidedTable(98))

	shape, _ := MapFile(v, ws, "notes/tx.md")
	out := shape.Render(8192)

	for _, want := range []string{"98", "apiTransaction", "USDAmount", "table"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered map omits %q:\n%s", want, out)
		}
	}
}

// A small file needs no strategy advice — the warning machinery must not fire
// on an ordinary note.
func TestMapFileOnASmallNoteWarnsAboutNothing(t *testing.T) {
	v := New(t.TempDir())
	const ws = "u1"
	mustWrite(t, v, ws, "notes/small.md", "# Note\n\njust a couple of lines\n")

	shape, err := MapFile(v, ws, "notes/small.md")
	if err != nil {
		t.Fatalf("MapFile: %v", err)
	}
	if len(shape.Warnings) != 0 {
		t.Errorf("warned about a small note: %v", shape.Warnings)
	}
}

// A path outside the vault must be refused by the same primitive every other
// read path uses, not by a second ad-hoc check.
func TestMapFileRejectsAnEscapingPath(t *testing.T) {
	v := New(t.TempDir())
	if _, err := MapFile(v, "u1", "../../etc/passwd"); err == nil {
		t.Error("an escaping path was accepted")
	}
}
