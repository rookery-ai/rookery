# Reconnect that reconnects, action-required alerts, and workspace images

**Date:** 2026-08-11
**Status:** approved, ready for implementation
**Scope:** Spec A of four. See also: `2026-08-11-cli-coder-model-and-ai-providers-design.md` (B),
`2026-08-11-sigv4-auth-kind-and-aws-design.md` (C),
`2026-08-11-connector-expansion-waves-design.md` (D).

Three defects, all confirmed by reading the source, none requiring a provider to be
verified against a live API. They ship together because they are small, independent,
and all three are visible to a user on their first afternoon with the product.

## 1. Reconnect fires the re-authentication

### The defect

`ServiceWizard.tsx`'s `jumpToConnect` does two things and neither of them reconnects:

```ts
function jumpToConnect(seedLabel: string) {
  setView("connect");
  setLabel(seedLabel);
}
```

It switches the panel to the connect view and types the account's label into the
label box. The actual OAuth journey lives in `handleConnect`, which calls the connect
mutation and then `window.location.assign(res.redirect_url)` — and nothing calls it.
So the button labelled Reconnect fills in a text field and stops.

### The fix

`AccountRow`'s Reconnect calls a new `reconnect(connection)` that branches on what the
provider can actually do:

- **OAuth with no required `connect_inputs`** — seed the label, then invoke the connect
  mutation immediately and assign `redirect_url`. One click to the consent screen. This
  is the overwhelming majority of OAuth providers.
- **OAuth with required `connect_inputs`** — `google_ads`, `mastodon`, `bluesky`. Land on
  the connect form with the label seeded, which is today's behaviour. These providers
  need values we must not guess: Google Ads collects a developer token, which is a
  secret we should not echo back into a form the user did not ask for.
- **`api_key`** — land on the paste form with the label seeded. Today's behaviour, and
  already correct: there is no consent URL to redirect to. 29 of the 32 providers
  declaring `connect_inputs` are this kind.

### Two constraints that are not obvious

**`handleConnect` must accept the label as an argument rather than read it from state.**
`setLabel` is asynchronous. Calling `handleConnect()` on the line after `setLabel(...)`
reads the *previous* label. The signature becomes `handleConnect(labelOverride?: string)`,
defaulting to the state value so the existing Connect button is unchanged.

**The label is load-bearing, not cosmetic.** `db.InsertServiceConnection` upserts on
`(workspace_id, provider, account_label)` and leaves the row `id` untouched:

```sql
ON CONFLICT(workspace_id, provider, account_label) DO UPDATE SET
  ..., status=excluded.status, updated_at=datetime('now')
```

Reconnecting under the *same* label therefore repairs the existing connection in place,
resets its status to `ACTIVE`, and preserves every `agent_connections` binding, which is
keyed by connection id. Reconnecting under a different or empty label creates a **second**
connection and leaves the broken one still bound to the agents. The user would see a
healthy connection in the list while their agents kept failing — a worse outcome than
the bug being fixed. This is why the broken code seeded the label at all; the fix keeps
that behaviour and adds the redirect, rather than replacing it.

### Tests

`ServiceWizard.test.tsx:308` currently asserts the broken behaviour ("shows a Reconnect
button that jumps to the connect flow") and is rewritten. New cases:

- OAuth without required inputs: clicking Reconnect issues the connect mutation and
  navigates, without a second click.
- OAuth with required inputs (`google_ads`): clicking Reconnect lands on the form and
  does **not** navigate.
- `api_key`: clicking Reconnect lands on the paste form and does **not** navigate.
- In every branch, the label sent to the mutation equals the existing connection's label.

## 2. "Action required" when a connection needs re-authentication

### Precondition: the status flip is currently unconditional, and must not stay that way

This was found while designing the alert, and the alert cannot be correct until it is
fixed. `DBTokenStore.refresh` flips a connection to `NEEDS_REAUTH` on **any** refresh
error whatsoever:

```go
// internal/connectors/dbstore.go:113-116
ts, err := s.OAuth.Refresh(ctx, prov, cid, csec, refreshTok)
if err != nil {
    s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
    return "", &ConnectorError{KindNeedsReauth, "token refresh failed for " + row.AccountLabel + ...}
}
```

A network blip, a DNS failure, a provider 500 or a 429 all mark the connection dead. And
because `ConnectionsNearExpiry` selects `WHERE status='ACTIVE'`, the row then **leaves the
refresh loop permanently** — the background renewal that would have fixed it on the next
tick never runs again. A transient provider outage therefore bricks a healthy connection
until a human reconnects it by hand.

This is a pre-existing bug, but the alert is what makes it user-visible: without the fix,
every provider hiccup sends "Action required!" for a connection that was fine. Shipping
the notification on top of the current behaviour would make a silent bug into a loud one.

**The fix is to classify, then gate.** The information is already there and is being
discarded — `tokenRequest` maps every status `>= 400` onto a single kind:

```go
// internal/connectors/oauth.go:99-100
if resp.StatusCode >= 400 {
    return TokenSet{}, &ConnectorError{KindAuth, fmt.Sprintf("token endpoint %d: %s", ...)}
}
```

Two changes, using kinds that already exist in `execute.go`:

1. **`tokenRequest` classifies by status.** `429` → `KindRateLimit`; `5xx` → `KindServer`;
   other `4xx` → `KindAuth`. Transport failures already return `KindNetwork` correctly.
2. **`refresh` flips only on `KindAuth`** — a definitive rejection by the provider, which
   is what OAuth's `invalid_grant` means. `KindNetwork`, `KindServer` and `KindRateLimit`
   return the error *without* flipping, leaving the row `ACTIVE` so the loop retries on
   the next tick.

This is the same reasoning used below to reject flipping status inside
`session_exchange`: a transient failure must not permanently mark a healthy connection
broken. It applies at least as strongly to the path that is actually hooked.

Tests: a 500 from the token endpoint leaves the row `ACTIVE` and sends nothing; a 400
flips it and sends once; a network error leaves it `ACTIVE`.

### Trigger

One hook at the single place a connection transitions to `NEEDS_REAUTH`: in
`DBTokenStore.refresh`, beside the existing `UpdateConnectionStatus` call. Not in the
refresh loop, not at read time — at the transition.

**Fire-once needs no schema change.** `db.ConnectionsNearExpiry` selects
`WHERE status='ACTIVE' AND ...`, so the row leaves the refresh loop's query the instant it
flips. The transition can happen at most once per repair cycle. No `notified_at` column,
no periodic sweep, no de-duplication logic, and no possibility of the loop re-sending
the alert on every tick.

### Delivery

Both surfaces, mirroring the approval gate's precedent and for the same stated reason —
a workspace with no chat platform connected must not be stuck:

- **Inbox** — `db.CreateInboxMessage`.
- **Chat** — `SendToUser(workspaceID, text)`, the narrow interface `internal/reminder` and
  `internal/approval` already depend on. The notifier takes that interface, not
  `*GatewayManager`, so it is injectable in tests.

A chat send failure must not prevent the inbox write. Write the inbox row first, then
attempt the chat send and log a failure without returning it.

**The inbox row needs a third `source` value.** `db.InboxMessage.Source` is documented as
`"agent_run" | "reminder"`; connection alerts are neither. Add `"connection"`, with
`Status: "error"`, `RefID` set to the connection id, and `AgentID` left empty — which
`CreateInboxMessage` already inserts as SQL NULL specifically so a non-agent row does not
trip the foreign key.

The card renderer degrades safely but reads wrong, and both lines need updating
(`HomePage.tsx:123-124`):

```ts
const Icon = msg.source === "reminder" ? Bell : Bot;
const name = msg.agent_name || (msg.source === "reminder" ? "Reminder" : "Notification");
```

An unknown source falls through to the robot icon and the label "Notification". Give
`"connection"` its own icon and the label "Connection", both from `lib/entityIcons.tsx` —
lucide only, `currentColor`, never a bare import. The "View agent" link at line 160 is
already guarded by `msg.agent_id &&`, so it correctly renders nothing; add a
"Reconnect" link to `/connections` in its place for this source.

Message text names the account and the remedy:

> ⚠️ **Action required** — your **Gmail (work)** connection needs reconnecting.
> Agents using it will fail until it is reconnected.
> Reconnect it in Settings → Connections.

### Deliberate non-goals

Stated here so they are recorded limits rather than gaps discovered later:

- **Providers that never refresh are not covered.** `token_expiry: never` (GitHub, Notion)
  and `auth.kind: none` connections never enter the refresh loop, so a server-side
  revocation stays invisible until an agent run gets a 401. Covering them properly needs a
  liveness probe, which is a different feature. Note that the user is *not* silent in that
  case: `Execute` returns `KindNeedsReauth` and `agentrunner.FriendlyRunError` already
  sends a run-failure message naming the cause. Hooking this notifier there as well was
  considered and rejected for now — it would deliver two messages about one event. If a
  future spec adds it, the two paths must be de-duplicated rather than both left to fire.
- **There is no advance warning for refresh-token providers.** The loop renews them
  indefinitely and failure is unpredictable — a revoked grant, a changed password, a
  rotated app secret. The failure *is* the first available signal. The alert fires at
  failure time, which is typically hours to days before the next scheduled run, and that
  is the honest extent of "before the agents start failing".
- **The inbox row is not reflected to the vault.** An inbox message is a delivery record,
  not knowledge. `vault.RemoveLegacyInboxNotes` exists because this was violated once and
  fed notification noise into designer retrieval.
- **`session_exchange` providers do not alert.** `DBTokenStore.sessionToken`
  (`dbstore.go:187`) returns `KindNeedsReauth` when the login POST fails — the case where
  a Bluesky app password has been revoked — but it does **not** flip the row's status, so
  the transition this feature hooks never happens. Flipping it there was considered and
  rejected for now: a transient 4xx would permanently mark a healthy connection as broken
  until someone reconnected it by hand, which is a worse failure than the missing alert.
  Doing it properly needs a consecutive-failure threshold, which is its own change.

### Tests

- A status flip to `NEEDS_REAUTH` writes exactly one inbox row and issues exactly one
  chat send, with the account label present in both.
- A refresh attempt against a row already at `NEEDS_REAUTH` sends nothing.
- A chat-send error still leaves the inbox row written.

## 3. Workspace images in owner settings

### The defect

`OwnerSections.tsx`'s `WorkspaceCard` renders `ws.name` and `ws.about` and no image, so
`/settings?section=owner-workspaces` lists workspaces as names and buttons while every
other surface shows their chosen artwork.

### The fix

Render the workspace avatar beside the name, reusing the component the icon rail already
uses. No backend work: `icon` is already on the DTO
(`web/api_auth.go:19` → `web/ui/src/lib/session.ts:8`), and `WorkspaceCard` already reads
from `useSession()`.

**Preserve the unset-versus-unknown distinction.** An *unset* icon renders
`DEFAULT_WORKSPACE_ICON` (the Rookery mark). An *unknown* slug falls back to the name
monogram, because an unknown value means a workspace configured by a newer build —
rendering the default there would present that build's choice as the user's own.

### Tests

- A workspace with a known preset slug renders that preset.
- A workspace with `icon: ""` renders the Rookery mark.
- A workspace with an unrecognised slug renders the monogram, not the mark.

## Out of scope

Custom workspace image upload remains deferred for the reasons already recorded: it needs
a multipart endpoint, an `iolimit` cap, MIME sniffing (SVG is an XSS vector), vault storage
with backup implications, and a two-shape icon field. Bundling it here would put a security
review on the critical path of a three-line rendering fix.
