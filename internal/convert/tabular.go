package convert

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
)

// maxTableRows is a SAFETY VALVE against a pathological file, not a routine
// truncation. It is deliberately high: a converted note is stored on disk and
// read through paging (read_file takes offset/limit) and chunked retrieval, so
// a long table costs nothing at rest — whereas dropping rows makes them
// unsearchable and invisible to every agent, which is silent data loss on the
// file type users are most likely to compute over. Anything actually omitted is
// recorded as a Result warning, which the importer writes into the note's
// frontmatter, so a truncated note always declares itself.
const maxTableRows = 50000

// tabularToMarkdown renders delimited data as a markdown table. The first
// record becomes the header, which is what a delimited export virtually always
// means.
func tabularToMarkdown(data []byte, kind Kind, opt Options) (Result, error) {
	// Excel on Windows writes CSV/TSV with a leading UTF-8 BOM by default. If
	// left in, it becomes invisibly fused onto the first header cell (e.g.
	// header "Region" arrives as the three BOM bytes plus "Region"), and
	// header names are exactly what an agent keys, matches, or joins on — so
	// this must be stripped from the raw bytes before the csv.Reader ever
	// tokenizes them. normalizeText (detect.go)
	// also strips a leading BOM, but only on the OUTPUT string this function
	// builds later; by then the BOM (if not handled here) would be buried
	// inside a rendered cell, not leading the string, so that alone would not
	// fix this.
	data = bytes.TrimPrefix(data, []byte(utf8BOM))
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // tolerate ragged rows rather than aborting
	// LazyQuotes tolerates stray quotes in real-world exports. Trade-off: on a
	// genuinely unterminated quote it silently merges the following physical
	// row into the previous cell instead of erroring — a row-misalignment
	// corruption, not just a cosmetic escaping relaxation.
	r.LazyQuotes = true
	if kind == KindTSV {
		r.Comma = '\t'
	}
	records, err := r.ReadAll()
	if err != nil {
		return Result{}, fmt.Errorf("convert: parse %s: %w", kind, err)
	}
	records = dropEmptyRecords(records)
	if len(records) == 0 {
		return Result{}, fmt.Errorf("convert: %s contained no rows", kind)
	}

	res := Result{Kind: kind, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	header := records[0]
	width := len(header)
	ragged := false
	for _, rec := range records[1:] {
		if len(rec) != len(header) {
			// A row longer OR shorter than the header is the same underlying
			// situation from an agent's perspective ("this table isn't
			// rectangular"), so it earns exactly one warning below. Comparing
			// against len(header) rather than the running width avoids the
			// previous double-report, where one wide row grew width and then
			// every OTHER row — including ones that matched the header
			// exactly — tripped a second "ragged" check against that new,
			// larger width.
			ragged = true
		}
		if len(rec) > width {
			width = len(rec)
		}
	}
	if ragged {
		res.Warnings = append(res.Warnings, "some rows had a different column count than the header row")
	}

	var sb strings.Builder
	writeRow(&sb, pad(header, width))
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(&sb, sep)

	body := records[1:]
	for i, rec := range body {
		if i >= maxTableRows {
			omitted := len(body) - maxTableRows
			fmt.Fprintf(&sb, "\n_%d further rows omitted (%d total)._\n", omitted, len(body))
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"row limit reached: %d of %d rows are not in this note — read the preserved original for the full data",
				omitted, len(body)))
			break
		}
		writeRow(&sb, pad(rec, width))
	}
	res.Markdown = normalizeText(sb.String())
	return res, nil
}

func writeRow(sb *strings.Builder, cells []string) {
	escaped := make([]string, len(cells))
	for i, c := range cells {
		escaped[i] = escapeCell(c)
	}
	sb.WriteString("| " + strings.Join(escaped, " | ") + " |\n")
}

// escapeCell makes a value safe inside a markdown table: a literal pipe would
// otherwise split the cell, and an embedded newline would break the row.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

// pad grows cells to width by appending empty strings. It must be grow-only:
// padding must never remove a value, because a dropped cell is unsearchable
// and invisible to every agent that reads the converted note back. The single
// current caller happens to always pass the true maximum width across all
// records, so len(cells) > width never occurs today — but nothing enforces
// that precondition on a future caller (e.g. a fixed display width), so the
// truncating branch this replaced was one careless call away from silent data
// loss.
func pad(cells []string, width int) []string {
	if len(cells) >= width {
		return cells
	}
	out := make([]string, width)
	copy(out, cells)
	return out
}

func dropEmptyRecords(records [][]string) [][]string {
	out := records[:0]
	for _, rec := range records {
		for _, c := range rec {
			if strings.TrimSpace(c) != "" {
				out = append(out, rec)
				break
			}
		}
	}
	return out
}
