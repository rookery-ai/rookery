# Owner re-authentication gate for install-level settings

**Date:** 2026-07-30
**Status:** approved

## Problem

The Owner tab in Settings is reachable by anyone holding a logged-in owner
session, with no further proof of identity. It fronts the most destructive actions
in the product:

- `POST /api/v1/backup/restore` stages a snapshot and shuts the server down so the
  next boot swaps the entire database and every workspace's knowledge base.
- `DELETE /api/v1/workspaces/:id` destroys a tenant.
- `DELETE /api/v1/backup/snapshots/:name` destroys the recovery path.
- `PUT /api/v1/admin/public-url` changes where OAuth callbacks land.

All of these are guarded only by `requireOwnerAPI`, which checks that a session
carries an `owner_id`. Once the owner has logged in — and the session outlives the
browser tab — the password is never asked for again.

The existing screen lock (`web/api_auth.go:74-96`) shows the shape of the answer
but not its scope: it is a server-side session flag, gated by `apiLockGate`
returning 423, and it is explicitly scoped to "someone walking up to an
unattended screen, not someone who already holds the session cookie". It is also
all-or-nothing — locked or unlocked — and unlocking it takes the *workspace*
master password, not the owner password.

## Goals

- Accessing install-level settings requires proving the owner password again,
  independent of how long the session has been alive.
- Enforced on the server, so the guarantee does not depend on the SPA.
- Not so aggressive that a normal owner task (configure backup, run it, list the
  snapshots) becomes a password-entry treadmill.

## Non-goals

- Protecting against an attacker who knows the owner password. Nothing at this
  layer can.
- Replacing or reworking the screen lock. The two are orthogonal: the lock covers
  the whole UI against a passer-by; this covers install-level actions against a
  session that has been left open or whose cookie has leaked.
- Gating workspace entry. `POST /api/v1/workspaces/:id/enter` already requires
  that workspace's master password via `verifyWorkspaceMasterPassword`.

## Components

### `POST /api/v1/auth/owner-verify`

```
Request:  {"password": "…"}
Response: 200 {"ok": true, "verified_until": "2026-07-30T14:47:00Z"}
          401 {"error": "invalid_password", "message": "…"}
```

Sits on the owner group (`requireOwnerAPI`) — you must already be logged in to
re-verify. The username comes from the session's owner record, never from the
request body: the single-owner model means there is exactly one valid username,
and accepting one from the client would add an oracle for nothing.

Verification calls `auth.Authenticate(s.db, o.Username, req.Password)`, the same
bcrypt path as login. On success it stamps `owner_verified_at` (Unix seconds) on
the session. On failure it audit-logs `owner_verify_failed` with `c.RealIP()`,
mirroring how `apiUnlock` logs `unlock_failed`, and returns 401 without
distinguishing "wrong password" from anything else.

Session-stored rather than a separate token because the session is already the
authentication carrier, is already `SameSite`-protected and server-signed, and is
already how `locked` works. A second token would be a second thing to expire.

### `requireOwnerVerified` middleware

Returns `403 {"error": "owner_verification_required"}` when the session has no
`owner_verified_at`, or when it is older than the TTL. Ordered **after**
`requireOwnerAPI`, so a request with no session at all still gets the 401 it
should — an unauthenticated caller must not learn that a verification gate
exists.

**TTL: 15 minutes**, a package constant. Chosen from the shape of real owner work:
saving a backup config, triggering a run, and listing the resulting snapshots is
three or four requests over a couple of minutes, which should cost one password
entry. A per-visit gate would charge four.

403 rather than 401: the caller *is* authenticated. 401 would invite the SPA's
generic handler to bounce them to the login screen and drop the session, which is
both wrong and hostile.

### Route coverage

Applied to:

- `/api/v1/admin/*` — overview, audit, settings, public-url read/write/test.
- `/api/v1/backup/*` — all eight routes.
- `DELETE /api/v1/workspaces/:id`.

`/backup/*` is not optional. Those routes sit on the owner group deliberately,
because one snapshot spans every workspace and must be configurable before any
workspace exists. Gating only the SPA's Owner tab while leaving
`POST /api/v1/backup/restore` on `requireOwnerAPI` alone means a leaked cookie
still replaces the whole install with one curl. A UI-only gate here deters
shoulder-surfing and nothing else.

Left ungated:

- `POST /api/v1/workspaces/:id/enter`, `POST /api/v1/workspaces/leave` — entering
  already demands the workspace master password; re-asking for the owner password
  on every workspace switch would be punitive.
- `GET /api/v1/workspaces`, `POST /api/v1/workspaces` — listing is already visible
  in the session payload, and creating a workspace is additive and reversible.
- `POST /api/v1/auth/change-password` — already requires the current password,
  which is a strictly stronger check than this gate.
- `POST /api/v1/auth/owner-verify` itself, and lock/unlock/logout/session, or the
  gate would be unescapable.

### Session reporting

`GET /api/v1/auth/session` gains `owner_verified: bool` (true iff a stamp exists
and is within the TTL). The SPA already loads and caches this payload once, so a
page reload lands in the right state without a probe request — the same reasoning
that put `locked` and `timezone` there.

### SPA

The Owner tab body is wrapped in a gate component. On a 403 with
`owner_verification_required` from any of its queries, it renders an inline
prompt in place of the body:

```
┌───────────────────────────────┐
│ 🛡 Owner settings              │
│ Confirm your owner password   │
│ [•••••••••••]        [Unlock] │
└───────────────────────────────┘
```

On success it invalidates the session query and the Owner tab's queries, and the
body renders. When the TTL lapses mid-session the next request 403s and the
prompt returns — no client-side timer, so the client and server can never
disagree about whether verification is still valid.

The other Settings tabs are untouched; they are workspace-scoped and already
require the workspace master password to have been entered.

## Data flow

```
click Owner tab
   │
   ├─► GET /api/v1/admin/overview
   │      requireOwnerAPI ──► ok (session has owner_id)
   │      requireOwnerVerified ──► no stamp
   │      403 {"error":"owner_verification_required"}
   │
   ├─► SPA renders inline password prompt
   │
   ├─► POST /api/v1/auth/owner-verify {password}
   │      auth.Authenticate(db, session.owner.Username, password)
   │      session["owner_verified_at"] = now
   │      200 {"ok":true,"verified_until":…}
   │
   └─► refetch ──► 200, body renders

  …15 minutes later…
       any /admin/* or /backup/* request ──► 403 ──► prompt returns
```

## Error handling

- Wrong password: 401 `invalid_password`, audit-logged with the IP. No lockout or
  rate limit is added — the existing login path has none either, and adding one
  here alone would be inconsistent security theatre. Worth noting as a known gap
  rather than solving asymmetrically.
- Session write failure when stamping: 500, and the gate stays closed. Failing
  closed is right here — unlike `ParkerFor`, where failing closed would silently
  halt an autonomous agent, a failed verification just means the owner tries
  again.
- Clock skew is not a concern: both the stamp and the comparison come from the
  server's own clock.
- A stamp from before a server restart survives if the session key is persistent
  (`ROOKERY_SESSION_KEY`) and dies with it otherwise. Either is acceptable; the
  gate is a re-auth, not a secret.

## Testing

**`web` (Go)**
- `requireOwnerVerified` table test: no stamp → 403 `owner_verification_required`;
  stamp within TTL → passes; stamp older than TTL → 403; no session at all →
  401 from `requireOwnerAPI` (proving middleware order).
- **Route coverage test**: every registered route under `/api/v1/admin/` and
  `/api/v1/backup/`, plus `DELETE /api/v1/workspaces/:id`, carries the gate —
  enumerated from `s.echo.Routes()` the way `TestAPIParityInventory` does. This
  is the test that stops a future route added to the owner group from silently
  skipping the gate.
- `owner-verify` with the correct password stamps the session and returns
  `verified_until`; with a wrong password returns 401 and writes an audit row;
  the request body's username, if any, is ignored.
- `GET /auth/session` reports `owner_verified` true after verify, false before,
  and false once the stamp is stale.
- `owner-verify`, lock, unlock, logout, and session remain reachable without a
  stamp.

**`web/ui` (vitest)**
- The Owner tab renders the password prompt when its query 403s with
  `owner_verification_required`, and the body after a successful verify.
- A 403 with a different error code does not render the prompt (so an unrelated
  permission error is not mistaken for a verification gate).
- Other Settings tabs render without any prompt.

## Accepted costs

- **Not protection against a known password.** Stated plainly: this raises the bar
  against an unattended-but-unlocked session and against a leaked cookie being
  used for install-destroying actions. Someone with the owner password is
  unaffected.
- **No rate limiting on `owner-verify`.** Consistent with the existing login and
  unlock paths, which have none. Recorded as a gap to address across all three at
  once, not in one of them.
- **15 minutes is a judgement call.** Long enough for one owner task, short enough
  that a walked-away-from browser re-locks within a coffee break. A constant, so
  changing it is a one-line edit.
- **A restart can invalidate the stamp** when the session key is ephemeral. The
  owner re-enters their password once; acceptable.
