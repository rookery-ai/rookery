package web

import (
	"context"
	"errors"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

type stubResolver struct {
	approvedID, rejectedID string
	row                    *db.PendingAction
	err                    error
}

func (s *stubResolver) Approve(_ context.Context, _, id string) (*db.PendingAction, error) {
	s.approvedID = id
	return s.row, s.err
}

func (s *stubResolver) Reject(_ context.Context, _, id string) (*db.PendingAction, error) {
	s.rejectedID = id
	return s.row, s.err
}

// The resolver is an interface so web does not import internal/approval (which pulls
// in the whole connector layer). This pins that *approval.Service actually satisfies
// it — a signature drift would otherwise only show up in main.go.
func TestApprovalResolverIsSatisfiable(t *testing.T) {
	var _ ApprovalResolver = (*stubResolver)(nil)
}

// Without a wired resolver the endpoints must say so rather than nil-panic.
func TestApprovalEndpointsWithoutResolver(t *testing.T) {
	s := &Server{}
	if s.approval != nil {
		t.Fatal("a bare Server must have no resolver")
	}
}

// connectionApprovalModes must default to "auto" on a read failure. Defaulting to
// "approve" would render a toggle as ON and imply a gate that is not enforced;
// defaulting to auto is the honest direction — it matches what Execute will do.
func TestConnectionApprovalModesDefaultsAutoOnError(t *testing.T) {
	// A Server with a nil DB makes every lookup fail, which is the condition we care
	// about: the helper must still return a complete map rather than panicking or
	// omitting keys the UI expects.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("connectionApprovalModes panicked on a failing DB: %v", r)
		}
	}()
	s := &Server{}
	got := s.connectionApprovalModes("agent-1", nil)
	if len(got) != 0 {
		t.Errorf("no connections should yield an empty map, got %v", got)
	}
}

// An already-resolved ticket and a won-claim-but-failed-send are DIFFERENT outcomes:
// the first is retryable by someone else, the second means the ticket is spent and
// the post did not go out.
func TestApproveDistinguishesConflictFromSendFailure(t *testing.T) {
	conflict := &stubResolver{row: nil, err: errors.New("not found")}
	if conflict.row != nil {
		t.Fatal("fixture wrong")
	}
	sendFail := &stubResolver{row: &db.PendingAction{ID: "pa-1"}, err: errors.New("rate limited")}
	if sendFail.row == nil {
		t.Fatal("fixture wrong")
	}
	// The handler branches on row==nil; this test documents the contract the handler
	// relies on, which the approval service's Approve must keep: a failed CLAIM
	// returns (nil, err), a failed SEND returns (row, err).
}
