package vault

import (
	"strings"
	"testing"
)

// A tool call goes in its own fence, and the fence must survive its content.
//
// CommonMark closes a fenced block at the first line of AT LEAST as many
// backticks, and agents write shell one-liners and markdown snippets — so a
// fixed three-backtick fence would be terminated early by the content and spill
// the rest of the call into the document as markup. That is not merely ugly: a
// note that no longer round-trips opens READ-ONLY, so the owner cannot edit it.
func TestActivityFenceSurvivesBackticksInAToolCall(t *testing.T) {
	call := "🔧 bash(cd . && echo ```oops``` && ls)"
	out := activityEntry(call)

	fence := strings.SplitN(out, "text\n", 2)[0]
	if len(fence) <= 3 {
		t.Fatalf("fence %q is not longer than the backtick run inside the content", fence)
	}
	// The content must be enclosed, not truncated.
	if !strings.Contains(out, call) {
		t.Errorf("the tool call was mangled: %q", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), fence) {
		t.Errorf("the block does not close with its own fence: %q", out)
	}
}

// An ordinary call gets the ordinary fence — the guard must not inflate every
// block on the off chance.
func TestActivityFenceIsThreeBackticksNormally(t *testing.T) {
	out := activityEntry("🔧 read_file(notes/a.md)")
	if !strings.HasPrefix(out, "```text\n") {
		t.Errorf("expected a plain three-backtick fence, got %q", out)
	}
}

// A coder turn is prose. Fencing an English sentence makes it harder to read,
// not easier — the fences exist for shell commands.
func TestCoderTurnsStayProse(t *testing.T) {
	out := activityEntry("**coder:** I found three new transactions.")
	if strings.Contains(out, "```") {
		t.Errorf("a coder turn was fenced: %q", out)
	}
}

// "Reported as zero" and "never reported" are different claims, and the second
// must never render as a price. A CLI coder reports no usage at all.
func TestUsageBlockSaysWhenCostWasNotReported(t *testing.T) {
	out := usageBlock(RunNote{TotalTokens: 100, PromptTokens: 90, CompletionTokens: 10})
	if strings.Contains(out, "$0.00") {
		t.Errorf("an unreported cost rendered as a price: %q", out)
	}
	if !strings.Contains(out, "not reported") {
		t.Errorf("the block does not say the cost is unknown: %q", out)
	}
}

func TestUsageBlockRendersAReportedCostAndCacheShare(t *testing.T) {
	out := usageBlock(RunNote{
		TotalTokens: 1000, PromptTokens: 800, CompletionTokens: 200,
		CachedTokens: 400, CacheReported: true,
		CostUSD: 0.000123, CostReported: true,
	})
	if !strings.Contains(out, "50% of prompt") {
		t.Errorf("cache share missing: %q", out)
	}
	if !strings.Contains(out, "$0.000123") {
		t.Errorf("cost missing or rounded away: %q", out)
	}
}

// A run costing $0.0002 rendered at two decimals is "$0.00", which reads as
// free. That is the common case in this product, not an edge case.
func TestFormatCostDoesNotRoundSmallChargesToZero(t *testing.T) {
	if got := FormatCostUSD(0.000228); got != "$0.000228" {
		t.Errorf("got %q, want the real figure", got)
	}
	if got := FormatCostUSD(12.5); got != "$12.50" {
		t.Errorf("got %q, want $12.50 for an amount where cents matter", got)
	}
	if got := FormatCostUSD(0); got != "$0.00" {
		t.Errorf("got %q, want $0.00 for a genuine zero", got)
	}
}
