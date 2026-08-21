package vault

import (
	"fmt"
	"sort"
	"strings"
)

// This file answers one question a model could not previously ask: "what is in
// this file, and what will it cost me to read it?"
//
// Without it the only way to approach a file over the tool-result cap is
// read_file from byte 0, page after page. That is how a chat turn on a 155 KB
// converted CSV died: 88% of the file was one column of raw JSON payloads the
// question never needed, the model spent its turn budget hauling it into
// context, and returned nothing. The nine columns that answered the question
// were 8.3 KB.
//
// The map is deliberately cheap and structural. It does not summarise content —
// that would need the model it is trying to save.

// dominantShare is the fraction of a file's bytes a single column or section
// must hold before it is worth warning about.
//
// 40% is a judgement: below it, reading the whole file is a defensible
// strategy; above it, that one part effectively IS the file, and selecting it
// blindly is what exhausts the context. Set it much lower and the warning fires
// on ordinary files, at which point a model learns to ignore it.
const dominantShare = 0.40

// bytesPerToken is the rough divisor used to quote a reading cost. Deliberately
// approximate: the number exists to convey "this is expensive" to a model
// deciding on a strategy, not to budget a request precisely.
const bytesPerToken = 4

// ColumnStat is one column of a table and how much of the file it occupies.
type ColumnStat struct {
	Name  string
	Bytes int
	Share float64 // of the total cell bytes, 0..1
}

// SectionStat is one heading in a document and the size of its body.
type SectionStat struct {
	Heading string
	Bytes   int
	Line    int
}

// FileShape is a structural description of one vault file.
type FileShape struct {
	Path     string
	Kind     string // "table" | "prose" | "code" | "other"
	Bytes    int
	Tokens   int
	Rows     int          // tables only
	Columns  []ColumnStat // tables only, largest first
	Sections []SectionStat
	Warnings []string
}

// MapFile describes a file's shape without reading it into a model's context.
//
// Path safety goes through Vault.ReadNote, which resolves via the same
// Resolve primitive every other read path uses — deliberately not a second
// ad-hoc check that could drift from it.
func MapFile(v *Vault, workspaceID, rel string) (FileShape, error) {
	data, err := v.ReadNote(workspaceID, rel)
	if err != nil {
		return FileShape{}, err
	}
	content := string(data)
	shape := FileShape{
		Path:   rel,
		Bytes:  len(data),
		Tokens: len(data) / bytesPerToken,
		Kind:   "other",
	}

	if tbl, err := ParseTable(content); err == nil && len(tbl.Rows) > 0 {
		shape.Kind = "table"
		shape.Rows = len(tbl.Rows)
		shape.Columns = columnStats(tbl)
		for _, c := range shape.Columns {
			if c.Share >= dominantShare {
				shape.Warnings = append(shape.Warnings, fmt.Sprintf(
					"%s holds %.0f%% of this file's bytes (%s). Selecting it will exhaust your context.",
					c.Name, c.Share*100, humanBytes(c.Bytes)))
			}
		}
		return shape, nil
	}

	// Sections come from ChunkMarkdown's heading trail rather than a second
	// heading parser, so an outline and a retrieved chunk can never disagree
	// about where a section begins.
	shape.Sections = sectionStats(rel, content)
	if len(shape.Sections) > 0 {
		shape.Kind = "prose"
		total := 0
		for _, s := range shape.Sections {
			total += s.Bytes
		}
		for _, s := range shape.Sections {
			if total > 0 && float64(s.Bytes)/float64(total) >= dominantShare && len(shape.Sections) > 1 {
				shape.Warnings = append(shape.Warnings, fmt.Sprintf(
					"the %q section is %s of this file — read it on its own rather than the whole document.",
					s.Heading, humanBytes(s.Bytes)))
			}
		}
	}
	return shape, nil
}

// columnStats totals each column's cell bytes, largest first. The share is of
// CELL bytes rather than file bytes so headers and delimiters do not dilute it.
func columnStats(t Table) []ColumnStat {
	totals := make([]int, len(t.Columns))
	grand := 0
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(totals) {
				totals[i] += len(cell)
				grand += len(cell)
			}
		}
	}
	out := make([]ColumnStat, 0, len(t.Columns))
	for i, name := range t.Columns {
		share := 0.0
		if grand > 0 {
			share = float64(totals[i]) / float64(grand)
		}
		out = append(out, ColumnStat{Name: name, Bytes: totals[i], Share: share})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

func sectionStats(rel, content string) []SectionStat {
	var out []SectionStat
	seen := map[string]int{}
	for _, c := range ChunkMarkdown(rel, content) {
		if c.Heading == "" {
			continue
		}
		// A section split across several chunks is one section to a reader.
		if idx, ok := seen[c.Heading]; ok {
			out[idx].Bytes += len(c.Text)
			continue
		}
		seen[c.Heading] = len(out)
		out = append(out, SectionStat{Heading: c.Heading, Bytes: len(c.Text), Line: c.Line})
	}
	return out
}

// Render turns the shape into the text a model reads, bounded by maxBytes.
//
// Truncation is always announced. An outline that silently stops reads as a
// short document, which would send the model back to blind paging — the exact
// behaviour this call exists to prevent.
func (f FileShape) Render(maxBytes int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s, %s (~%d tokens)\n", f.Path, f.Kind, humanBytes(f.Bytes), f.Tokens)
	if f.Kind == "table" {
		fmt.Fprintf(&b, "%d rows × %d columns\n\n", f.Rows, len(f.Columns))
	} else {
		b.WriteString("\n")
	}

	for _, w := range f.Warnings {
		fmt.Fprintf(&b, "⚠ %s\n", w)
	}
	if len(f.Warnings) > 0 {
		b.WriteString("\n")
	}

	// Largest first: if the budget runs out, what survives is what matters most
	// to a reading strategy.
	if len(f.Columns) > 0 {
		b.WriteString("columns (largest first):\n")
		written := 0
		for i, c := range f.Columns {
			line := fmt.Sprintf("  %-24s %8s  %4.1f%%\n", c.Name, humanBytes(c.Bytes), c.Share*100)
			if b.Len()+len(line) > maxBytes-budgetTail {
				fmt.Fprintf(&b, "  …and %d more columns\n", len(f.Columns)-written)
				break
			}
			b.WriteString(line)
			written = i + 1
		}
	}
	if len(f.Sections) > 0 {
		b.WriteString("sections:\n")
		written := 0
		for i, s := range f.Sections {
			line := fmt.Sprintf("  %-40s %8s  (line %d)\n", truncateRunesVault(s.Heading, 40), humanBytes(s.Bytes), s.Line)
			if b.Len()+len(line) > maxBytes-budgetTail {
				fmt.Fprintf(&b, "  …and %d more sections\n", len(f.Sections)-written)
				break
			}
			b.WriteString(line)
			written = i + 1
		}
	}

	out := b.String()
	if len(out) > maxBytes {
		out = out[:runeSafeCut(out, maxBytes-1)] + "…"
	}
	return out
}

// budgetTail reserves room for the "…and N more" line so the truncation notice
// itself can never be what pushes the result over the cap.
const budgetTail = 64

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func truncateRunesVault(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:runeSafeCut(s, max-1)] + "…"
}
