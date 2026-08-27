package agentdesigner

import "testing"

func TestSpecDeclaresIrreversible(t *testing.T) {
	yes := []string{
		"Irreversible actions: yes — pays the invoice",
		"Irreversible actions: yes",
		"irreversible actions:yes",
		"Irreversible: YES - deletes the row",
	}
	for _, line := range yes {
		if !SpecDeclaresIrreversible("Tier: 1\n" + line + "\nSkills: none") {
			t.Errorf("%q not read as declaring an irreversible action", line)
		}
	}
}

func TestSpecDeclaresIrreversibleReadsNo(t *testing.T) {
	for _, line := range []string{
		"Irreversible actions: no",
		"Irreversible actions: none",
		"Irreversible actions:",
		"Irreversible actions: no — it only reads the page",
	} {
		if SpecDeclaresIrreversible("Tier: 1\n" + line) {
			t.Errorf("%q read as declaring an irreversible action", line)
		}
	}
}

// A spec with no such line means no. Defaulting the other way would put a
// payment warning on the build button of every agent whose model omitted a
// line, which teaches the user that the warning is noise — and the warning only
// works if it is rare.
func TestASpecWithoutTheLineIsNotDestructive(t *testing.T) {
	spec := "Tier: 1\nSchedule: 0 8 * * *\nNotifies user: yes\nSkills: none\n"
	if SpecDeclaresIrreversible(spec) {
		t.Error("a spec with no irreversible line was treated as destructive")
	}
}

// Prose mentioning deletion is not a declaration — the line is.
func TestProseInTheSpecIsNotADeclaration(t *testing.T) {
	spec := "Tier: 1\nKnowledge base writes: notes/log.md\nSecrets: none\n" +
		"External services: the billing portal, to read (never to pay or delete)\n"
	if SpecDeclaresIrreversible(spec) {
		t.Error("prose was read as a declaration")
	}
}

// The interface only asks about this once a plan is SETTLED. Asking mid-question
// would put a payment warning under "which page should I watch?".
func TestPlanDestructiveRequiresASettledPlan(t *testing.T) {
	spec := "Tier: 1\nIrreversible actions: yes — pays the bill\n"
	if !SpecDeclaresIrreversible(spec) {
		t.Fatal("fixture does not declare an irreversible action")
	}
	// planFromHistory returns ready=false when no assistant turn carries a spec,
	// and PlanDestructive is derived as planReady && declared.
	if _, ready := planFromHistory(nil); ready {
		t.Error("an empty history reported a settled plan")
	}
}
