package convert

import (
	"regexp"
	"strings"
)

// Recovering tables from pdftotext's -layout output.
//
// runPdftotext passes -layout specifically to keep columns and tables readable:
// poppler then pads each line with spaces so that columns line up as they do on
// the page. paragraphize immediately undid that, joining every line of a block
// with a single space — so a table arrived in the knowledge base as one run-on
// paragraph with the numbers interleaved, which is worse than useless because it
// still LOOKS like prose. The layout information was being computed and thrown
// away one function later.
//
// The recovery is deliberately conservative. A false positive turns prose into a
// nonsense table, which is harder to read and harder to repair than the run-on
// paragraph it replaced, so every rule below is a reason to DECLINE.

// colGapRE matches the run of two or more spaces that -layout uses to separate
// columns. A single space is ordinary word spacing and never a column boundary.
var colGapRE = regexp.MustCompile(` {2,}`)

const (
	// minTableRows is a header plus two data rows. Two lines that happen to
	// share a gap are common in ordinary prose (an indented continuation, a
	// signature block); three aligned rows are not.
	minTableRows = 3
	// minTableCols — a "table" of one column is just text.
	minTableCols = 2
	// maxTableCols guards against a line of scattered single words being read as
	// a many-column table.
	maxTableCols = 12
)

// looksTabular reports whether a block of -layout lines is a table, and returns
// the split cells when it is.
//
// Requiring a CONSISTENT column count across every line is what makes this safe.
// Ragged prose produces a different count on nearly every line, so it fails
// immediately; a real table produces the same count throughout because poppler
// padded it to.
func looksTabular(lines []string) ([][]string, bool) {
	if len(lines) < minTableRows {
		return nil, false
	}
	var rows [][]string
	width := -1
	for _, ln := range lines {
		// Leading indentation is not a column gap — a whole indented block
		// would otherwise read as an empty first column on every row.
		cells := colGapRE.Split(strings.TrimSpace(ln), -1)
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) < minTableCols || len(cells) > maxTableCols {
			return nil, false
		}
		if width == -1 {
			width = len(cells)
		} else if len(cells) != width {
			return nil, false
		}
		rows = append(rows, cells)
	}
	// A block where every cell in a column is empty is padding, not data.
	if !hasContent(rows) {
		return nil, false
	}
	return rows, true
}

// hasContent reports whether the candidate rows carry any non-empty cell beyond
// the first column. A block whose every line is "word<gap>" splits into a real
// first cell and an empty second one on every row, which satisfies the column
// count without being a table.
func hasContent(rows [][]string) bool {
	for _, r := range rows {
		for _, c := range r[1:] {
			if c != "" {
				return true
			}
		}
	}
	return false
}

// tabularBlock renders recovered rows as a markdown table, treating the first
// row as the header — which is what a -layout table's first line almost always
// is, and what markdown requires in any case (there is no way to express a
// headerless table).
//
// Cells go through escapeCell like any other table content, so a pipe or an
// angle bracket in the PDF cannot break the row or the editor round trip.
func tabularBlock(rows [][]string) string {
	var sb strings.Builder
	writeRow(&sb, rows[0])
	sep := make([]string, len(rows[0]))
	for i := range sep {
		sep[i] = "---"
	}
	sb.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, r := range rows[1:] {
		writeRow(&sb, r)
	}
	return strings.TrimRight(sb.String(), "\n")
}
