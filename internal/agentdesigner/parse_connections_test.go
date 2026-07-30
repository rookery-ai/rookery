package agentdesigner

import (
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

func avail() []db.ServiceConnection {
	return []db.ServiceConnection{
		{ID: "c1", Provider: "google", AccountLabel: "work", AccountIdentity: "work@x.com"},
		{ID: "c2", Provider: "google", AccountLabel: "personal", AccountIdentity: "me@x.com"},
	}
}

func TestParseConnectionsByLabel(t *testing.T) {
	md := "# Connections: google/work\n\nBody"
	got := parseConnectionsLine(md, avail())
	if len(got) != 1 || got[0] != "c1" {
		t.Fatalf("got %v", got)
	}
}

func TestParseConnectionsBareProviderBindsAll(t *testing.T) {
	got := parseConnectionsLine("# Connections: google\n", avail())
	if len(got) != 2 {
		t.Fatalf("bare provider should bind all, got %v", got)
	}
}

func TestParseConnectionsByIdentity(t *testing.T) {
	got := parseConnectionsLine("# Connections: me@x.com\n", avail())
	if len(got) != 1 || got[0] != "c2" {
		t.Fatalf("got %v", got)
	}
}

func TestParseConnectionsNoneHeader(t *testing.T) {
	got := parseConnectionsLine("# Connections: none\n", avail())
	if got == nil || len(got) != 0 {
		t.Fatalf("none header must be non-nil empty, got %v", got)
	}
}

func TestParseConnectionsMissingHeader(t *testing.T) {
	if got := parseConnectionsLine("no header here", avail()); got != nil {
		t.Fatalf("missing header must be nil, got %v", got)
	}
}

func TestParseConnectionsBulletForm(t *testing.T) {
	// The form deepseek actually emits: empty inline + a bullet naming the account.
	md := "# Suggested schedule: none\n\n# Connections:\n# - google account \"work\" — work@x.com\n\nBody"
	got := parseConnectionsLine(md, avail())
	if len(got) != 1 || got[0] != "c1" {
		t.Fatalf("bullet-form header should bind c1 (work@x.com), got %v", got)
	}
}

func TestParseConnectionsBulletMultiple(t *testing.T) {
	md := "# Connections:\n- google/work\n- me@x.com\n"
	got := parseConnectionsLine(md, avail())
	if len(got) != 2 {
		t.Fatalf("both bullet accounts should bind, got %v", got)
	}
}
