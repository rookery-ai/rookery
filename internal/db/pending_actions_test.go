package db_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
)

func pendingFixture(t *testing.T) (*db.DB, context.Context, string, string) {
	t.Helper()
	d, agentID, ws := connTestDB(t)
	ctx := context.Background()
	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "conn-1", WorkspaceID: ws, Provider: "linkedin", AccountLabel: "me",
	}); err != nil {
		t.Fatal(err)
	}
	return d, ctx, ws, agentID
}

func park(t *testing.T, d *db.DB, ctx context.Context, ws, agentID, id string) {
	t.Helper()
	if err := d.CreatePendingAction(ctx, &db.PendingAction{
		ID: id, WorkspaceID: ws, AgentID: agentID, AgentName: "poster",
		ConnectionID: "conn-1", Provider: "linkedin", Action: "linkedin_create_post",
		ArgsJSON: `{"text":"hello"}`, Summary: "Post to LinkedIn: hello",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPendingActionRoundTrip(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-1")

	got, err := d.GetPendingAction(ctx, ws, "pa-1")
	if err != nil {
		t.Fatalf("GetPendingAction: %v", err)
	}
	if got.Status != db.PendingStatusPending {
		t.Errorf("status = %q, want %q", got.Status, db.PendingStatusPending)
	}
	if got.ArgsJSON != `{"text":"hello"}` || got.Action != "linkedin_create_post" {
		t.Errorf("round trip lost data: %+v", got)
	}
	if got.ResolvedAt != nil {
		t.Error("a pending row must have no resolved_at")
	}
}

// A ticket id from another workspace must not resolve — the id is handed to a coder
// and travels through chat, so it is not a secret.
func TestPendingActionIsWorkspaceScoped(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-1")

	if _, err := d.GetPendingAction(ctx, "some-other-workspace", "pa-1"); err == nil {
		t.Fatal("a pending action must not be readable from another workspace")
	}
	if _, err := d.ClaimPendingAction(ctx, "some-other-workspace", "pa-1", db.PendingStatusApproved); err == nil {
		t.Fatal("a pending action must not be claimable from another workspace")
	}
}

// The whole point of the claim: chat's /approve and the web inbox button can fire
// simultaneously, and an unguarded read-then-update would publish the post twice.
func TestClaimPendingActionIsSingleWinner(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-1")

	const racers = 8
	var wg sync.WaitGroup
	wins := make(chan string, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			if _, err := d.ClaimPendingAction(ctx, ws, "pa-1", db.PendingStatusApproved); err == nil {
				wins <- "won"
			}
		}()
	}
	wg.Wait()
	close(wins)

	n := 0
	for range wins {
		n++
	}
	if n != 1 {
		t.Fatalf("%d concurrent claims succeeded, want exactly 1 — the post would be sent %d times", n, n)
	}
}

// Approving something already rejected must fail rather than resurrect it.
func TestClaimRejectedCannotBeApproved(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-1")

	if _, err := d.ClaimPendingAction(ctx, ws, "pa-1", db.PendingStatusRejected); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if _, err := d.ClaimPendingAction(ctx, ws, "pa-1", db.PendingStatusApproved); err == nil {
		t.Fatal("a rejected action must not be approvable afterwards")
	}
	got, _ := d.GetPendingAction(ctx, ws, "pa-1")
	if got.Status != db.PendingStatusRejected {
		t.Errorf("status = %q, want %q", got.Status, db.PendingStatusRejected)
	}
	if got.ResolvedAt == nil {
		t.Error("a resolved action must record resolved_at")
	}
}

// Claiming into a non-terminal or nonsense status is a programming error, not a
// silent no-op.
func TestClaimRejectsBadTargetStatus(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-1")

	for _, bad := range []string{db.PendingStatusPending, db.PendingStatusFailed, "nonsense"} {
		if _, err := d.ClaimPendingAction(ctx, ws, "pa-1", bad); err == nil {
			t.Errorf("claiming into %q should be rejected", bad)
		}
	}
}

func TestFinishRecordsFailure(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-1")
	if _, err := d.ClaimPendingAction(ctx, ws, "pa-1", db.PendingStatusApproved); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishPendingAction(ctx, ws, "pa-1", "", "rate limited"); err != nil {
		t.Fatalf("FinishPendingAction: %v", err)
	}
	got, _ := d.GetPendingAction(ctx, ws, "pa-1")
	if got.Status != db.PendingStatusFailed || got.Error != "rate limited" {
		t.Errorf("failure not recorded: status=%q err=%q", got.Status, got.Error)
	}
}

func TestListPendingFiltersByStatus(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-1")
	park(t, d, ctx, ws, agentID, "pa-2")
	if _, err := d.ClaimPendingAction(ctx, ws, "pa-2", db.PendingStatusRejected); err != nil {
		t.Fatal(err)
	}

	pending, err := d.ListPendingActions(ctx, ws, db.PendingStatusPending, 50)
	if err != nil {
		t.Fatalf("ListPendingActions: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "pa-1" {
		t.Fatalf("want only pa-1 pending, got %+v", pending)
	}
	all, _ := d.ListPendingActions(ctx, ws, "", 50)
	if len(all) != 2 {
		t.Errorf("want 2 rows unfiltered, got %d", len(all))
	}
}

// An unresolved post from last week should not be sendable — and an unbounded queue
// becomes a list nobody reads.
func TestExpireOnlyTouchesPending(t *testing.T) {
	d, ctx, ws, agentID := pendingFixture(t)
	park(t, d, ctx, ws, agentID, "pa-old")
	park(t, d, ctx, ws, agentID, "pa-done")
	if _, err := d.ClaimPendingAction(ctx, ws, "pa-done", db.PendingStatusApproved); err != nil {
		t.Fatal(err)
	}

	// Nothing is old enough yet.
	if n, err := d.ExpirePendingActionsOlderThan(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("expected no expiries, got n=%d err=%v", n, err)
	}

	// Everything is older than zero.
	n, err := d.ExpirePendingActionsOlderThan(ctx, 0)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired %d rows, want 1 (only the pending one)", n)
	}
	done, _ := d.GetPendingAction(ctx, ws, "pa-done")
	if done.Status != db.PendingStatusApproved {
		t.Errorf("an already-approved action must not be expired, got %q", done.Status)
	}
}
