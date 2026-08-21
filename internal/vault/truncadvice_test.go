package vault

import (
	"fmt"
	"strings"
	"testing"
)

// wideTable mirrors the shape that caused the failure: many columns, one of them
// enormous. The default projection drops only the enormous one, leaving a result
// wide enough that very few rows fit.
func wideTable(rows int) string {
	cols := []string{"date", "merchantName", "USDAmount", "status", "mcc", "authCode",
		"externalTxId", "externalRootTxId", "last4", "accountCurrency", "raw"}
	var b strings.Builder
	b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(cols)) + "\n")
	for i := 0; i < rows; i++ {
		b.WriteString(fmt.Sprintf(
			"| 2026-08-%02d | Merchant %d | %d.50 | APPROVED | 5411 | AB%04d | tx-%08d | rt-%08d | 5035 | USD | %s |\n",
			i%28+1, i%9, i+1, i, i, i, strings.Repeat("z", 900)))
	}
	return b.String()
}

// The advice a truncated result gives has to name the lever that works.
//
// "Narrow the query with where or limit" is exactly wrong when the caller needs
// every row — which is the commonest agent task, deciding which rows are new.
// An agent told that queries again and again until its context is gone.
func TestTruncatedWideResultAdvisesFewerColumns(t *testing.T) {
	tbl, err := ParseTable(wideTable(98))
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	res, err := tbl.Query(TableQuery{Select: ModestColumns(tbl)})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	out := RenderQueryResult(res, 8*1024)
	if !strings.Contains(out, "more rows") {
		t.Skip("this fixture no longer truncates; the advice path is untested")
	}
	if !strings.Contains(out, "select:") {
		t.Errorf("truncation advice does not point at the column count:\n%s",
			out[strings.Index(out, "…and"):])
	}
	if !strings.Contains(out, "columns") {
		t.Errorf("advice does not say how many columns filled the budget")
	}
}

// A NARROW result that still truncates is genuinely a row-count problem, and
// there the original advice is the right one.
func TestTruncatedNarrowResultAdvisesFewerRows(t *testing.T) {
	tbl, _ := ParseTable(wideTable(4000))
	res, _ := tbl.Query(TableQuery{Select: []string{"date", "merchantName"}})
	out := RenderQueryResult(res, 8*1024)
	if !strings.Contains(out, "more rows") {
		t.Fatal("expected a truncated result from 4000 rows")
	}
	if strings.Contains(out, "select:") {
		t.Errorf("advised dropping columns on a 2-column result:\n%s",
			out[strings.Index(out, "…and"):])
	}
	if !strings.Contains(out, "where or limit") {
		t.Errorf("advice does not name the row-count lever:\n%s", out)
	}
}

// The count line must say how many of how many, so a truncated result cannot be
// mistaken for the whole table. "23 row(s)" reads as a complete answer.
func TestTruncatedResultReportsBothCounts(t *testing.T) {
	tbl, _ := ParseTable(wideTable(98))
	res, _ := tbl.Query(TableQuery{Select: ModestColumns(tbl)})
	out := RenderQueryResult(res, 8*1024)
	if !strings.Contains(out, "of 98 row(s)") {
		t.Errorf("the count line hides that the result is partial:\n%s",
			out[strings.LastIndex(out, "\n"):])
	}
}
