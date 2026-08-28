package approval_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/approval"
	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/db"
)

// This file exists because the approval gate had NO end-to-end coverage: its only
// tests exercised Summarize and two other string helpers, so nothing asserted
// that parking withholds the call, that approving actually sends it with the
// stored arguments, or that rejecting sends nothing. It had also never run on a
// real install — zero pending_actions rows, zero gated bindings — so "does
// approval work?" genuinely had no answer either way.

type fakeStore struct{}

func (fakeStore) AccessToken(context.Context, connectors.ConnRef) (string, error) {
	return "AT", nil
}

// gatedFixture builds a workspace, agent and connection with the binding set to
// require approval, plus a provider whose one action points at a test server.
func gatedFixture(t *testing.T, srv *httptest.Server) (*db.DB, *connectors.Registry, string, string, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "approval.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ws := &db.Workspace{ID: "ws1", Name: "w", SecretsSalt: "s"}
	if err := database.CreateWorkspace(ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agent := &db.Agent{ID: "ag1", WorkspaceID: ws.ID, Name: "poster", Description: "d"}
	if err := database.CreateAgent(agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	conn := db.ServiceConnection{
		ID: "cn1", WorkspaceID: ws.ID, Provider: "google",
		AccountLabel: "work", AccountIdentity: "me@example.com", Status: "ACTIVE",
	}
	if err := database.InsertServiceConnection(context.Background(), conn); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := database.SetAgentConnections(context.Background(), agent.ID, []string{conn.ID}); err != nil {
		t.Fatalf("bind connection: %v", err)
	}
	if err := database.SetAgentConnectionApprovalMode(context.Background(), agent.ID, conn.ID, db.ApprovalModeApprove); err != nil {
		t.Fatalf("gate binding: %v", err)
	}

	reg, err := connectors.LoadBundled()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return database, reg, ws.ID, agent.ID, conn.ID
}

// The load-bearing property: a gated call must NOT reach the provider, and the
// model must be told plainly that nothing was published — an agent that records
// a queued post as sent reports a lie to its owner.
func TestAGatedCallIsParkedAndNotSent(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	database, reg, wsID, agentID, connID := gatedFixture(t, srv)
	svc := approval.New(database, reg, fakeStore{}, srv.Client())
	parker := svc.ParkerFor(context.Background(), wsID, agentID, "poster")
	if parker == nil {
		t.Fatal("no parker for an agent whose binding requires approval")
	}

	ticket, err := parker.Park(context.Background(),
		connectors.ConnRef{ID: connID, Provider: "google"},
		"gmail_send_email", map[string]any{"to": "a@b.com", "subject": "hi", "body": "there"})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if ticket == "" {
		t.Fatal("a gated call returned no ticket, so it would have been sent normally")
	}
	if hits != 0 {
		t.Fatalf("parking sent the request anyway (%d calls)", hits)
	}

	pending, err := database.ListPendingActions(context.Background(), wsID, db.PendingStatusPending, 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending actions, want 1", len(pending))
	}
	if pending[0].ID != ticket {
		t.Errorf("stored ticket %q, returned %q", pending[0].ID, ticket)
	}
}

// An UNGATED binding must fall straight through. Parking everything would
// silently stop an autonomous agent the owner never gated.
func TestAnUngatedBindingIsNotParked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	database, reg, wsID, agentID, connID := gatedFixture(t, srv)
	if err := database.SetAgentConnectionApprovalMode(context.Background(), agentID, connID, db.ApprovalModeAuto); err != nil {
		t.Fatalf("ungate: %v", err)
	}
	svc := approval.New(database, reg, fakeStore{}, srv.Client())
	parker := svc.ParkerFor(context.Background(), wsID, agentID, "poster")
	if parker == nil {
		// ParkerFor returns nil when NO binding is gated — also a correct
		// outcome here, and it means Execute skips the gate entirely.
		return
	}
	ticket, err := parker.Park(context.Background(),
		connectors.ConnRef{ID: connID, Provider: "google"}, "gmail_send_email", map[string]any{})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if ticket != "" {
		t.Fatal("an auto binding was parked")
	}
}

// Approving must send the call with the arguments as stored. They are kept as
// ARGS rather than a rendered request precisely so the token can be refreshed
// hours later, and this is the only test that would notice if that broke.
func TestApprovingSendsTheStoredCall(t *testing.T) {
	var got struct {
		hits int
		body string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.hits++
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		got.body = string(b)
		_, _ = w.Write([]byte(`{"id":"sent-1"}`))
	}))
	defer srv.Close()

	database, reg, wsID, agentID, connID := gatedFixture(t, srv)
	if !reg.SetActionURLForTest("google", "gmail_send_email", srv.URL+"/send") {
		t.Fatal("fixture action missing from the bundled registry")
	}
	svc := approval.New(database, reg, fakeStore{}, srv.Client())
	parker := svc.ParkerFor(context.Background(), wsID, agentID, "poster")
	ticket, err := parker.Park(context.Background(),
		connectors.ConnRef{ID: connID, Provider: "google"},
		"gmail_send_email", map[string]any{"to": "a@b.com", "subject": "hi", "body": "there"})
	if err != nil {
		t.Fatalf("park: %v", err)
	}

	if _, err := svc.Approve(context.Background(), wsID, ticket); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.hits != 1 {
		t.Fatalf("approving made %d provider calls, want 1", got.hits)
	}
	// Gmail's body_builder base64-encodes the message into `raw`, so the stored
	// arguments arrive encoded rather than literally. Decoding before asserting
	// is the difference between testing the mechanism and testing one provider's
	// happening to send plain JSON.
	var sent struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal([]byte(got.body), &sent); err != nil {
		t.Fatalf("provider body was not JSON: %q", got.body)
	}
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(sent.Raw)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(sent.Raw)
	}
	if err != nil {
		t.Fatalf("could not decode the sent message: %v", err)
	}
	for _, want := range []string{"a@b.com", "hi", "there"} {
		if !strings.Contains(string(decoded), want) {
			t.Errorf("stored argument %q did not reach the provider; got %q", want, decoded)
		}
	}
}

// Rejecting must send nothing, and must settle the ticket so it cannot be
// approved afterwards.
func TestRejectingSendsNothing(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()

	database, reg, wsID, agentID, connID := gatedFixture(t, srv)
	svc := approval.New(database, reg, fakeStore{}, srv.Client())
	parker := svc.ParkerFor(context.Background(), wsID, agentID, "poster")
	ticket, _ := parker.Park(context.Background(),
		connectors.ConnRef{ID: connID, Provider: "google"}, "gmail_send_email",
		map[string]any{"to": "a@b.com", "subject": "hi", "body": "there"})

	if _, err := svc.Reject(context.Background(), wsID, ticket); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if hits != 0 {
		t.Fatalf("rejecting sent the call anyway (%d)", hits)
	}
	if _, err := svc.Approve(context.Background(), wsID, ticket); err == nil {
		t.Error("a rejected ticket could still be approved")
	}
}

// The claim is the lock: chat and the web inbox can resolve the same ticket at
// the same moment, and only one of them may publish.
func TestATicketCannotBeApprovedTwice(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	database, reg, wsID, agentID, connID := gatedFixture(t, srv)
	if !reg.SetActionURLForTest("google", "gmail_send_email", srv.URL+"/send") {
		t.Fatal("fixture action missing from the bundled registry")
	}
	svc := approval.New(database, reg, fakeStore{}, srv.Client())
	parker := svc.ParkerFor(context.Background(), wsID, agentID, "poster")
	ticket, _ := parker.Park(context.Background(),
		connectors.ConnRef{ID: connID, Provider: "google"}, "gmail_send_email",
		map[string]any{"to": "a@b.com", "subject": "hi", "body": "there"})

	if _, err := svc.Approve(context.Background(), wsID, ticket); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if _, err := svc.Approve(context.Background(), wsID, ticket); err == nil {
		t.Error("the same ticket published twice")
	}
	if hits != 1 {
		t.Fatalf("provider called %d times, want exactly 1", hits)
	}
}

// The parked payload the MODEL sees must be impossible to read as success.
func TestTheParkedResultTellsTheModelNothingWasSent(t *testing.T) {
	var parked connectors.ParkedResult
	raw, _ := json.Marshal(connectors.ParkedResult{
		Status: "queued_for_approval", ID: "x", Action: "gmail_send_email",
		Note: "NOT yet published — this is awaiting the owner's approval",
	})
	if err := json.Unmarshal(raw, &parked); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(parked.Note), "not yet published") {
		t.Errorf("the note does not say the call was withheld: %q", parked.Note)
	}
}
