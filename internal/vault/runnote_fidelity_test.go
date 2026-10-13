package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A coder turn is the model's own text: several lines, often carrying the
// output-protocol markers. Written as raw prose, the editor escaped the
// brackets and joined the lines into one paragraph, so EVERY run note with such
// a turn opened read-only. Multi-line or markup-bearing turns are fenced; a plain
// one-line sentence still reads as prose (TestCoderTurnsStayProse).
func TestCoderTurnWithProtocolMarkersIsFenced(t *testing.T) {
	out := activityEntry("**coder:** Order complete. Notifying the user.\n\n[CHAT] 🛒 Order placed\n[STATE]{\"k\": 1}[/STATE]")
	if !strings.HasPrefix(out, "**coder:**\n\n") {
		t.Fatalf("label not kept as prose: %q", out)
	}
	if !strings.Contains(out, "```text\nOrder complete.") || !strings.Contains(out, "[STATE]{\"k\": 1}[/STATE]\n```") {
		t.Fatalf("multi-line coder turn not fenced verbatim: %q", out)
	}
	// A single line with a markdown-significant character is fenced too.
	if out := activityEntry("**coder:** [CHAT] done"); !strings.Contains(out, "```") {
		t.Errorf("a line carrying a protocol marker was left as prose: %q", out)
	}
}

// Real reflected run notes go into the shared fidelity corpus so the editor
// check (web/ui convertFidelity.test.ts) runs over them — the same arrangement
// TestImportedNoteFidelityCorpus uses for imported notes. Regenerate with:
//
//	go test ./internal/vault/ -run TestRunNoteFidelityCorpus -update-fidelity
func TestRunNoteFidelityCorpus(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	start := time.Date(2026, 10, 13, 17, 1, 1, 0, time.UTC)
	err := v.Reflector().ReflectAgentRun(ws, RunNote{
		RunID: "run-1", AgentID: "agent-1", AgentName: "Toolshop restock",
		Trigger: "manual", ExitCode: 0, StartedAt: start, FinishedAt: start.Add(19 * time.Minute),
		TotalTokens: 4204354, PromptTokens: 4164260, CompletionTokens: 40094,
		CachedTokens: 3844352, CacheReported: true, CostUSD: 0.21, CostReported: true,
		ChatLines: []string{"🛒 Monthly Toolshop order placed — total: $39.78, invoice: INV-2026000008"},
		Activity: []string{
			"🔧 browser_fill({\"ref\": \"f16e211\", \"value\": \"${CARD_NUMBER}\"})",
			"🔧 bash(cd . && echo ```oops``` && ls)",
			"**coder:** I found the cart.",
			"**coder:** Order complete. Notifying the user.\n\n[CHAT] 🛒 Monthly Toolshop order placed\n[STATE]{\"last_invoice\": \"INV-2026000008\"}[/STATE]",
			"**summary:** browser_fill×27=77401B browser_click×19=41463B(3 err)",
		},
		Output: "Order complete.\n\n[CHAT] 🛒 Monthly Toolshop order placed\n[STATE]{\"last_invoice\": \"INV-2026000008\"}[/STATE]",
	})
	if err != nil {
		t.Fatalf("ReflectAgentRun: %v", err)
	}
	raw, err := v.ReadNote(ws, RunNotePath("agent-1", start))
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	body := stripFrontmatter(string(raw))
	path := filepath.Join("..", "convert", "testdata", "fidelity", "runnote-agent-run.md")
	if *updateImportFidelity {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (regenerate with -update-fidelity): %v", path, err)
	}
	if string(want) != body {
		t.Errorf("corpus file %s is stale.\n got: %q\nwant: %q", path, body, want)
	}
}
