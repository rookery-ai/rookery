package convert

import (
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
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1 // tolerate ragged rows rather than aborting
	r.LazyQuotes = true    // tolerate stray quotes in real-world exports
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
	for _, rec := range records {
		if len(rec) > width {
			width = len(rec)
		}
	}
	if width > len(header) {
		res.Warnings = append(res.Warnings, "some rows had more columns than the header row")
	}

	var sb strings.Builder
	writeRow(&sb, pad(header, width))
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(&sb, sep)

	body := records[1:]
	ragged := false
	for i, rec := range body {
		if i >= maxTableRows {
			omitted := len(body) - maxTableRows
			fmt.Fprintf(&sb, "\n_%d further rows omitted (%d total)._\n", omitted, len(body))
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"row limit reached: %d of %d rows are not in this note — read the preserved original for the full data",
				omitted, len(body)))
			break
		}
		if len(rec) != width {
			ragged = true
		}
		writeRow(&sb, pad(rec, width))
	}
	if ragged {
		res.Warnings = append(res.Warnings, "some rows had a different column count than the header row")
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

func pad(cells []string, width int) []string {
	if len(cells) >= width {
		return cells[:width]
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
