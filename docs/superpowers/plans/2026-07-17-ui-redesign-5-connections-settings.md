# UI Redesign Sub-plan 5: Connections, Settings & Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The guided-connection experience (chat apps + 28 services with logo galleries and hold-your-hand wizards), the fully redesigned settings (providers-first coder config, owner sections absorbed), and the full-screen onboarding wizard — plus the SP4 carry-over batch.

**Architecture:** Frontend on the SP1 endpoints, with three small deliberate backend changes: (1) `saveWorkspaceCoderCore` falls back to an existing `CODER_KEY_<PROVIDER>` secret when no key is pasted (enables providers-first), (2) services OAuth redirects land on `/app/connections` instead of the template page, (3) the SP4 carry-over fixes (`already_running` status, `IsCoreSkill` case-insensitivity, skill-failure History note). Brand logos via the `simple-icons` npm package with a letter-badge fallback. Spec: `docs/superpowers/specs/2026-07-16-ui-redesign-design.md` §8, §9 + the validated `connections.html`/`home-search-settings.html` mockups' language.

**Tech Stack:** simple-icons (per-icon imports, tree-shaken), existing shell/query/designer infra. No new heavy deps.

## Global Constraints

- Branch `ui-redesign`. Suites green at every commit: `go test ./... -count=1 -timeout 120s`, `cd web/ui && npm test -- --run`, `npm run build`.
- Backend DTO contracts (SP1, verified — grep the named file before writing types; adjust types not tests):
  - `GET /api/v1/connectors` (web/api_connectors.go) → `{platforms:[{platform,label,blurb,setup_steps,fields:[{name,label,secret?}],connected,identity}]}`; `POST /api/v1/connectors {platform, values}` → `{ok,identity,warning?}` / 400 `invalid_credentials`; `DELETE /:platform`; `POST /:platform/test` → `{ok, identity|error}`.
  - `GET /api/v1/services` (web/api_services.go) → `{providers:[{name,label,kind,setup_url,setup_steps,has_creds,connect_inputs:[{key,label,hint,required}],connections:[{id,label,identity,status}]}]}`; `POST /:provider/creds {client_id,client_secret}`; `POST /:provider/connect {label?}` → `{redirect_url}`; `POST /:provider/apikey {key, inputs}`; `DELETE /api/v1/services/:id`.
  - `GET /api/v1/settings` (web/api_settings.go) → `{profile, workspace, coder, detected_coders, api_providers, coder_catalog:[{name,base,model,docs,requiresKey,custom,hasKey}], secret_names}`; PUTs profile/workspace/coder/master-password; `POST /api/v1/settings/coder/test` → `{ok, reply|error}`.
  - `GET /api/v1/setup` + `POST /api/v1/setup` (web/api_settings.go apiSetup* — grep request fields per step).
  - Owner: workspaces CRUD/permissions (SP1 Task 3), `GET/PUT /api/v1/admin/settings`, `GET /api/v1/admin/overview`, `GET /api/v1/admin/audit`.
- **Never render secret VALUES** — keys/tokens/client_secrets are write-only fields everywhere.
- Key-secret naming convention (binding): a provider key saved from the AI-Providers card is `POST /api/v1/secrets {name: "CODER_KEY_" + provider.toUpperCase(), value}` — matches `coder.CoderKeySecretName` so `coder_catalog[].hasKey` reflects it.
- Slide-over panels use the NEW `<PanelBody>` wrapper (Task 1) — the Sheet well is unpadded by design.
- ProviderLogo: one component used EVERYWHERE a provider/platform/AI-vendor is named (spec §5). simple-icons where available; deterministic letter+color fallback otherwise; never a broken image.
- react-router v8; `npx shadcn@3 add` for any new shadcn component; lockfile committed.
- Deliberate deferrals: ZIP skill import (SP6), drag-move (SP6), template deletion + `/` cutover (SP6).

---

### Task 1: SP4 carry-over batch (backend + frontend)

**Files:** Modify `web/api_agents.go` (+test), `internal/skilllibrary/library.go` (+test), `internal/skilldesigner/flow.go` (+test), `web/handlers_misc.go` (`saveWorkspaceCoderCore`) (+test in web), `web/ui/src/pages/agents/RunPanel.tsx` (+tests), `web/ui/src/components/designer/DesignerSurface.tsx`, `web/ui/src/components/shell/PanelBody.tsx` (new), `web/ui/src/components/chat/GlobalChatButton.tsx` (adopt PanelBody? NO — global chat stays unpadded full-height; PanelBody is for form/content panels).

1. `apiRunAgent`: use `startManualRun`'s bool — already-running → 202 `{"status":"already_running"}` (202 keeps client simple); RunPanel: on `already_running`, show "A run is already in progress" note + attach the SSE (don't treat as error). Go test + frontend test.
2. `skilllibrary.IsCoreSkill`: case-insensitive (normalize slug before the existing checks). Go test (`IsCoreSkill("PDF")` true). Verify all guard call-sites still pass their tests.
3. `skilldesigner` `markGenerationFailed`: append a failure note to session History (mirror agentdesigner's `recordGenerationFailure` message shape) so rebuilds aren't context-blind. Extend the existing generation_failed test.
4. `saveWorkspaceCoderCore`: when `pastedKey == ""` and `w.CoderAPIKeySecret == ""`, check the workspace's secret names for `coder.CoderKeySecretName(provider)` — if present, use it (no write) instead of erroring. The core needs access to secret names — pass them in or query inside (match the handler's existing style). Go test: pre-save `CODER_KEY_OPENROUTER` via secrets svc → PUT coder {kind:api, provider:openrouter, model:x, no key} → 200 and `coder_api_key_secret` set.
5. Frontend: `unmountedRef` guards in RunPanel.handleRun and DesignerSurface.handleSend/ensureSSE post-await attach paths; RunPanel 503 + generic-error tests; `<PanelBody>` (`p-4 space-y-4 overflow-y-auto` wrapper + doc comment "standard slide-over padding — chat panels opt out by not using it").

- [ ] TDD each numbered item (backend first, then frontend); commits may be split `fix(api): ...` / `fix(ui): ...`; suites green per commit.

---

### Task 2: ProviderLogo + connections data layer

**Files:** Create `web/ui/src/components/brand/ProviderLogo.tsx` (+`logos.ts` map, +test), `web/ui/src/lib/connections.ts` (+test).

- `npm install simple-icons`. `logos.ts`: curated map `slug → { path: string; hex: string; title: string }` importing per-icon (`import { siTelegram, siDiscord, siSlack, siGithub, siNotion, siOpenai, ... } from "simple-icons"`) for: chat platforms (telegram/discord/slack), the 28 service providers (grep `internal/connectors/providers/*.yaml` filenames for slugs; google-family → siGoogle/siGmail/siGoogledrive/siGooglesheets/siGoogledocs where they exist), AI providers (openai/anthropic/openrouter/ollama/deepseek/mistral/perplexity + others present in simple-icons). **Icons that don't exist in the installed simple-icons version are OMITTED from the map** (the fallback handles them) — check availability by import, note the missing ones in the report.
- `<ProviderLogo name size?>`: map hit → inline `<svg>` with the brand path on a rounded tile (white/brand-colored per contrast); miss → rounded tile with the capitalized initial + deterministic bg color (hash of name over the token status palette). Test: known slug renders svg path; unknown renders initial; same name → same color.
- `lib/connections.ts`: hooks per Global Constraints DTOs — `useConnectors ["connectors"]`, `useSaveConnector`, `useDeleteConnector`, `useTestConnector`, `useServices ["services"]`, `useSaveProviderCreds`, `useConnectService` (returns redirect_url — CALLER navigates), `useConnectAPIKey`, `useDeleteServiceConnection`; invalidations on all mutations. Tests: URL/body assertions per hook.

- [ ] TDD; commit `feat(ui): ProviderLogo (simple-icons + fallback) + connections data layer`.

---

### Task 3: Connections page + galleries

**Files:** Create `web/ui/src/pages/connections/ConnectionsPage.tsx` (+test); Modify router (swap /connections).

- ContextPane (per the validated mockup): "Connections" title; search box (filters BOTH galleries by name/label, 150ms debounce, client-side); two category rows ("💬 Chat apps · n" / "🧩 Services · n of 28") that scroll-to/filter the section; explainer card ("Chat apps are where you talk to your assistant… Services are the accounts your agents can act on…" — mockup copy).
- Content: **Chat apps** section — one card per platform (ProviderLogo, label, connected → green dot + identity, else "Not connected"); card button Connect/Manage → opens Task 4's wizard in the slide-over. **Services** section — grid of provider tiles (ProviderLogo, label, `● n account(s)` green when connections non-empty — needs-reauth status shows amber "reconnect" hint; else "Connect"); tile click → Task 5's wizard. Section subtitles per mockup ("Talk to your workspace from the messenger you already use." / "Give your agents superpowers…").
- Tests: both sections render from mocked fetches; search filters; wizard-open spies fire with the platform/provider.

- [ ] TDD; commit `feat(ui): connections page — chat-app + service galleries with search`.

---

### Task 4: Chat-app connect wizard

**Files:** Create `web/ui/src/pages/connections/ChatAppWizard.tsx` (+test); Modify ConnectionsPage (wire).

- Slide-over (`useSlideOver().open(<ChatAppWizard platform=…/>, {title: "Connect " + label})`) with `<PanelBody>`. Steps (state-machine, chips per the mockup: 1 Setup — 2 Credentials — 3 Test):
  1. **Setup**: `setup_steps` rendered as numbered cards (linkify bare URLs); "Next".
  2. **Credentials**: `fields` → labeled inputs (`secret` fields `type=password`); Save → `useSaveConnector` → 400 `invalid_credentials` message inline; success (+`warning` shown amber if present) → step 3.
  3. **Test**: auto-fires `useTestConnector` on entry; ok → green "Connected as <identity> ✓" + Done (closes, invalidates); fail → error + Retry.
- Connected platforms open in a **Manage** variant: identity, Test button, Disconnect (confirm → DELETE → close).
- Tests: steps advance; save posts {platform, values}; invalid creds inline; test ok/fail branches; disconnect confirm.

- [ ] TDD; commit `feat(ui): chat-app connect wizard — CredSpec steps, credentials, live test`.

---

### Task 5: Services connect flows + SPA callback landing

**Files:** Create `web/ui/src/pages/connections/ServiceWizard.tsx` (+test); Modify `web/handlers_services.go` (redirect targets, +adjust any Go tests), ConnectionsPage (wire + landing banner).

- **Backend (deliberate, small):** every `c.Redirect(..., "/dashboard/connectors/services"...)` in web/handlers_services.go (success and `redirectWithError` paths — grep all sites) → `/app/connections` preserving/adding query params: success → `?connected=<provider>`, errors keep `?error=<msg>`. Template services page remains reachable directly; only the OAuth round-trip lands in the SPA. Update any Go test asserting the old target.
- `ServiceWizard` (slide-over + PanelBody), branching on `kind`:
  - **oauth**: has_creds=false → creds step (setup_url link + setup_steps cards + client_id/client_secret form → useSaveProviderCreds); then Connect step: optional label input + "Connect <label> →" button → `useConnectService` → `window.location.href = redirect_url` (full-page consent round-trip).
  - **api_key**: key input + `connect_inputs` fields (label/hint/required) → useConnectAPIKey → success closes + invalidates.
  - Connected accounts list at top (label, identity, status — `needs-reauth`/error statuses amber with a Reconnect button re-running the connect flow); Disconnect per account (confirm → DELETE).
- ConnectionsPage landing effect: on mount read `?connected`/`?error` params → success toast-banner ("<Provider> connected ✓") / error banner; clear params; invalidate ["services"].
- Tests: oauth-no-creds shows creds form and posts them; connect returns redirect_url and the navigate spy fires (stub location); api_key path posts {key, inputs}; landing banner for both params; disconnect.

- [ ] TDD both sides; commits `fix(api): services OAuth round-trip lands on /app/connections` + `feat(ui): service connect wizards (OAuth + API-key) with connected-account management`.

---

### Task 6: Settings — profile, workspace, appearance, master password

**Files:** Create `web/ui/src/pages/settings/SettingsPage.tsx`, `web/ui/src/lib/settings.ts` (+tests); Modify router (swap /settings).

- `lib/settings.ts`: `useSettings ["settings"]` + mutations (saveProfile, saveWorkspaceMeta, saveCoder, testCoder, changeMasterPassword) with URL/body tests; types mirror the GET DTO.
- SettingsPage: ContextPane section nav (Profile / Workspace / AI Providers / Coder / Master password / Appearance / Owner — mockup order; sections are anchors on one scrollable page OR a `?section=` param — pick one, note it). Sections this task: **Profile** (display_name/email/location/timezone/tone/language/notes form → PUT, saved toast), **Workspace** (name/about → PUT; session invalidate so the rail initial updates), **Appearance** (light/dark/system radio via useTheme — no backend), **Master password** (current/new/confirm → PUT; `wrong_master_password` 401 message inline; success clears fields). AI Providers/Coder/Owner render placeholder stubs for Task 7.
- Tests: profile round-trip; workspace save invalidates session; master-password wrong-current inline; appearance toggles the theme class.

- [ ] TDD; commit `feat(ui): settings — profile, workspace, appearance, master password`.

---

### Task 7: Settings — AI Providers, Coder (providers-first), Owner sections

**Files:** Create `web/ui/src/pages/settings/ProviderCards.tsx`, `web/ui/src/pages/settings/CoderSection.tsx`, `web/ui/src/pages/settings/OwnerSections.tsx` (+tests); Modify SettingsPage (mount), `web/ui/src/lib/settings.ts` (owner hooks: useAdminSettings/saveAdminSettings/useAuditLog/useWorkspacesAdmin: create/delete/permissions — reuse SP1 endpoints).

- **ProviderCards** (spec §9 / mockup): grid of `coder_catalog` entries — ProviderLogo, name, docs link, state: hasKey → "● Key saved" green; requiresKey=false → "No key needed"; else "＋ Add key". Card expand → paste-key form → `POST /api/v1/secrets {name: "CODER_KEY_"+name.toUpperCase(), value}` → invalidate ["settings"] (hasKey flips). Never display the value; "already set — paste to override" hint when hasKey.
- **CoderSection**: engine toggle (Local CLI / API). Local: detected_coders picker (name+bin; empty → "no coder CLIs found" note). API: provider select from catalog entries where `hasKey || !requiresKey` (others disabled with "add key above"), model input (placeholder from catalog `model`), Advanced collapsible base_url (required only when `custom`). Save → PUT settings/coder (NO key field — Task 1's fallback makes the saved card key work). **Test button** → POST coder/test → green "✓ <reply>" / red error, with a running spinner (can take ~1min — say so in the UI).
- **OwnerSections**: Workspaces (list cards + Create dialog (name/about) + Delete confirm + per-workspace permissions checkboxes → PUT), System settings (claude_bin/coder_timeout/agent_timeout/memory_mb form + sandbox/landlock readonly indicators → PUT), Audit log (last 100 rows table: time/workspace/action/target).
- Tests: key save posts the CONVENTION name; coder save body; disabled provider options; test button both branches; owner permissions PUT body; audit renders rows.

- [ ] TDD; commit `feat(ui): providers-first coder settings + owner sections`.

---

### Task 8: Onboarding wizard

**Files:** Create `web/ui/src/pages/setup/SetupWizard.tsx` (+test); Modify router (add /setup OUTSIDE AppShell, guarded: session must be authed; needs_setup workspace required else → /), `web/ui/src/router.tsx` RequireAuth (needs_setup → `/setup` replacing the /workspaces?setup=<id> target), `web/ui/src/pages/Workspaces.tsx` (drop the classic-UI note; needs_setup entries now route to /setup after enter).

- GET /api/v1/setup → current step; full-screen centered card with a 5-step progress header (Basics → Master password → Coder → Profile → Chat app). Per step, POST /api/v1/setup with the step's fields (grep web/api_settings.go apiSetup* for exact request keys — mirror them): basics (name/about), master password (+confirm; explain what it protects), coder (REUSE ProviderCards + CoderSection components in wizard mode — a `compact` prop if needed; test must pass before Next is enabled? NO — offer "Test" but Next only requires a saved config; keep friction low), profile (display_name/timezone/etc — skippable), connector (REUSE the ChatAppWizard credentials+test steps inline for telegram/discord/slack OR "Skip for now").
- Done state: "🎉 You're set up" + primary CTA "Create your first agent" → /agents/new + secondary "Explore the knowledge base" → /kb. Session invalidated so needs_setup clears.
- Tests: step progression posts correct bodies; skip paths; done CTA navigation; RequireAuth now redirects needs_setup → /setup.

- [ ] TDD; commit `feat(ui): full-screen onboarding wizard reusing provider/connector components`.

---

### Task 9: Close-out

- [ ] Full suites; e2e smoke (`make build`, serve :8090, /app 200, kill own PID); CLAUDE.md — note the services-callback redirect change in the routes section (one line); ledger close by controller. Commit `docs: SP5 close-out` (or none if only CLAUDE.md line → include it).

---

## Self-review notes (already applied)

- **Spec §8 fully covered** (galleries, logos, guided wizards with live test, per-provider setup guidance, disconnect/reauth); **§9 settings + owner absorption + onboarding** covered; providers-first is made REAL by the Task 1 backend fallback (without it the flow errors on first coder save — verified against PlanKeySecret's semantics).
- **The OAuth-callback redirect change** is the one place SP5 touches template-era behavior — deliberate, small, and the template page stays directly reachable.
- **Type consistency**: all hooks defined in lib/connections.ts (T2) and lib/settings.ts (T6, extended T7) before consumption; ProviderLogo (T2) consumed T3/T4/T5/T7/T8; PanelBody (T1) consumed T4/T5; wizard component reuse T8←T4/T7 via props not copies.
- Carry-over batch (T1) clears the entire SP4 ledger backlog before new surface work begins.
