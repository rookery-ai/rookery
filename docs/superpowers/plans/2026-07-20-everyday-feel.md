# Everyday Feel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the SPA feel good in daily use — a resizable context pane, a readable inbox, undoable deletes, consistent panes, and an accessibility floor.

**Architecture:** All work is in the React SPA (`web/ui`) except one DTO field in `web/api_home.go`. New shared primitives live in `web/ui/src/components/shell/`. No new backend endpoints, no schema changes.

**Tech Stack:** React 19 + TypeScript + Vite 8 + Tailwind 4 + shadcn/ui (CLI pinned to `shadcn@3`) + TanStack Query + React Router v8. Tests: vitest + @testing-library/react.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-20-everyday-feel-design.md`. It governs; this plan implements it.
- **Baseline: 417 frontend tests green.** Do not regress any. Run `cd web/ui && npx vitest run`.
- **`web/ui/dist/.gitkeep` must stay git-tracked** or `go:embed all:dist` breaks on a fresh clone. Verify with `git ls-files web/ui/dist/.gitkeep` before every commit.
- **Every behaviour must be pinned by a test that fails without its implementation.** The SP7 review found tests that passed either way. Red-verify.
- Context pane range: **200px–560px**, default **256px**.
- Undo window: **5 seconds**. Deferred-commit, never server-side restore.
- Undo scope: **inbox messages and reminders only**. KB note deletion is explicitly out of scope.
- `muted-2` is for metadata only (timestamps, counts, hints). Content/body text uses the primary foreground token.
- No external network requests — a strict CSP applies. All assets bundled.
- Run `npm run build` before committing frontend work.

---

### Task 1: Resizable context pane

**Files:**
- Create: `web/ui/src/components/shell/usePaneWidth.ts`
- Modify: `web/ui/src/components/shell/AppShell.tsx` (the `aside` at ~:60)
- Test: `web/ui/src/components/shell/panewidth.test.tsx`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `usePaneWidth()` returning `{ width: number, setWidth: (n:number)=>void, reset: ()=>void }`, and a `<PaneResizeHandle>` component. Task 4 does not depend on these.

- [ ] **Step 1: Write the failing test**

```tsx
// panewidth.test.tsx
import { render, screen } from "@testing-library/react";
import { clampPaneWidth, PANE_MIN, PANE_MAX, PANE_DEFAULT, readStoredWidth } from "./usePaneWidth";

test("clamps to range", () => {
  expect(clampPaneWidth(50)).toBe(PANE_MIN);
  expect(clampPaneWidth(9999)).toBe(PANE_MAX);
  expect(clampPaneWidth(300)).toBe(300);
});

test("corrupt stored value falls back to default", () => {
  localStorage.setItem("sa.paneWidth", "not-a-number");
  expect(readStoredWidth()).toBe(PANE_DEFAULT);
  localStorage.setItem("sa.paneWidth", "99999");
  expect(readStoredWidth()).toBe(PANE_DEFAULT);
  localStorage.removeItem("sa.paneWidth");
  expect(readStoredWidth()).toBe(PANE_DEFAULT);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web/ui && npx vitest run src/components/shell/panewidth.test.tsx`
Expected: FAIL — cannot resolve `./usePaneWidth`.

- [ ] **Step 3: Implement `usePaneWidth.ts`**

Export `PANE_MIN = 200`, `PANE_MAX = 560`, `PANE_DEFAULT = 256`, `STORAGE_KEY = "sa.paneWidth"`.

`clampPaneWidth(n)` → `Math.min(PANE_MAX, Math.max(PANE_MIN, n))`.

`readStoredWidth()` → read `localStorage`, `parseInt`; return `PANE_DEFAULT` when absent, `NaN`, or outside `[PANE_MIN, PANE_MAX]`. **An out-of-range stored value returns the default, it is not clamped** — an out-of-range value means the storage is untrustworthy, not that the user wanted the extreme.

`usePaneWidth()` mirrors `theme.tsx`: lazy `useState(readStoredWidth)`, and an effect writing `localStorage.setItem(STORAGE_KEY, String(width))`. `reset()` sets `PANE_DEFAULT`.

- [ ] **Step 4: Add `PaneResizeHandle`**

In the same file or a sibling — a 4px-wide grab strip absolutely positioned on the aside's trailing edge, `cursor-col-resize`.

Required attributes: `role="separator"`, `aria-orientation="vertical"`, `aria-label="Resize sidebar"`, `aria-valuenow={width}`, `aria-valuemin={PANE_MIN}`, `aria-valuemax={PANE_MAX}`, `tabIndex={0}`.

Pointer: `onPointerDown` → `e.currentTarget.setPointerCapture(e.pointerId)`, record start x and start width; `onPointerMove` while captured → `setWidth(clampPaneWidth(startW + (e.clientX - startX)))`; `onPointerUp` → release capture. Set `document.body.style.userSelect = "none"` during drag, restore on release.

Keyboard `onKeyDown`: `ArrowLeft` → `setWidth(clamp(width - 16))`, `ArrowRight` → `+16`, `Home` → `PANE_MIN`, `End` → `PANE_MAX`. Call `e.preventDefault()` on each.

`onDoubleClick` → `reset()`.

- [ ] **Step 5: Wire into `AppShell`**

Replace the aside's `w-64` with `style={{ width }}` and keep `shrink-0`. Render `<PaneResizeHandle>` inside the aside, positioned on its trailing edge. The aside must become `relative` for the handle's absolute positioning. Keep `hidden md:flex`.

- [ ] **Step 6: Add interaction tests**

```tsx
test("arrow keys resize and Home/End jump", async () => {
  render(<AppShellWithPane />);           // follow existing AppShell test harness
  const sep = screen.getByRole("separator", { name: /resize sidebar/i });
  sep.focus();
  await userEvent.keyboard("{ArrowRight}");
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_DEFAULT + 16));
  await userEvent.keyboard("{Home}");
  expect(sep).toHaveAttribute("aria-valuenow", String(PANE_MIN));
});
```

- [ ] **Step 7: Run all tests + build**

Run: `cd web/ui && npx vitest run && npm run build`
Expected: all green, 417 + new tests. Verify `git ls-files web/ui/dist/.gitkeep`.

- [ ] **Step 8: Commit**

```bash
git add -A && git commit -m "feat(ui): resizable context pane with keyboard support"
```

---

### Task 2: Toast host and live region

**Files:**
- Create: `web/ui/src/components/shell/Toast.tsx`
- Modify: `web/ui/src/components/shell/AppShell.tsx`
- Test: `web/ui/src/components/shell/toast.test.tsx`

**Interfaces:**
- Produces: `useToast()` → `{ toast(opts) }` where `opts = { message: string, action?: { label: string, onClick: () => void }, variant?: "default" | "error", durationMs?: number }`. `toast()` returns a dismiss function. **Task 3 depends on this exact shape.**
- Produces: `<ToastHost>` mounted once in `AppShell`.

**Do not add a dependency.** shadcn's toast/sonner is not installed; a local implementation is ~80 lines, has no CSP implications, and gives exact control over the undo timing Task 3 needs. Build it.

- [ ] **Step 1: Write the failing test**

```tsx
test("toast renders, auto-dismisses, and announces politely", async () => {
  vi.useFakeTimers();
  render(<Harness />);                     // a button calling toast({message:"Saved"})
  await userEvent.click(screen.getByRole("button", { name: /go/i }));
  expect(screen.getByText("Saved")).toBeInTheDocument();
  const live = document.querySelector('[aria-live="polite"]');
  expect(live).toHaveTextContent("Saved");
  act(() => { vi.advanceTimersByTime(5000); });
  expect(screen.queryByText("Saved")).not.toBeInTheDocument();
});

test("action button fires and dismisses", async () => {
  const onClick = vi.fn();
  // toast({ message: "Deleted", action: { label: "Undo", onClick } })
  await userEvent.click(screen.getByRole("button", { name: "Undo" }));
  expect(onClick).toHaveBeenCalledOnce();
  expect(screen.queryByText("Deleted")).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web/ui && npx vitest run src/components/shell/toast.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

A context provider holding `toasts: Toast[]`. `toast(opts)` pushes with a generated id and schedules removal after `durationMs ?? 5000`. Clicking the action calls `onClick` then removes immediately. A dismiss (×) button removes without firing the action.

Render toasts fixed bottom-right, stacked, `z-50`. Include a single always-mounted `<div aria-live="polite" aria-atomic="true" className="sr-only">` containing the newest toast's message — this is the a11y live region for the whole app.

Animations use `motion-safe:` variants so reduced-motion users get no transition.

- [ ] **Step 4: Mount in `AppShell`**

Wrap the shell contents in the provider and render `<ToastHost/>` once, inside `TooltipProvider`.

- [ ] **Step 5: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(ui): toast host with polite live region"
```

---

### Task 3: Deferred-commit undo for inbox and reminders

**Files:**
- Create: `web/ui/src/lib/useDeferredDelete.ts`
- Modify: `web/ui/src/pages/home/HomePage.tsx` (inbox card delete, reminder row delete)
- Test: `web/ui/src/lib/deferreddelete.test.tsx`

**Interfaces:**
- Consumes: `useToast()` from Task 2 (exact shape above).
- Produces: `useDeferredDelete({ commit, onRestore })` → `{ schedule(id, label), flushAll() }`.

**The contract, restated because it is the whole point:** the API call is NOT made on click. It is made when the 5s window expires. Undo means it is never made at all.

- [ ] **Step 1: Write the failing tests — all four rules**

```tsx
test("delete hides row and does NOT call the API within the window", async () => {
  const commit = vi.fn();
  // schedule("m1", "Message deleted")
  expect(screen.queryByText("m1 body")).not.toBeInTheDocument();
  expect(commit).not.toHaveBeenCalled();
});

test("undo cancels the call entirely", async () => {
  await userEvent.click(screen.getByRole("button", { name: "Undo" }));
  act(() => { vi.advanceTimersByTime(10000); });
  expect(commit).not.toHaveBeenCalled();
});

test("expiry commits", () => {
  act(() => { vi.advanceTimersByTime(5000); });
  expect(commit).toHaveBeenCalledWith("m1");
});

test("failed commit restores the row and shows an error toast", async () => {
  commit.mockRejectedValueOnce(new Error("boom"));
  act(() => { vi.advanceTimersByTime(5000); });
  await waitFor(() => expect(onRestore).toHaveBeenCalledWith("m1"));
  expect(screen.getByText(/couldn't delete/i)).toBeInTheDocument();
});

test("flushAll commits pending deletes immediately (navigation/unmount)", () => {
  // schedule then flushAll without advancing timers
  expect(commit).toHaveBeenCalledWith("m1");
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web/ui && npx vitest run src/lib/deferreddelete.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `useDeferredDelete`**

Hold a `Map<string, ReturnType<typeof setTimeout>>` in a ref. `schedule(id, label)`:
1. Add `id` to a `pending` set (the caller filters pending ids out of its rendered list).
2. `toast({ message: label, action: { label: "Undo", onClick: () => cancel(id) } })`.
3. `setTimeout(() => run(id), 5000)`.

`cancel(id)` clears the timer, removes from pending, removes the map entry — no API call.

`run(id)` removes the timer entry, then `await commit(id)`; on rejection call `onRestore(id)`, remove from pending, and `toast({ variant: "error", message: "Couldn't delete — it's back in the list." })`.

`flushAll()` clears every timer and calls `run(id)` for each pending id synchronously.

Effect on mount: `window.addEventListener("beforeunload", flushAll)`; cleanup removes it **and calls `flushAll()`** so unmount (route change) also commits. Idempotence: `run` is a no-op if the id is no longer in the map.

- [ ] **Step 4: Wire into HomePage**

Inbox: replace `del.mutate(msg.id)` with `schedule(msg.id, "Notification deleted")`, and filter pending ids out of `messages` before rendering. `commit` is the existing delete mutation's `mutateAsync`; `onRestore` invalidates the inbox query so the row reappears from the server.

Reminders: same pattern with the reminder delete mutation and "Reminder deleted".

- [ ] **Step 5: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(ui): undoable deletes for inbox and reminders"
```

---

### Task 4: Context-pane primitives

**Files:**
- Create: `web/ui/src/components/shell/ContextPaneParts.tsx`
- Modify: all five pages rendering a `<ContextPane>` (verified list, no need to re-derive):
  `pages/home/HomePage.tsx`, `pages/kb/KBPage.tsx`, `pages/chats/ChatsPage.tsx`,
  `pages/connections/ConnectionsPage.tsx`, `pages/settings/SettingsPage.tsx`
- Test: `web/ui/src/components/shell/contextpaneparts.test.tsx`

**Interfaces:**
- Produces: `<ContextPaneHeader title action?>` and `<ContextSection title action? children>`. Task 5 renders the inbox inside a `ContextSection`.

- [ ] **Step 1: Write the failing test**

```tsx
test("header renders title and action slot", () => {
  render(<ContextPaneHeader title="Home" action={<button>+</button>} />);
  expect(screen.getByRole("heading", { name: "Home" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "+" })).toBeInTheDocument();
});

test("section renders an uppercase-styled heading", () => {
  render(<ContextSection title="Reminders"><p>x</p></ContextSection>);
  expect(screen.getByRole("heading", { name: "Reminders" })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run to verify failure.** Expected: module not found.

- [ ] **Step 3: Implement, encoding today's canonical values**

`ContextPaneHeader`: `<div className="flex items-center justify-between px-4 pt-3 pb-1"><h2 className="text-sm font-bold">{title}</h2>{action}</div>`

`ContextSection`: heading `<h3 className="mb-1.5 px-1 text-[11px] font-bold uppercase tracking-wide text-muted-2">` with an optional right-aligned action, then children.

These are exactly the values `HomePage` uses today, so adopting them is a no-op there and a correction elsewhere.

- [ ] **Step 4: Adopt in every context pane**

Replace hand-rolled headers/sections. Note any page whose appearance changes and say so in your report — a change means that page had drifted, which is the point, but it must be deliberate.

- [ ] **Step 5: Run all + build, then commit**

```bash
git add -A && git commit -m "refactor(ui): shared context-pane primitives"
```

---

### Task 5: Inbox redesign

**Files:**
- Modify: `web/api_home.go` (add `agent_id` to `apiInboxMessage` + its mapper)
- Modify: `web/ui/src/lib/home.ts` (add `agent_id` to the `InboxMessage` type)
- Modify: `web/ui/src/pages/home/HomePage.tsx` (`InboxCard`, `InboxSection`)
- Test: `web/api_home_test.go` (DTO field), `web/ui/src/pages/home/inbox.test.tsx`

**Interfaces:**
- Consumes: `ContextSection` (Task 4), `useDeferredDelete` (Task 3).

- [ ] **Step 1: Backend — add `agent_id`**

`db.InboxMessage` already has `AgentID` (`internal/db/models.go:174`). Add `AgentID string \`json:"agent_id"\`` to `apiInboxMessage` (`web/api_home.go:57-66`) and map it in `toAPIInboxMessage`. It is empty for reminders by design.

Go test: assert the JSON body of the inbox list endpoint contains `agent_id` for an agent-sourced message.

Run: `go test ./web/... -count=1`

- [ ] **Step 2: Write the failing frontend tests**

```tsx
test("groups messages under Today / Yesterday / date headers", () => {
  vi.setSystemTime(new Date("2026-07-20T12:00:00Z"));   // deterministic clock
  // messages at 2026-07-20, 2026-07-19, 2026-07-14
  expect(screen.getByText("Today")).toBeInTheDocument();
  expect(screen.getByText("Yesterday")).toBeInTheDocument();
  expect(screen.getByText(/14 Jul/)).toBeInTheDocument();
});

test("error status renders a badge; ok status does not", () => {
  // two messages, status "error" and "ok"
  expect(screen.getByText(/failed/i)).toBeInTheDocument();
  expect(screen.getAllByText(/failed/i)).toHaveLength(1);
});

test("expanding reveals full body and the View agent link", async () => {
  await userEvent.click(screen.getByRole("button", { name: /daily digest/i }));
  expect(screen.getByRole("link", { name: /view agent/i }))
    .toHaveAttribute("href", "/agents/agent-1");
});

test("a reminder-sourced message offers no View agent link", async () => {
  // agent_id: "" → link absent
});
```

- [ ] **Step 3: Run to verify failure.** Expected: FAIL on all four.

- [ ] **Step 4: Implement**

Add a `groupByDay(messages, now)` helper — pure, exported, unit-testable. Returns `[{ label, messages }]` with labels `"Today"`, `"Yesterday"`, else `toLocaleDateString(undefined,{weekday:"short",day:"numeric",month:"short"})`. Group headers are sticky (`sticky top-0 bg-chrome/95 backdrop-blur z-10`).

`InboxCard` per §5.2:
- Row 1: icon · name · `{status === "error" && <Badge variant="danger">Failed</Badge>}` · relative time (`ml-auto`, `text-muted-2`).
- Row 2: body in the primary foreground token, `line-clamp-3` when collapsed.
- Unread: `border-l-2 border-primary` on the card plus `font-medium` on the name. **Remove the dot.**
- Expanded: full body, a trigger line when `trigger` is non-empty, then actions — `View agent` (`<Link to={"/agents/"+agent_id}>`, rendered only when `agent_id` is non-empty) and Delete (via Task 3's `schedule`).

`InboxSection` uses `ContextSection` with the unread count as a badge in its action slot alongside a "Mark all read" button.

- [ ] **Step 5: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(ui): readable inbox — day grouping, status, deep links"
```

---

### Task 6: Reduced motion and contrast retune

**Files:**
- Modify: `web/ui/src/index.css` (or wherever the theme tokens and base layer live — `grep -rn "prefers-color-scheme\|--background" web/ui/src`)
- Test: `web/ui/src/styles.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { readFileSync } from "node:fs";
const css = readFileSync(new URL("./index.css", import.meta.url), "utf8");

test("reduced-motion rule exists and disables animation", () => {
  expect(css).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)/);
  expect(css).toMatch(/animation-duration:\s*0\.01ms/);
});
```

- [ ] **Step 2: Run to verify failure.** Expected: FAIL — no such rule.

- [ ] **Step 3: Add the reduced-motion base rule**

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }
}
```

- [ ] **Step 4: Contrast retune**

Compute the contrast ratio of each muted foreground token against its background in **both** themes. Any token used for text that falls below **4.5:1** is lifted at its definition until it passes. Do not override per-component.

Record the before/after ratio for every token you touch in your report. If every token already passes, say so and change nothing — do not repaint for its own sake.

- [ ] **Step 5: Run all + build, then commit**

```bash
git add -A && git commit -m "feat(ui): respect reduced motion; lift muted-token contrast"
```

---

### Task 7: Verification sweep and docs

**Files:**
- Modify: `CLAUDE.md` (SPA section — note the resizable pane, toast/undo system, and context-pane primitives)
- Test: full suites

- [ ] **Step 1: Full verification**

```bash
cd web/ui && npx vitest run && npm run build && npx tsc -b
cd /home/rookie/simple-agents-v2 && go build ./... && go test ./... -count=1 -timeout 120s
git ls-files web/ui/dist/.gitkeep
```

Expected: all green; `.gitkeep` tracked.

- [ ] **Step 2: Manual-check list for the operator**

Write `docs/superpowers/sp8-smoke.md` listing what to verify by hand: drag the pane and reload (width persists); tab to the handle and arrow-resize; delete an inbox message and hit Undo; delete one and let it expire; inbox day grouping across a date boundary; both themes.

- [ ] **Step 3: Update `CLAUDE.md`**

Add the new primitives to the SPA description. Match the document's existing density and voice. No changelog entries.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "docs: SP8 everyday-feel close-out"
```

---

## Notes for the executing agent

- Tasks 1, 2, 4, and 6 are independent. Task 3 needs Task 2. Task 5 needs Tasks 3 and 4. Execute in numeric order.
- The inbox is the operator's own complaint. If the day-grouping/status/unread treatment lands and it still reads as a wall of grey, say so in your report rather than declaring it done.
- Where this plan names a Tailwind class, it is a starting point, not a contract — match the surrounding code's conventions if they differ.
