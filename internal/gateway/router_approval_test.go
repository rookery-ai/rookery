package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

type fakeApproval struct {
	approved, rejected string
	err                error
	ret                *db.PendingAction
}

func (f *fakeApproval) Approve(_ context.Context, _, id string) (*db.PendingAction, error) {
	f.approved = id
	return f.ret, f.err
}

func (f *fakeApproval) Reject(_ context.Context, _, id string) (*db.PendingAction, error) {
	f.rejected = id
	return f.ret, f.err
}

func captureRouter(t *testing.T, svc ApprovalService) (*Router, *[]string) {
	t.Helper()
	r := &Router{}
	if svc != nil {
		r.WithApproval(svc)
	}
	var out []string
	return r, &out
}

// Chat clients wrap pasted ids in backticks or trailing punctuation; the id must
// survive that, or every copy-paste approval fails with "nothing pending".
func TestApprovalArgStripsChatPunctuation(t *testing.T) {
	fa := &fakeApproval{}
	r, out := captureRouter(t, fa)
	send := func(s string) { *out = append(*out, s) }

	for _, in := range []string{"pa-1", "`pa-1`", " pa-1 ", "(pa-1)", "pa-1.", "\"pa-1\""} {
		got, ok := r.approvalArg(in, send)
		if !ok || got != "pa-1" {
			t.Errorf("approvalArg(%q) = %q ok=%v, want pa-1", in, got, ok)
		}
	}
}

func TestApprovalArgRejectsEmptyWithGuidance(t *testing.T) {
	r, out := captureRouter(t, &fakeApproval{})
	send := func(s string) { *out = append(*out, s) }

	if _, ok := r.approvalArg("", send); ok {
		t.Fatal("an empty id must not be accepted")
	}
	if len(*out) == 0 || !strings.Contains((*out)[0], "/pending") {
		t.Errorf("the error should point at /pending, got %v", *out)
	}
}

// With no service wired the commands must explain themselves rather than panic on a
// nil interface.
func TestApprovalCommandsWithoutServiceAreSafe(t *testing.T) {
	r, out := captureRouter(t, nil)
	send := func(s string) { *out = append(*out, s) }

	if _, ok := r.approvalArg("pa-1", send); ok {
		t.Fatal("with no service the command must not proceed")
	}
	if len(*out) == 0 || !strings.Contains(strings.ToLower((*out)[0]), "aren't configured") {
		t.Errorf("expected a not-configured message, got %v", *out)
	}
}

func TestApproveHappyPath(t *testing.T) {
	fa := &fakeApproval{ret: &db.PendingAction{ID: "pa-1"}}
	r, out := captureRouter(t, fa)
	send := func(s string) { *out = append(*out, s) }

	if err := r.handleApprove(context.Background(), Message{WorkspaceID: "w1"}, "`pa-1`", send); err != nil {
		t.Fatalf("handleApprove: %v", err)
	}
	if fa.approved != "pa-1" {
		t.Errorf("approved id = %q, want pa-1", fa.approved)
	}
	if len(*out) == 0 || !strings.Contains((*out)[0], "Approved") {
		t.Errorf("expected a confirmation, got %v", *out)
	}
}

// An already-resolved ticket returns (nil, err) from the claim. The user gets a plain
// explanation, not a stack-trace-shaped error.
func TestApproveAlreadyResolvedExplains(t *testing.T) {
	fa := &fakeApproval{ret: nil, err: errors.New("not found")}
	r, out := captureRouter(t, fa)
	send := func(s string) { *out = append(*out, s) }

	if err := r.handleApprove(context.Background(), Message{WorkspaceID: "w1"}, "pa-1", send); err != nil {
		t.Fatalf("handleApprove: %v", err)
	}
	if len(*out) == 0 || !strings.Contains((*out)[0], "already") {
		t.Errorf("expected an already-resolved explanation, got %v", *out)
	}
}

// A claim that succeeded but whose send failed is a DIFFERENT message: the ticket is
// consumed and the post did not go out, which the user must not read as "not found".
func TestApproveSendFailureIsDistinct(t *testing.T) {
	fa := &fakeApproval{ret: &db.PendingAction{ID: "pa-1"}, err: errors.New("rate limited")}
	r, out := captureRouter(t, fa)
	send := func(s string) { *out = append(*out, s) }

	if err := r.handleApprove(context.Background(), Message{WorkspaceID: "w1"}, "pa-1", send); err != nil {
		t.Fatalf("handleApprove: %v", err)
	}
	got := (*out)[0]
	if !strings.Contains(got, "publishing failed") || !strings.Contains(got, "rate limited") {
		t.Errorf("expected a publish-failure message naming the cause, got %q", got)
	}
	if strings.Contains(got, "already") {
		t.Errorf("a send failure must not read as already-resolved: %q", got)
	}
}

func TestRejectHappyPath(t *testing.T) {
	fa := &fakeApproval{ret: &db.PendingAction{ID: "pa-2"}}
	r, out := captureRouter(t, fa)
	send := func(s string) { *out = append(*out, s) }

	if err := r.handleReject(context.Background(), Message{WorkspaceID: "w1"}, "pa-2", send); err != nil {
		t.Fatalf("handleReject: %v", err)
	}
	if fa.rejected != "pa-2" {
		t.Errorf("rejected id = %q, want pa-2", fa.rejected)
	}
	if len(*out) == 0 || !strings.Contains((*out)[0], "Declined") {
		t.Errorf("expected a decline confirmation, got %v", *out)
	}
}

// The commands must be listed, or nobody discovers them.
func TestHelpMentionsApprovalCommands(t *testing.T) {
	h := helpText("telegram")
	for _, want := range []string{"/pending", "/approve", "/reject"} {
		if !strings.Contains(h, want) {
			t.Errorf("help text is missing %s", want)
		}
	}
}
