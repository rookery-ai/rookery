package coder

import "testing"

// The rule is keyed on OUR tool registry, not on any provider's markup dialect. We
// know exactly which tools were offered on a given run, so "this text names one of
// our tools where prose would not put it" is decisive — and it does not go stale
// when the next provider invents new syntax. Matching dialects is an unwinnable
// blacklist.
func TestLooksLikeToolScaffolding(t *testing.T) {
	tools := []string{"adguard_query_log", "write_file", "web_search"}

	scaffolding := []string{
		// The exact payload from the incident: delivered to a user's phone as if it
		// were a notification.
		"<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"adguard_query_log\">\n" +
			"<｜DSML｜parameter name=\"limit\" string=\"false\">10</｜DSML｜parameter>\n" +
			"</｜DSML｜invoke>\n</｜DSML｜tool_calls>",
		// Other dialects, so the rule is not fitted to one vendor.
		"<tool_call>{\"name\": \"web_search\", \"arguments\": {\"query\": \"x\"}}</tool_call>",
		"<function=write_file>{\"path\": \"notes/a.md\"}</function>",
	}
	for _, s := range scaffolding {
		if !LooksLikeToolScaffolding(s, tools) {
			t.Errorf("LooksLikeToolScaffolding(%.40q) = false, want true — this would reach the user", s)
		}
	}

	prose := []string{
		// The case the prose fallback exists for: a real message whose [CHAT] marker
		// the model forgot. Suppressing this would be worse than the bug being fixed.
		"3 new blocked domains overnight: doubleclick.net, app-measurement.com.",
		"I checked AdGuard and nothing new was blocked since yesterday.",
		// Naming a tool in a sentence is prose, not a call.
		"I used adguard_query_log to fetch the overnight entries and found nothing new.",
		// Angle brackets in ordinary writing must not trip the markup heuristic.
		"Latency was <100ms for every request, so nothing looked wrong.",
		"",
	}
	for _, s := range prose {
		if LooksLikeToolScaffolding(s, tools) {
			t.Errorf("LooksLikeToolScaffolding(%.40q) = true, want false — a real message was suppressed", s)
		}
	}
}

// Every API run offers a tool literally named `glob`, so a bare-substring test for a
// tool name suppressed any genuine message containing "global" or "globe" that also
// carried a bracketed aside. The name must appear as a whole token — anywhere in the
// text, still, not only inside the markup.
func TestLooksLikeToolScaffoldingMatchesWholeTokensOnly(t *testing.T) {
	tools := []string{"glob", "web_search", "read_file"}

	notFlagged := []string{
		// "glob" is a fragment of "global"/"globe" here. Each carries a real markup
		// token, so rule 1 IS reached and the boundary test is what has to save it.
		"Our global dashboard still renders a literal <name> placeholder in the header, which nobody has fixed.",
		"The globe icon is misaligned inside the <header> element on narrow screens, but only in dark mode.",
		// Underscored names must not match through a longer identifier either.
		"The my_web_search_helper column was <null> for every row I checked.",
	}
	for _, s := range notFlagged {
		if LooksLikeToolScaffolding(s, tools) {
			t.Errorf("LooksLikeToolScaffolding(%.50q) = true, want false — a real message was suppressed", s)
		}
	}

	flagged := []string{
		// A real call naming the same tool: delimited by non-word bytes, so it matches.
		"<tool_call>{\"name\": \"glob\", \"arguments\": {\"pattern\": \"**/*.md\"}}</tool_call>",
		// The full-width bars some providers use count as boundaries, not as cover.
		"<｜DSML｜invoke name=｜glob｜>",
	}
	for _, s := range flagged {
		if !LooksLikeToolScaffolding(s, tools) {
			t.Errorf("LooksLikeToolScaffolding(%.50q) = false, want true — this would reach the user", s)
		}
	}
}

// With no registry (a CLI coder reports none) the name rule is inert and only the
// markup-density backstop applies.
func TestLooksLikeToolScaffoldingWithoutRegistry(t *testing.T) {
	if !LooksLikeToolScaffolding("<tool_call><invoke name=\"x\"><parameter/></invoke></tool_call>", nil) {
		t.Error("dense markup with no registry should still be refused")
	}
	if LooksLikeToolScaffolding("Nothing new to report today.", nil) {
		t.Error("plain prose with no registry must be delivered")
	}
}
