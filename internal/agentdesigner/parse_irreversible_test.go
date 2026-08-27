package agentdesigner

import "testing"

func TestParseIrreversibleLineReadsTheDeclaration(t *testing.T) {
	yes := []string{
		"# Irreversible actions: yes",
		"# Irreversible actions: YES",
		"## Irreversible action - yes",
		"# Destructive actions: true",
		"# Irreversible actions: `yes`",
	}
	for _, md := range yes {
		if !ParseIrreversibleLine("# Agent\n" + md + "\n\nDo the thing.") {
			t.Errorf("%q not read as a declaration", md)
		}
	}
}

func TestParseIrreversibleLineReadsANegative(t *testing.T) {
	for _, md := range []string{
		"# Irreversible actions: no",
		"# Irreversible actions: none",
		"# Irreversible actions:",
	} {
		if ParseIrreversibleLine("# Agent\n" + md) {
			t.Errorf("%q read as a declaration", md)
		}
	}
}

// A missing header must read as "no". Defaulting the other way would put a
// payment warning on every agent whose model forgot the line, and a warning that
// appears everywhere is one nobody reads. The false negative is covered
// elsewhere: the first run that actually attempts the action is refused and
// records the finding itself.
func TestAMissingHeaderMeansNo(t *testing.T) {
	if ParseIrreversibleLine("# My agent\n\nRead a page and report.") {
		t.Error("an agent with no declaration was treated as irreversible")
	}
}

// The word "delete" appearing in ordinary prose is not a declaration.
func TestProseIsNotMistakenForADeclaration(t *testing.T) {
	md := "# My agent\n\nRead the invoices. Do not delete anything or pay any bills.\n"
	if ParseIrreversibleLine(md) {
		t.Error("prose was read as a declaration")
	}
}

// The schedule and skills headers sit alongside it and must not be confused for
// it — a false positive here is a warning on an agent that never needed one.
func TestOtherHeadersAreIgnored(t *testing.T) {
	md := "# Suggested schedule: 0 8 * * 1\n# Skills: pdf, web-research\n# Connections: none\n"
	if ParseIrreversibleLine(md) {
		t.Error("an unrelated header was read as a declaration")
	}
}
