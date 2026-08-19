package agentstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A brand-new agent has NO state.md — nothing seeds one (see
// agentdesigner/flow.go's "No state file is seeded here"). So the first write
// of its first run creates the file from the template, and every write after
// that only splices the fence. A blank agent name written into the heading at
// that moment is therefore permanent, not something a later run repairs.
//
// This is what made an uncalled Coder.WithAgentName a real defect rather than
// dead code: the state tools resolved the path from the working directory but
// carried no name, so the first set_state on a fresh agent would have produced
// "# State — " and left it there forever.
func TestCreatingStateWithABlankNameIsVisibleInTheHeading(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.md")

	if _, err := Apply(p, "", map[string]any{"seen": 1}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)

	// Not an assertion that blank is REJECTED — Apply cannot know the caller
	// forgot a name. It pins the consequence, so the wiring that supplies the
	// name has a test to fail if it is ever removed again.
	if !strings.Contains(string(raw), "# State — \n") {
		t.Fatalf("expected a blank heading from a blank name, got:\n%s", raw)
	}
}

// The same call with a real name produces the heading the KB browser shows and
// the fidelity corpus pins.
func TestCreatingStateWithANameHeadsTheFileWithIt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.md")

	if _, err := Apply(p, "hn-watch", map[string]any{"seen": 1}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "# State — hn-watch") {
		t.Fatalf("agent name missing from a freshly created state file:\n%s", raw)
	}
	st, understood, err := Get(p)
	if err != nil || !understood || st["seen"] == nil {
		t.Fatalf("state not readable back: %#v understood=%v err=%v", st, understood, err)
	}
}
