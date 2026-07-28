# Connector actions browser

**Date:** 2026-07-28
**Status:** Approved

## Problem

A workspace owner can connect 46 services but has no way to learn what any of
them lets an agent *do*. The action manifests exist — ~214 curated actions across
the providers, each with a description written for an LLM — but they are visible
only to the model. The connections page shows a logo, a connection count, and a
connect form. Nothing answers "if I connect Gmail, what can my agents actually
do with it?", which is the question that decides whether connecting is worth the
OAuth setup at all.

## Solution

A read-only **actions view** inside the existing service slide-over, reached by a
button at the top of the panel. It lists every action the provider exposes:
plain-English description first, a capability badge (read / writes / posts
publicly), and — behind a per-row expand — the technical tool name and its
parameters.

## Placement decision

The actions list is a **third view of `ServiceWizard`**, not a second overlay.

`AppShell`'s slide-over is a single slot (`SlideOverState = { node, title } | null`);
calling `open()` replaces the panel rather than stacking on it. A second overlay
would therefore unmount `ServiceWizard`, discarding a half-typed client ID, client
secret, API key, label, or `connect_inputs` value, and closing it would return to
nothing rather than to the connect form. Making stacking work means converting the
shell's slide-over into a stack — work every other page inherits, for a feature
that does not need it.

`ServiceWizard` already carries a `view` state (`"creds" | "connect"`). This adds
`"actions"`. The user gets a separate screen that does not crowd the connect form,
which is the requirement; the shell is untouched. If a genuinely nested panel is
needed elsewhere later, the stack refactor can happen then.

## Backend

### New endpoint

`GET /api/v1/services/:provider/actions`

Registered in `registerServicesAPI` (`web/api_services.go`) on the same group as
its siblings, already guarded by `requireOwnerAPI` + `requireActiveWorkspaceAPI` +
`requireSetupCompleteAPI`.

```json
{
  "actions": [
    {
      "name": "github_search_issues",
      "description": "Search GitHub issues and pull requests across repos you can access…",
      "mutating": false,
      "public_write": false,
      "params": { "type": "object", "properties": {…}, "required": ["query"] }
    }
  ]
}
```

Data comes from `Registry.Actions(provider)`, which already exists. An unknown
provider returns 404, matching `apiDeleteServiceConnection`. A known provider with
no manifest returns `{"actions": []}` — an empty list, never `null`, so the client
never branches on nullness.

**`Action.Request` and `Action.ResponseExtract` are never serialized.** HTTP
method, URL templates, query/body construction, and the extraction path are
internal plumbing: noise to a reader, and a needless widening of what the API
discloses about how requests are built.

The response is derived per request from embedded data with no DB access, so no
caching layer is warranted.

### Why not inline it into `GET /api/v1/services`

That endpoint is fetched on every visit to the connections page. Adding ~214
actions with their compiled JSON schemas to that payload is a real size
regression on the page's critical path, paid by every user on every visit to
serve a panel most visits never open.

Instead `apiServiceProvider` gains one cheap field:

```go
ActionCount int `json:"action_count"`
```

— `len(registry.Actions(provider))`. That is enough for the button to render its
count and to hide itself entirely when a provider has no actions, with no second
fetch.

## Frontend

### Data hook

`useProviderActions(name)` in `web/ui/src/lib/connections.ts`, keyed
`["services", name, "actions"]` with `staleTime: Infinity`. The manifests are
compiled into the binary via `go:embed` and cannot change while the server runs,
so a fetched list never needs revalidating. It is fetched lazily — the hook is
only mounted by the actions view, so opening the wizard costs nothing.

### Components

**`ProviderActions.tsx`** (new file) renders the list. It lives in its own file
rather than inside `ServiceWizard.tsx`, which is already 359 lines and would lose
readability carrying a second substantial UI.

Props: `{ provider: ServiceProvider; onBack: () => void }`. `ProviderActions`
owns its header — the provider label, the action count, and the "← Back" control
that invokes `onBack`. `ServiceWizard` supplies the callback and decides where
Back lands; it does not render the control itself.

Each row:

- **Description** as the primary line, in the provider manifest's own words.
- **Capability badge**, right-aligned:
  - `mutating: false` → **read** (neutral tone)
  - `mutating: true, public_write: false` → **writes** (warn tone)
  - `public_write: true` → **posts publicly** (danger tone)
  The distinction is load-bearing and already modeled in the data: pausing an ad
  campaign is mutating but private and reversible, while a LinkedIn post is
  neither.
- **Expand control** revealing the tool name (monospace) and a parameter list —
  each parameter's name, type, `*` when required, and its schema `description`
  when the manifest supplies one. Rows are independently expandable and all start
  collapsed.

Loading and error states follow the page's existing `LoadingNote` / `ErrorNote`
conventions.

**`ServiceWizard.tsx`** changes:

- `view` widens to `"creds" | "connect" | "actions"`.
- A button above the connected-accounts block: `What can it do? · N actions`,
  rendered only when `provider.action_count > 0`.
- `view === "actions"` renders `<ProviderActions>` **instead of** the creds/connect
  body, passing an `onBack` that restores the previously active view.
- The previous view is remembered so Back lands where the user left. Because the
  actions view replaces only the rendered body and `ServiceWizard` itself stays
  mounted, all form state (`clientId`, `clientSecret`, `apiKey`, `label`, `inputs`)
  survives the round trip untouched.

### Availability

The actions view is shown for **unconnected** providers too. "What can this do for
me" is the strongest reason to connect in the first place, and the manifests are
static embedded data — nothing account-specific or credential-derived is exposed,
so there is nothing to gate on.

## Out of scope

- **No "try it" / "run this action" affordance.** The requirement is display-side.
  A browser-triggered execute path would mean mutating third-party calls
  originating from the UI — a materially larger security surface than a read-only
  reference pane, and a separate design if ever wanted.
- **No cross-provider action catalog.** A global searchable index of all ~214
  actions is a different feature with a different entry point.
- **No slide-over stacking refactor** (see Placement decision).
- **No changes to how agents discover or bind actions.** `connectors.ToolDefs` and
  the `agent_connections` binding path are untouched; this is a human-facing view
  of the same manifests.

## Platform parity

The standing rule is that the web UI and chat platforms offer the same experience,
and divergence is a bug. This feature does not breach it: it adds no capability an
agent gains or loses, and no `/connections` chat command exists to extend. It is a
reference display over data agents already receive as tool definitions. If a
`/connections` command is built later, an actions listing belongs in it.

## Testing

**Backend** (`web/api_services_test.go`, existing style):

- Unauthenticated request → 401.
- Unknown provider → 404.
- Known provider → 200, non-empty `actions`, each carrying name/description/flags.
- Response contains no request-template fields (asserted against the raw JSON, so
  the check survives a future struct change).
- `GET /api/v1/services` carries `action_count` matching the actions endpoint's
  length for the same provider.

**Route registration** (`web/api_parity_test.go`): the new route is added to the
`want` table, which is a merge gate — a route absent from it fails the build.

**Frontend** (`web/ui/src/pages/connections/ServiceWizard.test.tsx` and a new
`ProviderActions.test.tsx`):

- The button renders with the provider's action count.
- Clicking it shows the actions list.
- Back returns to the prior view with typed form input intact — the regression
  that motivated the placement decision.
- A provider with `action_count: 0` renders no button.
- Badges map correctly across all three states, including `public_write`.
- Expanding a row reveals its tool name and parameters; rows expand independently.

## Files touched

| File | Change |
|---|---|
| `web/api_services.go` | New `apiListProviderActions` handler + route; `ActionCount` on `apiServiceProvider` |
| `web/api_services_test.go` | Handler tests |
| `web/api_parity_test.go` | Route added to the `want` table |
| `web/ui/src/lib/connections.ts` | `useProviderActions` hook; `action_count` on the `ServiceProvider` type |
| `web/ui/src/pages/connections/ProviderActions.tsx` | New — the actions list |
| `web/ui/src/pages/connections/ProviderActions.test.tsx` | New — component tests |
| `web/ui/src/pages/connections/ServiceWizard.tsx` | `"actions"` view, entry button, back navigation |
| `web/ui/src/pages/connections/ServiceWizard.test.tsx` | Navigation + state-preservation tests |
