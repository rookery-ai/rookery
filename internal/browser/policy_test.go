package browser

import (
	"errors"
	"testing"
)

// Reading during a build must keep working. A rehearsal that cannot look at the
// page cannot rehearse, and a build that silently loses the browser would send
// the model hunting for a script-based workaround.
func TestBuildPhaseAllowsReadingAndRefusesActing(t *testing.T) {
	pol := Policy{BuildPhase: true, AllowActing: true, AllowIrreversible: true}
	if err := CheckAct(pol, ActionRead, ""); err != nil {
		t.Fatalf("read refused during build: %v", err)
	}
	if err := CheckAct(pol, ActionWait, ""); err != nil {
		t.Fatalf("wait refused during build: %v", err)
	}
	for _, a := range []Action{ActionClick, ActionFill, ActionPress} {
		err := CheckAct(pol, a, "Continue")
		if !errors.Is(err, ErrActingDisabled) {
			t.Fatalf("%s permitted during a build: %v", a, err)
		}
	}
}

// The build-phase refusal must not be defeatable by the owner's own grants.
// A dry run happens before the agent has been approved at all, so a grant made
// for the finished agent cannot license clicking during its rehearsal.
func TestBuildPhaseOutranksEveryGrant(t *testing.T) {
	err := CheckAct(Policy{BuildPhase: true, AllowActing: true, AllowIrreversible: true}, ActionClick, "Pay now")
	if !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("grants overrode the build-phase refusal: %v", err)
	}
}

func TestActingIsRefusedWithoutAGrant(t *testing.T) {
	err := CheckAct(Policy{}, ActionClick, "Next")
	if !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("clicking allowed with no grant: %v", err)
	}
}

func TestIrreversibleActionsNeedTheirOwnGrant(t *testing.T) {
	pol := Policy{AllowActing: true}
	if err := CheckAct(pol, ActionClick, "Next page"); err != nil {
		t.Fatalf("ordinary click refused: %v", err)
	}
	err := CheckAct(pol, ActionClick, "Pay now")
	if !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("irreversible click allowed on the acting grant alone: %v", err)
	}
	if err := CheckAct(Policy{AllowActing: true, AllowIrreversible: true}, ActionClick, "Pay now"); err != nil {
		t.Fatalf("irreversible click refused despite its grant: %v", err)
	}
}

// The heuristic must not fire on ordinary navigation. If "Payment history" were
// treated as irreversible, an owner would have to grant the irreversible tier
// just to let an agent BROWSE a billing site — which would make the tier
// meaningless exactly where it matters most.
func TestIrreversibleHeuristicIgnoresOrdinaryNavigation(t *testing.T) {
	safe := []string{
		"Payment history", "Paypal settings", "Payments", "Buyer protection",
		"Deleted items", "Order history", "Transfers overview", "About paying",
	}
	for _, name := range safe {
		if LooksIrreversible(name) {
			t.Errorf("%q classified as irreversible", name)
		}
	}
}

func TestIrreversibleHeuristicCatchesTheRealOnes(t *testing.T) {
	risky := []string{
		"Pay", "Pay now", "Confirm payment", "Place order", "Submit order",
		"Buy now", "Delete", "Transfer", "Withdraw", "Cancel subscription",
	}
	for _, name := range risky {
		if !LooksIrreversible(name) {
			t.Errorf("%q not classified as irreversible", name)
		}
	}
}
