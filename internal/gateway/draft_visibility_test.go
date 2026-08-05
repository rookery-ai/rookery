package gateway_test

import (
	"context"
	"strings"
	"testing"
)

// A build that never reaches approval leaves agents/draft_<slug>/ on disk with no
// agents row. /run then answered a flat `agent "x" not found` immediately after
// the designer had said it built one — two true statements that read as a
// contradiction, with nothing pointing at the way out.
//
// The reported transcript ends on exactly this: /run hackernews → not found,
// while the draft was sitting there and was later finished through the web UI.

func TestRunOnAnUnsavedDraftExplainsRatherThanDenying(t *testing.T) {
	r, database, _, _ := newTestRouter(t)
	seedAgentDraft(t, database, "hackernews")

	send, got := collect()
	if err := r.Handle(context.Background(), testMsg("/run hackernews"),
		send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	joined := strings.Join(*got, "\n")
	if !strings.Contains(joined, "never saved") {
		t.Errorf("replies = %q, want an explanation that the build was not saved", joined)
	}
	if !strings.Contains(joined, "/agent create hackernews") {
		t.Errorf("replies = %q, want it to name the command that resumes the draft", joined)
	}
	// It must NOT start running, because there is nothing to run.
	if strings.Contains(joined, "Running agent") {
		t.Errorf("replies = %q, must not claim to run an unsaved draft", joined)
	}
}

// Matching is case-insensitive: a user who typed the name with different casing
// gets the same explanation, not the confusing denial.
func TestRunDraftHintIsCaseInsensitive(t *testing.T) {
	r, database, _, _ := newTestRouter(t)
	seedAgentDraft(t, database, "hackernews")

	send, got := collect()
	if err := r.Handle(context.Background(), testMsg("/run HackerNews"),
		send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(strings.Join(*got, "\n"), "never saved") {
		t.Errorf("replies = %q, want the draft explanation", *got)
	}
}

// A name that matches no agent AND no draft must fall through to the normal run
// path, so this cannot mask a genuine "no such agent".
func TestRunOnAnUnrelatedNameStillRunsNormally(t *testing.T) {
	r, database, _, _ := newTestRouter(t)
	seedAgentDraft(t, database, "hackernews")

	send, got := collect()
	if err := r.Handle(context.Background(), testMsg("/run something-else"),
		send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	joined := strings.Join(*got, "\n")
	if strings.Contains(joined, "never saved") {
		t.Errorf("replies = %q, must not claim an unrelated name is a draft", joined)
	}
}

// The draft is otherwise invisible in chat, which is how a user ends up believing
// in an agent that /run cannot find.
func TestAgentListShowsAnUnfinishedDraft(t *testing.T) {
	r, database, _, _ := newTestRouter(t)
	seedAgentDraft(t, database, "hackernews")

	send, got := collect()
	if err := r.Handle(context.Background(), testMsg("/agent list"),
		send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	joined := strings.Join(*got, "\n")
	if !strings.Contains(joined, "hackernews") {
		t.Errorf("replies = %q, want the draft listed", joined)
	}
	if !strings.Contains(joined, "Unfinished") {
		t.Errorf("replies = %q, want the draft marked as unfinished", joined)
	}
}

// With no draft the listing must be exactly as before — this cannot add noise to
// the common case.
func TestAgentListWithoutADraftIsUnchanged(t *testing.T) {
	r, _, _, _ := newTestRouter(t)

	send, got := collect()
	if err := r.Handle(context.Background(), testMsg("/agent list"),
		send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	joined := strings.Join(*got, "\n")
	if strings.Contains(joined, "Unfinished") {
		t.Errorf("replies = %q, want no draft section", joined)
	}
	if !strings.Contains(joined, "no agents yet") {
		t.Errorf("replies = %q, want the empty-state message", joined)
	}
}
