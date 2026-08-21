package vault

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Arithmetic over a markdown table, done in Go so a model never has to do it in
// its head.
//
// Two decisions are worth stating because both have an obvious-looking
// alternative that is wrong for this product.
//
// The model fills PARAMETERS, it does not write SQL. Writing SQL requires valid
// syntax and exact column names — including names with spaces, which need
// quoting — and this platform runs small, fast models where that is precisely
// the failure mode. Every malformed query also costs a turn from the budget
// that is usually already the problem. Filling a JSON schema is what a
// function-calling model is trained to do, and an invalid value becomes a
// precise validation error rather than a syntax error it must debug blind.
//
// And because the interface is fixed parameters, there is no database here.
// Loading the rows into SQLite would mean generating SQL from those parameters
// anyway — sanitising identifiers, inferring types, building statements — which
// is more machinery for the same closed operation set, and puts string-building
// next to a query engine. Emphatically NOT the application database, which
// holds every stored credential: this reads one vault file and computes in
// memory.

// Table is a parsed markdown table.
type Table struct {
	Columns []string
	Rows    [][]string // each row padded to len(Columns)
}

// TableQuery is a closed set of operations over a Table. Every field is
// optional; the zero query returns every row.
type TableQuery struct {
	Select  []string          // columns to return; empty means all
	Where   map[string]string // exact, case-insensitive equality
	GroupBy string            // a column, or date:month / date:day / date:year
	Metric  string            // column to aggregate
	Op      string            // sum | avg | count | min | max
	Order   string            // asc | desc
	OrderBy string            // "metric" (default) | "group"
	Limit   int
}

// orderColumn decides what Order sorts by.
//
// The two common shapes want opposite answers and both are phrased with the
// same word. "Top five merchants" ranks by the VALUE; "spend per month" wants
// the months in order, and sorting those by value produced 08, 06, 05, 07 on
// the real data — technically an ascending sort, obviously not what was asked.
//
// A date grouping therefore defaults to sorting by the group key, which for an
// ISO-8601 prefix is chronological, and everything else defaults to the metric.
// OrderBy overrides both, so "which was my most expensive month" is still
// expressible.
func (q TableQuery) orderColumn() string {
	if q.OrderBy != "" {
		return q.OrderBy
	}
	if _, datePart := splitGroupBy(q.GroupBy); datePart != "" {
		return "group"
	}
	return "metric"
}

// QueryResult is a small, renderable answer.
//
// Skipped and Notes are not decoration: a total computed from 94 of 98 rows
// without saying so is worse than an error, because nothing about the number
// reveals that it is wrong.
type QueryResult struct {
	Columns []string
	Rows    [][]string
	Skipped int
	Notes   []string
}

var validOps = map[string]bool{"sum": true, "avg": true, "count": true, "min": true, "max": true}

// ParseTable reads the first markdown table in content.
//
// It requires the delimiter row rather than merely counting pipes: prose
// containing a pipe is ordinary, `|---|---|` is not. That is the same test
// tableHeader applies, kept consistent so chunking and querying agree about
// what a table is.
func ParseTable(content string) (Table, error) {
	lines := strings.Split(normalizeLineEndings(content), "\n")
	start := -1
	for i := 0; i+1 < len(lines); i++ {
		if _, ok := tableHeader(lines[i] + "\n" + lines[i+1]); ok {
			start = i
			break
		}
	}
	if start < 0 {
		return Table{}, fmt.Errorf("no markdown table found")
	}

	t := Table{Columns: splitTableRow(lines[start])}
	for _, line := range lines[start+2:] {
		if !strings.Contains(line, "|") || strings.TrimSpace(line) == "" {
			break // the table ends at the first non-row line
		}
		cells := splitTableRow(line)
		// Pad rather than skip: a ragged row still carries real values, and
		// padding keeps column access safe. Truncate an over-long row for the
		// same reason — never shift data into the wrong column.
		for len(cells) < len(t.Columns) {
			cells = append(cells, "")
		}
		t.Rows = append(t.Rows, cells[:len(t.Columns)])
	}
	if len(t.Rows) == 0 {
		return Table{}, fmt.Errorf("table has no data rows")
	}
	return t, nil
}

// splitTableRow splits on unescaped pipes only. A `\|` inside a cell is content;
// splitting on it would shift every later column and produce a confidently
// wrong answer.
func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")

	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	for i, c := range cells {
		cells[i] = unescapeMarkdownCell(c)
	}
	return cells
}

// unescapeMarkdownCell undoes the escaping a converter adds when it writes a
// value into a table cell. A real row holds `\-10.85` and `BKG\*HOTEL`; showing
// those backslashes to the reader is noise that came from the storage format,
// not from their data.
//
// Only the punctuation markdown actually requires escaping inside a cell is
// unescaped, so a legitimate backslash in a value survives.
func unescapeMarkdownCell(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("*_|[]()#+-.!\\`", s[i+1]) >= 0 {
			i++
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (t Table) columnIndex(name string) int {
	for i, c := range t.Columns {
		if strings.EqualFold(c, name) {
			return i
		}
	}
	return -1
}

// Query filters, groups, aggregates, sorts and limits — in that order.
func (t Table) Query(q TableQuery) (QueryResult, error) {
	if q.Op != "" && !validOps[q.Op] {
		return QueryResult{}, fmt.Errorf("op %q is not supported; use one of sum, avg, count, min, max", q.Op)
	}
	if q.Metric != "" && t.columnIndex(q.Metric) < 0 {
		return QueryResult{}, fmt.Errorf("no column named %q; columns are: %s", q.Metric, strings.Join(t.Columns, ", "))
	}
	if q.GroupBy != "" {
		if col, _ := splitGroupBy(q.GroupBy); t.columnIndex(col) < 0 {
			return QueryResult{}, fmt.Errorf("no column named %q; columns are: %s", col, strings.Join(t.Columns, ", "))
		}
	}
	for name := range q.Where {
		if t.columnIndex(name) < 0 {
			return QueryResult{}, fmt.Errorf("no column named %q; columns are: %s", name, strings.Join(t.Columns, ", "))
		}
	}

	rows := t.filter(q.Where)
	if q.Op == "" || q.Metric == "" {
		return t.project(rows, q), nil
	}
	return t.aggregate(rows, q)
}

func (t Table) filter(where map[string]string) [][]string {
	if len(where) == 0 {
		return t.Rows
	}
	var out [][]string
	for _, row := range t.Rows {
		keep := true
		for name, want := range where {
			if !strings.EqualFold(strings.TrimSpace(row[t.columnIndex(name)]), strings.TrimSpace(want)) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, row)
		}
	}
	return out
}

// project returns rows unaggregated. This is the escape hatch for every
// question the operation set cannot express: hand back the table with the fat
// columns dropped, small enough to read directly.
func (t Table) project(rows [][]string, q TableQuery) QueryResult {
	cols := q.Select
	if len(cols) == 0 {
		cols = t.Columns
	}
	idx := make([]int, 0, len(cols))
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		if i := t.columnIndex(c); i >= 0 {
			idx = append(idx, i)
			names = append(names, t.Columns[i])
		}
	}
	res := QueryResult{Columns: names, Rows: [][]string{}}
	for _, row := range rows {
		out := make([]string, 0, len(idx))
		for _, i := range idx {
			out = append(out, row[i])
		}
		res.Rows = append(res.Rows, out)
	}
	res.Rows = applyOrderAndLimit(res.Rows, 0, q, false)
	return res
}

func (t Table) aggregate(rows [][]string, q TableQuery) (QueryResult, error) {
	metricIdx := t.columnIndex(q.Metric)
	groupCol, datePart := splitGroupBy(q.GroupBy)
	groupIdx := -1
	if groupCol != "" {
		groupIdx = t.columnIndex(groupCol)
	}

	type acc struct {
		sum, min, max float64
		n             int
	}
	order := []string{}
	groups := map[string]*acc{}
	res := QueryResult{Skipped: 0, Rows: [][]string{}, Notes: []string{}}

	for _, row := range rows {
		key := "all"
		if groupIdx >= 0 {
			key = groupKey(row[groupIdx], datePart)
			if key == "" {
				res.Skipped++
				continue
			}
		}
		val, ok := coerceNumber(row[metricIdx])
		if !ok {
			// count is the one op that does not need a number.
			if q.Op != "count" {
				res.Skipped++
				continue
			}
		}
		a, seen := groups[key]
		if !seen {
			a = &acc{min: val, max: val}
			groups[key] = a
			order = append(order, key)
		}
		a.sum += val
		a.n++
		if val < a.min {
			a.min = val
		}
		if val > a.max {
			a.max = val
		}
	}

	if res.Skipped > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"%d row(s) were skipped because %q could not be read as a number — this total covers the rest.",
			res.Skipped, q.Metric))
	}

	label := groupCol
	if label == "" {
		label = "all"
	}
	res.Columns = []string{label, q.Op + "(" + t.Columns[metricIdx] + ")"}
	for _, key := range order {
		a := groups[key]
		var v float64
		switch q.Op {
		case "sum":
			v = a.sum
		case "avg":
			if a.n > 0 {
				v = a.sum / float64(a.n)
			}
		case "count":
			v = float64(a.n)
		case "min":
			v = a.min
		case "max":
			v = a.max
		}
		res.Rows = append(res.Rows, []string{key, formatNumber(v, q.Op)})
	}
	if q.orderColumn() == "group" {
		// Sorting the KEY, which is a string: an ISO-8601 prefix sorts
		// chronologically, and any other group label sorts alphabetically.
		res.Rows = applyOrderAndLimit(res.Rows, 0, q, false)
	} else {
		res.Rows = applyOrderAndLimit(res.Rows, 1, q, true)
	}
	return res, nil
}

// applyOrderAndLimit sorts by column `by` and truncates. numeric decides whether
// to compare as numbers — sorting "100.00" before "12.50" as strings is exactly
// the kind of quietly wrong answer this package exists to prevent.
func applyOrderAndLimit(rows [][]string, by int, q TableQuery, numeric bool) [][]string {
	if q.Order != "" && len(rows) > 0 && by < len(rows[0]) {
		desc := strings.EqualFold(q.Order, "desc")
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i][by], rows[j][by]
			if numeric {
				af, _ := coerceNumber(a)
				bf, _ := coerceNumber(b)
				if desc {
					return af > bf
				}
				return af < bf
			}
			if desc {
				return a > b
			}
			return a < b
		})
	}
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows
}

// splitGroupBy splits "date:month" into ("date", "month").
func splitGroupBy(g string) (string, string) {
	if i := strings.Index(g, ":"); i >= 0 {
		return g[:i], g[i+1:]
	}
	return g, ""
}

// groupKey derives a group label, truncating an ISO-8601 timestamp for the
// date: forms so "per month" does not require the model to reason about string
// slicing. A value that is not a date under a date: grouping returns "" and is
// counted as skipped rather than silently bucketed as itself.
func groupKey(raw, datePart string) string {
	v := strings.TrimSpace(raw)
	if datePart == "" {
		return v
	}
	var want int
	switch datePart {
	case "year":
		want = 4
	case "month":
		want = 7
	case "day":
		want = 10
	default:
		return v
	}
	if len(v) < want {
		return ""
	}
	return v[:want]
}

// coerceNumber reads a number out of a table cell: markdown-escaped minus
// signs (\-10.85), thousands separators, currency symbols and stray spaces all
// appear in real converted CSVs.
func coerceNumber(s string) (float64, bool) {
	v := strings.TrimSpace(s)
	v = strings.ReplaceAll(v, `\`, "")
	v = strings.ReplaceAll(v, ",", "")
	v = strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+' || r == 'e' || r == 'E' {
			return r
		}
		return -1
	}, v)
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func formatNumber(v float64, op string) string {
	if op == "count" {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
