package vault

import (
	"strings"
	"testing"
)

const txNote = `# Transactions

| Date | Merchant | Amount |
|---|---|---|
| 2026-08-01 | Kaufland Skopje | 1240.00 |
| 2026-08-02 | Neptun | 8990.00 |
`

// A 200-byte cut through a converted-CSV row shows about a tenth of it, mid
// cell, with no column names — so the model cannot tell an amount from a date.
// Carrying the header is what makes a table hit mean anything.
func TestSnippetForCarriesTableHeaders(t *testing.T) {
	got := snippetFor(txNote, 5, "| 2026-08-01 | Kaufland Skopje | 1240.00 |")
	for _, want := range []string{"Date", "Merchant", "Amount", "Kaufland Skopje", "1240.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet is missing %q:\n%s", want, got)
		}
	}
}

// The header belongs to the table the hit is IN. A note with two tables must
// not label a row with the other one's columns — that is worse than no header,
// because it reads as authoritative.
func TestSnippetForUsesTheEnclosingTablesHeader(t *testing.T) {
	note := txNote + `
## Budget

| Category | Limit |
|---|---|
| Food | 20000 |
`
	// "| Food | 20000 |" is the last line of the second table.
	lines := strings.Split(note, "\n")
	var lineNo int
	for i, l := range lines {
		if strings.Contains(l, "Food") {
			lineNo = i + 1
		}
	}
	got := snippetFor(note, lineNo, "| Food | 20000 |")
	if !strings.Contains(got, "Category") {
		t.Errorf("did not use the enclosing table's header:\n%s", got)
	}
	if strings.Contains(got, "Merchant") {
		t.Errorf("labelled the row with a DIFFERENT table's header:\n%s", got)
	}
}

// Prose must not grow a header, and must keep its existing budget: raising the
// cap for every hit would spend the shared byte budget on fewer results.
func TestSnippetForLeavesProseAlone(t *testing.T) {
	note := "# Notes\n\nI saw the dentist on Tuesday and it went fine.\n"
	got := snippetFor(note, 3, "I saw the dentist on Tuesday and it went fine.")
	if got != "I saw the dentist on Tuesday and it went fine." {
		t.Errorf("prose snippet was altered: %q", got)
	}
	long := strings.Repeat("word ", 200)
	if n := len(snippetFor("x\n"+long, 2, long)); n > snippetMax+8 {
		t.Errorf("prose snippet exceeded its budget: %d", n)
	}
}

// The block constructs the KB editor produces are structure, not content. A hit
// on one should return the readable text, never the raw HTML wrapper.
func TestSnippetForUnwrapsSlashMenuConstructs(t *testing.T) {
	cases := []struct {
		name, line, want, absent string
	}{
		{"callout", "> [!note] Remember the dentist", "Remember the dentist", "[!note]"},
		{"toggle summary", "<summary>Trip checklist</summary>", "Trip checklist", "<summary>"},
		{"columns wrapper", `<div data-cols="2">`, "", "data-cols"},
		{"alignment wrapper", `<div align="center">`, "", "align"},
		{"closing div", "</div>", "", "div"},
	}
	for _, tc := range cases {
		got := snippetFor(tc.line, 1, tc.line)
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: want %q in %q", tc.name, tc.want, got)
		}
		if strings.Contains(got, tc.absent) {
			t.Errorf("%s: raw markup %q leaked into %q", tc.name, tc.absent, got)
		}
	}
}

// Images are excluded per the request: an image's path and alt text are not
// content someone searching their notes is looking for.
func TestSnippetForExcludesImages(t *testing.T) {
	line := "![a diagram|420](img/x.png) and some real text"
	got := snippetFor(line, 1, line)
	if strings.Contains(got, "img/x.png") {
		t.Errorf("image path leaked into a snippet: %q", got)
	}
	if !strings.Contains(got, "some real text") {
		t.Errorf("dropped the prose beside the image: %q", got)
	}
}

// A cut must land on a rune boundary — this operator's notes are routinely
// Cyrillic, and a raw byte cut corrupts the last character rather than merely
// shortening the text.
func TestSnippetForCutsOnRuneBoundaries(t *testing.T) {
	line := strings.Repeat("СМЕТКА ", 400)
	got := snippetFor(line, 1, line)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected a truncated snippet, got %d bytes", len(got))
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("cut landed mid-rune: %q", got)
		}
	}
}
