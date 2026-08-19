package agentstate

import (
	"testing"
)

// A well-formed but EMPTY fence must read as UNDERSTOOD.
//
// This is not an exotic shape: it is what saveState writes after any run that
// emits no [STATE], so it is the commonest state.md on a live install. Reporting
// it as not-understood disables the runner's end-of-turn self-heal for every
// agent with legitimately empty state, and makes the state.json migration's
// verify-read fail forever — state.json never deleted, an error logged on every
// boot.
//
// It regressed because fenceLoc.OrphanOpen's zero value is a valid line index,
// so a struct literal that set OK: true and omitted OrphanOpen looked like
// "damaged at line 0" to read()'s `OrphanOpen < 0` test.
func TestWellFormedEmptyFenceIsUnderstood(t *testing.T) {
	p := write(t, "# State — a\n\n*intro*\n\n```json\n{}\n```\n")

	st, understood, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if !understood {
		t.Fatal("an empty fence is a fresh agent, not a damaged file")
	}
	if len(st) != 0 {
		t.Fatalf("expected empty state, got %#v", st)
	}
}

// The same, without the surrounding template — the bare shape a test or an
// early migration can produce.
func TestBareEmptyFenceIsUnderstood(t *testing.T) {
	p := write(t, "```json\n{}\n```\n")

	_, understood, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if !understood {
		t.Fatal("a bare empty fence must still read as understood")
	}
}
