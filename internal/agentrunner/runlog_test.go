package agentrunner

import (
	"os"
	"strings"
	"testing"
)

// `agentrunner: run finished` exists so a silent run can be diagnosed from the
// log instead of from the database. Two of the warnings it counts are appended
// in the DELIVERY phase:
//
//	"no [CHAT] marker emitted; delivered prose as fallback"
//	"no deliverable prose (markers only, or tool-call scaffolding) — nothing sent"
//
// The log statement used to sit above that phase, so it read len(rctx.warnings)
// before either could be appended and reported warnings=0 for precisely the runs
// it was built to explain. Observed live on three runs that carried the second
// string in agent_runs.stderr while logging warnings=0 — diagnosing them meant
// opening the database, the exact outcome this line exists to prevent.
//
// The fix is to emit it from a DEFERRED closure: Run has three exit paths (coder
// error, produced-nothing, success) and only the third reaches the delivery
// phase, so moving the statement down the function would silently drop the line
// for the other two. A defer runs on all three, once, with the final counts.
//
// Asserted by reading the source, following the precedent set by
// TestRunInjectsTheStateBridgeEnvVars in this package and for the same reason:
// Run needs a live coder, a database, a vault and a workspace, and the property
// at issue is purely WHERE the statement sits relative to the appends. This is a
// structural proxy, not a behavioural test, and it is chosen deliberately over
// building a full harness to assert one log field.
func TestRunFinishedLogIsDeferredSoLateWarningsAreCounted(t *testing.T) {
	src, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	const logCall = `slog.Info("agentrunner: run finished"`
	idx := strings.Index(s, logCall)
	if idx < 0 {
		t.Fatal("the run-finished log is gone; silent runs become undiagnosable from the log")
	}
	if strings.Count(s, logCall) != 1 {
		t.Fatal("more than one run-finished log: two lines reporting overlapping fields " +
			"is how the counts drift apart again")
	}

	// The statement must be the body of a deferred closure. Look at what precedes
	// it, ignoring whitespace and comments.
	before := s[:idx]
	deferIdx := strings.LastIndex(before, "defer func() {")
	if deferIdx < 0 {
		t.Fatal("run-finished log is not inside a deferred closure — it will read " +
			"len(rctx.warnings) before the delivery phase appends and report 0")
	}
	between := before[deferIdx:]
	if strings.Contains(between, "\n\t}()") {
		t.Fatal("the nearest preceding `defer func() {` closes before the log statement, " +
			"so the log is not deferred")
	}

	// And the two delivery-phase appends must still come after it in the file. If
	// someone moves them above the defer registration this test would pass while
	// meaning nothing, so pin their position too.
	for _, warn := range []string{
		`"no [CHAT] marker emitted; delivered prose as fallback"`,
		`"no deliverable prose (markers only, or tool-call scaffolding) — nothing sent"`,
	} {
		w := strings.Index(s, warn)
		if w < 0 {
			t.Errorf("delivery-phase warning %s is gone — if it was renamed, re-check that "+
				"the run-finished count still includes it", warn)
			continue
		}
		if w < deferIdx {
			t.Errorf("delivery warning %s now precedes the deferred logger; this test no "+
				"longer proves anything", warn)
		}
	}
}
