package vault

import (
	"fmt"
	"sort"
	"strings"
)

// The model-facing edges of the big-file tools: fetching one section, choosing
// a sane default projection, and rendering a query result.
//
// They live here rather than in internal/coder so they are testable without a
// coder, matching how SearchKB is arranged.

// SectionOf returns the body of one heading.
//
// Headings are matched against ChunkMarkdown's trail, so what this returns and
// what retrieval indexes are the same unit — an outline that pointed somewhere
// retrieval disagreed with would be worse than no outline.
//
// Matching is case-insensitive and accepts either the full trail
// ("Trip > Flights") or just the leaf ("Flights"), because a model reading an
// outline will type back whichever it saw.
func SectionOf(path, content, heading string) (string, error) {
	want := strings.ToLower(strings.TrimSpace(heading))
	if want == "" {
		return "", fmt.Errorf("section is required")
	}
	var parts []string
	var available []string
	for _, c := range ChunkMarkdown(path, content) {
		if c.Heading == "" {
			continue
		}
		available = append(available, c.Heading)
		trail := strings.ToLower(c.Heading)
		leaf := trail
		if i := strings.LastIndex(trail, ">"); i >= 0 {
			leaf = strings.TrimSpace(trail[i+1:])
		}
		if trail == want || leaf == want {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 {
		// Naming what IS there turns a dead end into a next step; the model
		// otherwise has to guess or fall back to paging.
		return "", fmt.Errorf("no section %q in %s; available: %s",
			heading, path, strings.Join(dedupe(available), ", "))
	}
	// A long section arrives as several chunks; the reader wants one section.
	return strings.Join(parts, "\n\n"), nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) > 20 {
		out = append(out[:20], fmt.Sprintf("…and %d more", len(in)-20))
	}
	return out
}

// ModestColumns is the default projection: every column except any that alone
// dominates the table's bytes.
//
// This is what keeps the most obvious call — kb_table_query with nothing but a
// path — from dragging a 131 KB JSON column back into the context and
// reproducing the failure the tool was built to fix.
func ModestColumns(t Table) []string {
	stats := columnStats(t)
	drop := map[string]bool{}
	for _, c := range stats {
		if c.Share >= dominantShare {
			drop[c.Name] = true
		}
	}
	if len(drop) == 0 {
		return nil // nil means "all columns" to Query
	}
	out := make([]string, 0, len(t.Columns))
	for _, name := range t.Columns {
		if !drop[name] {
			out = append(out, name)
		}
	}
	return out
}

// RenderQueryResult formats a result as a markdown table, bounded by maxBytes.
//
// Notes are emitted FIRST and never truncated away: they carry the count of
// rows a total could not include, and a number whose caveat was dropped is a
// number the reader will trust wrongly.
func RenderQueryResult(res QueryResult, maxBytes int) string {
	var b strings.Builder
	for _, n := range res.Notes {
		fmt.Fprintf(&b, "⚠ %s\n", n)
	}
	if len(res.Notes) > 0 {
		b.WriteString("\n")
	}
	if len(res.Rows) == 0 {
		// Non-error: "nothing matched" is a normal outcome, and an `error:`
		// string would trip the engine's oscillation guard.
		b.WriteString("(no rows matched)")
		return b.String()
	}

	widths := make([]int, len(res.Columns))
	for i, c := range res.Columns {
		widths[i] = len(c)
	}
	for _, row := range res.Rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	writeRow := func(cells []string) string {
		var line strings.Builder
		line.WriteString("|")
		for i, c := range cells {
			w := 0
			if i < len(widths) {
				w = widths[i]
			}
			fmt.Fprintf(&line, " %-*s |", w, c)
		}
		line.WriteString("\n")
		return line.String()
	}

	b.WriteString(writeRow(res.Columns))
	sep := make([]string, len(res.Columns))
	for i := range sep {
		sep[i] = strings.Repeat("-", widths[i])
	}
	b.WriteString(writeRow(sep))

	written := 0
	for i, row := range res.Rows {
		line := writeRow(row)
		if b.Len()+len(line) > maxBytes-budgetTail {
			fmt.Fprintf(&b, "…and %d more rows. %s\n", len(res.Rows)-written, truncationAdvice(res.Columns))
			break
		}
		b.WriteString(line)
		written = i + 1
	}
	fmt.Fprintf(&b, "\n%d of %d row(s)", written, len(res.Rows))
	return b.String()
}

// wideResult is the column count past which "you are asking for too many
// columns" is the likelier reason a result did not fit than "you are asking for
// too many rows". Six is a judgement: a question about a table rarely needs more
// than a handful of fields, and a converted CSV routinely has fifteen or more.
const wideResult = 6

// truncationAdvice names the lever that will actually help.
//
// It used to say "narrow the query with where or limit" unconditionally, which
// is precisely wrong for the commonest agent task: enumerating every row to
// decide which are new. Told to return fewer rows when it needs all of them, an
// agent queries again, and again, accumulating results until its context is
// gone. That is not hypothetical — an agent watching a 98-row table spent
// 148,574 prompt tokens doing it and then produced nothing.
//
// On the file that caused it, the default projection kept 17 of 18 columns and
// fitted 23 rows. Four columns fit all 98. So the lever was never the row count.
func truncationAdvice(columns []string) string {
	if len(columns) > wideResult {
		return fmt.Sprintf(
			"This result has %d columns, which is what filled the budget — ask for only the "+
				"columns you need (select: [...]) and the rows will fit. Use where or limit only "+
				"if you genuinely want fewer rows.", len(columns))
	}
	return "Narrow the query with where or limit, or aggregate with group_by/op instead of " +
		"listing rows."
}

// sortedColumnNames is used by tests and by callers that want a stable listing.
func sortedColumnNames(t Table) []string {
	out := append([]string(nil), t.Columns...)
	sort.Strings(out)
	return out
}
