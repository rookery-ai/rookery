# OAuth credential field labels

**Date:** 2026-08-04
**Status:** Design approved, ready for implementation planning

## Problem

The service connect wizard hardcodes **Client ID** and **Client secret** for every
OAuth provider (`web/ui/src/pages/connections/ServiceWizard.tsx:507`). Thirteen of
the twenty-one OAuth providers call those fields something else in their own
developer console. A user following Meta's setup steps is looking at a page
labelled *App ID* and *App Secret* while our form asks for a client id, and has to
guess that they are the same thing.

This is the same class of bug fixed for AdGuard Home in PR #80, where the paste form
said "AdGuard Home API key" for a service that has no API key and reuses the web-UI
password. That fix added `key_label` / `key_hint` to the `api_key` path. The OAuth
path never got the equivalent.

### What is already correct

The API-key half needs no work and is explicitly out of scope:

- All 48 `api_key` providers declare `key_label`; `TestAuthConfigIsCoherent` fails
  the build if one is missing.
- The username half of the HTTP Basic providers — AdGuard, Nextcloud, Zendesk,
  Twilio, Last.fm, Steam, Trello — is per-field labelled through
  `connect_inputs[].label`.

Chat-platform connectors (Telegram, Discord, Slack) are a different framework
(`gateway.CredSpec`) and are already per-field labelled.

## Approach

Add an optional `oauth_creds` block to the provider YAML naming what the provider's
console actually shows, resolve it through `auth_parent`, and render it in the
wizard with the current strings as the fallback.

Two alternatives were considered and rejected. **Flat top-level keys** mirroring
`key_label` are a smaller diff but leave four loose keys that read as if they apply
to every auth kind. **A unified `credential_fields:` list** covering every auth
kind (the shape `gateway.CredSpec` uses) is more uniform long-term but rewrites the
api_key path that already works across 48 providers and touches the connect
endpoints — a refactor wearing a bug fix's clothes. `session_exchange` (Bluesky) was
the moment that might have justified it, and it fit the existing api_key form fine.

## Design

### Data model

A new optional block on `connectors.Provider`, deliberately **outside** `auth:`:
most OAuth providers (Google, GitHub, Notion) have no `auth:` block at all, so
nesting there would force one into files that do not otherwise need it.

```yaml
# internal/connectors/providers/facebook.yaml
oauth_creds:
  id_label: "App ID"
  secret_label: "App Secret"
```

```go
// OAuthCreds names the two fields the provider's own developer console shows, so
// the connect form asks for what the user is actually looking at. Any empty field
// falls back to "Client ID"/"Client secret", which is correct for the majority.
type OAuthCreds struct {
    IDLabel     string `yaml:"id_label"`
    IDHint      string `yaml:"id_hint"`
    SecretLabel string `yaml:"secret_label"`
    SecretHint  string `yaml:"secret_hint"`
}
```

All four fields are optional and default **independently**. Outlook declares an
`id_label` and a `secret_hint` while leaving `secret_label` to the default; X
declares only hints. That is not an edge case — see the X row below.

A provider with `auth_parent` set must **not** declare `oauth_creds`: it has no
OAuth app of its own, the block would never be read, and its presence would state
something false about where the credentials go. `TestOAuthCredLabelsAreNonBlank`
rejects it.

### Resolution through `auth_parent`

The labels describe an **OAuth app**, and a child provider does not have one.
Twelve providers reuse a parent's app: the nine `google_*`, `youtube`,
`linkedin_ads`, and `teams`.

The labels therefore resolve on the call that already exists for exactly this
purpose. `web/api_services.go` computes:

```go
credsProvider := provider
if op, ok := s.connectors.OAuthProvider(provider); ok && op.Name != provider {
    credsProvider = op.Name
}
```

`Registry.OAuthProvider` returns the parent `Provider` when `auth_parent` is set,
and that resolved provider is the one whose `oauth_creds` the DTO must read.
Reading the child's own record is the failure mode to guard against: `teams` would
print "Client ID" over a form feeding a Microsoft registration whose console says
"Application (client) ID".

The DTO carries the block nested, matching the YAML:

```go
type apiOAuthCreds struct {
    IDLabel     string `json:"id_label"`
    IDHint      string `json:"id_hint"`
    SecretLabel string `json:"secret_label"`
    SecretHint  string `json:"secret_hint"`
}
// on apiServiceProvider:
OAuthCreds apiOAuthCreds `json:"oauth_creds"`
```

It must be a **value struct, not a pointer**. `web/api_services_test.go:35` asserts
that no field on this payload serializes as `null` — the convention `connections`,
`connect_inputs` and `setup_steps` already follow. A pointer would emit
`"oauth_creds":null` for every api_key and keyless provider and break that test; a
value emits `{}`.

### Rendering

`ServiceWizard.tsx` replaces the two hardcoded strings with the same `||` fallback
the API-key branch already uses, and gains an optional hint line under each field
mirroring `key_hint`:

```tsx
<Label htmlFor="svc-client-id">
  {provider.oauth_creds?.id_label || "Client ID"}
</Label>
```

Presentation only. `client_id` and `client_secret` remain the wire and storage
names; there is no migration and no change to the connect or callback flow.

## Per-provider labels

Labels are taken from each provider's own `setup_steps`, which were written when the
connector was added and already name the console field verbatim (`dropbox.yaml`:
"Copy the App key (client id) and App secret (client secret)"). Where a provider is
marked `unverified: true`, that marker continues to carry the uncertainty.

| Provider | `id_label` | `secret_label` | Hints |
|---|---|---|---|
| dropbox | App key | App secret | |
| facebook | App ID | App Secret | |
| instagram | App ID | App Secret | |
| meta_ads | App ID | App Secret | |
| threads | Threads App ID | Threads App Secret | id: distinct from the Facebook app's |
| pinterest | App ID | App secret key | |
| salesforce | Consumer Key | Consumer Secret | |
| tiktok | Client key | Client secret | |
| mastodon | Client key | Client secret | id: registered on your own instance |
| outlook | Application (client) ID | *(default)* | secret: the **Value**, not the Secret ID |
| reddit | client ID | secret | id: shown under the app name |
| notion | OAuth client ID | OAuth client secret | |
| x | *(default)* | *(default)* | both: the **OAuth 2.0** pair, not API Key / API Key Secret |

The remaining eight OAuth providers declare no `oauth_creds` because their console
genuinely says Client ID / Client secret: **github, slack, google, spotify, strava,
jira, linkedin, oura**. Children inherit from their parent.

X is why the four fields default independently. Its developer portal shows *two*
credential pairs — an OAuth 1.0a "API Key / API Key Secret" and an OAuth 2.0
"Client ID / Client Secret". The labels are already right; the disambiguating hint
is the entire value, and it is precisely the confusion this work was reported for.

### Label verification

Every label in the table above was checked mechanically against its provider's
`setup_steps` — the same case-insensitive substring assertion
`TestOAuthCredLabelsMatchSetupSteps` will make. 22 of 23 declared labels pass
unchanged. Two rows need attention:

**Notion fails and needs a prose edit.** Its steps read "Copy the OAuth client ID
and client secret and paste them below", so `secret_label: "OAuth client secret"`
does not appear as a contiguous substring. The step becomes "…and OAuth client
secret" in the same commit. Renaming a field label and leaving the step prose behind
is the internal-consistency failure mode this whole change exists to remove, so the
test forcing the edit is working as intended.

**Reddit's assertion is vacuous.** `secret_label: "secret"` is a four-letter
substring that matches almost any prose, so the test proves nothing for that row —
it is pinned by `TestDivergentOAuthLabelsStayDeclared` instead. Reddit's console
genuinely shows the value unlabelled beside the word "secret", so the label is
right; the weakness is in what the test can assert about it, and it is recorded here
rather than papered over.

### Consequent prose edit

Dropbox's steps read "Copy the App key (client id) and App secret (client secret)".
The parentheses exist only because the form said "Client ID" — once the field itself
says "App key" they read as though there are two separate values to find, so they
come out in the same commit.

## Testing

| Test | Package | What it pins |
|---|---|---|
| `TestOAuthCredLabelsMatchSetupSteps` | `connectors` | Every declared label appears (case-insensitively) in that provider's own `setup_steps`. The labels are derived from that prose, so this ties them to their source and catches card-says-X / step-says-Y drift. |
| `TestDivergentOAuthLabelsStayDeclared` | `connectors` | The thirteen providers above, pinned by name against their full expected `OAuthCreds` value — labels *and* hints, and empty where the table says *(default)*. Deleting Meta's "App ID" or X's disambiguating hint fails CI instead of silently reverting to the default. |
| `TestOAuthCredLabelsAreNonBlank` | `connectors` | A declared field is not whitespace-only, and no `auth_parent` child declares the block at all. Deliberately does **not** require all 21 root providers to declare a label — the default is correct for eight, and a blanket requirement would invite wrong entries. |
| `TestChildProvidersInheritParentCredLabels` | `web` | The services DTO for `teams` carries Outlook's "Application (client) ID"; `google_calendar` carries Google's (empty → default). This is the one place a plausible-looking implementation is silently wrong. |
| `TestAuthConfigIsCoherent` (extended) | `connectors` | A new `session_exchange` case. Bluesky pastes a credential and renders `key_label`, but the existing switch covers only `IsKeyless()` / `IsAPIKey()`, so a blank label there passes today. |
| `ServiceWizard.test.tsx` (extended) | `web/ui` | One assertion for the fallback path rendering "Client ID", one for a declared label overriding it. |

The label-vs-`setup_steps` test is the load-bearing one: it is what makes "derived
from the repo's own evidence" a checkable property rather than an authoring
convention.

### Existing tests over the services payload

Adding a field to `apiServiceProvider` widens a payload four test files already
assert against, so they are touched files rather than surprises:
`web/api_services_test.go` (the no-`null` assertion above),
`web/api_services_keyless_test.go`, `web/api_services_preflight_test.go`, and
`web/ui/src/lib/connections.test.ts`. All assert presence rather than exact shape,
so a value-struct addition is compatible with each; the one to keep in view is the
`:null` check.

## Out of scope

- **The 48 `api_key` labels.** Already correct and already test-enforced.
- **Chat-platform connectors.** Separate `gateway.CredSpec` framework, already
  per-field labelled.
- **Explaining shared parent apps.** Connecting Google Calendar edits the same
  OAuth app as Gmail, and the creds form says nothing about it. A real gap, but it
  is an explanation, not a field name — its own change.
- **Wire and storage names.** `client_id` / `client_secret` are unchanged. No
  migration.
- **Live-console verification.** Labels come from the in-repo `setup_steps`.
  Verifying all ~20 developer consoles against current documentation was considered
  and rejected as a multi-hour third-party pass for a presentation fix; the
  `unverified: true` marker continues to carry the uncertainty where it exists.
