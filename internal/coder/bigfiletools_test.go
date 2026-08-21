package coder

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/vault"
)

// bigTableNote is shaped after the note that motivated these tools: most of the
// bytes live in ONE junk column that no question needs.
func bigTableNote(rows int) string {
	var b strings.Builder
	b.WriteString("# Transactions\n\n*Converted from card-transactions.csv.*\n\n")
	b.WriteString("| date | merchantName | USDAmount | status | apiTransaction |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for i := 0; i < rows; i++ {
		status := "APPROVED"
		if i%7 == 0 {
			status = "PENDING"
		}
		fmt.Fprintf(&b, "| 2026-0%d-1%d | Merchant %d | %d.50 | %s | %s |\n",
			(i%3)+6, i%10, i%5, i+1, status, strings.Repeat("j", 1200))
	}
	return b.String()
}

func toolSetWithNote(t *testing.T, rel, content string) (*hostToolSet, string) {
	t.Helper()
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := v.WriteNote(ws, rel, []byte(content)); err != nil {
		t.Fatalf("write note: %v", err)
	}
	return &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}, ws
}

// Chat is where the reported failure happened, and chat has no exec tools to
// fall back on — so these must be offered without the exec gate.
func TestBigFileToolsAreOfferedInChat(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	names := map[string]bool{}
	for _, tool := range h.tools() {
		names[tool.Name] = true
	}
	for _, want := range []string{"kb_file_map", "kb_table_query"} {
		if !names[want] {
			t.Errorf("%s is not offered to a chat coder", want)
		}
	}
}

// The map is the whole fix: it is what tells the model not to page.
func TestFileMapWarnsAboutTheDominantColumn(t *testing.T) {
	h, _ := toolSetWithNote(t, "notes/tx.md", bigTableNote(98))
	out := h.execute(context.Background(), toolCall("kb_file_map", `{"path":"notes/tx.md"}`))

	if strings.HasPrefix(out, "error:") {
		t.Fatalf("kb_file_map failed: %s", out)
	}
	if !strings.Contains(out, "apiTransaction") {
		t.Errorf("the dominant column was not flagged:\n%s", out)
	}
	if !strings.Contains(out, "98") {
		t.Errorf("row count missing:\n%s", out)
	}
	if len(out) > maxToolResult {
		t.Errorf("map is %d bytes, over the %d cap", len(out), maxToolResult)
	}
}

// The most obvious call — a path and nothing else — must not drag the junk
// column back into the context. That would reproduce the exact bug these tools
// exist to fix, by the most likely route.
func TestTableQueryDefaultProjectionOmitsTheFatColumn(t *testing.T) {
	h, _ := toolSetWithNote(t, "notes/tx.md", bigTableNote(98))
	out := h.execute(context.Background(), toolCall("kb_table_query", `{"path":"notes/tx.md","limit":3}`))

	if strings.HasPrefix(out, "error:") {
		t.Fatalf("kb_table_query failed: %s", out)
	}
	if strings.Contains(out, strings.Repeat("j", 100)) {
		t.Errorf("the dominant column was selected by default:\n%s", out[:200])
	}
	if len(out) > maxToolResult {
		t.Errorf("result is %d bytes, over the %d cap", len(out), maxToolResult)
	}
	if !strings.Contains(out, "merchantName") {
		t.Errorf("useful columns were dropped too:\n%s", out)
	}
}

// The arithmetic the model must not do itself.
func TestTableQueryAggregates(t *testing.T) {
	h, _ := toolSetWithNote(t, "notes/tx.md", bigTableNote(30))
	out := h.execute(context.Background(), toolCall("kb_table_query",
		`{"path":"notes/tx.md","where":{"status":"APPROVED"},"group_by":"date:month","metric":"USDAmount","op":"sum"}`))

	if strings.HasPrefix(out, "error:") {
		t.Fatalf("aggregate failed: %s", out)
	}
	if !strings.Contains(out, "2026-06") {
		t.Errorf("no monthly groups in:\n%s", out)
	}
}

// "Nothing matched" is a normal outcome. An `error:` prefix would trip the API
// engine's oscillation guard, which counts a failing call against the loop.
func TestTableQueryNoMatchesIsNotAnError(t *testing.T) {
	h, _ := toolSetWithNote(t, "notes/tx.md", bigTableNote(10))
	out := h.execute(context.Background(), toolCall("kb_table_query",
		`{"path":"notes/tx.md","where":{"status":"NOPE"},"metric":"USDAmount","op":"sum"}`))

	if strings.HasPrefix(out, "error:") {
		t.Errorf("a no-match result was reported as an error: %q", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("empty tool result — breaks strict provider serializers")
	}
}

// A rejected parameter must NAME the offending value, which is what lets a
// small model fix itself in one turn instead of guessing.
func TestTableQueryRejectsBadOpByName(t *testing.T) {
	h, _ := toolSetWithNote(t, "notes/tx.md", bigTableNote(10))
	out := h.execute(context.Background(), toolCall("kb_table_query",
		`{"path":"notes/tx.md","metric":"USDAmount","op":"median"}`))

	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("unknown op accepted: %s", out)
	}
	if !strings.Contains(out, "median") || !strings.Contains(out, "sum") {
		t.Errorf("error names neither the bad value nor the valid ones: %s", out)
	}
}

// read_file by heading, so a document does not have to be paged by byte offset.
func TestReadFileBySection(t *testing.T) {
	doc := "# Trip\n\nintro\n\n## Flights\n\nBA 342 at 09:15\n\n## Hotels\n\nSomewhere central\n"
	h, _ := toolSetWithNote(t, "notes/trip.md", doc)

	out := h.execute(context.Background(), toolCall("read_file", `{"path":"notes/trip.md","section":"Flights"}`))
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("section read failed: %s", out)
	}
	if !strings.Contains(out, "BA 342") {
		t.Errorf("wrong section returned:\n%s", out)
	}
	if strings.Contains(out, "Somewhere central") {
		t.Errorf("section read leaked another section:\n%s", out)
	}
}

// A wrong heading must say what IS there — otherwise the model's only recourse
// is guessing, or falling back to the paging this replaces.
func TestReadFileUnknownSectionListsTheRealOnes(t *testing.T) {
	doc := "# Trip\n\nintro\n\n## Flights\n\nBA 342\n"
	h, _ := toolSetWithNote(t, "notes/trip.md", doc)

	out := h.execute(context.Background(), toolCall("read_file", `{"path":"notes/trip.md","section":"Trains"}`))
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("unknown section accepted: %s", out)
	}
	if !strings.Contains(out, "Flights") {
		t.Errorf("error does not name the available sections: %s", out)
	}
}

// search_files scoped to one file, which is how a model finds the part of a big
// document it needs without reading the whole thing.
func TestSearchFilesScopedToOnePath(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	v.WriteNote(ws, "notes/a.md", []byte("# A\n\nthe dentist is on Tuesday\n"))
	v.WriteNote(ws, "notes/b.md", []byte("# B\n\nanother dentist mention\n"))
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out := h.execute(context.Background(), toolCall("search_files", `{"query":"dentist","path":"notes/a.md"}`))
	if strings.Contains(out, "notes/b.md") {
		t.Errorf("scoped search answered from another file:\n%s", out)
	}
	if !strings.Contains(out, "notes/a.md") {
		t.Errorf("scoped search found nothing in the file it was given:\n%s", out)
	}
}
