package agentdesigner

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

const sampleSpec = `Tier: 1
Schedule: 0 8 * * *
Notifies user: yes ([CHAT] contains: the deal list)
Knowledge base writes: notes/deals.md
Secrets: none
External services: none`

func proposal(prose string) string {
	return prose + "\n\n" + specOpen + "\n" + sampleSpec + "\n" + specClose
}

func TestExtractTechnicalSpecNeedsACloser(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool // want a non-empty extraction
	}{
		{"well formed", proposal("Here's the plan."), true},
		{"no block at all", "Which site should I watch?", false},
		{
			// A response truncated by a token cap ends mid-block. Treating that as
			// a finished plan would arm the build button under a proposal the model
			// never got to write.
			name: "unterminated opener",
			in:   "Here's the plan.\n\n" + specOpen + "\nTier: 1\nSchedule:",
			want: false,
		},
		{"closer with no opener", "Here's the plan.\n" + specClose, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTechnicalSpec(tt.in)
			if (got != "") != tt.want {
				t.Fatalf("extractTechnicalSpec(%q) = %q, want non-empty=%v", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractTechnicalSpecTakesTheLastBlock(t *testing.T) {
	// A long conversation accumulates one block per proposal turn. The current
	// plan is the most recent one.
	in := proposal("First plan.") + "\n\n" + specOpen + "\nTier: 3\n" + specClose
	got := extractTechnicalSpec(in)
	if !strings.Contains(got, "Tier: 3") {
		t.Fatalf("extractTechnicalSpec took the wrong block: %q", got)
	}
}

func TestStripTechnicalSpecLeavesTheProse(t *testing.T) {
	got := stripTechnicalSpec(proposal("Here's the plan.\n\n- Watch the page\n- Message you"))
	if strings.Contains(got, "TECHNICAL SPEC") || strings.Contains(got, "Tier:") {
		t.Fatalf("spec block survived the strip: %q", got)
	}
	for _, want := range []string{"Here's the plan.", "- Watch the page", "- Message you"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stripTechnicalSpec dropped prose %q from: %q", want, got)
		}
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("stripTechnicalSpec left trailing whitespace: %q", got)
	}
}

func TestStripTechnicalSpecDropsAnUnterminatedBlock(t *testing.T) {
	// Asymmetric with extractTechnicalSpec on purpose: a half-written block is
	// not a plan (must not arm the button) but also not prose (must not be shown).
	got := stripTechnicalSpec("Here's the plan.\n\n" + specOpen + "\nTier: 1\nSched")
	if strings.Contains(got, "Tier:") || strings.Contains(got, "TECHNICAL SPEC") {
		t.Fatalf("unterminated block survived: %q", got)
	}
	if !strings.Contains(got, "Here's the plan.") {
		t.Fatalf("prose before the block was dropped: %q", got)
	}
}

func TestPlanFromHistoryReadsOnlyTheLastAssistantTurn(t *testing.T) {
	tests := []struct {
		name string
		hist []db.ChatMessage
		want bool
	}{
		{
			name: "proposal turn arms the plan",
			hist: []db.ChatMessage{
				{Role: "user", Content: "watch this page"},
				{Role: "assistant", Content: proposal("Here's the plan.")},
			},
			want: true,
		},
		{
			// The retraction case. A latch-once-true flag would be a worse defect
			// than the one this replaces: the button would stay armed under a
			// question the user has not yet answered.
			name: "a later question retracts it",
			hist: []db.ChatMessage{
				{Role: "assistant", Content: proposal("Here's the plan.")},
				{Role: "user", Content: "actually make it hourly"},
				{Role: "assistant", Content: "Sure — at the top of every hour, or every 60 minutes from now?"},
			},
			want: false,
		},
		{
			name: "clarifying question alone",
			hist: []db.ChatMessage{
				{Role: "user", Content: "watch a page"},
				{Role: "assistant", Content: "Which page?"},
			},
			want: false,
		},
		{
			// roleNote is coder-facing steering recorded after a failed build,
			// never a proposal. Folding it in would let a failure note mask the
			// real last proposal.
			name: "a trailing note does not mask the proposal",
			hist: []db.ChatMessage{
				{Role: "assistant", Content: proposal("Here's the plan.")},
				{Role: roleNote, Content: "the last build timed out"},
			},
			want: true,
		},
		{name: "empty history", hist: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ready := planFromHistory(tt.hist)
			if ready != tt.want {
				t.Fatalf("planFromHistory ready = %v, want %v", ready, tt.want)
			}
			if ready && spec == "" {
				t.Fatal("planFromHistory reported ready with an empty spec")
			}
		})
	}
}

func TestApproveAndBuildIsAnApproval(t *testing.T) {
	// The web button SENDS this phrase. isApproval is exact-match, so a phrase
	// missing here falls through to an ordinary design turn and the button
	// silently does nothing — which is the whole failure mode this guards.
	for _, s := range []string{
		"approve and build it", "Approve and build it", "approve and build",
		"approve & build it", "approve and build it.",
	} {
		if !isApproval(s) {
			t.Errorf("isApproval(%q) = false, want true — the build button sends this", s)
		}
		if !isVerifyApproval(s) {
			t.Errorf("isVerifyApproval(%q) = false, want true", s)
		}
	}
	// The pre-existing strictness must survive: a casual confirmation while
	// answering design questions still must not launch a build.
	for _, s := range []string{"ok", "yes", "sure", "approve and build the second one"} {
		if isApproval(s) {
			t.Errorf("isApproval(%q) = true, want false — strictness regressed", s)
		}
	}
}

// TestVerifyApprovalAcceptsNaturalConfirmations pins the words a person actually
// types at a finished build. This is not a nicety: a confirmation that misses the
// list is routed to the CHANGE-REQUEST branch, which drops the FSM back to
// designing — and the next "approve" then matches the DESIGNING predicate and
// launches a whole second build. The user is never told; they just watch their
// agent get rebuilt instead of saved.
//
// Observed in production: "Approved" cost a six-minute rebuild, because the list
// had "approve" and "confirmed" but not "approved". Every entry below is one the
// past-tense/synonym gap would otherwise swallow.
func TestVerifyApprovalAcceptsNaturalConfirmations(t *testing.T) {
	for _, s := range []string{
		"Approved", "approved", "approved.", "Accept", "accepted", "accept it",
		"save agent", "Save agent", "save the agent", "yep", "yeah", "sure",
		"sounds good", "go for it", "that's fine", "all good",
	} {
		if !isVerifyApproval(s) {
			t.Errorf("isVerifyApproval(%q) = false, want true — this silently rebuilds instead of saving", s)
		}
	}

	// The negative-cue guard must survive: these are change requests wearing
	// approval-shaped words, and treating one as a save would ship an agent the
	// user had just asked to alter.
	for _, s := range []string{
		"approved, but change the time", "not yet", "don't save it",
		"accepted apart from the schedule — change it to 9", "wait",
		"looks good but use a different sheet instead",
	} {
		if isVerifyApproval(s) {
			t.Errorf("isVerifyApproval(%q) = true, want false — a change request was read as approval", s)
		}
	}

	// Approving must stay distinct from approving-and-building: the strict
	// designing-state test must NOT grow these, or a casual "sure" mid-design
	// launches a build.
	for _, s := range []string{"approved", "accept", "save agent", "yep", "sounds good"} {
		if isApproval(s) {
			t.Errorf("isApproval(%q) = true, want false — designing-state strictness regressed", s)
		}
	}
}
