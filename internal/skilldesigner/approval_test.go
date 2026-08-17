package skilldesigner

import "testing"

// TestVerifyApprovalAcceptsNaturalConfirmations mirrors the agent designer's test
// of the same name, because the two designers share ONE DesignerSurface and one
// set of buttons. A word that saves an agent but rebuilds a skill is the worst
// kind of inconsistency — invisible until it costs someone a full build.
//
// The failure this guards is not "the word was not recognised". It is that an
// unrecognised confirmation is routed to the CHANGE-REQUEST branch, dropping the
// FSM back to designing, so the user's next "approve" matches the designing-state
// predicate and starts a second build instead of saving the first.
func TestVerifyApprovalAcceptsNaturalConfirmations(t *testing.T) {
	for _, s := range []string{
		"Approved", "approved", "approved.", "Accept", "accepted", "accept it",
		"save skill", "save the skill", "yep", "yeah", "sure",
		"sounds good", "go for it", "that's fine", "all good",
	} {
		if !isVerifyApproval(s) {
			t.Errorf("isVerifyApproval(%q) = false, want true — this silently rebuilds instead of saving", s)
		}
	}

	// The negative-cue guard must survive: saving a skill the user had just asked
	// to alter is worse than making them repeat themselves.
	for _, s := range []string{
		"approved, but change the name", "not yet", "don't save it",
		"accepted apart from the script — change it", "wait",
		"looks good but use a different approach instead",
	} {
		if isVerifyApproval(s) {
			t.Errorf("isVerifyApproval(%q) = true, want false — a change request was read as approval", s)
		}
	}
}
