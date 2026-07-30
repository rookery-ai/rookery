package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/ilijad1/rookery/internal/db"
)

// ApprovalService is the subset of *approval.Service the router needs. An interface
// rather than the concrete type so the gateway does not import the approval package
// (which imports connectors, which would drag the whole connector layer into every
// gateway test).
type ApprovalService interface {
	Approve(ctx context.Context, workspaceID, id string) (*db.PendingAction, error)
	Reject(ctx context.Context, workspaceID, id string) (*db.PendingAction, error)
}

// WithApproval wires the /pending, /approve, and /reject commands. Without it those
// commands report that approvals are not configured rather than erroring.
func (r *Router) WithApproval(s ApprovalService) *Router {
	r.approval = s
	return r
}

// handlePending lists what is waiting. Ids are long, so the list is the practical way
// to get one to paste into /approve.
func (r *Router) handlePending(ctx context.Context, msg Message, send func(string)) error {
	rows, err := r.db.ListPendingActions(ctx, msg.WorkspaceID, db.PendingStatusPending, 20)
	if err != nil {
		send("⚠️ Couldn't read the approval queue: " + err.Error())
		return nil
	}
	if len(rows) == 0 {
		send("Nothing is waiting for approval.")
		return nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%d waiting for approval**\n\n", len(rows)))
	for _, p := range rows {
		b.WriteString(fmt.Sprintf("• `%s`\n  %s\n  _%s via %s_\n\n",
			p.ID, p.Summary, orAgent(p.AgentName), p.Provider))
	}
	b.WriteString("Approve with `/approve <id>` or decline with `/reject <id>`.")
	send(b.String())
	return nil
}

// handleApprove sends a parked call for real.
func (r *Router) handleApprove(ctx context.Context, msg Message, arg string, send func(string)) error {
	id, ok := r.approvalArg(arg, send)
	if !ok {
		return nil
	}
	p, err := r.approval.Approve(ctx, msg.WorkspaceID, id)
	if err != nil {
		// A claim failure means it was already resolved; anything else is a real send
		// failure, and the service has already reported that to the inbox.
		if p == nil {
			send("⚠️ Nothing pending with that id — it may already have been approved, declined, or expired.")
			return nil
		}
		send("⚠️ Approved, but publishing failed: " + err.Error())
		return nil
	}
	send("✅ Approved — publishing now.")
	return nil
}

// handleReject discards a parked call without calling the provider.
func (r *Router) handleReject(ctx context.Context, msg Message, arg string, send func(string)) error {
	id, ok := r.approvalArg(arg, send)
	if !ok {
		return nil
	}
	if _, err := r.approval.Reject(ctx, msg.WorkspaceID, id); err != nil {
		send("⚠️ Nothing pending with that id — it may already have been approved, declined, or expired.")
		return nil
	}
	send("🚫 Declined — it will not be published.")
	return nil
}

// approvalArg validates the shared preconditions for /approve and /reject.
func (r *Router) approvalArg(arg string, send func(string)) (string, bool) {
	if r.approval == nil {
		send("Approvals aren't configured on this install.")
		return "", false
	}
	id := strings.TrimSpace(arg)
	// Chat clients love to wrap a pasted id in backticks or punctuation.
	id = strings.Trim(id, "`'\"<>()[].,")
	if id == "" {
		send("Which one? Use `/pending` to list what's waiting, then `/approve <id>`.")
		return "", false
	}
	return id, true
}

func orAgent(name string) string {
	if strings.TrimSpace(name) == "" {
		return "An agent"
	}
	return name
}
