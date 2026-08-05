# Sign out, and an owner-password gate on workspace creation

## Problem

Two gaps, both on the pre-workspace surfaces.

**There is no way to sign out.** `POST /api/v1/auth/logout` has existed since the
JSON API was written — it is registered, audited and covered by
`api_auth_test.go` — and the SPA calls it from nowhere. The icon rail offers
Lock and Settings; the workspace menu offers *Leave workspace*, which drops the
active workspace but keeps the owner session. An owner who wants to end their
session has to clear a cookie by hand.

**Anyone holding a logged-in owner session can create a workspace.**
`POST /api/v1/workspaces` sits on the plain `owner` route group. Workspace
*delete* is already behind `requireOwnerVerified`; create is not, so an
unattended browser is one click away from a new tenant. The asymmetry is not
deliberate — it predates the owner-verification gate.

## Scope

Sign out appears on the standalone screens only — `/workspaces` and the lock
screen. It is deliberately **not** added inside `AppShell`.

Two reasons. The first is the user's call. The second is that the shell has
nowhere to put it: every page owns its own top-right (`AgentsPage`,
`SkillsPage`, `SecretsPage` and `HomePage` all render a search box and a primary
action in a `sm:justify-between` header row inside `PageContainer`), so a fixed
top-right button lands on "New agent". Reserving right-side clearance across
~16 page headers is a larger change than the feature.

The route out of the app is unchanged and already exists: workspace menu →
*Leave workspace* → `/workspaces`, where Sign out lives.

## Sign out

A single `SignOutButton` component, mounted by `Workspaces.tsx` and
`LockScreen.tsx`. Positioned `fixed top-4 right-4` — the viewport corner, not
inside either card, which is what makes one component work for two very
different layouts (a centered card on `bg-chrome` vs. a `fixed inset-0`
overlay). It renders above the lock screen's `z-50`.

It is `variant="destructive"` with a `LogOut` icon and a visible "Sign out"
label.

**On the convention.** The design system reserves `variant="destructive"` for
actions that remove data, and signing out removes none. It is used here anyway,
because `destructive` is the app's red and the requirement is a visible red
button; the alternative — a new colour token — would force a re-run of
`contrast.test.ts` to buy a shade nobody asked for. This is the one sanctioned
exception and it is recorded here so the next reader does not treat it as drift.

**It confirms before acting.** A mis-click in a screen corner costs a re-login
*and* a workspace master-password re-entry, so the button opens a small
"Sign out?" dialog rather than firing immediately. Deliberately a plain confirm,
not the deferred-delete/undo pattern used for inbox rows: session teardown
cannot be un-done from the client.

**It works while locked, and that is not incidental.** `api_auth.go` exempts
logout from `apiLockGate` specifically "so the SPA can render the lock screen
and the user can always escape it". The escape hatch was built and never given
an affordance; this supplies it.

On success the handler drops every cached query except the session, invalidates
the session, and lets `RequireAuth` navigate to `/login` — the same cache
discipline `resetWorkspaceScopedCache` already applies on a tenant switch, for
the same reason (a tenant's rows must not survive into the next screen).

**Icon note.** `WorkspaceMenu` already imports lucide `LogOut` for *Leave
workspace*. The two never render on the same screen — the menu is in-shell, the
button is standalone-only — so no icon changes, but a future sign-out inside the
shell would have to resolve the collision.

## Owner gate on workspace creation

### Server

`POST /api/v1/workspaces` moves from the `owner` group to the `ownerVerified`
group in `registerWorkspacesAPI` — the same middleware already guarding
`/admin/*`, `/backup/*` and `DELETE /workspaces/:id`. It then answers
`403 owner_verification_required` until the owner has confirmed their password
within `ownerVerifyTTL` (15 minutes).

Nothing else moves. `GET /workspaces` and `POST /workspaces/:id/enter` stay
open, as `TestOwnerGateLeavesEscapeHatchesOpen` requires — gating those would
make the gate inescapable.

Creating a workspace from Owner settings is unaffected in practice: reaching
that section already required the same confirmation, so the stamp is fresh.

### Client

`CreateWorkspaceDialog` is one component rendered by both `Workspaces.tsx` and
`WorkspaceMenu.tsx`, so both entry points named in the request are fixed at
once.

The flow is **attempt → catch the gate → prompt → retry**: submit the name;
if the server answers `owner_verification_required`, the dialog swaps its body
for an owner-password step while keeping the typed name; on `POST
/auth/owner-verify` succeeding it retries the create automatically and proceeds
exactly as before.

This is extracted as a `useOwnerVerify()` hook (`lib/ownerVerify.ts`) so
workspace delete — already gated server-side, with no client affordance for the
403 — can adopt it later without a second implementation.

**Why not read `session.owner_verified` and show the field conditionally.**
`OwnerGate` already states the rule for this codebase: "the server owns expiry,
and a timer here could only disagree with it." A client-side check can be stale
(the cached session predates the lapse) or race the TTL mid-dialog, and it needs
the 403 fallback regardless — so it adds a disagreeing clock and removes
nothing. Rejected for the same reason a password field posted on every create
was: that would grow a second authentication path on the create endpoint beside
`owner-verify`.

### Accepted friction: first run

On a fresh install the owner logs in, has no workspace, is routed to
`/workspaces`, clicks Create, and is asked for the password they typed seconds
earlier.

This is accepted as-is. The alternative — stamping `setOwnerVerified` during
`apiLogin` — would also silently un-gate Owner settings for 15 minutes after
every login, changing an existing security gate as a side effect of a UI
nicety. A once-per-install extra prompt is the cheaper cost.

## Testing

**Go.**

- `TestEveryInstallLevelRouteIsGated` enumerates install-level routes with a
  hand-maintained predicate; `POST /api/v1/workspaces` is added to it. Without
  this the new gate ships with no coverage, which is precisely the failure the
  test was written to prevent.
- Roughly six tests create a workspace over the API with a plain login cookie
  (`api_workspaces_test.go`, `api_identity_test.go`, `api_settings_test.go`).
  They move to the existing `bootstrapLoginAndVerify` / `verifyOwnerCookies`
  helpers, which exist for exactly this.
- New: create answers 403 `owner_verification_required` before verification and
  200 after.

**SPA.**

- `SignOutButton` renders on `/workspaces` and on the lock screen, and posts to
  `/api/v1/auth/logout` only after the confirm is accepted.
- `CreateWorkspaceDialog` shows the owner-password step when create 403s with
  the gate code, and retries the create after a successful verify — carrying the
  already-typed name through.

## Not built

- Sign out inside the shell (rail, workspace menu, or a fixed top-right button).
  Out of scope by decision; the collision analysis above is the record of why a
  fixed top-right button is not a small change.
- Adopting `useOwnerVerify()` for workspace delete. The hook is shaped for it;
  wiring it is a separate change.
- Any change to `ownerVerifyTTL`, to the lock mechanism, or to what "Leave
  workspace" does.
