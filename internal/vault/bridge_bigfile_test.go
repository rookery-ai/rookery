package vault

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// These pin coder-kind PARITY, which this command set has already lost once:
// kbsearch.go's doc comment records the bridge missing the ranked-BM25 upgrade,
// leaving a CLI-coder workspace "strictly worse retrieval for no reason a user
// could see or control".
//
// Without /map and /table, a CLI-coder workspace gets none of the big-file
// handling — the same drift, in a new place. It was in fact shipped that way
// for one commit and caught by an end-to-end run, not by the unit tests.

func lopsidedBridgeTable(rows int) string {
	var b strings.Builder
	b.WriteString("# Transactions\n\n*Converted from card-transactions.csv.*\n\n")
	b.WriteString("| date | merchantName | USDAmount | apiTransaction |\n")
	b.WriteString("|---|---|---|---|\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "| 2026-0%d-15 | Merchant %d | %d.50 | %s |\n",
			(i%3)+6, i%4, i+1, strings.Repeat("j", 1200))
	}
	return b.String()
}

func TestBridgeMapDescribesAFile(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/tx.md", []byte(lopsidedBridgeTable(60)))

	resp, out := post(t, b.URL()+"/map", token, map[string]any{"path": "notes/tx.md"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	m, _ := out["map"].(string)
	if !strings.Contains(m, "apiTransaction") {
		t.Errorf("the dominant column was not flagged for a CLI coder:\n%s", m)
	}
	if !strings.Contains(m, "60") {
		t.Errorf("row count missing:\n%s", m)
	}
}

func TestBridgeTableAggregates(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/tx.md", []byte(lopsidedBridgeTable(30)))

	resp, out := post(t, b.URL()+"/table", token, map[string]any{
		"path": "notes/tx.md", "group_by": "date:month",
		"metric": "USDAmount", "op": "sum",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	res, _ := out["result"].(string)
	if !strings.Contains(res, "2026-06") {
		t.Errorf("no monthly groups:\n%s", res)
	}
}

// The default projection must drop the dominant column here too, or the CLI
// path reproduces the original bug by the most obvious call.
func TestBridgeTableDefaultProjectionOmitsTheFatColumn(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/tx.md", []byte(lopsidedBridgeTable(30)))

	_, out := post(t, b.URL()+"/table", token, map[string]any{"path": "notes/tx.md", "limit": 3})
	res, _ := out["result"].(string)
	if strings.Contains(res, strings.Repeat("j", 100)) {
		t.Errorf("the dominant column was returned by default")
	}
	if !strings.Contains(res, "merchantName") {
		t.Errorf("useful columns were dropped too:\n%s", res)
	}
}

// A bad parameter must come back naming the offending value, exactly as it does
// for the API engine.
func TestBridgeTableRejectsBadOpByName(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/tx.md", []byte(lopsidedBridgeTable(5)))

	resp, out := post(t, b.URL()+"/table", token, map[string]any{
		"path": "notes/tx.md", "metric": "USDAmount", "op": "median",
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("unknown op accepted: %v", out)
	}
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "median") {
		t.Errorf("error does not name the bad value: %q", msg)
	}
}

// Scoped search over the bridge, so a CLI coder can search inside one file.
func TestBridgeSearchAcceptsAPathScope(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/a.md", []byte("# A\n\nthe dentist is on tuesday\n"))
	v.WriteNote("ws1", "notes/b.md", []byte("# B\n\nanother dentist mention\n"))

	_, out := post(t, b.URL()+"/search", token, map[string]any{
		"query": "dentist", "path": "notes/a.md",
	})
	results, _ := out["results"].(string)
	if bytes.Contains([]byte(results), []byte("notes/b.md")) {
		t.Errorf("scoped search answered from another file:\n%s", results)
	}
	if !bytes.Contains([]byte(results), []byte("notes/a.md")) {
		t.Errorf("scoped search found nothing in the file it was given:\n%s", results)
	}
}

// The unscoped form must keep working exactly as before.
func TestBridgeSearchWithoutPathStillSearchesEverything(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/a.md", []byte("# A\n\nthe dentist is on tuesday\n"))
	v.WriteNote("ws1", "notes/b.md", []byte("# B\n\nanother dentist mention\n"))

	_, out := post(t, b.URL()+"/search", token, map[string]any{"query": "dentist"})
	results, _ := out["results"].(string)
	if !bytes.Contains([]byte(results), []byte("notes/b.md")) {
		t.Errorf("vault-wide search lost a file:\n%s", results)
	}
}
