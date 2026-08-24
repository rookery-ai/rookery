package vault

import (
	"fmt"
	"strings"
)

// usageBlock renders the run's cost and token accounting.
//
// A fenced block rather than a blockquote line, because it is a small table of
// figures people scan rather than prose they read, and because the same shape is
// shown in the run panel — one format for one fact.
//
// Every line is conditional on its REPORTED flag. A CLI coder runs a subprocess
// and reports no usage at all; a provider may report tokens but not price. A
// row rendered as "Cost: $0.00" in either case would read as free, which is a
// stronger claim than "we don't know" and a wrong one.
func usageBlock(n RunNote) string {
	var rows []string
	if n.TotalTokens > 0 {
		rows = append(rows,
			fmt.Sprintf("Total tokens:      %d", n.TotalTokens),
			fmt.Sprintf("  prompt:          %d", n.PromptTokens),
			fmt.Sprintf("  completion:      %d", n.CompletionTokens))
	}
	if n.CacheReported {
		line := fmt.Sprintf("Cached tokens:     %d", n.CachedTokens)
		if n.PromptTokens > 0 {
			line += fmt.Sprintf(" (%.0f%% of prompt)",
				float64(n.CachedTokens)/float64(n.PromptTokens)*100)
		}
		rows = append(rows, line)
	}
	if n.CostReported {
		rows = append(rows, "Cost:              "+FormatCostUSD(n.CostUSD))
	}
	if len(rows) == 0 {
		return ""
	}
	// Named so a reader knows the absent rows are unreported rather than zero.
	if !n.CostReported {
		rows = append(rows, "Cost:              not reported by this coder")
	}
	return "```text\n" + strings.Join(rows, "\n") + "\n```\n\n"
}

// FormatCostUSD renders a dollar amount at a precision that does not round a
// real charge away to nothing.
//
// A run here costs on the order of $0.0002, so two decimal places would render
// every run in this product as "$0.00" — a rounding that reads as a claim the
// agent is free. Small amounts get six decimals; amounts where cents matter get
// two, because "$12.34" is what someone checking a bill expects.
func FormatCostUSD(v float64) string {
	if v > 0 && v < 0.01 {
		return fmt.Sprintf("$%.6f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// activityEntry renders one transcript entry for the run note.
//
// A tool call goes in its own fenced block: a run's calls include shell one-liners
// with embedded quotes and newlines (`bash(cd . && python3 -c "...")`), and as a
// markdown list item those wrap into an unreadable smear. A coder turn is prose
// and stays prose — fencing an English sentence would be worse, not better.
func activityEntry(line string) string {
	if strings.HasPrefix(line, "**") {
		// A labelled non-tool entry (coder turn, summary).
		return line + "\n\n"
	}
	fence := fenceFor(line)
	return fence + "text\n" + line + "\n" + fence + "\n\n"
}

// fenceFor returns a code fence guaranteed to survive the content it wraps.
//
// CommonMark closes a fenced block at the first line of AT LEAST as many
// backticks, so content containing ``` would terminate a three-backtick fence
// early — spilling the rest of the tool call into the document as markup. That
// is not hypothetical here: agents write shell commands and markdown snippets,
// and a corrupted note is not merely ugly, it opens READ-ONLY (checkFidelity
// refuses to round-trip it), so the owner cannot edit the note at all.
func fenceFor(content string) string {
	longest := 0
	run := 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}
