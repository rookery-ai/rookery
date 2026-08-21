package vault

import (
	"strings"
	"testing"
)

// Values shaped like the real file: markdown-escaped negatives (\-5.00),
// ISO-8601 timestamps, mixed statuses. A fixture of clean integers would pass a
// parser that cannot read the data this exists for.
const txFixture = `| date | merchantName | USDAmount | status |
|---|---|---|---|
| 2026-06-02T10:00:00Z | Kaufland | 12.50 | APPROVED |
| 2026-06-11T10:00:00Z | Neptun | 100.00 | APPROVED |
| 2026-07-03T10:00:00Z | Kaufland | 7.25 | APPROVED |
| 2026-07-04T10:00:00Z | OpenRouter | 10.85 | PENDING |
| 2026-07-20T10:00:00Z | Neptun | \-5.00 | APPROVED |
`

func TestParseTableReadsHeaderAndRows(t *testing.T) {
	tab, err := ParseTable(txFixture)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if got := len(tab.Columns); got != 4 {
		t.Errorf("columns = %d, want 4: %v", got, tab.Columns)
	}
	if got := len(tab.Rows); got != 5 {
		t.Errorf("rows = %d, want 5", got)
	}
	if tab.Columns[1] != "merchantName" {
		t.Errorf("column 1 = %q, want merchantName", tab.Columns[1])
	}
}

// The arithmetic the model must not do in its head. Expected values are
// computed by hand here, never from the tool's own output.
func TestQuerySumsByMonth(t *testing.T) {
	tab, err := ParseTable(txFixture)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	res, err := tab.Query(TableQuery{
		Where:   map[string]string{"status": "APPROVED"},
		GroupBy: "date:month",
		Metric:  "USDAmount",
		Op:      "sum",
		Order:   "asc",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// 2026-06: 12.50 + 100.00 = 112.50   2026-07: 7.25 + (-5.00) = 2.25
	want := map[string]string{"2026-06": "112.50", "2026-07": "2.25"}
	if len(res.Rows) != 2 {
		t.Fatalf("got %d groups, want 2: %v", len(res.Rows), res.Rows)
	}
	for _, r := range res.Rows {
		if want[r[0]] != r[1] {
			t.Errorf("%s = %s, want %s", r[0], r[1], want[r[0]])
		}
	}
}

func TestQueryTopNByGroup(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	res, err := tab.Query(TableQuery{
		GroupBy: "merchantName",
		Metric:  "USDAmount",
		Op:      "max",
		Order:   "desc",
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "Neptun" {
		t.Errorf("top merchant = %s, want Neptun (100.00)", res.Rows[0][0])
	}
}

// A total computed from some of the rows without saying so is worse than an
// error: the reader cannot tell it is wrong.
func TestQueryReportsUncoercibleValues(t *testing.T) {
	bad := strings.Replace(txFixture, "7.25", "n/a", 1)
	tab, _ := ParseTable(bad)
	res, err := tab.Query(TableQuery{Metric: "USDAmount", Op: "sum"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
	if len(res.Notes) == 0 {
		t.Error("skipped rows were not reported to the caller")
	}
}

// A pipe inside a cell must not silently shift every later column — that is a
// wrong answer presented as a right one.
func TestParseTableHandlesEscapedPipes(t *testing.T) {
	tab, err := ParseTable("| a | b |\n|---|---|\n| x \\| y | z |\n")
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if len(tab.Rows[0]) != 2 {
		t.Fatalf("row has %d cells, want 2: %v", len(tab.Rows[0]), tab.Rows[0])
	}
	if tab.Rows[0][1] != "z" {
		t.Errorf("columns shifted: %v", tab.Rows[0])
	}
}

// A closed operation set is the point: an unknown op is a precise validation
// error the model can correct, not a silent wrong answer.
func TestQueryRejectsUnknownOp(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	_, err := tab.Query(TableQuery{Metric: "USDAmount", Op: "median"})
	if err == nil {
		t.Fatal("unknown op accepted")
	}
	if !strings.Contains(err.Error(), "median") {
		t.Errorf("error does not name the offending value: %v", err)
	}
}

func TestQueryRejectsUnknownColumn(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	_, err := tab.Query(TableQuery{Metric: "nope", Op: "sum"})
	if err == nil {
		t.Fatal("unknown column accepted")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error does not name the offending column: %v", err)
	}
}

// Filtering with no aggregation returns rows — the projection path that covers
// every question the parameters cannot express.
func TestQueryProjectsSelectedColumns(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	res, err := tab.Query(TableQuery{
		Select: []string{"merchantName", "USDAmount"},
		Where:  map[string]string{"merchantName": "Kaufland"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Columns) != 2 {
		t.Errorf("columns = %v, want 2", res.Columns)
	}
	if len(res.Rows) != 2 {
		t.Errorf("rows = %d, want 2 Kaufland rows", len(res.Rows))
	}
}

// "Spend per month" wants the months in order. Sorting those by VALUE produced
// 2026-08, 2026-06, 2026-05, 2026-07 on the real file — an ascending sort of
// the wrong column, and obviously not what was asked. A date grouping therefore
// orders by the key.
func TestDateGroupsOrderChronologicallyByDefault(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	res, err := tab.Query(TableQuery{
		GroupBy: "date:month", Metric: "USDAmount", Op: "sum", Order: "asc",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Rows[0][0] != "2026-06" || res.Rows[1][0] != "2026-07" {
		t.Errorf("months out of order: %v", res.Rows)
	}
}

// ...but "which was my most expensive month" is still expressible.
func TestOrderByMetricOverridesTheDateDefault(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	res, err := tab.Query(TableQuery{
		GroupBy: "date:month", Metric: "USDAmount", Op: "sum",
		Order: "desc", OrderBy: "metric",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// June totals 112.50, July 2.25.
	if res.Rows[0][0] != "2026-06" {
		t.Errorf("largest month first expected, got %v", res.Rows)
	}
}

// A non-date grouping still ranks by value, which is what "top N" means.
func TestNonDateGroupsRankByValue(t *testing.T) {
	tab, _ := ParseTable(txFixture)
	res, _ := tab.Query(TableQuery{
		GroupBy: "merchantName", Metric: "USDAmount", Op: "sum", Order: "desc",
	})
	if res.Rows[0][0] != "Neptun" {
		t.Errorf("expected Neptun first (95.00), got %v", res.Rows)
	}
}

// Markdown escaping is an artefact of the storage format, not the user's data.
// The real file holds merchants like `BKG\*HOTEL AT BOOKING.C`.
func TestParseTableUnescapesCellValues(t *testing.T) {
	src := "| merchant | amount |\n|---|---|\n| BKG\\*HOTEL | \\-10.85 |\n"
	tab, err := ParseTable(src)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if tab.Rows[0][0] != "BKG*HOTEL" {
		t.Errorf("merchant = %q, want BKG*HOTEL", tab.Rows[0][0])
	}
	if tab.Rows[0][1] != "-10.85" {
		t.Errorf("amount = %q, want -10.85", tab.Rows[0][1])
	}
	// The escaped value must still read as a number.
	if v, ok := coerceNumber(tab.Rows[0][1]); !ok || v != -10.85 {
		t.Errorf("coerceNumber = %v, %v; want -10.85, true", v, ok)
	}
}

func TestParseTableRejectsNonTable(t *testing.T) {
	if _, err := ParseTable("# Just a note\n\nsome prose\n"); err == nil {
		t.Error("prose accepted as a table")
	}
}

// A ragged row must not be silently padded into a wrong answer.
func TestParseTableReportsRaggedRows(t *testing.T) {
	tab, err := ParseTable("| a | b | c |\n|---|---|---|\n| 1 | 2 |\n| 1 | 2 | 3 |\n")
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if len(tab.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(tab.Rows))
	}
	// The short row is padded to the header width so column access is safe,
	// but the padding must be empty rather than shifted data.
	if tab.Rows[0][2] != "" {
		t.Errorf("short row was shifted rather than padded: %v", tab.Rows[0])
	}
}
