# Simple Agents UI Redesign — Master Design

**Date:** 2026-07-16
**Status:** Approved (brainstorming session with visual mockups; mockups archived in `.superpowers/brainstorm/`)
**Scope:** Ground-up replacement of the entire web UI with a modern SPA, embedded in the single deploy binary. No backend business-logic changes; a new JSON API layer exposes existing services. Full functionality parity is a hard requirement, verified by the route inventory in §12.

---

## 1. Goals

- Replace the ~6k-line server-rendered template UI with a modern, Slack-inspired app shell and Notion-ish visual language (light + dark).
- Keep single-binary deployment: the SPA compiles into the Go binary via `go:embed`.
- Make every "hard" flow guided: connecting chat apps/services, configuring the coder, onboarding.
- Global search (⌘K) across everything + per-page local search.
- One modern chat surface shared by chat, agent designer, agent editor, and skill creator — with live progress, typing indicators, streaming activity.
- WYSIWYG markdown knowledge base editor (Slite/Tolaria class) on plain `.md` vault files.
- Provider brand logos everywhere a service is named — never bare text placeholders.
- Owner/admin surface fully absorbed into the one shell (single-owner model).
- Desktop-first, gracefully responsive on small screens.

Non-goals: backend feature work (new adapters, MCP, etc.); mobile-parity design; multi-user auth changes.

## 2. Tech stack

| Concern | Choice |
|---|---|
| Framework | React 18 + TypeScript, Vite |
| Styling | Tailwind CSS + design tokens (CSS variables), dark mode via `dark:` + `prefers-color-scheme` with manual toggle |
| Components | shadcn/ui (Radix primitives), Lucide icons |
| WYSIWYG editor | TipTap (ProseMirror) with markdown serialization |
| Command palette | cmdk |
| Data | TanStack Query; React Router for routing |
| Provider logos | Curated SVG set (simple-icons sourced) bundled as a `<ProviderLogo name/>` component |

**Embedding & build:** SPA lives in `web/ui/`. `vite build` → `web/ui/dist/` → `go:embed` in the binary, served by Echo with SPA fallback (all non-`/api`, non-asset routes serve `index.html`). `make build` runs `npm ci && npm run build` then `go build`. Node is a build-time dependency only; `dist/` is not committed.

**Dev loop:** `vite dev` proxies `/api` (including SSE) to the Go server on `:8080` for hot reload against the real backend.

## 3. API layer

- New `/api/v1/*` JSON endpoints in `web/`, thin handlers over the same internal services the template handlers call today. The template handlers are deleted at merge (§11).
- **Auth unchanged:** Echo cookie sessions; `requireOwner` / `requireActiveWorkspace` / `requireSetupComplete` middleware retained, returning JSON `401` (not logged in), `403 {code:"no_workspace"}` (no active workspace), `403 {code:"needs_setup"}` — the SPA routes to login / workspace gate / onboarding accordingly.
- **Error envelope:** every error is `{"error": {"code": string, "message": string}}`. UI renders toasts or inline field errors; no raw error pages.
- **SSE:** existing streams (design progress, skill-design progress, run progress) move under `/api/v1/` unchanged in protocol. The client auto-reconnects with a visible "reconnecting…" state.
- **New endpoints:**
  - `GET /api/v1/search?q=` — aggregated global search: vault full-text (existing `vault.Searcher` / ripgrep), agents, chats, skills, connections, secret **names**, reminders, plus static action entries. Returns grouped, ranked results.
  - Gap-fillers discovered during the parity inventory (e.g. JSON variants of currently form-POST-only actions).

## 4. App shell

Four zones (validated mockup: `shell-layout.html`):

1. **Icon rail** (global, fixed, 56px): workspace icon at top → menu with switch workspace (master-password gate, as today) and create workspace (owner flow). Then: Home, Knowledge Base, Agents, Skills, Connections, Chats, Secrets. Bottom: profile avatar → Settings.
2. **Context pane** (per-page, ~250px): Home → inbox + reminders; KB → file tree; Chats → session list; Connections → category list. Agents, Skills, Secrets → no pane (full-width content).
3. **Content area** — the page itself.
4. **Right slide-over** — universal drill-in panel (Slack-thread style): agent details, run logs, connect wizards, secret editor, note preview, and the **global chat** (floating button + keyboard shortcut on every page).

Owner functions absorbed: workspace management via the rail's workspace menu; system settings, audit log, workspace permissions as sections in Settings. No separate admin site.

**Responsive:** below tablet width the rail becomes bottom/hamburger nav, context pane and content become separate screens, slide-over goes full-screen.

## 5. Design system

- Tokens in one file (CSS variables + Tailwind config): white content surfaces, warm light-gray chrome `#f7f6f3`, near-black text `#37352f`, hairline borders `#e9e7e3`, one restrained accent, semantic status colors (green/amber/red chips). Dark mode mirrors every token.
- Typography: system font stack; generous whitespace; 13–14px base.
- Status chips, cards, activity feeds, empty states, and skeleton loaders are shared components — every page uses the same vocabulary.
- Provider logos: one component, used in connections, agent cards, settings, chat headers, search results — everywhere a provider is named.

## 6. Knowledge Base (validated mockup: `kb-editor.html`)

- **True WYSIWYG on plain `.md`:** TipTap renders formatted text while editing; saves serialize to clean markdown through the existing vault API. Agents/coders keep reading and writing the same files. Raw-markdown toggle available.
- Bubble toolbar on text selection (bold, italic, underline, strike, code, headings, link, lists, todo, quote) + `/` slash menu for blocks (headings, lists, tables, code, dividers, images).
- **UI-owned page header:** title (from filename; renaming the title renames the file), breadcrumb, edited time, backlinks count, save state — never written into file content.
- **File tree** in context pane: folder/file icons, drag-to-move, inline rename, create; user notes and `memory/` first; system folders (`chats/`, `agents/<id>/logs`) visible but muted and read-only where system-owned; dotfiles/`.kb` hidden.
- `[[wikilinks]]` render as clickable pills; backlinks panel in the slide-over.
- Autosave with debounce + explicit save state indicator; conflict-safe via the existing atomic `WriteNote`.

## 7. Chat & Agent Designer (validated mockup: `chat-designer.html`)

- **One chat component** (bubbles, markdown rendering, streaming, typing indicator, day separators) shared by: one-off chat, agent designer, agent editor, skill creator, global chat slide-over.
- **Designer wrapper adds:** 4-step indicator (Describe → Design → Build → Review) mapped to the existing FSM states; explicit action buttons (**Build it** / **Make changes**, and at Review: **Save** / **Request changes**) that submit the exact approval phrases the FSM already accepts — typed approval still works.
- **Live build-activity card** in the chat stream, fed by existing SSE milestones: checkmarked completed steps, current tool call, elapsed time, progress bar, collapsible detail, red error state with plain-language message. The same card renders agent runs (agent page, slide-over, home).
- **Agents page:** full-width searchable card grid; status chips (Active / Building n% / Needs attention / Paused / Draft); provider logos per agent (from bound connections); schedule + last-run inline; draft cards with resume/discard. Card click opens the slide-over summary; a full agent page remains for deep work (AGENT.md editor with ethics check, state, run history, skills card, attach-connections card, schedule).

## 8. Connections (validated mockup: `connections.html`)

- One rail item, two plain-language categories: **Chat apps** ("where you talk to your assistant" — Telegram, Slack, Discord, driven by the CredSpec registry) and **Services** ("accounts your agents act on" — the 28 connector providers).
- Logo gallery with connected-state badges (account label, e.g. bot name / email), per-section search, category filters for services.
- **Guided connect wizard in the slide-over,** generated from existing CredSpec `SetupSteps` / provider YAML `setup_steps`: numbered step chips, copy-paste blocks (e.g. Slack app manifest pre-configuring scopes), inline format validation on paste ("bot tokens start with xoxb-"), OAuth redirect handling for services, and a **live test final step** ("we just sent you a DM — did it arrive?" / fetch identity for OAuth).
- Disconnect, re-auth (needs-reauth state surfaced), and per-provider OAuth-app credential management live in the same surface.

## 9. Home, Search, Settings (validated mockup: `home-search-settings.html`)

- **Home:** context pane = notification inbox (agent/reminder deliveries, unread state) + reminders with inline natural-language add. Content = greeting, stat tiles (active agents, runs this week with failures, connected services), "Next up" (upcoming scheduled runs; failures with one-click **view log** and **ask the designer to fix it** — the latter opens an edit-design session pre-filled with the run error).
- **Global search (⌘K):** cmdk palette from anywhere; grouped results (notes full-text, chats, agents, skills, connections, secret names, reminders) + actions ("New agent", "Open settings", "Ask assistant about …"). Per-page search boxes remain for local filtering.
- **Settings (profile icon):** Profile · Workspace (name/about) · **AI Providers** · Coder · Master password (change; re-encrypts secrets) · Appearance (theme) · Owner sections (workspaces, permissions, system settings, audit log).
- **Providers-first coder config:** providers are connected once as logo cards (API key → stored via the existing `CODER_KEY_<PROVIDER>` encrypted-secret path; installed CLI binaries auto-detected via `coder.DetectInstalled()`). Coder config then selects: engine (CLI/API) → provider **from connected only** → model, with a mandatory live **Test** (existing `Smoke`/`Ping`) that must pass green. This also adds the missing local-coder Model field (closes a known gap).
- **Secrets page:** full-width searchable list; add/edit is **write-only** (value never displayed) and edit/delete require master-password confirmation.
- **Skills page:** your skills + core skills as cards, search, draft-resume card; skill creator uses the shared designer chat surface.
- **Onboarding:** the setup wizard becomes a full-screen guided flow (workspace basics → master password → AI provider + coder with live test → profile → chat app connect with live test) reusing the same provider cards and connect wizards, ending in a "create your first agent" handoff into the designer.

## 10. Error handling & resilience

- API errors → toast or inline field error from the error envelope; auth errors route to login/workspace gate.
- SSE disconnects → auto-retry with visible reconnecting state; build/run cards mark interrupted streams and recover via the existing design-session `/state` recovery endpoint pattern.
- Long operations always render progress (activity card / skeletons); destructive actions (delete agent/note/connection/workspace) require typed or modal confirmation; master-password-gated actions per §9.

## 11. Execution plan (big bang, structured)

One long-lived branch `ui-redesign`; `main` keeps the old UI working until merge. Six sequential sub-plans, each independently reviewable:

1. **API layer + parity inventory** — `/api/v1` endpoints, JSON middleware, route-inventory checklist wired into tests.
2. **Shell + design system** — Vite/embed pipeline, tokens, rail/panes/slide-over, login, workspace enter/create/switch, dark mode.
3. **Knowledge base** — tree, TipTap editor, wikilinks/backlinks, search.
4. **Chat surfaces** — shared chat component, one-off chat, designer, editor, skill creator, activity cards, global chat slide-over.
5. **Connections + Settings + Onboarding** — galleries, wizards, providers-first coder, owner sections.
6. **Home + global search + secrets + skills + inbox; delete templates/old handlers; polish pass.**

At merge: `web/templates/`, `web/static/`, and all template handlers are removed; `/api/v1` + embedded SPA is the only web surface.

## 12. Functionality parity — route inventory

Every current route maps to a new surface. Implementation of each sub-plan checks off its rows; merge requires all rows checked.

| Current route(s) | New surface |
|---|---|
| `/login`, `/logout` | SPA login screen → `POST /api/v1/auth/login`, `/logout` |
| `/change-password` | Settings → Owner → change password |
| `/dashboard/setup` (5 steps) | Full-screen onboarding flow (§9) |
| `/workspace/leave`, `/admin/workspaces/:id/enter` | Rail workspace menu (switch = master-password gate; leave) |
| `/dashboard` | Home (§9) |
| `/dashboard/agents` (+search) | Agents card grid |
| `/dashboard/agents/new`, `/design`, `/design/cancel`, `/design/progress`, `/design/state` | Designer chat surface + SSE + recovery |
| `/dashboard/agents/:id` (AGENT.md, state, logs, schedule, skills) | Agent page + slide-over summary |
| `/dashboard/agents/:id/edit`, `/edit/start` | Edit design session in designer surface |
| `/dashboard/agents/:id/delete` | Agent page / card menu with confirm |
| `/dashboard/agents/:id/run`, `/run/progress` | Run button + live activity card (SSE) |
| `/dashboard/agents/:id/schedule[/delete]` | Agent page schedule card |
| `/dashboard/agents/:id/agent-md` | AGENT.md editor (ethics check preserved) |
| `/dashboard/agents/:id/skills` | Agent page skills card |
| `/dashboard/agents/:id/connections` | Agent page attach-connections card |
| Agent import (`agent_import.html` — orphaned template, no registered route or handler exists today) | No API port (dead surface); delete the orphan template in sub-plan 6, or scope an import endpoint deliberately if wanted |
| `/dashboard/skills`, `/skills/new`, `/skills/design*`, `/skills/core/:slug`, `/skills/:id` | Skills page, skill-creator chat, core-skill viewer, skill detail |
| `/dashboard/secrets` | Secrets page (write-only edit, master-password gates) |
| `/dashboard/connectors` | Connections → Chat apps |
| `/dashboard/connectors/services`, `/:provider/creds`, `/:provider/connect`, `/callback/:provider`, `/:id/delete` | Connections → Services + connect wizard (OAuth callback stays a server route) |
| `/dashboard/chats`, `/chats/:id`, `/messages`, `/resume` (+stop/delete) | Chats page + global chat slide-over |
| `/dashboard/reminders` | Home context pane (list/add/delete) |
| `/dashboard/memory` | KB tree (`memory/` section) |
| `/dashboard/kb*` (browse/view/edit/raw/save/new/delete/rename/search) | KB editor (§6); raw = toggle + download |
| Inbox (`/dashboard/inbox`, navbar badge) | Home inbox pane + rail badge |
| `/dashboard/settings`, `/settings/workspace`, `/settings/coder`, `/settings/master-password` | Settings (§9) |
| `/admin` (stats, cards, audit) | Settings → Owner → Workspaces / Audit |
| `/admin/workspaces*` (list/detail/delete/permissions) | Settings → Owner → Workspaces |
| `/admin/settings` | Settings → Owner → System |
| `/admin/audit` | Settings → Owner → Audit log |

## 13. Testing

- **Go:** handler tests for every `/api/v1` endpoint (auth, happy path, error envelope) — replaces template smoke tests. Search endpoint tested against a seeded vault.
- **Frontend:** Vitest component tests for high-risk logic: markdown↔TipTap round-trip fidelity, chat stream/protocol-marker rendering, activity-card SSE state machine. One Playwright smoke flow (login → enter workspace → create note → chat turn) run manually before merge.
- **Manual E2E** per sub-plan against the live home server (project norm), including the guided connect wizards against real Telegram/Slack.

## 14. Risks

- **TipTap↔markdown fidelity** — mitigated by round-trip tests and the raw toggle; unknown markdown constructs must pass through unmangled (round-trip test corpus includes real vault files).
- **Big-bang drift** — mitigated by the six-sub-plan structure and the parity inventory as a merge gate.
- **Node build dependency** — accepted; documented in README/Makefile.
- **Bundle size in binary** — acceptable for a home server; code-split per route to keep first paint fast.
