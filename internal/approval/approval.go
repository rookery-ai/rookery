// Package approval implements the run-time gate for irreversible public writes.
//
// It is the only place that knows both halves of the decision: connectors knows an
// action is `public_write`, and db knows a binding's `approval_mode`. Keeping the
// join here lets internal/connectors stay ignorant of agents, and lets the runner
// stay ignorant of action metadata.
//
// The semantics are "park, plain" (see the design doc): a gated call is stored and
// the run finishes immediately. The agent never learns the outcome — that goes to the
// owner's inbox. The costs of that choice (no chaining, no error reaction, and state
// drift if the owner rejects) are accepted deliberately and mitigated only by the
// wording of the parked tool result.
package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
)

// Notifier delivers a message to the workspace owner. Implemented by the gateway
// manager; nil disables chat delivery (the inbox still records everything).
type Notifier interface {
	SendToUser(workspaceID, message string) error
}

// Service parks gated calls and performs them once approved.
type Service struct {
	db     *db.DB
	reg    *connectors.Registry
	store  connectors.TokenStore
	client *http.Client
	notify Notifier
}

// New builds the service. client may be nil (Execute supplies a default).
func New(database *db.DB, reg *connectors.Registry, store connectors.TokenStore, client *http.Client) *Service {
	return &Service{db: database, reg: reg, store: store, client: client}
}

// WithNotifier attaches chat delivery for park/outcome notices.
func (s *Service) WithNotifier(n Notifier) *Service { s.notify = n; return s }

// ParkerFor returns a connectors.Parker scoped to one agent run, or nil when the
// agent has no gated binding at all.
//
// Returning nil in the common case matters: a nil Parker means Execute skips the gate
// branch entirely, so an install that never enables approval pays nothing — not even
// a DB read per public_write call.
func (s *Service) ParkerFor(ctx context.Context, workspaceID, agentID, agentName string) connectors.Parker {
	if agentID == "" {
		return nil // chat has no binding rows, so nothing can be gated
	}
	gated, err := s.db.AgentHasGatedConnection(ctx, agentID)
	if err != nil {
		// Fail CLOSED would mean parking everything on a transient DB error, which
		// silently stops an autonomous agent the user never gated. Fail open, loudly.
		slog.Error("approval: could not read gate config; running ungated", "agent", agentID, "err", err)
		return nil
	}
	if !gated {
		return nil
	}
	return &runParker{svc: s, workspaceID: workspaceID, agentID: agentID, agentName: agentName}
}

type runParker struct {
	svc                *Service
	workspaceID        string
	agentID, agentName string
}

// Park implements connectors.Parker. It returns ("", nil) when this particular
// binding is on 'auto', which tells Execute to send the call normally.
func (p *runParker) Park(ctx context.Context, conn connectors.ConnRef, action string, args map[string]any) (string, error) {
	mode, err := p.svc.db.AgentConnectionApprovalMode(ctx, p.agentID, conn.ID)
	if err != nil {
		return "", err
	}
	if mode != db.ApprovalModeApprove {
		return "", nil // not gated
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	rec := &db.PendingAction{
		ID: id, WorkspaceID: p.workspaceID, AgentID: p.agentID, AgentName: p.agentName,
		ConnectionID: conn.ID, Provider: conn.Provider, Action: action,
		ArgsJSON: string(raw), Summary: Summarize(action, args),
	}
	if err := p.svc.db.CreatePendingAction(ctx, rec); err != nil {
		return "", err
	}
	p.svc.announce(rec)
	return id, nil
}

// Summarize renders a short human preview of a parked call. The owner approves from a
// chat message, so this is the ONLY thing they see before saying yes — it must show
// the actual content, not just the action name.
func Summarize(action string, args map[string]any) string {
	var b strings.Builder
	b.WriteString(action)
	for _, key := range []string{"text", "message", "title", "caption", "body", "comment", "status"} {
		if v, ok := args[key]; ok {
			if sv, ok := v.(string); ok && strings.TrimSpace(sv) != "" {
				b.WriteString(": ")
				b.WriteString(truncateRunes(strings.TrimSpace(sv), 280))
				return b.String()
			}
		}
	}
	return b.String()
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// announce records the parked call in the inbox and pings chat.
func (s *Service) announce(p *db.PendingAction) {
	body := fmt.Sprintf("⏸ %s wants to publish via %s and is waiting for your approval.\n\n%s\n\nApprove with `/approve %s` or decline with `/reject %s`.",
		orDefault(p.AgentName, "An agent"), p.Provider, p.Summary, p.ID, p.ID)

	if err := s.db.CreateInboxMessage(&db.InboxMessage{
		ID: uuid.NewString(), WorkspaceID: p.WorkspaceID, Source: "approval",
		AgentID: p.AgentID, AgentName: p.AgentName, RefID: p.ID,
		Body: body, Status: "ok",
	}); err != nil {
		slog.Error("approval: inbox write failed", "pending", p.ID, "err", err)
	}
	if s.notify != nil {
		if err := s.notify.SendToUser(p.WorkspaceID, body); err != nil {
			slog.Warn("approval: chat notify failed", "pending", p.ID, "err", err)
		}
	}
}

// Approve claims the ticket and performs the real call. The claim is atomic, so two
// approvals racing (chat and the web inbox) cannot both publish.
func (s *Service) Approve(ctx context.Context, workspaceID, id string) (*db.PendingAction, error) {
	p, err := s.db.ClaimPendingAction(ctx, workspaceID, id, db.PendingStatusApproved)
	if err != nil {
		return nil, err
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(p.ArgsJSON), &args); err != nil {
		_ = s.db.FinishPendingAction(ctx, workspaceID, id, "", "stored arguments were unreadable: "+err.Error())
		return p, err
	}

	conn, err := s.db.GetServiceConnection(ctx, p.ConnectionID)
	if err != nil {
		msg := "the connected account is gone: " + err.Error()
		_ = s.db.FinishPendingAction(ctx, workspaceID, id, "", msg)
		s.report(p, "", msg)
		return p, err
	}

	// Policy{} deliberately: the gate has already been satisfied by the owner, and
	// re-parking here would loop forever. The token is refreshed inside Execute, which
	// is why args were stored rather than a rendered request.
	res, execErr := connectors.Execute(ctx, s.reg, s.store, s.client,
		connectors.ConnRef{ID: conn.ID, Provider: conn.Provider,
			AccountIdentity: conn.AccountIdentity, Extra: connectors.ParseExtra(conn.Extra)},
		p.Action, args, connectors.Policy{})

	if execErr != nil {
		_ = s.db.FinishPendingAction(ctx, workspaceID, id, "", execErr.Error())
		s.report(p, "", execErr.Error())
		return p, execErr
	}
	_ = s.db.FinishPendingAction(ctx, workspaceID, id, string(res.Data), "")
	s.report(p, string(res.Data), "")
	return p, nil
}

// Reject discards the ticket without calling the provider.
func (s *Service) Reject(ctx context.Context, workspaceID, id string) (*db.PendingAction, error) {
	p, err := s.db.ClaimPendingAction(ctx, workspaceID, id, db.PendingStatusRejected)
	if err != nil {
		return nil, err
	}
	s.deliver(p.WorkspaceID, fmt.Sprintf("🚫 Declined — %s was not published.", p.Summary))
	return p, nil
}

// report tells the owner what actually happened. The agent is long gone by now, which
// is the accepted cost of parking, so this message is the only record of the outcome.
func (s *Service) report(p *db.PendingAction, result, errMsg string) {
	var body string
	if errMsg != "" {
		body = fmt.Sprintf("⚠️ Approved, but publishing failed — %s\n\n%s", p.Summary, errMsg)
	} else {
		body = fmt.Sprintf("✅ Published — %s", p.Summary)
		if link := firstURL(result); link != "" {
			body += "\n" + link
		}
	}
	s.deliver(p.WorkspaceID, body)
}

func (s *Service) deliver(workspaceID, body string) {
	if err := s.db.CreateInboxMessage(&db.InboxMessage{
		ID: uuid.NewString(), WorkspaceID: workspaceID, Source: "approval",
		Body: body, Status: "ok",
	}); err != nil {
		slog.Error("approval: inbox write failed", "err", err)
	}
	if s.notify != nil {
		if err := s.notify.SendToUser(workspaceID, body); err != nil {
			slog.Warn("approval: chat notify failed", "err", err)
		}
	}
}

// firstURL pulls a permalink out of a provider response so the owner can click
// through to what was just published. Best-effort: a response with no URL is normal.
func firstURL(result string) string {
	if result == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(result), &v); err != nil {
		return ""
	}
	return findURL(v, 0)
}

func findURL(v any, depth int) string {
	if depth > 4 {
		return ""
	}
	switch t := v.(type) {
	case string:
		if strings.HasPrefix(t, "https://") {
			return t
		}
	case map[string]any:
		// Prefer conventionally-named permalink keys before scanning everything, so a
		// nested avatar URL does not win over the post's own link.
		for _, k := range []string{"html_url", "permalink", "url", "uri", "link"} {
			if s, ok := t[k].(string); ok && strings.HasPrefix(s, "https://") {
				return s
			}
		}
		for _, sub := range t {
			if u := findURL(sub, depth+1); u != "" {
				return u
			}
		}
	case []any:
		for _, sub := range t {
			if u := findURL(sub, depth+1); u != "" {
				return u
			}
		}
	}
	return ""
}

// ExpireStale marks parked calls older than age as expired. A post approved a week
// after it was drafted is almost never what the owner meant.
func (s *Service) ExpireStale(ctx context.Context, age time.Duration) {
	n, err := s.db.ExpirePendingActionsOlderThan(ctx, age)
	if err != nil {
		slog.Error("approval: expiry sweep failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("approval: expired stale pending actions", "count", n)
	}
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
