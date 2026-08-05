# Aliased-provider OAuth: redirect URI, existing-app guidance, multi-account

**Date:** 2026-08-05
**Status:** Design approved, implementation not started

## Summary

Thirteen connector providers reuse another provider's OAuth application through
`auth_parent`. Every one of them currently sends a redirect URI that the OAuth
application does not have registered, so connecting any of them fails at the
provider's consent screen. The failure is invisible in the UI because the
setup panel that would show the redirect URI is skipped for exactly these
providers.

This spec fixes the redirect URI, replaces the missing guidance with an
explicit "update your existing application" panel, and makes connecting a
second account to one OAuth app both possible and safe.

## Problem

### 1. Aliased children send an unregistered redirect URI

`web/handlers_services.go:85` and `web/api_services.go:247` both build the
redirect URI from the **child** provider name:

```go
return s.publicBaseURL(c) + "/dashboard/connectors/services/callback/" + provider
```

Thirteen providers declare `auth_parent`:

| Parent | Children |
|---|---|
| `google` | `youtube`, `google_drive`, `google_tasks`, `google_ads`, `google_health`, `google_analytics`, `google_searchconsole`, `google_sheets`, `google_calendar`, `google_docs`, `google_adsense` |
| `outlook` | `teams` |
| `linkedin` | `linkedin_ads` |

A user who sets up Google (Gmail) registers
`…/callback/google` in the Google Cloud console. Connecting Google Calendar
then sends `…/callback/google_calendar`, which is not registered, and Google
rejects it with `redirect_uri_mismatch`.

The rejection happens at the **consent screen**, before an authorization code
is ever issued. `explainOAuthError` (`web/oauth_errors.go:18`) only runs on the
token *exchange*, so its redirect-mismatch message — the one place the product
tells the user what to register — is unreachable for this failure. The user
sees Google's own error page instead.

### 2. The guidance that would prevent it never renders

`web/ui/src/pages/connections/ServiceWizard.tsx:196`:

```tsx
const [view, setView] = useState<"creds" | "connect">(
  provider.has_creds ? "connect" : "creds",
);
```

`has_creds` is computed against the **resolved parent** (`api_services.go:225-241,272`),
so every aliased child reports `has_creds: true` the moment its parent is
configured. The wizard therefore opens directly on the Connect step.

Precisely what that hides: `setup_url` and `setup_steps` live inside the
`view === "creds"` branch and are skipped, so the child's own instruction —
"In Google Cloud Console, also enable the Google Calendar API" — is never
rendered. The redirect URI block is **not** hidden; it sits outside the branch
(`ServiceWizard.tsx:313-328`) and shows on both steps. The URI was always
visible — it was simply the wrong URI.

The two failures compound: the connect is broken, and the per-service setup
instruction that would at least hint at the cause is the part that is skipped.

### 3. Connecting a second account is unsafe

The schema supports multiple accounts per provider —
`migrations/005_connectors.up.sql:29` is `UNIQUE(workspace_id, provider, account_label)`
— and `connectors.ToolDefs` already disambiguates tool names per account label.
Two things stop it working in practice:

- `google.yaml`'s `authorize_extra` sets `prompt: consent` with no
  `select_account`. Google reuses the already-signed-in account, so a second
  connect returns the *same* account rather than offering a chooser.
- `db.InsertServiceConnection` **upserts** on
  `(workspace_id, provider, account_label)` — pinned by
  `TestInsertServiceConnectionReconnectUpsertsPreservingID`. Connecting a
  genuinely different account under a label that is already in use silently
  overwrites the first connection's tokens. No error, no warning, and the
  original account's agents quietly start acting as the new account.

## Design

### Component 1 — Parent-scoped callback URI

The redirect URI becomes a property of the OAuth **application**, not of the
service being connected. One URI per OAuth app covers all of its children.

- A single resolver returns the OAuth-app owner for a provider name — the
  parent when `auth_parent` is set, the provider itself otherwise. This is
  `Registry.OAuthProvider(name).Name`, which already exists and is already used
  for credential lookup at `handlers_services.go:296`; the redirect URI simply
  starts using the same resolution.
- Both URI construction sites (`callbackURL`, and the `redirect_uri` field the
  services API reports to the SPA) build from the resolved owner.
- `handleOAuthCallback` treats its path parameter as the **auth** provider. The
  effective child provider comes from the signed state's `parts[1]` and
  continues to drive everything downstream: `ProviderByName`, scopes,
  `post_connect`, `InsertServiceConnection.Provider`, and the success redirect's
  `?connected=` value.

**Backward compatibility.** The identity check at `handlers_services.go:271`
currently requires `parts[1] == provider`. It becomes: accept the callback when
the path parameter equals **either** the state's provider (the legacy shape) or
the OAuth-app owner of the state's provider (the new shape). The route is
registered as `:provider`, so both paths keep matching. This covers states
already in flight across a deploy, bounded by the existing 10-minute state TTL.

The pinned-redirect-URI logic at `handlers_services.go:303-320` is unchanged in
behaviour: the URI recorded in the state at consent time is still used verbatim
for the exchange, and a divergence is still logged rather than rejected.

**Who this changes.** A user who has already configured Google keeps working
and gains working children with no console visit. The one user who could
regress is someone who reached the creds step from a *child* card, saw the
child's redirect URI, and registered only that URI. Component 2 addresses them
directly: the URI is now always visible in the wizard, and it is now the
parent's.

### Component 2 — "Update your existing application" guidance

The services API gains, per provider:

- `setup_mode`: `"create"` when `has_creds` is false, `"update"` when true.
- the resolved OAuth-app owner's name and display label, so the SPA can name
  the application the user must edit ("your Google (Gmail) app") rather than
  the service they clicked.

`ServiceWizard` keeps opening on the Connect step when `has_creds` is true —
forcing credential re-entry to show one paragraph would be worse — but for
in `update` mode it renders a guidance section above the Connect control:

- Headed as an instruction to update the existing application, not to create
  one, naming the **parent** app ("Update your existing Google (Gmail)
  application") — a child has no application of its own in the console.
- It renders the provider's own `setup_steps` through the existing `SetupStep`
  component, which for children already carry the per-service instruction ("In
  Google Cloud Console, also enable the Google Calendar API").
- It tells the user to confirm the redirect URI is listed under the existing
  application's authorized redirect URIs. The URI block itself is **not**
  duplicated here — it already renders above, on both steps.

A "Re-enter credentials" control returns the wizard to the `creds` view, so
`has_creds` stops being a one-way door — today there is no path back to fix a
wrong client secret from this screen.

No new provider YAML field is required. The child `setup_steps` already exist;
what was missing was rendering them at all, plus the structural redirect-URI
block, which is derived from the API's `redirect_uri`.

### Component 3 — Multiple accounts on one OAuth application

**Consent chooser.** `google.yaml` sets `prompt: "select_account consent"` —
space-separated values are valid per OpenID Connect Core, and the existing
`consent` behaviour (which is what forces a refresh token to be issued) is
preserved. All eleven Google children inherit this through parent resolution.
`outlook.yaml` gains `prompt: select_account` the same way.

**Duplicate rules.** Before inserting, `handleOAuthCallback` lists the
workspace's existing connections for the effective provider (via
`db.ListServiceConnections`, filtered by provider) and compares against the
`AccountIdentity` just returned by `FetchIdentity`:

| Existing row | Incoming | Outcome |
|---|---|---|
| same label, same identity | reconnect | Upsert, as today — refreshes tokens, preserves the row ID |
| different label, same identity | already connected | **No write occurs.** Redirect with "You have already connected `<identity>` for `<service>` as `<label>` — reconnect under that name to refresh it." Creating a second row for one account would produce two tool-name variants pointing at the same mailbox. |
| same label, different identity | name collision | **Refuse.** Redirect with "`<label>` is already used by `<other identity>` — choose a different name." No write occurs. |
| no match | new account | Insert a new row |

The third row is the data-loss case the upsert currently causes silently. It is
the reason this component is in scope rather than deferred: the account chooser
alone would make the overwrite *easier* to trigger, not harder.

## Testing

**Go**

- An aliased child's reported and sent redirect URI is the parent's
  (`google_calendar` → `…/callback/google`).
- A callback arriving on the legacy child path with a child-provider state is
  still accepted.
- A callback whose state names a provider belonging to a different OAuth app is
  rejected.
- The three duplicate rules: same-identity-different-label creates no second
  row; same-label-different-identity is refused and writes nothing;
  same-label-same-identity still upserts preserving the row ID.
- A YAML assertion that `google` and `outlook` request the account chooser, so
  removing it fails the build rather than silently regressing multi-account.

**Vitest**

- With `has_creds: true`, the wizard renders the redirect URI and update-mode
  wording, and still opens on the Connect step.
- "Re-enter credentials" returns the wizard to the creds view.

## Out of scope

- Registering redirect URIs programmatically through Google's API. It needs
  administrative scopes well beyond what the connector requests, and would make
  the product a manager of the user's cloud project.
- Verified-application review and test-user management. Both are console-only
  operations; the spec's contribution is telling the user the 100-test-user cap
  exists before they hit it.
- The other two reported problems — chat-app slash-command menus, and agent
  designer reliability over chat — are separate specs.

## Risks

Changing the redirect URI the product sends is a live behavioural change for
anyone mid-setup. It is mitigated on both sides: the callback accepts the
legacy path shape for the state TTL window, and the URI is now always visible
in the wizard, so a mismatch becomes self-diagnosing instead of surfacing as an
opaque provider error page.
