# Reconnect, Action-Required Alerts and Workspace Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Reconnect button actually re-authenticate, alert the user once when a connection is definitively rejected, and show workspace images in owner settings.

**Architecture:** Six tasks in dependency order. Tasks 1–2 are a Go bug fix in `internal/connectors` that must land before the alert, because the status flip they gate is the alert's trigger. Task 3 adds the notifier and its wiring. Tasks 4–6 are independent frontend changes. Every task ends green and committable.

**Tech Stack:** Go 1.x (stdlib + `modernc.org/sqlite`), React 19 + TypeScript + TanStack Query, Tailwind v4, vitest + Testing Library, `go test`.

**Spec:** `docs/superpowers/specs/2026-08-11-reconnect-and-workspace-images-design.md`

## Global Constraints

- **Never commit to `main`.** Work happens on a feature branch; `main` advances only through merged PRs.
- **Conventional Commits**: `type(scope): summary`. Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- **Go tests must pass with `-race`**: `go test ./... -count=1 -timeout 120s`.
- **Frontend gate**: `cd web/ui && npx tsc -b && npx oxlint && npx vitest run`.
- **Icons are lucide only, `currentColor` always**, from `web/ui/src/lib/entityIcons.tsx`. Never import a lucide icon directly into a page for an entity kind that belongs in that map. `size-4` inline.
- **No new dependencies.** This project deliberately keeps its dependency count low.
- **`web/ui/src/lib/copyText.ts` is the only clipboard write in the app.** Not needed here, but do not add another.
- The full local gate is `make ci`. It does not run the container or package smoke tests.

---

## File Structure

**Modified — Go:**
- `internal/connectors/oauth.go:99-100` — classify token-endpoint failures by HTTP status instead of collapsing every `>= 400` onto `KindAuth`.
- `internal/connectors/dbstore.go:97-120` — flip to `NEEDS_REAUTH` only on a definitive rejection; notify on the flip.
- `cmd/rookery/main.go:302, ~505` — construct and attach the notifier.
- `web/server.go:141` — attach the notifier to the web server's own token store.

**Created — Go:**
- `internal/connalert/connalert.go` — the concrete notifier: writes an inbox row and sends a chat message. Its own package so it can depend on `internal/db` without `internal/connectors` gaining a dependency it does not need, mirroring how `internal/approval` is structured.
- `internal/connalert/connalert_test.go`

**Modified — frontend:**
- `web/ui/src/pages/connections/ServiceWizard.tsx:80-169, 221-224, 240-252, 307` — `AccountRow` gains provider context; `reconnect()` replaces `jumpToConnect`; `handleConnect` takes an explicit label.
- `web/ui/src/pages/connections/ServiceWizard.test.tsx:308` — rewrite the test that pins the broken behaviour.
- `web/ui/src/pages/home/HomePage.tsx:123-124, 159-164` — render the `connection` inbox source.
- `web/ui/src/pages/settings/OwnerSections.tsx:66-82` — render the workspace avatar.

**Reused as-is (do not modify):**
- `web/ui/src/lib/workspaceIcons.tsx:234-259` — `WorkspaceAvatar` already implements the unset-vs-unknown fallback the spec requires.
- `internal/db/repositories.go:947` — `CreateInboxMessage` already inserts SQL NULL for an empty `AgentID`.

---

### Task 1: Classify token-endpoint failures by HTTP status

Today `tokenRequest` maps every status `>= 400` onto `KindAuth`, discarding the difference between "this refresh token is dead" and "the provider is having a bad minute". Task 2 needs that difference.

**Files:**
- Modify: `internal/connectors/oauth.go:99-100`
- Test: `internal/connectors/oauth_test.go`

**Interfaces:**
- Consumes: `ConnectorError`, `KindAuth`, `KindRateLimit`, `KindServer`, `KindNetwork` — all already defined in `internal/connectors/execute.go:18-26`.
- Produces: `OAuthClient.Refresh` now returns a `*ConnectorError` whose `Kind` is `KindRateLimit` for 429, `KindServer` for 5xx, and `KindAuth` for other 4xx. Task 2 branches on exactly this.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/oauth_test.go`:

```go
func TestTokenRequestClassifiesByStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   Kind
	}{
		{"rate limited", 429, KindRateLimit},
		{"server error", 500, KindServer},
		{"bad gateway", 502, KindServer},
		{"invalid grant", 400, KindAuth},
		{"unauthorized", 401, KindAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(`{"error":"nope"}`))
			}))
			defer srv.Close()

			c := OAuthClient{HTTP: srv.Client()}
			p := Provider{Name: "p", TokenURL: srv.URL + "/token"}
			_, err := c.Refresh(context.Background(), p, "cid", "csec", "RT")

			var ce *ConnectorError
			if !errors.As(err, &ce) {
				t.Fatalf("want *ConnectorError, got %T (%v)", err, err)
			}
			if ce.Kind != tc.want {
				t.Fatalf("status %d: got kind %v, want %v", tc.status, ce.Kind, tc.want)
			}
		})
	}
}
```

Add `"errors"` to the file's import block if it is not already there.

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./internal/connectors/ -run TestTokenRequestClassifiesByStatus -v`

Expected: FAIL on the `rate limited` and both server-error subtests, reporting `got kind 1, want 2` and `got kind 1, want 3` — `KindAuth` is 1. The three 4xx subtests pass already.

- [ ] **Step 3: Implement the classification**

In `internal/connectors/oauth.go`, replace lines 99-100:

```go
	if resp.StatusCode >= 400 {
		return TokenSet{}, &ConnectorError{KindAuth, fmt.Sprintf("token endpoint %d: %s", resp.StatusCode, string(b))}
	}
```

with:

```go
	if resp.StatusCode >= 400 {
		// Classify rather than collapsing everything onto KindAuth. The caller
		// (DBTokenStore.refresh) uses this to decide whether to mark the
		// connection dead: only a definitive rejection by the provider should,
		// because a row marked NEEDS_REAUTH leaves ConnectionsNearExpiry's
		// status='ACTIVE' filter and is never renewed again. A 500 must not
		// permanently brick a healthy connection.
		kind := KindAuth
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			kind = KindRateLimit
		case resp.StatusCode >= 500:
			kind = KindServer
		}
		return TokenSet{}, &ConnectorError{kind, fmt.Sprintf("token endpoint %d: %s", resp.StatusCode, string(b))}
	}
```

- [ ] **Step 4: Run the test and confirm it passes**

Run: `go test ./internal/connectors/ -run TestTokenRequestClassifiesByStatus -v`
Expected: PASS, all five subtests.

- [ ] **Step 5: Run the whole package to check nothing regressed**

Run: `go test ./internal/connectors/ -count=1`
Expected: PASS. If an existing test asserted `KindAuth` for a 5xx, that assertion encoded the bug — update it and note why in the commit.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/oauth.go internal/connectors/oauth_test.go
git commit -m "fix(connectors): classify token-endpoint failures by status

Every status >= 400 became KindAuth, so a 500 was indistinguishable from
invalid_grant. DBTokenStore.refresh needs the difference to decide whether a
connection is really dead."
```

---

### Task 2: Flip to NEEDS_REAUTH only on a definitive rejection

**Files:**
- Modify: `internal/connectors/dbstore.go:97-120`
- Test: `internal/connectors/dbstore_test.go`

**Interfaces:**
- Consumes: the classified `*ConnectorError` from Task 1.
- Produces: `DBTokenStore.refresh` leaves `status` as `ACTIVE` for `KindServer` / `KindRateLimit` / `KindNetwork`, and sets `NEEDS_REAUTH` only for `KindAuth` (and for the missing-refresh-token case, which is definitive without any network call). Task 3 hooks the same branch.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/dbstore_test.go`:

```go
// refreshFixture wires one google connection whose token expired a minute ago,
// pointed at srv as its token endpoint.
func refreshFixture(t *testing.T, srv *httptest.Server) (*db.DB, *DBTokenStore, []byte) {
	t.Helper()
	d, ws := storeTestDB(t)
	key := mkKey()
	ctx := context.Background()
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{
		ID: "pc1", WorkspaceID: ws, Provider: "google",
		EncryptedClientID: encID, EncryptedClientSecret: encSec})
	encRefresh, _ := secrets.EncryptWithSystemKey("RT", key)
	encOld, _ := secrets.EncryptWithSystemKey("OLD", key)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		EncryptedAccessToken: encOld, EncryptedRefreshToken: encRefresh,
		ExpiresAt: past, Status: "ACTIVE"})

	reg := testRegistry(t)
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	return d, &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}, key
}

func TestRefreshKeepsConnectionActiveOnTransientFailure(t *testing.T) {
	for _, status := range []int{429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()
			d, store, _ := refreshFixture(t, srv)

			if _, err := store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"}); err == nil {
				t.Fatal("want an error from a failing token endpoint")
			}
			got, _ := d.GetServiceConnection(context.Background(), "c1")
			if got.Status != "ACTIVE" {
				t.Fatalf("status %d must not brick the connection: got %q, want ACTIVE", status, got.Status)
			}
		})
	}
}

func TestRefreshMarksNeedsReauthOnDefinitiveRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	d, store, _ := refreshFixture(t, srv)

	if _, err := store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"}); err == nil {
		t.Fatal("want an error from a rejected refresh token")
	}
	got, _ := d.GetServiceConnection(context.Background(), "c1")
	if got.Status != "NEEDS_REAUTH" {
		t.Fatalf("got %q, want NEEDS_REAUTH", got.Status)
	}
}
```

- [ ] **Step 2: Run the tests and confirm the transient one fails**

Run: `go test ./internal/connectors/ -run 'TestRefreshKeepsConnectionActiveOnTransientFailure|TestRefreshMarksNeedsReauthOnDefinitiveRejection' -v`

Expected: `TestRefreshMarksNeedsReauthOnDefinitiveRejection` PASSES (that is today's behaviour); all three subtests of `TestRefreshKeepsConnectionActiveOnTransientFailure` FAIL with `got "NEEDS_REAUTH", want ACTIVE`. That failure is the bug.

- [ ] **Step 3: Gate the flip**

In `internal/connectors/dbstore.go`, replace the refresh-error branch at lines 113-116:

```go
	ts, err := s.OAuth.Refresh(ctx, prov, cid, csec, refreshTok)
	if err != nil {
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		return "", &ConnectorError{KindNeedsReauth, "token refresh failed for " + row.AccountLabel + "; reconnect it (" + err.Error() + ")"}
	}
```

with:

```go
	ts, err := s.OAuth.Refresh(ctx, prov, cid, csec, refreshTok)
	if err != nil {
		// Only a definitive rejection marks the connection dead. A row set to
		// NEEDS_REAUTH leaves ConnectionsNearExpiry's status='ACTIVE' filter and
		// is never renewed again, so treating a 500 or a network blip as fatal
		// permanently bricks a healthy connection that would have recovered on
		// the next tick.
		if !definitiveRejection(err) {
			return "", err
		}
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		return "", &ConnectorError{KindNeedsReauth, "token refresh failed for " + row.AccountLabel + "; reconnect it (" + err.Error() + ")"}
	}
```

Add this helper at the end of `internal/connectors/dbstore.go`:

```go
// definitiveRejection reports whether err means the provider has rejected the
// credential itself, as opposed to being temporarily unable to answer.
func definitiveRejection(err error) bool {
	var ce *ConnectorError
	if !errors.As(err, &ce) {
		// An unclassified error is not proof of rejection. Failing open here
		// costs one more retry; failing closed costs the connection.
		return false
	}
	return ce.Kind == KindAuth
}
```

Add `"errors"` to the import block at the top of `internal/connectors/dbstore.go`.

- [ ] **Step 4: Run the tests and confirm both pass**

Run: `go test ./internal/connectors/ -run 'TestRefreshKeepsConnectionActiveOnTransientFailure|TestRefreshMarksNeedsReauthOnDefinitiveRejection' -v`
Expected: PASS, all four subtests.

- [ ] **Step 5: Run the whole package**

Run: `go test ./internal/connectors/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/dbstore.go internal/connectors/dbstore_test.go
git commit -m "fix(connectors): do not brick a connection on a transient refresh failure

refresh() flipped a connection to NEEDS_REAUTH on ANY error — a 500, a 429, a
network blip. ConnectionsNearExpiry selects only status='ACTIVE', so the row
then left the refresh loop for good and a momentary provider outage cost the
user a connection until they reconnected it by hand.

Flip only on KindAuth, the provider's own rejection of the credential."
```

---

### Task 3: Alert the user once, to the inbox and to chat

**Files:**
- Create: `internal/connalert/connalert.go`
- Create: `internal/connalert/connalert_test.go`
- Modify: `internal/connectors/dbstore.go` (add the notifier field and the call)
- Modify: `cmd/rookery/main.go:302` and after `gwManager := gateway.New(...)` at line 499
- Modify: `web/server.go:141`

**Interfaces:**
- Consumes: the gated flip from Task 2; `db.CreateInboxMessage` (`internal/db/repositories.go:947`); `gateway.GatewayManager.SendToUser(workspaceID, text string) error`.
- Produces:
  - `connectors.ReauthNotifier` interface: `ConnectionNeedsReauth(workspaceID, connectionID, providerLabel, accountLabel string)`.
  - `(*DBTokenStore).WithNotifier(n ReauthNotifier) *DBTokenStore` — returns the receiver so it can be chained or used as a statement.
  - `connalert.Service` implementing that interface; constructed as `connalert.New(database, sender)` where `sender` satisfies `connalert.Sender`.
  - Inbox rows written with `Source: "connection"`, `Status: "error"`, `RefID: <connection id>`, and an empty `AgentID`. Task 4 renders exactly this shape.

- [ ] **Step 1: Write the failing test for the notifier**

Create `internal/connalert/connalert_test.go`:

```go
package connalert

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
)

type fakeSender struct {
	sent []string
	err  error
}

func (f *fakeSender) SendToUser(workspaceID, message string) error {
	f.sent = append(f.sent, message)
	return f.err
}

func testDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	ws := uuid.New().String()
	if err := d.CreateWorkspace(&db.Workspace{ID: ws, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return d, ws
}

func TestNeedsReauthWritesInboxAndSendsChat(t *testing.T) {
	d, ws := testDB(t)
	sender := &fakeSender{}
	New(d, sender).ConnectionNeedsReauth(ws, "conn-1", "Gmail", "work")

	msgs, err := d.ListInboxMessages(ws, 10, 0)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d inbox messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Source != "connection" {
		t.Fatalf("source = %q, want connection", m.Source)
	}
	if m.Status != "error" {
		t.Fatalf("status = %q, want error", m.Status)
	}
	if m.AgentID != "" {
		t.Fatalf("agent_id = %q, want empty", m.AgentID)
	}
	if m.RefID != "conn-1" {
		t.Fatalf("ref_id = %q, want conn-1", m.RefID)
	}
	for _, want := range []string{"Gmail", "work", "Action required"} {
		if !strings.Contains(m.Body, want) {
			t.Fatalf("body %q missing %q", m.Body, want)
		}
	}
	if len(sender.sent) != 1 {
		t.Fatalf("got %d chat sends, want 1", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0], "Gmail") {
		t.Fatalf("chat message %q does not name the provider", sender.sent[0])
	}
}

func TestNeedsReauthWritesInboxEvenWhenChatFails(t *testing.T) {
	d, ws := testDB(t)
	// The common case, not an exotic one: a workspace with no chat platform
	// connected errors here on every send.
	sender := &fakeSender{err: errors.New("no platform connected")}
	New(d, sender).ConnectionNeedsReauth(ws, "conn-1", "Gmail", "work")

	msgs, _ := d.ListInboxMessages(ws, 10, 0)
	if len(msgs) != 1 {
		t.Fatalf("got %d inbox messages, want 1 despite the chat failure", len(msgs))
	}
}

func TestNilSenderStillWritesInbox(t *testing.T) {
	d, ws := testDB(t)
	New(d, nil).ConnectionNeedsReauth(ws, "conn-1", "Gmail", "work")

	msgs, _ := d.ListInboxMessages(ws, 10, 0)
	if len(msgs) != 1 {
		t.Fatalf("got %d inbox messages, want 1", len(msgs))
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/connalert/ -v`
Expected: FAIL to build — `undefined: New`. The package does not exist yet.

- [ ] **Step 3: Implement the notifier**

Create `internal/connalert/connalert.go`:

```go
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
	"github.com/ilijad1/rookery/internal/db"
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
```

- [ ] **Step 4: Run the notifier tests and confirm they pass**

Run: `go test ./internal/connalert/ -v`
Expected: PASS, three tests.

- [ ] **Step 5: Write the failing test for the hook in the token store**

Append to `internal/connectors/dbstore_test.go`:

```go
type recordingNotifier struct {
	calls [][4]string
}

func (r *recordingNotifier) ConnectionNeedsReauth(workspaceID, connectionID, providerLabel, accountLabel string) {
	r.calls = append(r.calls, [4]string{workspaceID, connectionID, providerLabel, accountLabel})
}

func TestRefreshNotifiesOnceOnDefinitiveRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	_, store, _ := refreshFixture(t, srv)
	n := &recordingNotifier{}
	store.WithNotifier(n)

	store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	if len(n.calls) != 1 {
		t.Fatalf("got %d notifications, want 1", len(n.calls))
	}
	if n.calls[0][1] != "c1" || n.calls[0][3] != "work" {
		t.Fatalf("unexpected notification payload: %v", n.calls[0])
	}

	// The row is NEEDS_REAUTH now, so AccessToken short-circuits before refresh
	// and must not notify again. This is what makes fire-once free.
	store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	if len(n.calls) != 1 {
		t.Fatalf("got %d notifications after a second call, want still 1", len(n.calls))
	}
}

func TestRefreshDoesNotNotifyOnTransientFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	_, store, _ := refreshFixture(t, srv)
	n := &recordingNotifier{}
	store.WithNotifier(n)

	store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	if len(n.calls) != 0 {
		t.Fatalf("got %d notifications for a 503, want 0", len(n.calls))
	}
}
```

- [ ] **Step 6: Run and confirm it fails**

Run: `go test ./internal/connectors/ -run 'TestRefreshNotifies|TestRefreshDoesNotNotify' -v`
Expected: FAIL to build — `store.WithNotifier undefined`.

- [ ] **Step 7: Add the notifier hook to the token store**

In `internal/connectors/dbstore.go`, add above the `DBTokenStore` struct:

```go
// ReauthNotifier is told when a connection has been definitively rejected by its
// provider and needs a human to reconnect it. Implemented by internal/connalert.
//
// It returns nothing: the caller is a background token refresh, and a failed
// notification must not fail the refresh that triggered it.
type ReauthNotifier interface {
	ConnectionNeedsReauth(workspaceID, connectionID, providerLabel, accountLabel string)
}
```

Add a field to the `DBTokenStore` struct, after `HTTP`:

```go
	// notifier is optional; nil means no alerting (tests, the livecheck harness).
	notifier ReauthNotifier
```

Add the setter after the `now()` method:

```go
// WithNotifier attaches the alert sink. Set after construction rather than in a
// literal because the notifier needs the chat gateway, which is built later in
// serve's wiring than the token store is — the same ordering constraint
// approvalSvc.WithNotifier solves.
func (s *DBTokenStore) WithNotifier(n ReauthNotifier) *DBTokenStore {
	s.notifier = n
	return s
}
```

In `refresh`, extend the flip branch from Task 2 to notify:

```go
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		if s.notifier != nil {
			label := row.Provider
			if p, ok := s.Reg.ProviderByName(row.Provider); ok && p.Label != "" {
				label = p.Label
			}
			s.notifier.ConnectionNeedsReauth(row.WorkspaceID, row.ID, label, row.AccountLabel)
		}
		return "", &ConnectorError{KindNeedsReauth, "token refresh failed for " + row.AccountLabel + "; reconnect it (" + err.Error() + ")"}
```

Also notify in the missing-refresh-token branch at line 109-112, which is equally definitive:

```go
	refreshTok, err := secrets.DecryptWithSystemKey(row.EncryptedRefreshToken, s.SystemKey)
	if err != nil || refreshTok == "" {
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		if s.notifier != nil {
			s.notifier.ConnectionNeedsReauth(row.WorkspaceID, row.ID, row.Provider, row.AccountLabel)
		}
		return "", &ConnectorError{KindNeedsReauth, "no refresh token — reconnect " + row.AccountLabel}
	}
```

- [ ] **Step 8: Run and confirm the hook tests pass**

Run: `go test ./internal/connectors/ -run 'TestRefreshNotifies|TestRefreshDoesNotNotify' -v`
Expected: PASS, both tests.

- [ ] **Step 9: Wire it in `cmd/rookery/main.go`**

Immediately after `gwManager := gateway.New(database, sysKey, router)` (line 499) and beside the existing `approvalSvc.WithNotifier(gwManager)` call, add:

```go
			// Connection re-auth alerts. Attached here rather than at connStore's
			// construction for the same reason approvalSvc is: the notifier needs
			// the gateway, which does not exist until this line. Set before the
			// refresh loop starts below.
			connStore.WithNotifier(connalert.New(database, gwManager))
```

Add the import `"github.com/ilijad1/rookery/internal/connalert"` to `cmd/rookery/main.go`.

- [ ] **Step 10: Wire it in `web/server.go`**

At `web/server.go:141`, the server builds its own token store. `s.gateway` is already assigned at line 124, so it is available. Replace line 141 with:

```go
	s.connStore = (&connectors.DBTokenStore{DB: s.db, SystemKey: s.systemKey, Reg: s.connectors, OAuth: connectors.OAuthClient{}}).
		WithNotifier(connalert.New(s.db, s.gatewaySender()))
```

Add a small accessor below `NewServer`, because `s.gateway` may be nil in tests and a nil `*gateway.GatewayManager` stored in a non-nil interface would panic on call:

```go
// gatewaySender returns the chat sender, or nil when no gateway is wired (tests).
// Returning the typed nil directly would produce a non-nil interface holding a
// nil pointer, which panics on the first send rather than being skipped.
func (s *Server) gatewaySender() connalert.Sender {
	if s.gateway == nil {
		return nil
	}
	return s.gateway
}
```

Add the import `"github.com/ilijad1/rookery/internal/connalert"` to `web/server.go`.

- [ ] **Step 11: Build and run the full suite**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/connalert/ internal/connectors/dbstore.go internal/connectors/dbstore_test.go cmd/rookery/main.go web/server.go
git commit -m "feat(connectors): alert the owner when a connection needs reconnecting

Writes an inbox row and sends a chat message the moment a connection is
definitively rejected. Fire-once needs no schema change: ConnectionsNearExpiry
selects status='ACTIVE', so the row leaves the refresh loop on the transition
and the transition cannot repeat.

The inbox row is written first and independently of the chat send — a
workspace with no chat platform errors on every send, and the inbox is
precisely that user's only surface."
```

---

### Task 4: Render the `connection` inbox source

**Files:**
- Modify: `web/ui/src/pages/home/HomePage.tsx:123-124, 159-164`
- Test: `web/ui/src/pages/home/inbox.test.tsx` (append). **Note: there is no `HomePage.test.tsx`** — the inbox card's tests live in `inbox.test.tsx`, and `home.test.tsx` holds the read/delete/mark-all-read wiring assertions.

**Interfaces:**
- Consumes: inbox rows with `source: "connection"` and empty `agent_id`, produced by Task 3. The Go DTO already serialises `source` (`web/api_home.go:63`) and the SPA type already declares it (`web/ui/src/lib/home.ts:120`), so no DTO change is needed on either side.
- **`RefID` is deliberately not asserted here.** `db.InboxMessage` carries it, but the API DTO (`web/ui/src/lib/home.ts:118-128`) does not expose it, so a fixture that sets `ref_id` fails `tsc`. The reconnect link goes to `/connections`, not to a specific connection, so nothing needs it.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/home/inbox.test.tsx`, using that file's existing `msg()` factory, module-level `messages` variable, `mockFetch()` and `wrap()`:

```tsx
test("a connection alert reads as a connection, not as an agent", async () => {
  messages = [
    msg({
      id: "m1",
      source: "connection",
      status: "error",
      body: "⚠️ Action required — your Gmail connection (work) needs reconnecting.",
      created_at: new Date().toISOString(),
    }),
  ];
  mockFetch();
  wrap();

  // Not the fall-through label "Notification", which is what an unhandled
  // source renders as today.
  expect(await screen.findByText("Connection")).toBeInTheDocument();

  // Expand the card to reveal its footer actions.
  fireEvent.click(screen.getByText(/Action required/));
  // No agent to view; the actionable link is the connections page.
  expect(screen.queryByRole("link", { name: /view agent/i })).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: /reconnect/i })).toHaveAttribute("href", "/connections");
});
```

`msg()` defaults `agent_id` and `agent_name` to `""`, which is exactly the shape Task 3 writes, so neither needs overriding.

- [ ] **Step 2: Run it and confirm it fails**

Run: `cd web/ui && npx vitest run src/pages/home/inbox.test.tsx`
Expected: FAIL — `Unable to find an element with the text: Connection`. The card currently renders the fall-through label "Notification".

- [ ] **Step 3: Implement the rendering**

In `web/ui/src/pages/home/HomePage.tsx`, replace lines 123-124:

```tsx
  const Icon = msg.source === "reminder" ? Bell : Bot;
  const name = msg.agent_name || (msg.source === "reminder" ? "Reminder" : "Notification");
```

with:

```tsx
  // A connection alert is neither an agent run nor a reminder. Without its own
  // branch it fell through to the robot icon and the generic label
  // "Notification", which reads as an agent that has no name.
  const Icon =
    msg.source === "reminder" ? Bell : msg.source === "connection" ? ENTITY_ICONS.connections : Bot;
  const name =
    msg.agent_name ||
    (msg.source === "reminder" ? "Reminder" : msg.source === "connection" ? "Connection" : "Notification");
```

Add `import { ENTITY_ICONS } from "@/lib/entityIcons";` to the file's imports. `ENTITY_ICONS.connections` is `Plug`, the same glyph the rail uses for the connections destination.

Then in the expanded footer, add the reconnect link beside the existing guarded agent link (lines 159-164):

```tsx
          <div className="flex items-center justify-end gap-1">
            {msg.agent_id && (
              <Button variant="ghost" size="xs" asChild>
                <Link to={`/agents/${msg.agent_id}`}>View agent</Link>
              </Button>
            )}
            {msg.source === "connection" && (
              <Button variant="ghost" size="xs" asChild>
                <Link to="/connections">Reconnect</Link>
              </Button>
            )}
            <Button variant="ghost" size="xs" className="text-danger" onClick={onDelete}>
              <Trash2 /> Delete
            </Button>
          </div>
```

- [ ] **Step 4: Run the test and confirm it passes**

Run: `cd web/ui && npx vitest run src/pages/home/inbox.test.tsx`
Expected: PASS.

- [ ] **Step 5: Run the frontend gate**

Run: `cd web/ui && npx tsc -b && npx oxlint && npx vitest run`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/home/HomePage.tsx web/ui/src/pages/home/inbox.test.tsx
git commit -m "feat(web/home): render connection alerts as their own inbox kind

An unhandled source fell through to the robot icon and the label
Notification, so a connection alert read as a nameless agent. Give it the
connections glyph, its own label, and a link to the page that fixes it."
```

---

### Task 5: Reconnect fires the re-authentication

**Files:**
- Modify: `web/ui/src/pages/connections/ServiceWizard.tsx:80-169, 221-224, 240-252, 307`
- Test: `web/ui/src/pages/connections/ServiceWizard.test.tsx:308` (rewrite) and new cases

**Interfaces:**
- Consumes: `ServiceProvider.kind` (`"oauth" | "api_key" | "keyless"`) and `ServiceProvider.connect_inputs[].required`, both already on the DTO (`web/ui/src/lib/connections.ts:120-156`).
- Produces: nothing other tasks depend on.

**Why the label matters, restated because getting it wrong looks like success:** `db.InsertServiceConnection` upserts on `(workspace_id, provider, account_label)` and keeps the row id, so reconnecting under the *same* label repairs the connection in place and preserves its `agent_connections` bindings. A different or empty label creates a second connection and leaves the broken one bound to the agents — the UI would look fixed while the agents kept failing.

- [ ] **Step 1: Write the failing tests**

In `web/ui/src/pages/connections/ServiceWizard.test.tsx`, replace the existing test at line 308 (`"a needs-reconnect account shows a Reconnect button that jumps to the connect flow"`) with these three. Add the fixture above them:

```tsx
const OAUTH_NEEDS_REAUTH_WITH_INPUTS: ServiceProvider = {
  ...OAUTH_NEEDS_REAUTH,
  name: "google_ads",
  label: "Google Ads",
  connect_inputs: [
    { key: "developer_token", label: "Developer token", hint: "From your Google Ads API centre", required: true },
  ],
};

test("reconnecting an oauth account goes straight to the provider, reusing its label", async () => {
  let captured: { label: string } | null = null;
  mockFetch({
    connect: (_provider, body) => {
      captured = body;
      return jsonResponse({ redirect_url: "https://provider.example/oauth/authorize?x=1" });
    },
  });
  const assignSpy = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ assign: assignSpy } as unknown as Location);
  const user = userEvent.setup();
  wrap(OAUTH_NEEDS_REAUTH);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /reconnect/i }));

  // One click, no second Connect press.
  expect(assignSpy).toHaveBeenCalledWith("https://provider.example/oauth/authorize?x=1");
  // The SAME label, so InsertServiceConnection upserts the existing row and its
  // agent bindings survive rather than a duplicate connection being created.
  expect(captured).toEqual({ label: "team", inputs: {} });
});

test("an oauth provider with required inputs lands on the form instead of redirecting", async () => {
  const assignSpy = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ assign: assignSpy } as unknown as Location);
  const user = userEvent.setup();
  wrapLive(OAUTH_NEEDS_REAUTH_WITH_INPUTS);

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /reconnect/i }));

  // The developer token cannot be guessed or echoed back, so the user supplies it.
  expect(await screen.findByLabelText(/developer token/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/label/i)).toHaveValue("team");
  expect(assignSpy).not.toHaveBeenCalled();
});

test("reconnecting an api_key account lands on the paste form, never redirecting", async () => {
  const assignSpy = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ assign: assignSpy } as unknown as Location);
  const user = userEvent.setup();
  wrapLive({
    ...API_KEY_PROVIDER,
    connections: [{ id: "c9", label: "personal", identity: "", status: "NEEDS_REAUTH" }],
  });

  await user.click(screen.getByText("open wizard"));
  await user.click(await screen.findByRole("button", { name: /reconnect/i }));

  expect(await screen.findByLabelText(/openai api key/i)).toBeInTheDocument();
  expect(assignSpy).not.toHaveBeenCalled();
});
```

- [ ] **Step 2: Run and confirm the first test fails**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`

Expected: the oauth redirect test FAILS with `assignSpy` never called — that is the bug. The other two should already pass, since landing on the form is today's behaviour for every branch.

- [ ] **Step 3: Make `handleConnect` take an explicit label**

In `ServiceWizard.tsx`, change `handleConnect` (line 240):

```ts
  // The label is a parameter, not read from state: reconnect() calls this
  // immediately after setLabel(), and setLabel is async — reading state here
  // would send the PREVIOUS label. That matters beyond cosmetics, because the
  // label is the upsert key: a wrong one creates a second connection and leaves
  // the broken one bound to the user's agents.
  async function handleConnect(labelOverride?: string) {
    setConnectError(null);
    try {
      const res = await connectServiceMutation.mutateAsync({
        provider: provider.name,
        label: labelOverride ?? label,
        inputs,
      });
      window.location.assign(res.redirect_url);
    } catch (err) {
      setConnectError(errMsg(err));
    }
  }
```

The existing Connect button at line 563 calls `handleConnect()` with no argument and is unchanged — it falls back to the state value.

- [ ] **Step 4: Replace `jumpToConnect` with `reconnect`**

Replace `jumpToConnect` (lines 221-224) with:

```ts
  // Reconnect must actually re-authenticate. It used to only switch the view and
  // seed the label, so the button labelled Reconnect filled in a text field and
  // stopped.
  //
  // Only an OAuth provider has a consent URL to send the user to. An api_key
  // provider has nothing to redirect to, and an OAuth provider with required
  // connect_inputs needs values we must not guess — Google Ads collects a
  // developer token, which is a secret we should not echo back into a form the
  // user did not ask for. Both land on the form, which is the correct behaviour
  // and was already what the old code did for every case.
  function reconnect(seedLabel: string) {
    setView("connect");
    setLabel(seedLabel);
    const needsInput = provider.connect_inputs.some((i) => i.required);
    if (provider.kind === "oauth" && !needsInput) {
      void handleConnect(seedLabel);
    }
  }
```

- [ ] **Step 5: Point the account row at it**

At line 307, change:

```tsx
              onReconnect={() => jumpToConnect(c.label)}
```

to:

```tsx
              onReconnect={() => reconnect(c.label)}
```

`AccountRow`'s `onReconnect: () => void` prop signature is unchanged.

- [ ] **Step 6: Run the tests and confirm all three pass**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`
Expected: PASS.

- [ ] **Step 7: Run the frontend gate**

Run: `cd web/ui && npx tsc -b && npx oxlint && npx vitest run`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/ui/src/pages/connections/ServiceWizard.tsx web/ui/src/pages/connections/ServiceWizard.test.tsx
git commit -m "fix(web/connections): make Reconnect actually reconnect

jumpToConnect switched the view and typed the label into a text box; nothing
called handleConnect, so the button labelled Reconnect filled in a field and
stopped.

OAuth providers without required inputs now go straight to consent. The label
is passed explicitly rather than read from state — setLabel is async, and the
label is the upsert key that keeps the repaired connection's agent bindings."
```

---

### Task 6: Workspace images in owner settings

**Files:**
- Modify: `web/ui/src/pages/settings/OwnerSections.tsx:66-82`
- Test: `web/ui/src/pages/settings/OwnerSections.test.tsx` (append)

**Interfaces:**
- Consumes: `WorkspaceAvatar` from `@/lib/workspaceIcons` — `{ name?: string; icon?: string; className?: string }`. It **already** implements the spec's unset-vs-unknown rule (`workspaceIcons.tsx:243-259`): an unset icon renders the Rookery mark, an unknown slug renders the name's initial. Do not reimplement or modify it.
- Consumes: `session.workspaces[].icon`, already on the DTO (`web/api_auth.go:19` → `web/ui/src/lib/session.ts:8`). No backend change.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/settings/OwnerSections.test.tsx`. That file's `wrap()` takes **no arguments** — it renders all five owner sections and the workspaces come from its module-level `SESSION_FIXTURE`, whose two workspaces (`Home Server`, `Side Project`) currently carry **no `icon` field**. That is the unset case, so it is the one to assert on; there is no need to add a fixture:

```tsx
test("each workspace card shows its image, not just its name", async () => {
  mockFetch();
  wrap();

  await waitFor(() => expect(workspaceCardNames().length).toBeGreaterThan(0));

  // The fixture's workspaces have no icon set, which WorkspaceAvatar renders as
  // the Rookery mark — an aria-hidden <svg>. The card previously rendered no
  // graphic at all, so any svg inside it is the assertion.
  const nameEl = document.querySelector(".truncate.font-semibold");
  const card = nameEl?.closest("div.rounded-lg");
  expect(card?.querySelector("svg")).toBeTruthy();
});
```

Note that `workspaceCardNames()` selects on `.truncate.font-semibold`, and Step 3 keeps that class on the name element — so the file's existing workspace-card assertions continue to pass unchanged.

- [ ] **Step 2: Run and confirm it fails**

Run: `cd web/ui && npx vitest run src/pages/settings/OwnerSections.test.tsx`
Expected: the new test FAILS — `expect(received).toBeTruthy()` receives `undefined`, because the card renders no `svg`. Every other test in the file passes.

- [ ] **Step 3: Render the avatar**

In `OwnerSections.tsx`, change the `WorkspaceCard` header block (lines 68-82) from:

```tsx
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-semibold">
            {ws.name}
```

to:

```tsx
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-start gap-2.5">
          {/* WorkspaceAvatar already distinguishes unset (Rookery mark) from an
              UNKNOWN slug (initial) — an unknown value means a workspace
              configured by a newer build, where rendering the default would
              present that build's choice as the user's own. */}
          <WorkspaceAvatar
            name={ws.name}
            icon={ws.icon}
            className="mt-0.5 size-8 shrink-0"
          />
          <div className="min-w-0">
          <div className="truncate font-semibold">
            {ws.name}
```

and close the extra `<div>` at the end of that block — the existing:

```tsx
          {ws.about && (
            <div className="truncate text-sm text-muted-2">{ws.about}</div>
          )}
        </div>
      </div>
```

becomes:

```tsx
          {ws.about && (
            <div className="truncate text-sm text-muted-2">{ws.about}</div>
          )}
          </div>
        </div>
      </div>
```

Add `import { WorkspaceAvatar } from "@/lib/workspaceIcons";` to the file's imports.

- [ ] **Step 4: Run the test and confirm it passes**

Run: `cd web/ui && npx vitest run src/pages/settings/OwnerSections.test.tsx`
Expected: PASS.

- [ ] **Step 5: Run the frontend gate**

Run: `cd web/ui && npx tsc -b && npx oxlint && npx vitest run`
Expected: PASS. `tsc -b` catches an unbalanced JSX tag from Step 3, which is the likeliest slip in this task.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/settings/OwnerSections.tsx web/ui/src/pages/settings/OwnerSections.test.tsx
git commit -m "fix(web/settings): show workspace images in owner settings

The owner workspaces list rendered names and buttons while every other
surface showed the workspace's chosen artwork. icon was already on the session
DTO, so this is a component insertion with no backend change."
```

---

### Task 7: Full gate and documentation check

**Files:** none modified unless the checks fail.

- [ ] **Step 1: Run the complete local gate**

Run: `make ci`
Expected: PASS — gofmt, `go vet`, `go test -race`, cross-compile for all six GOOS/GOARCH pairs, and the frontend typecheck/lint/vitest.

- [ ] **Step 2: Confirm the documentation surfaces are unaffected**

Run: `make docs-sync-check`
Expected: PASS. This change adds no connector provider, no `ROOKERY_*` variable, no CLI subcommand and no `/api/v1` route, so no count moves. If it reports a failure, it is pre-existing — check `git stash` against `main` before changing any prose.

- [ ] **Step 3: Verify the two user-visible fixes by hand**

Run: `make deploy`, then open `http://127.0.0.1:8080/settings?section=owner-workspaces` and confirm each workspace row shows its image. Open `/connections`, open a provider with a `NEEDS_REAUTH` account, and confirm Reconnect leaves the app for the provider's consent screen rather than filling in the label box.

There is no way to verify the alert by hand without revoking a real OAuth grant. The Go tests cover the transition, the fire-once property and both delivery surfaces; state in the PR that live verification was not performed.

- [ ] **Step 4: Open the pull request**

Title must itself be a valid Conventional Commit, because the squash-merge makes it the commit that lands on `main` and release-please reads it:

```
fix(connections): reconnect actually reconnects, and alert before agents fail
```

Body should name the pre-existing bug found during implementation (the unconditional `NEEDS_REAUTH` flip), since it is the most consequential change here and is not what the issue asked for.

---

## Self-Review

**Spec coverage.** Spec §1 (reconnect) → Task 5, all three branches plus the label-preservation assertion. Spec §2 precondition (unconditional flip) → Tasks 1–2. Spec §2 trigger and dual-surface delivery → Task 3. Spec §2's inbox `source` and card rendering → Task 4. Spec §3 (workspace images) → Task 6, including the unset-vs-unknown rule, which `WorkspaceAvatar` already satisfies and Task 6 therefore reuses rather than reimplements.

Spec §2's three non-goals are deliberately unimplemented: never-refreshing providers, advance warning, and `session_exchange` alerting. No task exists for them and none should.

**Type consistency.** `ConnectionNeedsReauth(workspaceID, connectionID, providerLabel, accountLabel string)` has the same four parameters in the interface (Task 3 Step 7), the implementation (Step 3), the recording fake (Step 5) and both call sites. `WithNotifier` returns `*DBTokenStore` in its definition and is used both as a statement (`main.go`) and chained (`web/server.go`). `connalert.New(database, sender)` matches at all three call sites. `handleConnect(labelOverride?: string)` is optional, so the existing no-argument call still typechecks.

**Placeholders.** None. Every code step carries the literal content to write.

**Helper names verified against the real test files**, after an earlier draft guessed three of them wrong: `inbox.test.tsx` (there is no `HomePage.test.tsx`) provides `msg()` / `mockFetch()` / `wrap()` plus a module-level `messages`; `OwnerSections.test.tsx` provides a zero-argument `wrap()`, `mockFetch()` and `workspaceCardNames()`, with workspaces coming from its own `SESSION_FIXTURE`; `ServiceWizard.test.tsx` provides `wrap()`, `wrapLive()`, `mockFetch()` and `jsonResponse()`. `Provider.Label` exists (`registry.go:137`) and `ProviderByName` returns `(Provider, bool)`, as Task 3 Step 7 assumes.

**One risk worth naming:** Task 6 Step 3 restructures JSX by hand and is the likeliest place to introduce an unbalanced tag. Step 5's `tsc -b` catches it.
