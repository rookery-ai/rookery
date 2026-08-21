package vault

import (
	"strings"
	"testing"
)

// An ungrouped aggregate must say that it is ungrouped.
//
// Asked for "cost per month", a model ran the sum WITHOUT group_by, got back a
// single row labelled only `all`, and reported the grand total across four
// months as "€676.75/month". The arithmetic was right; the label let it answer a
// different question than the one asked, and a reader would have budgeted on it.
func TestUngroupedAggregateSaysItIsUngrouped(t *testing.T) {
	tbl, err := ParseTable(txFixture)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	res, err := tbl.Query(TableQuery{Metric: "USDAmount", Op: "sum"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected one row for an ungrouped aggregate, got %d", len(res.Rows))
	}
	if !strings.Contains(res.Columns[0], "not grouped") {
		t.Errorf("column header does not say the figure is ungrouped: %q", res.Columns[0])
	}
	if !strings.Contains(res.Columns[0], "5") {
		t.Errorf("column header does not say how many rows it covers: %q", res.Columns[0])
	}
	// The row cell must not be a bare "all" either — a reader may see only it.
	if res.Rows[0][0] == "all" {
		t.Errorf("row cell is a bare %q, which reads as a group name", res.Rows[0][0])
	}
	if !strings.Contains(res.Rows[0][0], "rows") {
		t.Errorf("row cell does not describe the scope: %q", res.Rows[0][0])
	}
	// The sentinel used internally must never surface.
	if strings.Contains(strings.Join(res.Rows[0], " "), "\x00") {
		t.Errorf("internal sentinel leaked into the result: %q", res.Rows[0])
	}
}

// A GROUPED aggregate is unaffected — its labels are the group values.
func TestGroupedAggregateKeepsItsGroupLabels(t *testing.T) {
	tbl, _ := ParseTable(txFixture)
	res, err := tbl.Query(TableQuery{GroupBy: "date:month", Metric: "USDAmount", Op: "sum"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Columns[0] != "date" {
		t.Errorf("group column header = %q, want date", res.Columns[0])
	}
	for _, r := range res.Rows {
		if !strings.HasPrefix(r[0], "2026-") {
			t.Errorf("group label is not the group value: %q", r[0])
		}
	}
}

// Filtering still narrows what the count covers, and the label must reflect the
// FILTERED count rather than the table's size — otherwise it overstates.
func TestUngroupedLabelCountsOnlyMatchingRows(t *testing.T) {
	tbl, _ := ParseTable(txFixture)
	res, _ := tbl.Query(TableQuery{
		Where: map[string]string{"merchantName": "Kaufland"}, Metric: "USDAmount", Op: "sum",
	})
	if !strings.Contains(res.Columns[0], "2") {
		t.Errorf("label should count the 2 matching rows, got %q", res.Columns[0])
	}
}
