package audit

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// Writer writes audit events to the database.
type Writer struct {
	db *db.DB
}

func New(database *db.DB) *Writer {
	return &Writer{db: database}
}

// Log records an audit event. workspaceID may be empty for system events.
func (w *Writer) Log(workspaceID, action, target, detail, ipAddress string) {
	_ = w.db.WriteAuditLog(&db.AuditLog{
		ID:          uuid.New().String(),
		WorkspaceID: nullStr(workspaceID),
		Action:      action,
		Target:      target,
		Detail:      fmt.Sprintf("%.2000s", detail), // truncate to 2000 chars
		IPAddress:   ipAddress,
	})
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
