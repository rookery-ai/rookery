package coder

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// A run must record what it DID, not only what it cost.
//
// An agent failed three times in a row. Each diagnosis inferred the tool calls
// from the token count, and each was wrong — first "it never knew the tools
// existed", then "it hit the turn budget", then "it looped on truncation". The
// run recorded 165,136 tokens and nothing whatsoever about the path that spent
// them.
func TestResultCarriesAToolTrace(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		switch call {
		case 0:
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"a.txt","content":"hello"}`),
			}}, nil
		case 1:
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("read_file", `{"path":"a.txt"}`),
				toolCall("read_file", `{"path":"missing.txt"}`),
			}}, nil
		default:
			return &llm.Response{Content: "[CHAT] done"}, nil
		}
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.ToolTrace) != 3 {
		t.Fatalf("trace has %d entries, want 3: %+v", len(res.ToolTrace), res.ToolTrace)
	}
	if res.ToolTrace[0].Name != "write_file" {
		t.Errorf("first call = %q, want write_file", res.ToolTrace[0].Name)
	}
	// A failing call has to be distinguishable — "it called read_file twice" and
	// "it called read_file twice and one failed" are different diagnoses.
	if !res.ToolTrace[2].Error {
		t.Errorf("a failed read was not marked as an error: %+v", res.ToolTrace[2])
	}
	if res.ToolTrace[1].Bytes == 0 {
		t.Errorf("a successful call recorded no result size: %+v", res.ToolTrace[1])
	}
}

// The summary answers "what filled the context", which is a question about
// BYTES. Sorting by name or by call count buries the one call that mattered.
func TestSummarizeToolTraceOrdersByBytes(t *testing.T) {
	got := SummarizeToolTrace([]ToolCallStat{
		{Name: "list_dir", Bytes: 100},
		{Name: "read_file", Bytes: 40000},
		{Name: "list_dir", Bytes: 100},
		{Name: "kb_file_map", Bytes: 900},
	})
	if !strings.HasPrefix(got, "read_file×1=40000B") {
		t.Errorf("the biggest consumer is not first: %q", got)
	}
	if !strings.Contains(got, "list_dir×2=200B") {
		t.Errorf("repeat calls were not aggregated: %q", got)
	}
}

func TestSummarizeToolTraceReportsErrors(t *testing.T) {
	got := SummarizeToolTrace([]ToolCallStat{
		{Name: "read_file", Bytes: 10, Error: true},
		{Name: "read_file", Bytes: 20},
	})
	if !strings.Contains(got, "1 err") {
		t.Errorf("failed calls are invisible in the summary: %q", got)
	}
}

// An empty trace must say so rather than rendering as blank, which in a log line
// is indistinguishable from the field being absent.
func TestSummarizeToolTraceOnNoCalls(t *testing.T) {
	if got := SummarizeToolTrace(nil); got != "(no tool calls)" {
		t.Errorf("got %q, want an explicit no-calls marker", got)
	}
}
