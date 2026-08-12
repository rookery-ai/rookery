# UI Redesign Sub-plan 6: Home, Search & Cutover (Finale) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the redesign: home dashboard (inbox + reminders + pulse), ⌘K global search, secrets page, the accumulated polish queues — then DELETE the template UI and serve the SPA at `/`.

**Architecture:** One small backend addition (`GET /api/v1/dashboard` mirroring the template dashboard's workspace-scoped data). The cutover is two-phase: first move the SPA to `/` (Vite base, router basename, root serving with `/app→/` redirects — **the OAuth callback path `/dashboard/connectors/services/callback/:provider` MUST survive**, it's a registered redirect URI in external OAuth apps), then delete templates/handlers/template-tests and finish docs. Spec: `docs/superpowers/specs/2026-07-16-ui-redesign-design.md` §9 (home/search), §12 (final parity), §11 (cutover).

**Tech Stack:** shadcn command (cmdk) via `npx shadcn@3 add command`; React.lazy route splitting; @tiptap/extension-image. No other new deps.

## Global Constraints

- Branch `ui-redesign`. Suites green at every commit (`go test ./... -count=1 -timeout 120s`, `cd web/ui && npm test -- --run`, `npm run build`).
- Endpoints (grep before typing): search `GET /api/v1/search?q=` → `{query, groups:[{kind, items:[{title,id?,path?,line?,snippet?,url?}]}]}`; inbox `GET /api/v1/inbox?limit&offset` → `{messages:[{id,source,agent_name,trigger,status,body,read,created_at}],unread}` + poll/read/read-all/delete; reminders CRUD + poll (web/api_home.go); secrets list/create/delete (web/api_secrets.go — delete takes {master_password}, 401 wrong_master_password).
- **The OAuth callback route and `callbackURL()` builder keep their exact paths through the cutover** — they're registered in external provider consoles. Template DELETION must not remove them; they move to a standalone registration.
- All arrays in new DTOs non-nil (`[]` not `null`) — per the null-array hotfix convention.
- Search-result `url` fields from the backend are template-era paths (`/kb?path=`, `/agents/:id`…) — the SPA maps them client-side (they coincide with SPA routes post-cutover; verify each kind).
- After cutover: SPA at `/`; `/app` and `/app/*` 301 to the `/`-equivalents (bookmarks keep working); `GET /login`+`/dashboard*`+`/admin*` template routes are GONE (the SPA owns those paths where they exist in its router; unknown paths fall back to SPA index).
- Deletion is COMPLETE: `web/templates/`, `web/static/` (if only template assets), all template handlers/render helpers/`parseTemplates`/`TemplateRenderer`/template smoke tests, `pageData` and friends. `go build` must prove nothing references them. The `saveConnector`/`testConnectorIdentity`/`buildConsentURL`/cores extracted during SP1-5 SURVIVE (API uses them) — deletion is of render/redirect wrappers only; verify each shared helper's remaining callers before removing anything.
- react-router v8; `npx shadcn@3 add`; lockfile committed.
- Post-redesign backlog (NOT this sub-plan — record in the final ledger): alignment-preserving table serializer, drag-to-move tree, ZIP skill import UI, injectable OAuthClient, Playwright automation.

---

### Task 1: Dashboard endpoint + Home page

**Files:** Create `web/api_dashboard.go` (+test), `web/ui/src/pages/home/HomePage.tsx`, `web/ui/src/lib/home.ts` (+tests); Modify `web/api.go` (register), `web/api_parity_test.go` (+row `GET /api/v1/dashboard`), router (swap `/` placeholder).

- **Backend**: `GET /api/v1/dashboard` (dash group) mirroring template `showDashboard` + more: `{display_name, agent_count, active_agent_count, recent_runs:[{id,agent_id,agent_name,status,trigger,started_at,finished_at}], upcoming:[{agent_id,agent_name,cron_expr,next_run_at}], has_connector:bool}` — recent runs via `db.RecentAgentRunsWithNames(u.ID, 10)`; upcoming via listing the workspace's enabled schedules (grep db for the schedules-by-workspace query; join agent names); display_name from profile fallback workspace name. All arrays non-nil. Go test: seeded runs/schedules shape assertion + empty-case `[]`.
- **Home page** (spec §9 / mockup): ContextPane = **Inbox** (unread count title, message cards: source icon (Bot/Bell), agent_name, body 2-line clamp, read-state dot, timeAgo; click → mark read + expand full body inline; "Mark all read"; delete per row) + **Reminders** (list w/ remind_at formatted + delete; inline add form: message + natural-language when → POST, 400 unparseable_time inline). Content = greeting ("Good morning/afternoon/evening, {display_name}" by local hour), 3 stat tiles (active agents / recent runs incl. failed count badge / connected chip-apps? use has_connector + services count from useServices — or keep tile 3 = "reminders due today"; pick from available data, note it), "Next up" card (upcoming schedules w/ next_run_at timeAgo) + "Needs attention" card (recent runs with status error → link to `/agents/:id` + "Ask the designer to fix it" link → `/agents/:id/edit`). Empty states everywhere.
- Tests: DTO hooks; greeting branches; inbox read/read-all/delete flows; reminder add unparseable inline; failed-run links.

- [ ] TDD; commit `feat(ui): home dashboard — inbox + reminders pane, stats, next-up (+ /api/v1/dashboard)`.

---

### Task 2: ⌘K command palette + rail inbox badge

**Files:** Create `web/ui/src/components/search/CommandPalette.tsx` (+test); Modify AppShell (mount + shortcut), IconRail (Home icon unread badge).

- `npx shadcn@3 add command` (cmdk). Global **Ctrl/Cmd+K** (same input-focus guard pattern as Ctrl+J) opens a centered dialog: input → 200ms debounce → `GET /api/v1/search?q=` → grouped results (Notes/Agents/Chats/Skills/Connections/Secrets/Reminders — kind→label+icon map) rendering title + snippet (mark the match), keyboard nav native to cmdk; select navigates: map backend `url` per kind to SPA routes (notes url `/kb?path=…` → `/kb?path=…` ✓; agents `/agents/:id` ✓; chats → `/chats?chat=<id>`; skills `/skills/:id` ✓; connections → `/connections`; secrets → `/secrets`; reminders → `/`). PLUS a static **Actions** group always present (filtered by the query too): "New agent" → /agents/new, "New note" → /kb, "Open settings" → /settings, "Ask assistant about '<q>'" → opens the global chat panel with the query prefilled in the composer (extend GlobalChatPanel with an optional initialText prop — additive).
- Rail badge: Home icon shows an unread-count dot/badge from `GET /api/v1/inbox/poll` (30s refetchInterval on that one query — matches template navbar parity).
- Tests: shortcut opens; debounce fires search; groups render; selection navigates (spy) per kind mapping; actions group; badge renders from poll mock.

- [ ] TDD; commit `feat(ui): ⌘K global search palette + inbox rail badge`.

---

### Task 3: Secrets page

**Files:** Create `web/ui/src/pages/secrets/SecretsPage.tsx`, `web/ui/src/lib/secrets.ts` (+tests); Modify router (swap /secrets).

- lib: useSecrets ["secrets"] (names only), addSecret POST {name,value}, deleteSecret DELETE /:name {master_password}. 
- Page (spec §9): full-width; search filter over names; "Add secret" card (name input auto-UPPERCASE hint, value password field, write-only note "values are never displayed") → 201 → clear + invalidate; list rows (KeyRound icon, name, created hint if DTO has it — grep) with Delete → dialog REQUIRING master password input → 401 wrong_master_password inline → success invalidates. NEVER display values anywhere (no reveal affordance).
- Tests: add posts body + clears; delete requires password, wrong-pw inline, success removes; filter; value never rendered (assert absence after add).

- [ ] TDD; commit `feat(ui): secrets page — write-only add, master-password-gated delete`.

---

### Task 4: Editor polish batch

**Files:** Modify `web/ui/src/pages/kb/editor.ts` (+corpus/editor tests), `SlashMenu.tsx`, `NoteEditor.tsx`, `FileTree.tsx`.

1. **Image extension**: `npm i @tiptap/extension-image`; register in buildExtensions default set; corpus `image` entry flips expectLossy → false (verify alt/title/url escaping round-trips — add corpus entries for `![a|b](u "t")`-style edge if lossy, pin whichever way it lands); NoteEditor renders images (max-w-full styling in editor.css).
2. **Slash menu Escape** closes the popup (the suggestion onKeyDown returns true but never closed it — fix + test if achievable in jsdom; else fix + note manual verification).
3. **Duplicate red banners on rename-abort** — suppress the generic errorMessage banner while renameError is shown (one condition + test).
4. **memory/ sorts with user content** (spec §6 "user notes and memory/ first"): remove `memory` from the muted/system set in FileTree's sort/styling (it stays fully editable — verify backend System flag: if the API marks memory system:true, override client-side by name with a comment; test).

- [ ] TDD each; commit `fix(ui): editor polish — images round-trip, slash Escape, banner dedupe, memory/ placement`.

---

### Task 5: Platform polish batch

**Files (small diffs across):** RunPanel (collision-note wording → "Another run is in progress"), OwnerSections (audit uuid→name via session.workspaces map, fallback short-uuid), CoderSection (Test-button hint "tests the last SAVED config"), ConnectionsPage (?error banner: prefix "Connection failed: " + cap length), ProviderCards (drop duplicate ✓), Login page + tests ("Login"→"Log in" copy + the two /login/i regexes), FileTree a11y (Space key activates rows; dropdown trigger not nested in role=button — restructure the row so the trigger is a sibling), Go: `apiSetupCoder` gets the same provider-matched key precedence as saveWorkspaceCoderCore (+test), `InsertServiceConnection` UPSERT refresh-token non-empty guard (+test), delete dead `UpdateWorkspaceSetup` (inline into the test helper), `CoreSkillContent`/`readCoreSkill` case normalization (+test).

- [ ] TDD where behavioral; commits `fix(ui): platform polish batch (SP3-5 ledger)` + `fix(api): setup coder key precedence, upsert refresh guard, cleanups`.

---

### Task 6: Route-level code splitting

**Files:** Modify `web/ui/src/router.tsx` (+ vite config if manualChunks needed).

- `React.lazy` + `Suspense` (skeleton fallback) for the heavy routes: KB page (TipTap ~biggest), designer surfaces (agents/new, edit, skills/new), settings, connections, setup. Keep login/workspaces/home eager (first paint). Verify: `npm run build` output shows the main chunk well under the current 1.1MB (report before/after sizes); all tests still green (lazy imports in jsdom — vitest handles dynamic import; adjust tests that assert synchronous renders with findBy*).

- [ ] Commit `perf(ui): route-level code splitting — main chunk slimmed`.

---

### Task 7: Cutover phase 1 — SPA at `/`

**Files:** Modify `web/ui/vite.config.ts` (base "/"), `web/ui/src/router.tsx` (basename "/"), `web/ui/index.html` (any /app refs), `web/spa.go` (+tests), `web/server.go`.

- `web/spa.go`: serve the SPA at root — catch-all AFTER all API/static/callback routes: `GET /*` → spaHandler (assets by path, index fallback); `GET /app` + `/app/*` → 301 to the stripped path (`/app/kb?x` → `/kb?x`, preserve query). Echo ordering: register the catch-all LAST; verify `/api/v1/*` and the OAuth callback path win (Echo static-over-param/registration rules — the existing routes are explicit so they take precedence; TEST this: parity routes still hit handlers, unknown path → index, /app/agents → 301 /agents).
- Coexistence note: template routes still registered in this phase (deleted in Task 8) — they keep winning their exact paths (`/login`, `/dashboard...`) over the catch-all; the SPA's own /login is unreachable until Task 8 — ACCEPTABLE for the one-commit window (note it; do Task 7+8 back-to-back).
- Frontend: strip the /app basename everywhere (router basename "/", the workspaces hard-nav URLs `/app/workspaces?...` → `/workspaces?...`, GlobalChat "open full page" link, any literal "/app/" grep). Services redirect target in handlers_services.go: `/app/connections` → `/connections`. Tests updated accordingly.

- [ ] TDD (spa_test.go root cases + redirect cases); commit `feat(web): SPA served at / — /app 301s, base/basename stripped`.

---

### Task 8: Cutover phase 2 — template deletion

**Files:** Delete `web/templates/` + `web/static/` + template-only handler code; Modify `web/server.go`, remaining handlers files, `/home/user/simple-agents-v2/CLAUDE.md`.

- Remove from server.go: template route registrations (`showLogin`…`showAuditLog` blocks), `setupTemplates`/`parseTemplates`/`TemplateRenderer`, static route, template middlewares' redirect variants IF now unused (requireOwner/requireActiveWorkspace/requireSetupComplete — the API variants stay; grep usage). **KEEP**: the OAuth callback registration (move `GET /dashboard/connectors/services/callback/:provider` → registered standalone with a comment "registered redirect URI in external OAuth apps — path frozen"), all shared cores (saveConnector, testConnectorIdentity, buildConsentURL, connectAPIKeyCore, saveWorkspaceCoderCore, changeMasterPasswordCore, resubmitPasswordOverExistingSecrets, loadAgentDetail data assembly, enrichKBDisplayNames, renderMarkdown if the API uses it — verify each by callers).
- Delete per-handler render wrappers + pageData/`page()` + template-shape structs whose only consumers die; delete template smoke tests (template_smoke_test.go, kb_template_test.go) and any test referencing templates dir.
- `go build ./... && go test ./... -count=1 -timeout 120s` proves completeness. Also grep for `"dashboard/"` template names and `Render(` in web/ — zero template renders remain (except nothing).
- CLAUDE.md: rewrite the "Web UI routes" section — SPA at `/` + `/api/v1` groups + the frozen callback path; remove the template route list; update the deploy note (UI at `http://host:8080/`); note `/app` 301s.
- Config cleanup: `templates_dir`/`static_dir` config fields + SA_TEMPLATES_DIR handling removed (grep internal/config usage).

- [ ] Commit `feat(web)!: delete template UI — SPA is the only surface (big-bang cutover)`.

---

### Task 9: Final verification + close-out

- [ ] Full suites; `make build`; serve :8090: `/` serves SPA (grep "Simple Agents"), `/app/agents` → 301 `/agents`, `/api/v1/auth/session` 200, callback path still routes (GET without params → its error redirect, NOT SPA index — assert via curl -I), old `/dashboard/agents` → SPA index (200 text/html). Kill own PID.
- [ ] Spec §12 final sweep: read the parity table; every row's new surface exists in the SPA (walk the list against the router + pages; note the two documented exceptions: agent-import [dead template removed with the cutover — resolves the row], ZIP import [post-redesign backlog]).
- [ ] Write the **operator smoke checklist** into the report (login → workspace → home/inbox → ⌘K → KB edit incl. image → chat → designer round → run → connections wizard → settings → onboarding on a fresh workspace → secrets) — Ilija runs it post-deploy.
- [ ] CLAUDE.md final read-through for stale claims (make ui note, known-gaps section mentions of templates). Commit `docs: redesign complete — final docs pass`.

---

## Self-review notes (already applied)

- Spec §9 home/search/secrets fully mapped to Tasks 1-3 with template-parity data (new dashboard endpoint mirrors showDashboard rather than inventing analytics).
- **The cutover's three landmines are called out as constraints**: frozen OAuth callback path, Echo route-precedence testing, shared-core survival verification (grep-callers before delete).
- Tasks 7+8 are deliberately separate commits but executed back-to-back (documented one-commit window where SPA /login is shadowed).
- Every accumulated ledger item is either in Task 4/5, the cutover, or the named post-redesign backlog — nothing silently dropped.
