# Power & Creation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the user what the designer built before they approve it, give agent creation a starting point, and make the app keyboard-navigable.

**Architecture:** Mostly React SPA (`web/ui`), plus one additive Go change exposing build artifacts the design session already holds. No schema changes, no new endpoints — one existing endpoint gains two fields.

**Tech Stack:** React 19 + TypeScript + Vite 8 + Tailwind 4 + shadcn/ui (CLI pinned to `shadcn@3`) + TanStack Query + React Router v8 + cmdk. Tests: vitest + @testing-library/react; Go stdlib testing.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-21-power-and-creation-design.md`. It governs; this plan implements it.
- **Baseline: 456 frontend tests green and the Go suite green.** Do not regress any. `cd web/ui && npx vitest run`; `go test ./... -count=1 -timeout 120s`.
- **`web/ui/dist/.gitkeep` must stay git-tracked** or `go:embed all:dist` breaks on a fresh clone. Verify with `git ls-files web/ui/dist/.gitkeep` before every commit.
- **Every behaviour must be pinned by a test that fails without its implementation.** Red-verify. Previous sub-plans' reviews repeatedly found tests that passed either way.
- **`userEvent.click` hangs under `vi.useFakeTimers()` in this repo — use `fireEvent.click`.** Precedent: `CommandPalette.test.tsx:145-150`, `pages/kb/search.test.tsx`.
- **Single-key shortcuts must never fire while the user is typing** (input, textarea, `[contenteditable]`, or inside the ⌘K palette). This is the single most important rule in this plan.
- `localStorage` persistence follows the established pattern: lazy read in `useState`'s initializer, write in an effect, corrupt value falls back to the default (`web/ui/src/theme.tsx`, `components/shell/usePaneWidth.tsx`).
- No new dependencies. No external network requests (strict CSP).
- Run `npm run build` and `npx tsc -b` before committing frontend work.

---

### Task 1: Expose the build artifacts (Go)

**Files:**
- Modify: `internal/agentdesigner/flow.go` (`DesignSnapshot` at ~:595, `Snapshot()` at ~:612)
- Modify: `web/handlers_agents.go` (`handleDesignState` at ~:192)
- Test: `internal/agentdesigner/flow_test.go` (or the existing snapshot test file — `generation_keepfiles_test.go:243` has `TestSnapshot_ExposesLastProgress` as the pattern), `web/api_agents_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `pending_agent_md` (string) and `pending_tools` (object of filename → content) on `GET /api/v1/agents/design/state`. **Task 2 consumes these exact JSON names.**

- [ ] **Step 1: Write the failing Go test**

Follow `TestSnapshot_ExposesLastProgress` (`internal/agentdesigner/generation_keepfiles_test.go:243`) for how to construct a Flow with a session:

```go
func TestSnapshotExposesPendingBuild(t *testing.T) {
	// build a Flow with one session whose PendingAgentMD/PendingTools are set
	snap := f.Snapshot(workspaceID)
	if snap.PendingAgentMD != "# Test agent\n" {
		t.Fatalf("PendingAgentMD not exposed: %q", snap.PendingAgentMD)
	}
	if snap.PendingTools["tools/main.py"] != "print('hi')\n" {
		t.Fatalf("PendingTools not exposed: %#v", snap.PendingTools)
	}
}

func TestSnapshotPendingEmptyBeforeBuild(t *testing.T) {
	// a session that has not generated yet
	snap := f.Snapshot(workspaceID)
	if snap.PendingAgentMD != "" || len(snap.PendingTools) != 0 {
		t.Fatal("pending fields must be empty before a build")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentdesigner/... -run TestSnapshotPending -count=1`
Expected: FAIL — `snap.PendingAgentMD` undefined.

- [ ] **Step 3: Add the fields**

In `DesignSnapshot`:

```go
	PendingAgentMD string
	PendingTools   map[string]string
```

In `Snapshot()`, copy them from the session (`sess.PendingAgentMD`, `sess.PendingTools`). **Copy the map** — do not hand out the session's own map, which the flow mutates under its mutex:

```go
	tools := make(map[string]string, len(sess.PendingTools))
	for k, v := range sess.PendingTools {
		tools[k] = v
	}
```

- [ ] **Step 4: Expose in the handler**

In `handleDesignState`'s response map, add:

```go
		"pending_agent_md": snap.PendingAgentMD,
		"pending_tools":    snap.PendingTools,
```

`pending_tools` must serialise as `{}` rather than `null` when empty — the frontend maps over it. Initialise the map in `Snapshot()` even when the session has none.

- [ ] **Step 5: Add the API test**

In `web/api_agents_test.go`, assert the design-state JSON contains `pending_agent_md` and `pending_tools`, and that `pending_tools` is an object (not null) when there is no build.

- [ ] **Step 6: Run**

Run: `go test ./internal/agentdesigner/... ./web/... -count=1` then `go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat(designer): expose the pending build on the design state endpoint"
```

---

### Task 2: Designer spec panel

**Files:**
- Create: `web/ui/src/components/designer/SpecPanel.tsx`
- Modify: `web/ui/src/components/designer/DesignerSurface.tsx` (436 lines — read it before editing; its SSE/recovery logic is subtle and must not be disturbed)
- Test: `web/ui/src/components/designer/specpanel.test.tsx`

**Interfaces:**
- Consumes: `pending_agent_md`, `pending_tools` from Task 1.
- Produces: `<SpecPanel agentMD={string} tools={Record<string,string>} />`.

- [ ] **Step 1: Write the failing tests**

```tsx
test("empty-states before a build", () => {
  render(<SpecPanel agentMD="" tools={{}} />);
  expect(screen.getByText(/nothing built yet/i)).toBeInTheDocument();
});

test("renders the brief as markdown, not raw text", () => {
  render(<SpecPanel agentMD={"# Daily digest\n\nSummarises your mail."} tools={{}} />);
  expect(screen.getByRole("heading", { name: "Daily digest" })).toBeInTheDocument();
});

test("lists tool files, collapsed, expandable", async () => {
  render(<SpecPanel agentMD="# X" tools={{ "tools/main.py": "print('hi')" }} />);
  expect(screen.getByText("tools/main.py")).toBeInTheDocument();
  expect(screen.queryByText("print('hi')")).not.toBeInTheDocument();
  fireEvent.click(screen.getByText("tools/main.py"));
  expect(screen.getByText("print('hi')")).toBeInTheDocument();
});

test("shows the schedule in plain language", () => {
  render(<SpecPanel agentMD={"# Suggested schedule: */10 * * * *\n# X"} tools={{}} />);
  expect(screen.getByText(/every 10 minutes/i)).toBeInTheDocument();
});

test("lists declared skills and connections", () => {
  render(<SpecPanel agentMD={"# Skills: pdf, web-search\n# Connections: gmail\n# X"} tools={{}} />);
  expect(screen.getByText(/pdf/)).toBeInTheDocument();
  expect(screen.getByText(/gmail/)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run to verify failure.** Expected: module not found.

- [ ] **Step 3: Implement `SpecPanel`**

Parse the AGENT.md headers with small exported pure helpers so they are unit-testable: `parseSchedule(md)`, `parseSkills(md)`, `parseConnections(md)`.

`parseSchedule` returns plain language for the common cases and falls back to the raw expression when it does not recognise the shape — **do not build a general cron parser**. Handle: `*/N * * * *` → "every N minutes"; `0 * * * *` → "every hour"; `0 H * * *` → "every day at HH:00"; `0 H * * D` → "every \<weekday\> at HH:00"; anything else → the raw expression prefixed with "schedule: ". A wrong plain-language translation is worse than showing the cron, so only claim what you can prove.

For markdown rendering use `react-markdown`, which is already a dependency (`web/ui/package.json:37`) and already used elsewhere in the SPA — find the existing caller and match how it is configured. Do not add a library.

For tool file bodies, reuse SP7's read-only code presentation (`web/ui/src/pages/kb/FileViewer.tsx`) rather than inventing a second monospace style.

- [ ] **Step 4: Wire into `DesignerSurface`**

Add the panel as a second view alongside the transcript (a tab or a toggle — match the surrounding UI's conventions). Feed it from the design-state response. **Do not alter the SSE attach/recovery logic** (`attachSourceRef`, `doneRef`, the state GET) — it took three rounds to get right in a previous sub-plan and is load-bearing.

- [ ] **Step 5: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(designer): show the built spec before approval"
```

---

### Task 3: Agent templates

**Files:**
- Create: `web/ui/src/pages/agents/templates.ts`
- Modify: `web/ui/src/pages/agents/AgentNewPage.tsx` (108 lines)
- Test: `web/ui/src/pages/agents/templates.test.tsx`

**Interfaces:**
- Produces: `AGENT_TEMPLATES: { id, label, blurb, description }[]`.

- [ ] **Step 1: Write the failing tests**

```tsx
test("picking a template fills the description", async () => {
  render(<AgentNewPage />);   // follow the existing agents test harness
  fireEvent.click(screen.getByRole("button", { name: /daily digest/i }));
  expect(screen.getByLabelText(/what should it do/i)).toHaveValue(
    expect.stringContaining("summar"),
  );
});

test("the filled description stays editable", async () => {
  // pick a template, then type — the text must change
});

test("start from scratch leaves the field blank", async () => {
  fireEvent.click(screen.getByRole("button", { name: /from scratch/i }));
  expect(screen.getByLabelText(/what should it do/i)).toHaveValue("");
});

test("no template text mentions implementation", () => {
  const banned = /\b(script|python|cron|file|json|webhook|endpoint|api key)\b/i;
  for (const t of AGENT_TEMPLATES) {
    expect(t.description).not.toMatch(banned);
  }
});
```

That last test is the one that keeps this honest — the designer prompts enforce a non-technical register and the templates must not undercut it.

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Write the templates**

Six entries per spec §5: Daily digest, Watch for changes, Inbox triage, Scheduled report, Reminder with context, Start from scratch (empty `description`).

Each `description` is 1–3 sentences in the user's voice, saying **what they want**, never how to build it. Example shape:

```ts
{
  id: "daily-digest",
  label: "Daily digest",
  blurb: "A morning summary of something you care about",
  description:
    "Every morning, look through my email from the last day and send me a short summary of anything that needs my attention. Skip newsletters and automated notifications.",
}
```

- [ ] **Step 4: Render them in `AgentNewPage`**

A row/grid of selectable cards above the description field, each showing `label` + `blurb`. Selecting fills the description. Nothing is locked — the field stays a normal editable textarea.

- [ ] **Step 5: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(agents): template starting points for agent creation"
```

---

### Task 4: Keyboard model

**Files:**
- Create: `web/ui/src/lib/useKeyboardNav.ts`, `web/ui/src/components/shell/ShortcutsOverlay.tsx`
- Modify: `web/ui/src/components/shell/AppShell.tsx`
- Test: `web/ui/src/lib/keyboardnav.test.tsx`

**Interfaces:**
- Produces: a global key handler mounted in `AppShell`, and `<ShortcutsOverlay>`.

- [ ] **Step 1: Write the failing tests — the suppression test FIRST**

```tsx
test("single-key shortcuts do not fire while typing", async () => {
  render(<AppShellWithInput />);
  const input = screen.getByRole("textbox");
  input.focus();
  fireEvent.keyDown(input, { key: "j" });
  expect(navigateSpy).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "?" });
  expect(screen.queryByRole("dialog", { name: /shortcuts/i })).not.toBeInTheDocument();
});

test("cmd+1..7 navigate to the rail destinations", () => {
  fireEvent.keyDown(document.body, { key: "1", metaKey: true });
  expect(navigateSpy).toHaveBeenCalledWith("/");
  fireEvent.keyDown(document.body, { key: "3", metaKey: true });
  expect(navigateSpy).toHaveBeenCalledWith("/agents");
});

test("? opens the shortcuts overlay and Esc closes it", () => { /* … */ });

test("j/k move the highlight and Enter opens", () => { /* … */ });
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement the suppression guard first**

```ts
function isTypingTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName.toLowerCase();
  return (
    tag === "input" ||
    tag === "textarea" ||
    el.isContentEditable ||
    el.closest("[cmdk-root]") !== null
  );
}
```

Single-key handlers return early when `isTypingTarget(e.target)`. Modifier shortcuts (⌘1–7) do not.

- [ ] **Step 4: Rail navigation**

Destinations in `IconRail`'s existing order — `/`, `/kb`, `/agents`, `/skills`, `/connections`, `/chats`, `/secrets`. Map ⌘/Ctrl+1..7 to that array. Call `e.preventDefault()` so the browser does not also act.

**Verify on the real browser whether ⌘1–7 is swallowed for tab-switching** (spec §9). If it is, report it — do not silently pick a different chord without saying so.

- [ ] **Step 5: List navigation**

`j`/`k` move a highlight index within the active context-pane list; `Enter` activates the highlighted row. Keep this simple: a hook the list components opt into, not a global registry. If the KB tree's existing interaction conflicts, exclude the tree and say so in your report (spec §9 sanctions this).

- [ ] **Step 6: Shortcuts overlay**

A dialog listing every shortcut including the pre-existing ⌘K, ⌘J, ⌘S, and the ⌘K scope prefixes from Task 5. Opened with `?`, closed with `Esc`. Use the existing dialog primitive; give it an accessible name.

- [ ] **Step 7: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(ui): keyboard navigation and a shortcuts overlay"
```

---

### Task 5: Palette recents and scoping

**Files:**
- Modify: `web/ui/src/components/search/CommandPalette.tsx` (197 lines)
- Create: `web/ui/src/components/search/recents.ts`
- Test: extend `web/ui/src/components/search/CommandPalette.test.tsx`, create `recents.test.ts`

- [ ] **Step 1: Write the failing tests**

```ts
test("recents round-trip and cap at 8", () => {
  for (let i = 0; i < 12; i++) pushRecent({ id: `n${i}`, kind: "note", label: `N${i}`, url: `/kb?path=n${i}` });
  const r = readRecents();
  expect(r).toHaveLength(8);
  expect(r[0].id).toBe("n11");           // newest first
});

test("re-opening an item moves it to the front without duplicating", () => { /* … */ });

test("a corrupt stored value falls back to an empty list", () => {
  localStorage.setItem("sa.recents", "not json");
  expect(readRecents()).toEqual([]);
});
```

```tsx
test("recents show when the input is empty", () => { /* … */ });

test("a scope prefix filters to that kind", async () => {
  // type "#" → only note results; the scope badge shows
});

test("backspace on an empty input clears the scope", async () => { /* … */ });
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement `recents.ts`**

`readRecents()` / `pushRecent(entry)` over one `localStorage` key (`sa.recents`), newest-first, capped at 8, de-duplicated by id. Store **only** `{ id, kind, label, url }` — never note content. A parse failure returns `[]` rather than throwing (mirrors `usePaneWidth`'s corrupt-value handling).

- [ ] **Step 4: Wire into the palette**

Show recents in their own `CommandGroup` when the input is empty. Record a recent in the existing selection handler (~:109, where it calls `navigate(url)`).

Scope prefixes: `>` agents, `#` notes, `@` chats. When the input starts with one, strip it, set the scope, and filter results to that kind. Show the active scope as a badge in the input row. `Backspace` on an empty input clears it.

- [ ] **Step 5: Add the prefixes to the shortcuts overlay** (Task 4's component) so they are discoverable.

- [ ] **Step 6: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(search): palette recents and scope prefixes"
```

---

### Task 6: Verification and docs

**Files:**
- Create: `docs/superpowers/sp9-smoke.md`
- Modify: `CLAUDE.md` (SPA section)

- [ ] **Step 1: Full verification**

```bash
cd web/ui && npx vitest run && npm run build && npx tsc -b
cd /home/rookie/simple-agents-v2 && go build ./... && go test ./... -count=1 -timeout 120s
git ls-files web/ui/dist/.gitkeep
```

- [ ] **Step 2: Write `docs/superpowers/sp9-smoke.md`**

The operator runs this by hand on a real server with no staging. Cover: creating an agent from a template and confirming the designer's first reply engages with the brief; the spec panel showing the built AGENT.md and tool files before approval; ⌘1–7 navigation (**and whether the browser swallows it**); `j`/`k`/`Enter` in a list; `?` overlay; typing `j` in the note editor and confirming it types rather than navigates; ⌘K recents and each scope prefix.

- [ ] **Step 3: Update `CLAUDE.md`**

Add the spec panel, templates, keyboard model, and palette recents/scoping to the SPA description. Match the document's existing terse, technical voice. No changelog entries, no dated history.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "docs: SP9 power-and-creation close-out"
```

---

## Notes for the executing agent

- Task 2 needs Task 1. Task 5's prefixes are listed by Task 4's overlay, so do 4 before 5. Tasks 3 is independent. Execute in numeric order.
- `DesignerSurface.tsx`'s SSE recovery logic is load-bearing and took three review rounds to stabilise in a previous sub-plan. Add alongside it; do not refactor it.
- If a template's wording or a schedule translation feels wrong when you read it back, say so in your report rather than shipping it — these are user-facing text, and a plausible-but-wrong plain-language schedule is worse than showing the raw cron.
