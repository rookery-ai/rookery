package db_test

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
)

func auditTestDB(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	wsA, wsB := uuid.New().String(), uuid.New().String()
	for _, ws := range []string{wsA, wsB} {
		if err := database.CreateWorkspace(&db.Workspace{ID: ws, Name: "w-" + ws[:4]}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
	}
	return database, wsA, wsB
}

func logEvent(t *testing.T, d *db.DB, ws, action, target, detail, ip string) {
	t.Helper()
	w := ws
	a := &db.AuditLog{ID: uuid.New().String(), Action: action, Target: target, Detail: detail, IPAddress: ip}
	if ws != "" {
		a.WorkspaceID = &w
	}
	if err := d.WriteAuditLog(a); err != nil {
		t.Fatalf("create audit log: %v", err)
	}
}

func actions(logs []*db.AuditLog) []string {
	out := make([]string, len(logs))
	for i, l := range logs {
		out[i] = l.Action
	}
	return out
}

func TestAuditFilterByWorkspaceAndAction(t *testing.T) {
	d, wsA, wsB := auditTestDB(t)
	logEvent(t, d, wsA, "run_agent", "agent:1", "", "10.0.0.1")
	logEvent(t, d, wsA, "configure_coder", "workspace:a", "", "10.0.0.1")
	logEvent(t, d, wsB, "run_agent", "agent:2", "", "10.0.0.2")

	got, err := d.ListAuditLogsFiltered(db.AuditLogFilter{WorkspaceID: wsA})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("workspace filter: want 2 entries, got %d (%v)", len(got), actions(got))
	}

	got, err = d.ListAuditLogsFiltered(db.AuditLogFilter{Action: "run_agent"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("action filter: want 2 entries, got %d", len(got))
	}

	// Both filters together intersect rather than union.
	got, err = d.ListAuditLogsFiltered(db.AuditLogFilter{WorkspaceID: wsA, Action: "run_agent"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("combined filter: want 1 entry, got %d", len(got))
	}
}

func TestAuditFilterQuerySearchesTargetDetailAndIP(t *testing.T) {
	d, wsA, _ := auditTestDB(t)
	logEvent(t, d, wsA, "run_agent", "agent:invoice-bot", "", "10.0.0.1")
	logEvent(t, d, wsA, "configure_coder", "workspace:a", "api:openrouter/glm", "10.0.0.2")
	logEvent(t, d, wsA, "delete_agent", "agent:other", "", "192.168.1.7")

	for _, tc := range []struct {
		q    string
		want int
	}{
		{"invoice", 1},    // target
		{"openrouter", 1}, // detail
		{"192.168", 1},    // ip
		{"agent:", 2},     // matches two targets
		{"nope", 0},
	} {
		got, err := d.ListAuditLogsFiltered(db.AuditLogFilter{Query: tc.q})
		if err != nil {
			t.Fatalf("query %q: %v", tc.q, err)
		}
		if len(got) != tc.want {
			t.Errorf("query %q: want %d, got %d", tc.q, tc.want, len(got))
		}
	}
}

// A LIKE wildcard typed by the user must be matched literally — otherwise
// searching for "%" silently returns the entire log as though everything matched.
func TestAuditFilterQueryEscapesLikeWildcards(t *testing.T) {
	d, wsA, _ := auditTestDB(t)
	logEvent(t, d, wsA, "run_agent", "agent:one", "", "10.0.0.1")
	logEvent(t, d, wsA, "run_agent", "agent:two", "", "10.0.0.2")
	logEvent(t, d, wsA, "run_agent", "100% done", "", "10.0.0.3")

	got, err := d.ListAuditLogsFiltered(db.AuditLogFilter{Query: "%"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("a literal %% should match only the entry containing it, got %d", len(got))
	}
}

func TestAuditFilterLimitAndDefault(t *testing.T) {
	d, wsA, _ := auditTestDB(t)
	for i := 0; i < 5; i++ {
		logEvent(t, d, wsA, "run_agent", "agent:x", "", "10.0.0.1")
	}
	got, err := d.ListAuditLogsFiltered(db.AuditLogFilter{Limit: 2})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("limit 2: got %d", len(got))
	}
	// A zero filter must behave exactly like the old unfiltered call.
	got, err = d.ListAuditLogsFiltered(db.AuditLogFilter{})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("zero filter: want all 5, got %d", len(got))
	}
}

func TestDistinctAuditActions(t *testing.T) {
	d, wsA, _ := auditTestDB(t)
	logEvent(t, d, wsA, "run_agent", "a", "", "")
	logEvent(t, d, wsA, "run_agent", "b", "", "")
	logEvent(t, d, wsA, "configure_coder", "c", "", "")

	got, err := d.DistinctAuditActions()
	if err != nil {
		t.Fatalf("distinct: %v", err)
	}
	if len(got) != 2 || got[0] != "configure_coder" || got[1] != "run_agent" {
		t.Errorf("want [configure_coder run_agent], got %v", got)
	}
}
