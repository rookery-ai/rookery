package convert

import (
	"fmt"
	"strings"
	"testing"
)

func TestCSVToMarkdown(t *testing.T) {
	csv := "Region,Sales,Notes\nEMEA,120,\"grew, fast\"\nAPAC,98,flat\n"
	got, err := ToMarkdown([]byte(csv), Options{Filename: "q3-sales.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindCSV {
		t.Errorf("Kind = %q", got.Kind)
	}
	if got.Title != "q3 sales" {
		t.Errorf("Title = %q, want %q", got.Title, "q3 sales")
	}
	for _, want := range []string{
		"| Region | Sales | Notes |",
		"| --- | --- | --- |",
		"| EMEA | 120 | grew, fast |",
		"| APAC | 98 | flat |",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestTSVToMarkdown(t *testing.T) {
	got, err := ToMarkdown([]byte("a\tb\n1\t2\n"), Options{Filename: "x.tsv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "| a | b |") || !strings.Contains(got.Markdown, "| 1 | 2 |") {
		t.Errorf("unexpected tsv output:\n%s", got.Markdown)
	}
}

func TestCSVEscapesPipes(t *testing.T) {
	got, err := ToMarkdown([]byte("a,b\nx|y,z\n"), Options{Filename: "p.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, `x\|y`) {
		t.Errorf("a pipe in a cell must be escaped or it breaks the table, got:\n%s", got.Markdown)
	}
}

func TestCSVRaggedRows(t *testing.T) {
	// Real exports have ragged rows; they must not abort the conversion.
	got, err := ToMarkdown([]byte("a,b,c\n1,2\n3,4,5,6\n"), Options{Filename: "r.csv"})
	if err != nil {
		t.Fatalf("ragged rows must not fail: %v", err)
	}
	if !strings.Contains(got.Markdown, "| 1 | 2 |") {
		t.Errorf("short row should be padded, got:\n%s", got.Markdown)
	}
	if len(got.Warnings) == 0 {
		t.Error("ragged rows should be recorded as a warning")
	}
}

// A realistically large export must survive INTACT. Silently dropping rows makes
// them unsearchable and invisible to agents, so the row cap is a safety valve
// far above real-world sizes, not a routine truncation.
func TestCSVLargeSurvivesIntact(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("id,value\n")
	const rows = 5000
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&sb, "%d,v%d\n", i, i)
	}
	got, err := ToMarkdown([]byte(sb.String()), Options{Filename: "big.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.Contains(got.Markdown, "rows omitted") {
		t.Errorf("a %d-row csv must not be truncated", rows)
	}
	if !strings.Contains(got.Markdown, "| 4999 | v4999 |") {
		t.Error("the last row must be present and searchable")
	}
	if len(got.Warnings) != 0 {
		t.Errorf("no warning expected for a normal-sized file, got %v", got.Warnings)
	}
}

// Beyond the safety valve, truncation must announce itself in BOTH the body and
// Result.Warnings — the importer writes warnings into the note's frontmatter, so
// a truncated note declares itself rather than looking complete.
func TestCSVBeyondCapWarnsInBodyAndWarnings(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("id,value\n")
	for i := 0; i < maxTableRows+50; i++ {
		fmt.Fprintf(&sb, "%d,v%d\n", i, i)
	}
	got, err := ToMarkdown([]byte(sb.String()), Options{Filename: "huge.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "rows omitted") {
		t.Error("truncation must be stated in the body")
	}
	if len(got.Warnings) == 0 {
		t.Error("truncation must also surface as a Result warning, or the note's frontmatter will not declare it")
	}
}

func TestCSVEmptyIsError(t *testing.T) {
	if _, err := ToMarkdown([]byte("\n"), Options{Filename: "empty.csv"}); err == nil {
		t.Error("an empty csv must error rather than produce a blank note")
	}
}
