# Owner-page UX, brand icons, and shell polish — design

Date: 2026-07-24
Status: approved (user granted full autonomy; no approval gate)

## Scope

Nine independent items, grouped into four workstreams. They share no state, so they
can land in any order; the logo work is the only one with an external dependency.

| # | Item | Workstream |
|---|---|---|
| 1 | Delete dead workspace-permissions UI + backend | A. Dead settings |
| 9 | Owner "Claude binary" system settings — verify used, fix or remove | A. Dead settings |
| 2 | Real logos for every LLM/coder provider | B. Brand icons |
| 3 | Real logos for connections + chat apps (no placeholder tiles) | B. Brand icons |
| 4 | Substitute icons everywhere those entities are rendered | B. Brand icons |
| 5 | Timezone / location / language / tone → curated selects | C. Forms |
| 1b | Create-workspace asks "about" twice — ask name only, defer about to setup | C. Forms |
| 6 | Lock the UI (master password to unlock) without leaving the workspace | D. Shell |
| 7 | Richer default KB `README.md` | D. Shell |
| 8 | KB root folder order: system folders first, `notes` last | D. Shell |

## Findings that shape the design

Each of these was verified against the code, not assumed.

**Workspace permissions are dead.** `rbac.CanPerform` is the only consumer of the
`workspace_permissions` table and it has **zero callers** in the codebase. The four
permission constants (`bash`, `web-browser`, `system-tools`, `mcp-servers`) gate
nothing. The UI presents checkboxes that persist a value no runtime path reads.

**The owner's system settings are dead in the same way.** `loadAdminSettings` reads
`claude_bin` / `coder_timeout` / `agent_timeout` / `memory_mb` out of `system_settings`,
and the API writes them, but **nothing reads them back at runtime**:

- the coder binary comes from `cfg.Coder.ClaudeBin` (config.yaml), not the DB;
- the timeout comes from `cfg.Coder.Timeout`, and per-workspace from `workspaces.coder_timeout_s`;
- `sandbox.Spec.MemoryMB` is fed from `cfg.Sandbox.DefaultMemoryMB`, not from `memory_mb`.

So "make sure the defaults are reasonable" is moot — the values are inert. The two
genuinely-live fields in that panel are the read-only `Sandbox` and `Landlock` status
indicators.

**The setup wizard runs per workspace, not once per install.** `MarkWorkspaceSetupComplete`
is keyed by workspace id and `requireSetupCompleteAPI` gates on the *active* workspace.
Every newly created workspace therefore goes through the wizard, so moving the "about"
field out of the create dialog and into setup cannot strand a workspace without one.

**`ProviderLogo` renders monochrome single-path glyphs.** It takes `{path, hex, title}`
from `simple-icons` and draws one `<path>` in one fill colour on a brand-coloured tile.
Real brand SVGs are multi-path and multi-colour and cannot render through that contract.
This is a component rewrite, not a data swap.

**Ten brands are missing from `simple-icons` entirely** (removed upstream after trademark
takedowns, per the existing comment in `logos.ts`): slack, openai, outlook, teams,
salesforce, sendgrid, twilio, groq, xai, monday. These are exactly the tiles rendering
as coloured-initial placeholders today — the thing the user is reacting to.

**KB ordering is derived in the frontend, not the backend.** `sortNodes` in
`pages/kb/FileTree.tsx` implements the current rule (user content before system dirs).
`vault.go`'s sort is a plain dirs-then-alpha and is not where the root order comes from.
A per-directory user drag order is already persisted server-side and takes precedence
over the derived rule.

**"Browser" has no referent in the UI.** Grepping found no browser entity with placeholder
icons: the KB file tree already uses Lucide icons plus user-chosen emoji. The nearest
candidates are the removed `web-browser` rbac permission and the `playwright-browser`
core skill. Rather than guess, the acceptance criterion is stated in terms of outcome
(below) so every surface is covered regardless of which one the user meant.

## Workstream A — remove dead settings

Delete rather than wire up. Both features are UI that lies about having an effect, which
is the specific thing the user objected to; inventing real behaviour for them is scope
the user did not ask for.

**Permissions.** Remove the `PermissionsEditor` component and its expand affordance from
`OwnerSections.tsx`; the `useWorkspacePermissions` / `useSaveWorkspacePermissions` hooks;
the `GET`/`POST /api/v1/workspaces/:id/permissions` routes and handlers; the `rbac`
package; and `ListPermissions` / `GrantPermission` / `RevokePermission` from the db layer.
Drop the corresponding rows from `web/api_parity_test.go`'s `want` inventory — that table
is a merge gate, so leaving them listed would fail the build.

The `workspace_permissions` **table stays**. Dropping it needs a migration whose only
benefit is tidiness, and the consolidated `001_initial_schema` would then disagree with
the incremental chain. An unread table costs nothing; a bad migration on a live install
costs a restore.

**System settings.** Remove the four inert inputs and the save form, keeping the section
as a read-only status panel showing Sandbox and Landlock state (both genuinely live).
Remove `useSaveAdminSettings`, the `POST /api/v1/admin/settings` route, the writable
fields from `adminSettingsData`, and the matching parity-test row. `GET /api/v1/admin/settings`
stays, narrowed to the two status booleans.

## Workstream B — real brand logos

**Sourcing.** Two sources, both vendored into the repo as static SVG files under
`web/ui/src/assets/logos/`. Nothing is fetched at runtime and no CDN is referenced,
per the user's explicit instruction.

- `@lobehub/icons-static-svg` (npm, MIT, 903 icons) — the packaged form of the
  lobehub.com/icons set the user linked. Verified to cover every AI/LLM provider we
  support: openai, anthropic, mistral, deepseek, moonshot, perplexity, ollama,
  openrouter, gemini, groq, xai/grok, zhipu, github, notion.
- worldvectorlogo — for the business brands lobehub lacks. Direct guessed URLs 404,
  but the logo *page* resolves and exposes real CDN asset URLs; verified downloads for
  slack, microsoft-teams, salesforce, twilio, sendgrid, monday.
- `simple-icons` (already a dependency) — retained only as the source for brands both
  others lack, notably telegram and discord.

The package is used as a build-time source and the chosen SVGs are committed; it is
**not** added as a runtime dependency.

**Component contract.** `ProviderLogo` changes from "draw a path" to "render a vendored
asset". Assets are collected with Vite's `import.meta.glob('../assets/logos/*.svg', { eager: true, query: '?url' })`
into a slug→URL map, and rendered as an `<img>` inside the existing tile. This keeps
one lookup point, so item 4's "substitute everywhere" is satisfied by every existing
`ProviderLogo` call site automatically — the four known ones are ConnectionsPage,
ProviderCards, SetupWizard, and the component's own test.

Decision: **unify on full-colour vendored SVGs** rather than a hybrid that keeps
simple-icons glyphs where they exist. A grid mixing flat monochrome tiles with
full-colour logos looks less coherent than either alone, which is the complaint being
addressed. Where a vendored asset is a light-on-transparent mark, the tile keeps the
existing luminance check so the logo stays legible on both themes.

The coloured-initials fallback is **kept** as a safety net for slugs added later, but
no slug shipping today may reach it.

**Acceptance criterion (this is what "done" means for items 2–4).** Enumerate the full
slug set the app can render — 28 connector providers from `internal/connectors/providers/*.yaml`,
the 3 chat platforms from the gateway CredSpecs, and the ~16 coder providers from
`coder.APIProviders()` — and assert in a test that every one resolves to a vendored
asset and none falls through to the initials fallback. A slug set this size drifts
silently otherwise; the test is the only way "substituted everywhere" stays true.

## Workstream C — form inputs

**Curated selects.** One shared `<CuratedSelect>` component used by *both* the Profile
section of Settings and the setup wizard, satisfying the standing platform-parity rule.
Sources:

- **Timezone** — `Intl.supportedValuesOf('timeZone')` at runtime. No vendored data, and
  it stays current with the platform's tzdb.
- **Location** — a curated country list. "Location" as free text is ambiguous; country
  is the reading that makes a fixed list sensible, and it is what `profile.LoadLocation`
  needs for timezone-aware reminder parsing.
- **Language** — a curated list of common languages, value stored as the plain name
  already persisted today.
- **Tone** — a small fixed vocabulary (e.g. direct, friendly, formal, concise, warm).

**Preserving out-of-list values is required, not optional.** These fields hold free text
today and the owner's live install already has values in them. On load, a stored value
absent from the list is injected as an extra option and kept selected, so opening the
settings page can never silently blank a saved preference. This behaviour gets a test.

**Create-workspace flow.** `CreateWorkspaceDialog` drops its "About (optional)" input and
posts `{name}` only. Setup step 1 keeps both fields but its name input is pre-filled from
the workspace, so "about" is collected exactly once, at the point where the wizard already
explains why agents need it. The `POST /api/v1/workspaces` handler keeps accepting an
optional `about` — the field remains valid, it simply is not collected twice.

## Workstream D — shell and KB

**Lock.** Server-side, not a client overlay. A pure overlay is cleared by a reload and
leaves the API fully reachable, so "unlock requires the master password" would not be
true. Design:

- Session gains a `locked` boolean. `active_workspace_id` is **not** cleared — the
  requirement is explicitly to lock without leaving the workspace.
- `POST /api/v1/auth/lock` sets it; `POST /api/v1/auth/unlock` takes the master password,
  verifies it through the existing `verifyWorkspaceMasterPassword` (which compares against
  the system-key-encrypted stored copy), and clears the flag on success. Nothing about
  secret handling changes — the master password is not held in the session today and
  still is not.
- A middleware returns `423 Locked` for workspace- and owner-scoped API routes while the
  flag is set. `GET /api/v1/auth/session`, `unlock`, and `logout` stay reachable so the
  SPA can render the lock screen and the user can escape it.
- The SPA shows a full-screen lock view whenever the session reports `locked`, and the
  API client treats a 423 as "go to the lock screen". A lock rail item sits directly
  above Settings, per the user's placement.

Threat model is walk-up access to an unattended screen. The bar is "a reload stays locked
and the app is unusable until the master password is entered" — not resistance to an
attacker who already holds the session cookie.

**KB README.** Rewrite the `EnsureScaffold` template to describe each default folder,
what belongs in it, which are system-managed, and what the user can do in the KB
(create/edit/move/rename notes, drag to reorder, set folder emoji, search, upload and
convert files).

Scoped to **new vaults only.** `EnsureScaffold` already writes the README only when
absent, and that guard stays: the file is user-editable and overwriting an existing one
would destroy work. The owner's live vault keeps its current README.

**Folder order.** Change the derived rule in `sortNodes` to an explicit root-level rank:
`memory`, `agents`, `chats`, `skills` first in that order, then any other folders
alphabetically, then `notes` last among folders, then files. This applies to the vault
root only — nested directories keep dirs-then-alpha. The user's persisted drag order
still wins where it lists a name, so nobody's manual arrangement is disturbed.

This reverses the existing "user content first" rule from an earlier spec §6; the
comment block explaining that choice is updated rather than left contradicting the code.

## Testing

Per-workstream, matching the existing suites:

- **A** — parity test updated to the reduced route inventory; `OwnerSections.test.tsx`
  loses its permissions cases and asserts the settings panel is status-only.
- **B** — the slug-coverage test described above, plus `ProviderLogo.test.tsx` rewritten
  for the asset-rendering contract (renders an `img` for a known slug, initials for an
  unknown one).
- **C** — a test that an out-of-list stored value survives a load/save round trip; a test
  that the create dialog posts no `about`.
- **D** — Go tests for lock/unlock (423 while locked, session stays in the workspace,
  wrong password rejected) and for the README scaffold; a `sortNodes` test for the new
  root ordering and for drag-order precedence.

Full suite: `go test ./... -count=1` and `npm test` in `web/ui`, plus `make build` to
confirm the SPA still embeds.

## Out of scope

- Dropping the `workspace_permissions` table (migration risk without benefit).
- Any real permission system — the feature is removed, not reimplemented.
- Idle auto-lock on a timer. The user asked for a manual lock; a timeout is a separate
  preference with its own design questions.
- Rewriting existing vaults' README files.
