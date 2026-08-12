# UI Redesign Sub-plan 4: Chat Surfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every conversational surface: one shared chat component powering the one-off chat, the global chat slide-over, the agent designer/editor (with stepper + live build-activity cards), and the skill creator — plus the agents pages (list, detail with run/schedule/AGENT.md/skills/connections cards) they launch from.

**Architecture:** All frontend except zero backend changes — the SP1 endpoints are complete (chat + design families keep their LEGACY response shapes; this sub-plan builds the client for them). A parameterized `DesignerSurface` drives BOTH the agent designer and skill creator (same FSM shape, different endpoint set — the skill designer has NO /state recovery endpoint, drafts only). SSE via a small EventSource wrapper; build/run progress renders as the spec §7 ActivityCard. Spec: `docs/superpowers/specs/2026-07-16-ui-redesign-design.md` §7 + §5 status-chip language.

**Tech Stack:** react-markdown + remark-gfm (safe message rendering — never dangerouslySetInnerHTML), native EventSource (same-origin cookies work), existing shell/query/kb infra.

## Global Constraints

- Branch `ui-redesign`. `cd web/ui && npm test -- --run` + `npm run build` green at every commit; `go test ./... -count=1 -timeout 120s` untouched-sanity before each commit (no Go changes expected).
- **Legacy response shapes (SP1's documented exception — the client adapts, the backend is NOT changed):**
  - `POST /api/v1/chats/:id/messages` `{message}` → 200 `{"response": string}` OR 200 `{"error": string}` (coder failure returns HTTP 200 with an error FIELD — the client must check it).
  - `POST /api/v1/agents/design` `{name?, message}` → `{"response", "done", "state"?, "building"?, "generation_failed"?, "can_keep_as_is"?, "agent_id"?}`; errors `{"error": string}` (400/500/503).
  - `GET /api/v1/agents/design/state` → `{"active": bool, "generating", "state", "history":[{role,content}], "name", "agent_id", "is_edit", "last_progress", "generation_failed", "can_keep_as_is"}`.
  - `POST /api/v1/agents/design/resume` → `{"response","state","history","agent_id","agent_name","generation_failed","can_keep_as_is"}`; `POST .../dismiss` → `{"status":"ok"}`; `POST .../cancel` → `{"status":"cancelled"}`.
  - `POST /api/v1/agents/:id/edit/start` `{message}` → `{"response","done":false}`.
  - Skill designer mirrors agent design (`POST /api/v1/skills/design` `{name?,message}`, cancel/resume/dismiss/progress) but has **NO /state endpoint** — recovery is draft-resume only.
  - SSE streams (`/api/v1/agents/design/progress`, `/api/v1/agents/:id/run/progress`, `/api/v1/skills/design/progress`) emit `data: <string>` lines (plain milestone strings, often starting `⚙️`/`🤖`/`🔍`/`🔧`); stream close = done. 404 body if no active generation (client treats as "nothing to attach to").
- FSM state strings (from `agentdesigner.DesignState.String()`): `"describing"`, `"designing"`, `"verifying"`, `"done"` (+ skill designer `"idle"`, `"awaiting_resume"`). Stepper mapping: Describe=describing, Design=designing, Build=generating flag true, Review=verifying.
- **Approval phrases the buttons submit** (must match the FSM's matchers exactly): Design-stage approve button sends `build it`; Review-stage save button sends `save`; Review-stage change flow just focuses the composer. Never invent other phrases; typed input always works too.
- Agent status-chip vocabulary (spec §5/§7): `Active` (green, agent.active), `Paused` (muted, !active), `Building n%`-style is NOT available from the API — the draft card shows `Draft` (amber); `Running` (green pulse) comes from the run SSE being attached.
- Shared-component discipline: chat bubbles/composer/typing/scroll live in `components/chat/` and are consumed by ALL surfaces; `DesignerSurface` is parameterized by an endpoint config, never duplicated for skills.
- Markdown rendering of assistant messages: react-markdown + remark-gfm, links `target="_blank" rel="noreferrer"`, no raw HTML rendering (rehype-raw NOT installed).
- react-router v8; shadcn via `npx shadcn@3 add`; lockfile committed.
- Deliberate deferrals: agent import surface (orphaned — SP6 decision), Telegram-connect prompts on agent pages (SP5 connections), inbox/home cards (SP6).

---

### Task 1: Chat primitives + chats data layer

**Files:**
- Create: `web/ui/src/components/chat/Bubbles.tsx` (`ChatMessageBubble`, `TypingIndicator`), `web/ui/src/components/chat/Composer.tsx`, `web/ui/src/components/chat/ChatScroll.tsx`, `web/ui/src/lib/chats.ts`, `web/ui/src/lib/chats.test.ts`, `web/ui/src/components/chat/chat.test.tsx`

**Interfaces (binding for every later task):**

```ts
// lib/chats.ts
export type Chat = { id: string; name: string; platform: string; active: boolean; created_at: string; updated_at: string };
export type ChatMessage = { role: "user" | "assistant"; content: string; created_at?: string };
export function useChats()                    // ["chats"] → { chats: Chat[] }
export function useChatDetail(id: string | null) // ["chat", id] → { chat: Chat; messages: ChatMessage[] }
export function useCreateChat()               // POST /api/v1/chats {name?} → Chat; invalidates ["chats"]
export async function sendChatMessage(id: string, message: string): Promise<string>
  // POST legacy shape; resolves the response string; THROWS ApiError(200,"chat_error",body.error) when body.error present
export function useChatAction()               // resume|stop|del → invalidates ["chats"] + ["chat", id]
```

```tsx
// components/chat/
<ChatMessageBubble role content />   // user: dark bubble right; assistant: chrome bubble left; markdown via react-markdown+remark-gfm
<TypingIndicator label? />           // ●●● + optional label ("assistant is working…")
<Composer onSend busy placeholder? autoFocus? />  // textarea autosize (rows 1-6), Enter=send, Shift+Enter=newline, disabled while busy
<ChatScroll>{children}</ChatScroll>  // scrolls to bottom on new children unless user scrolled up >80px (stick-to-bottom)
```

- [ ] **Step 1: Install** — `cd web/ui && npm install react-markdown remark-gfm`
- [ ] **Step 2: Failing tests first** — `chats.test.ts`: sendChatMessage resolves `{response:"hi"}` → "hi"; REJECTS with ApiError code "chat_error" message "Couldn't reach X" for 200 `{error:"Couldn't reach X"}`; useChats fetch URL correct. `chat.test.tsx`: ChatMessageBubble renders **bold** markdown as `<strong>` and a link with target=_blank; user vs assistant alignment classes differ; Composer Enter fires onSend with trimmed value and clears, Shift+Enter does not, busy disables; empty/whitespace send is a no-op.
- [ ] **Step 3: Implement** all of it. ChatScroll: `useRef` container, on children change `if (nearBottom) scrollTo(bottom)`; track via onScroll.
- [ ] **Step 4:** `npm test -- --run` green; `npm run build` clean. Commit `feat(ui): chat primitives (bubbles/composer/scroll) + chats data layer`.

---

### Task 2: Chats page

**Files:**
- Create: `web/ui/src/pages/chats/ChatsPage.tsx`, `web/ui/src/pages/chats/ChatWindow.tsx`, `web/ui/src/pages/chats/chats.test.tsx`
- Modify: `web/ui/src/router.tsx` (swap /chats placeholder)

**Interfaces:**
- `ChatWindow` props `{ chatId: string }` — loads useChatDetail, renders ChatScroll of bubbles, TypingIndicator while awaiting sendChatMessage, Composer; on send: optimistic user bubble → await → append assistant bubble → invalidate ["chat", id]; on chat_error: red inline banner with the message, user bubble stays (matches template behavior: refresh clears). Header row: chat name + Active/Stopped chip + Stop/Resume + ⋯ menu (Delete with confirm). **ChatWindow is REUSED by Task 3's slide-over — keep it container-agnostic (no page margins).**
- `ChatsPage`: ContextPane = session list (name, Active/Stopped chip, relative time via a tiny `timeAgo` util, "+ New chat" button → useCreateChat → select); `?chat=<id>` search param selects; empty state when none selected.

- [ ] **Step 1:** failing tests: page lists sessions from mocked fetch; selecting renders history; send round-trip appends both bubbles; 200-with-error shows banner and keeps composer enabled; stop/resume/delete fire correct endpoints.
- [ ] **Step 2:** implement + router swap. **Step 3:** suites green; commit `feat(ui): chats page — session list + chat window on legacy message API`.

---

### Task 3: Global chat slide-over

**Files:**
- Create: `web/ui/src/components/chat/GlobalChatButton.tsx`, `web/ui/src/components/chat/globalchat.test.tsx`
- Modify: `web/ui/src/components/shell/AppShell.tsx` (mount the button inside the authed layout, floating bottom-right, above the mobile bottom bar: `bottom-20 md:bottom-6 right-4 md:right-6`)

**Interfaces:**
- Floating round button (MessageSquarePlus icon, accent bg) + keyboard shortcut **Ctrl/Cmd+J** (registered via useEffect keydown listener; ignore when focus is in an input/textarea/contenteditable) → `useSlideOver().open(<GlobalChatPanel/>, { title: "Chat" })`.
- `GlobalChatPanel`: picks the most recent ACTIVE chat from useChats; if none, useCreateChat on first open; renders `<ChatWindow chatId=.../>`; a "open full page ↗" link → `/chats?chat=<id>` (closes the panel).
- Hidden on /chats (redundant) — check via useLocation.

- [ ] Steps: failing test (button renders in shell, click opens panel with ChatWindow inside — reuse the shell test harness pattern with mocked session+chats fetches; Ctrl+J opens; hidden on /chats) → implement → suites green → commit `feat(ui): global chat slide-over — floating button + Ctrl/Cmd+J`.

---

### Task 4: SSE client + ActivityCard

**Files:**
- Create: `web/ui/src/lib/sse.ts`, `web/ui/src/lib/sse.test.ts`, `web/ui/src/components/chat/ActivityCard.tsx`, `web/ui/src/components/chat/activity.test.tsx`

**Interfaces (binding):**

```ts
// lib/sse.ts
export type SSEHandle = { close(): void };
export function openSSE(url: string, opts: {
  onMessage(line: string): void;
  onDone(): void;              // stream closed by server (normal completion)
  onError?(): void;            // failed to connect / connection lost (after internal retry ONCE)
}): SSEHandle
// EventSource wrapper; one silent reconnect attempt on error, then onError; close() is idempotent.
```

```tsx
<ActivityCard
  title="Building your agent…"        // or "Running <name>…"
  lines={string[]}                    // milestone lines in arrival order
  status="live" | "done" | "error"
  startedAt={number}                  // Date.now() at attach; renders elapsed mm:ss ticking while live
  collapsible                         // collapsed shows last line only; expanded shows all (monospace, scrollable max-h)
/>
```

Visual per spec §7 mockup: status dot (pulsing green live / green done / red error), title, elapsed, progress shimmer bar while live, lines in a `font-mono text-xs` block, ✓-prefix lines tinted ok, 🔧 lines default, error state tints the header danger.

- [ ] **Step 1:** failing tests — sse.ts with a stubbed global EventSource class (capture instance; simulate message/error/close events): onMessage receives data lines; server-close → onDone; two errors → onError; close() detaches. ActivityCard: renders lines, ticks elapsed (vi.useFakeTimers), collapsible toggles, error styling class present.
- [ ] **Step 2:** implement. EventSource close-detection: readyState CLOSED in onerror = done-vs-error disambiguation — after a successful open + subsequent error with readyState CLOSED, treat as onDone (server ended stream); before any message/open, treat as connect-failure path.
- [ ] **Step 3:** suites green; commit `feat(ui): SSE client + build/run ActivityCard`.

---

### Task 5: Agents list page

**Files:**
- Create: `web/ui/src/lib/agents.ts` (+test), `web/ui/src/pages/agents/AgentsPage.tsx`, `web/ui/src/pages/agents/agents.test.tsx`
- Modify: `web/ui/src/router.tsx` (swap /agents; add /agents/new + /agents/:id + /agents/:id/edit routes pointing at Task 6/7 placeholders for now)

**Interfaces:**

```ts
// lib/agents.ts — mirror web/api_agents.go DTOs (grep the api file for exact keys before writing; adjust types not tests)
export type Agent = { id: string; name: string; description: string; active: boolean; created_at: string; running: boolean };
export type AgentDraft = { agent_id?: string; agent_name?: string; is_edit?: boolean; state?: string; updated_at?: string; expires_at?: string } | null;
export function useAgents()      // ["agents"] → { agents: Agent[]; draft: AgentDraft }
export function useAgentDetail(id: string | null)  // ["agent", id] → the detail DTO (type it from api_agents.go's toAPIAgentDetail keys)
export function useAgentActions() // del(id), run(id) → 202, saveSchedule(id, cron), deleteSchedule(id), saveAgentMD(id, content), saveSkills(id, names[]), saveConnections(id, ids[])
```

- `AgentsPage`: full-width (no ContextPane); header with client-side search input + "New agent" button; card grid — name, description (2-line clamp), chips (`Active`/`Paused`, `Running` pulse when running), created date; **draft card** (dashed border, amber `Draft` chip, agent_name, "Resume" → `/agents/new?resume=1`, "Discard" → POST dismiss + invalidate); card click → `/agents/:id`. Empty state with a "create your first agent" CTA.

- [ ] Steps: failing tests (grid renders mocked agents with chips; search filters client-side; draft card resume/discard fire correctly) → implement + router (placeholders `<Placeholder/>` for new/detail/edit) → suites green → commit `feat(ui): agents list — searchable cards, status chips, draft resume/discard`.

---

### Task 6: DesignerSurface (agent designer + editor entry)

**Files:**
- Create: `web/ui/src/components/designer/DesignerSurface.tsx`, `web/ui/src/components/designer/Stepper.tsx`, `web/ui/src/components/designer/designer.test.tsx`, `web/ui/src/pages/agents/AgentNewPage.tsx`, `web/ui/src/pages/agents/AgentEditPage.tsx`
- Modify: `web/ui/src/router.tsx` (real /agents/new + /agents/:id/edit)

**Interfaces (binding — Task 8 reuses DesignerSurface for skills):**

```ts
export type DesignerEndpoints = {
  design: string;        // POST {name?,message} legacy shape
  cancel: string; resume: string; dismiss: string;
  progress: string;      // SSE
  state?: string;        // GET recovery — ABSENT for the skill designer
};
export type DesignerLabels = { steps: [string,string,string,string]; buildButton: string; saveButton: string; entityName: string };
<DesignerSurface endpoints labels startPayload? onDone(id?: string)>
```

Behavior (drives everything):
1. **Mount recovery**: if `endpoints.state` — GET it; `active` → replay `history` as bubbles, set FSM state, and if `generating` → attach ActivityCard to SSE + poll design POST is NOT needed (the next user message or SSE completion updates); show `last_progress` as the card's first line. If not active and useAgents().draft exists (agent case) → resume banner ("You have an unfinished draft: <name> — Resume / Discard") → resume → replay returned history.
2. **Stepper** (Describe → Design → Build → Review, labels from props): describing→0, designing→1, generating→2, verifying→3; done → onDone.
3. **Sending**: composer disabled while awaiting POST; response appended as assistant bubble (markdown). `building:true` response → show a persistent "still building" notice + attach SSE if not attached.
4. **Approval detection → buttons**: in designing state, show quick-action row `[<buildButton>] [Make changes]` under the last assistant bubble — buildButton sends the literal phrase `build it` AND immediately attaches the ActivityCard to the SSE (open BEFORE the POST resolves — the stream may start mid-generation; the POST itself resolves only when generation finishes or soft-fails). "Make changes" focuses the composer. In verifying state: `[<saveButton>] [Request changes]` — saveButton sends `save`; on `done:true` → onDone(agent_id).
5. **Soft failures**: `generation_failed:true` in a response → banner "The build hit a problem — describe a change or say 'try again'" (+ keep-as-is button when `can_keep_as_is`, sending `keep it as-is`). `{error}` → red banner. Cancel button (header) → POST cancel → navigate away.
6. **AgentNewPage**: name field shown ONLY for the very first message (design POST requires name to start); after that the surface takes over. `?resume=1` skips straight to resume. **AgentEditPage**: POST `/api/v1/agents/:id/edit/start` `{message}` with the first composer submit, then the shared surface continues via the normal design endpoint; labels steps ["Describe","Diagnose","Build","Review"].
- Wire labels for agents: steps ["Describe","Design","Build","Review"], buildButton "🔨 Build it", saveButton "✅ Save agent", entityName "agent"; onDone → navigate `/agents/<id>` (or `/agents` if no id).

- [ ] **Step 1:** failing tests — surface: send→assistant bubble; designing-state buttons send exact phrases ("build it" POST body asserted); building response attaches SSE (stub EventSource) and renders ActivityCard lines; verifying buttons send "save"; done navigates (spy); {error} banner; state-recovery replays history; resume banner appears when draft present & state inactive. Keep tests on DesignerSurface with a fake endpoints object hitting the mocked fetch.
- [ ] **Step 2:** implement; wire pages + router. **Step 3:** suites green; commit `feat(ui): DesignerSurface — stepper, approval buttons, SSE build card; agent new/edit pages`.

---

### Task 7: Agent detail page — run, activity, history, AGENT.md, schedule

**Files:**
- Create: `web/ui/src/pages/agents/AgentDetailPage.tsx` (+ small subcomponents in the same dir: `RunPanel.tsx`, `AgentMDCard.tsx`, `ScheduleCard.tsx`), `web/ui/src/pages/agents/detail.test.tsx`
- Modify: `web/ui/src/router.tsx` (real /agents/:id)

**Interfaces:** consumes useAgentDetail + useAgentActions. Layout: header (name, chips incl. Running, Edit → /agents/:id/edit, ⋯ menu: Delete w/ confirm → navigate /agents); two-column md+ (main: RunPanel + run history + AgentMDCard; side: ScheduleCard + placeholder slots where Task 8 adds Skills/Connections cards — leave `{/* Task 8: attachment cards */}`).
- `RunPanel`: "▶ Run now" → POST run (202) → attach ActivityCard to `/api/v1/agents/:id/run/progress` SSE (title "Running <name>…"); onDone → invalidate ["agent", id] (history refreshes); 503 not_configured → banner. If detail DTO says a run is already live (`live_run`/`running` — grep exact key), auto-attach on mount.
- Run history: last runs from the detail DTO (status chip ok/error, started time, duration if present; expandable output/log text block if the DTO carries it — grep `toAPIAgentDetail` for the runs array keys and render what exists, nothing more).
- `AgentMDCard`: readonly `<pre>` view + "Edit" toggles a textarea → Save → PUT agent-md; 400 `ethics_blocked` shows the envelope message inline; dirty-state guard (button disabled until changed).
- `ScheduleCard`: shows `schedule.cron_expr` + next-run if present; input + Save (PUT, 400 invalid_cron inline) + Remove (DELETE).

- [ ] Steps: failing tests (run fires POST + renders SSE lines; ethics 400 message shows; schedule save/delete + invalid cron; delete agent confirm → DELETE + nav spy) → implement → suites green → commit `feat(ui): agent detail — run with live activity, history, AGENT.md editor, schedule`.

---

### Task 8: Attachment cards + skill creator + skills pages

**Files:**
- Create: `web/ui/src/pages/agents/AttachmentCards.tsx` (Skills + Connections cards), `web/ui/src/lib/skills.ts` (+test), `web/ui/src/pages/skills/SkillsPage.tsx`, `web/ui/src/pages/skills/SkillNewPage.tsx`, `web/ui/src/pages/skills/SkillDetailPage.tsx`, `web/ui/src/pages/skills/skills.test.tsx`
- Modify: `web/ui/src/pages/agents/AgentDetailPage.tsx` (mount cards), `web/ui/src/router.tsx` (swap /skills; add /skills/new, /skills/core/:slug, /skills/:id)

**Interfaces:**
- `lib/skills.ts`: types + hooks mirroring web/api_skills.go (`useSkills` → {skills, core_skills, draft}, `useSkillDetail`, `useCoreSkill(slug)`, `useSkillActions` (create {content}, save, del)).
- Skills card (agent detail): checkboxes core ∪ user skills, checked = detail's attached names; Save → PUT skills {skill_names}; chip count in card header. Connections card: checkboxes from detail's workspace connections pool (id + provider + label), checked = attached ids; Save → PUT connections {connection_ids}; if pool empty → "Connect services first" note linking /connections.
- `SkillsPage`: full-width; two sections — "Your skills" cards (name/description, click → detail) + "Core skills" (muted cards, click → /skills/core/:slug readonly view); draft-resume banner like agents; "New skill" → /skills/new; "Import" button → dialog with a SKILL.md paste textarea → POST /api/v1/skills {content} (JSON path only — ZIP upload deferred to SP6, note it).
- `SkillNewPage`: `<DesignerSurface>` with skill endpoints (design/cancel/resume/dismiss/progress, **no state**), labels steps ["Describe","Design","Build","Vet & Review"], buildButton "🔨 Build it", saveButton "✅ Save skill", entityName "skill"; onDone → /skills.
- `SkillDetailPage`: content in a monospace editor (textarea) + Save (PUT {content}) + Delete (confirm) → /skills. Core viewer: readonly markdown render.

- [ ] Steps: failing tests (skills card PUT body {skill_names}; connections card PUT {connection_ids}; skills page sections render; import dialog POSTs content; SkillNewPage mounts DesignerSurface WITHOUT state-recovery fetch — assert no GET to a state URL) → implement → suites green → commit `feat(ui): agent attachment cards + skills pages + skill creator on shared DesignerSurface`.

---

### Task 9: Close-out — docs, smoke evidence

**Files:**
- Modify: `/home/user/simple-agents-v2/CLAUDE.md` (routes note only if anything changed — likely nothing; verify), ledger by controller.

- [ ] **Step 1:** Full suites: `go test ./... -count=1 -timeout 120s`; `cd web/ui && npm test -- --run && npm run build`.
- [ ] **Step 2:** e2e smoke evidence: `make build && SA_PORT=8090 ./bin/simple-agents serve &` → `/app` 200 → kill own PID. Note in report: interactive smoke (chat send, designer round with a real coder, run with SSE) is operator-driven post-deploy.
- [ ] **Step 3:** Commit `feat(ui): chat surfaces complete (sub-plan 4)` — or `docs:`-only if no file changes beyond docs; if literally nothing changed, skip the commit and say so.

---

## Self-review notes (already applied)

- **Spec §7 coverage:** one chat component (T1) reused by chats page (T2), global slide-over (T3), designer (T6), skill creator (T8); stepper + explicit approval buttons with the FSM's real phrases (T6); ActivityCard from SSE reused for builds (T6) and runs (T7); agents page with chips + draft resume (T5); full agent page cards (T7/T8). "Building n%" chips aren't API-backed — chip vocabulary constraint documents the substitution.
- **Legacy-shape handling** is concentrated in `sendChatMessage` + DesignerSurface's POST handler — nothing else parses those shapes.
- **Type consistency:** DesignerEndpoints/DesignerLabels defined in T6 and consumed by name in T8; ActivityCard/openSSE defined in T4, consumed T6/T7; ChatWindow defined T2, reused T3; all lib types instruct grepping the api_*.go DTOs before writing.
- Skill designer's missing /state endpoint is a first-class design input (optional `state?` field), not a surprise.
