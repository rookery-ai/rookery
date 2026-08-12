// Package connalert delivers "this connection needs reconnecting" notices to the
// two surfaces a workspace owner actually watches.
//
// It is its own package rather than a function inside internal/connectors because
// the alert needs the database and the chat gateway, and the connector layer
// deliberately knows about neither. internal/approval is structured the same way
// and for the same reason.
package connalert

import (
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// Sender delivers an unprompted message to a workspace's primary chat app. It is
// the narrow slice of gateway.GatewayManager this package needs, declared here so
// tests need no gateway.
type Sender interface {
	SendToUser(workspaceID, message string) error
}

// Service writes connection alerts. Sender may be nil (no chat delivery
// configured), in which case only the inbox row is written.
type Service struct {
	db     *db.DB
	sender Sender
}

func New(database *db.DB, sender Sender) *Service {
	return &Service{db: database, sender: sender}
}

// ConnectionNeedsReauth records that a connection has been definitively rejected
// by its provider. It never returns an error: the caller is a token refresh on a
// background loop, and a failure to notify must not fail the refresh.
//
// The inbox row is written FIRST and independently of the chat send. A workspace
// with no chat platform connected errors on every send, and that user's only
// surface is the inbox — so ordering it second would hand the failure to exactly
// the people who depend on it.
func (s *Service) ConnectionNeedsReauth(workspaceID, connectionID, providerLabel, accountLabel string) {
	body := fmt.Sprintf(
		"⚠️ Action required — your %s connection (%s) needs reconnecting. "+
			"Agents using it will fail until it is reconnected. "+
			"Reconnect it in Settings → Connections.",
		providerLabel, accountLabel)

	// Source is "connection": neither an agent run nor a reminder. AgentID stays
	// empty, which CreateInboxMessage inserts as SQL NULL so the foreign key is
	// not tripped by a row that belongs to no agent.
	err := s.db.CreateInboxMessage(&db.InboxMessage{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Source:      "connection",
		RefID:       connectionID,
		Body:        body,
		Status:      "error",
	})
	if err != nil {
		slog.Error("connalert: inbox write failed", "workspace_id", workspaceID, "conn", connectionID, "err", err)
	}

	if s.sender == nil {
		return
	}
	if err := s.sender.SendToUser(workspaceID, body); err != nil {
		// Expected whenever the workspace has no chat platform linked. The inbox
		// row above is already durable, so this is information, not a failure.
		slog.Info("connalert: chat delivery skipped", "workspace_id", workspaceID, "err", err)
	}
}
