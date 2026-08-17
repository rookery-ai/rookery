package agentdesigner

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// realWorkspaceConnections reproduces the shape that caused the incident: several
// unrelated providers sharing one short, generic account identity ("test"), and a
// whole family of Google children sharing one identity (the owner's email).
// Neither is exotic — "test" is what everyone calls a trial account, Stripe's own
// test mode invites it, and every google_* child inherits the same address.
func realWorkspaceConnections() []db.ServiceConnection {
	return []db.ServiceConnection{
		{ID: "c-adguard", Provider: "adguard", AccountLabel: "test", AccountIdentity: "test"},
		{ID: "c-mailchimp", Provider: "mailchimp", AccountLabel: "test", AccountIdentity: "test"},
		{ID: "c-stripe", Provider: "stripe", AccountLabel: "test", AccountIdentity: "test"},
		{ID: "c-sheets", Provider: "google_sheets", AccountLabel: "ilija 133", AccountIdentity: "ilija.d133061@gmail.com"},
		{ID: "c-drive", Provider: "google_drive", AccountLabel: "ilija 1330", AccountIdentity: "ilija.d133061@gmail.com"},
		{ID: "c-docs", Provider: "google_docs", AccountLabel: "ilija 133", AccountIdentity: "ilija.d133061@gmail.com"},
		{ID: "c-openai", Provider: "openai", AccountLabel: "personal", AccountIdentity: "personal"},
	}
}

func idSet(ids []string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// TestConnectionsHeaderDoesNotOverBindOnASharedIdentity is the security half of
// connection binding: a binding is a grant of live credentials, so a wrong one is
// not untidiness, it is access.
//
// The header below names exactly two accounts. The old matcher also did a raw
// substring test of every connection's AccountIdentity against the whole header
// region — and because "adguard/test" contains the substring "test", the agent
// was additionally granted Mailchimp and Stripe. Observed in production: a DNS
// watchdog holding payment and mailing-list credentials.
func TestConnectionsHeaderDoesNotOverBindOnASharedIdentity(t *testing.T) {
	available := realWorkspaceConnections()
	agentMD := "# Connections: adguard/test, google_sheets/ilija 133\n\n# DNS watch\nBody text.\n"

	got := idSet(parseConnectionsLine(agentMD, available))

	for _, want := range []string{"c-adguard", "c-sheets"} {
		if !got[want] {
			t.Errorf("declared connection %s was not bound", want)
		}
	}
	for _, forbidden := range []string{"c-mailchimp", "c-stripe"} {
		if got[forbidden] {
			t.Errorf("%s was bound but never declared — live credentials granted to an agent that has no use for them", forbidden)
		}
	}
	if len(got) != 2 {
		t.Fatalf("bound %d connections, want exactly the 2 declared: %v", len(got), got)
	}
}

// TestConnectionsHeaderDoesNotOverBindOnAFamilyIdentity is the same defect one
// step out. Every google_* child shares the owner's email, so an identity
// substring match on that address bound the entire Google family — Drive and Docs
// included — from a header naming only Sheets.
func TestConnectionsHeaderDoesNotOverBindOnAFamilyIdentity(t *testing.T) {
	available := realWorkspaceConnections()
	agentMD := "# Connections: google_sheets/ilija 133 (ilija.d133061@gmail.com)\n"

	got := idSet(parseConnectionsLine(agentMD, available))

	if !got["c-sheets"] {
		t.Fatal("the declared google_sheets connection was not bound")
	}
	for _, forbidden := range []string{"c-drive", "c-docs"} {
		if got[forbidden] {
			t.Errorf("%s was bound from a shared family email — the header named only Sheets", forbidden)
		}
	}
}

// TestConnectionsHeaderStillBindsFromADistinctIdentity keeps the capability the
// substring match existed for. A weak model often writes the bullet form with the
// account's email rather than "provider/label"; when that identity belongs to
// exactly ONE connection it is unambiguous evidence and must still bind.
func TestConnectionsHeaderStillBindsFromADistinctIdentity(t *testing.T) {
	available := []db.ServiceConnection{
		{ID: "c-google", Provider: "google", AccountLabel: "personal", AccountIdentity: "me@example.com"},
		{ID: "c-github", Provider: "github", AccountLabel: "work", AccountIdentity: "octo@example.com"},
	}
	agentMD := "# Connections:\n# - google account \"personal\" — me@example.com\n"

	got := idSet(parseConnectionsLine(agentMD, available))

	if !got["c-google"] {
		t.Fatal("a unique account identity in the bullet form must still bind its connection")
	}
	if got["c-github"] {
		t.Error("github was bound but never mentioned")
	}
}

// TestConnectionsHeaderContractUnchanged pins the nil-vs-empty distinction the
// caller depends on: nil means "no header, fall back to what the build used",
// empty means "the header said none, honour it". Getting these the wrong way
// round either ignores an explicit none or auto-binds over a deliberate choice.
func TestConnectionsHeaderContractUnchanged(t *testing.T) {
	available := realWorkspaceConnections()

	if got := parseConnectionsLine("# Agent\nNo header here.\n", available); got != nil {
		t.Errorf("absent header returned %v, want nil", got)
	}
	got := parseConnectionsLine("# Connections: none\n", available)
	if got == nil {
		t.Fatal("an explicit \"none\" returned nil — it must be an empty, non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("an explicit \"none\" bound %v", got)
	}
}
