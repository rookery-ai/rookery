# Everyday Feel — Design

**Date:** 2026-07-20
**Status:** Approved (operator delegated design authority: spec and implement without an approval gate)
**Scope:** Sub-plan 8 of the post-redesign track. SP7 (agent files as documents) shipped 2026-07-20. SP9 (power & creation) is a separate spec.

---

## 1. Problem

The redesign shipped a coherent structure, but daily use exposes friction the structure doesn't fix. Two complaints came from the operator directly:

- **The inbox is unreadable.** It is the first thing on the home page and the primary way agents reach you in the web UI, and it reads as a wall of grey.
- **The middle pane can't be resized.** It is a hard-coded `w-64` (`AppShell.tsx:60`). A knowledge-base tree or a long inbox has 256px forever, regardless of screen width.

Three more surfaced from review, all of the same character — things a person notices on the tenth use, not the first:

- **Destructive actions have no undo and no confirmation feedback.** Deleting an inbox message, a reminder, or a note is immediate and silent. There is no toast system anywhere in the SPA (`grep sonner|useToast|Toaster` → no hits).
- **Context panes drifted.** Each page hand-rolls its own header and section markup, so padding, heading case, and spacing differ page to page.
- **No accessibility floor.** Zero `aria-live` regions and zero `prefers-reduced-motion` handling in the entire SPA. Async events (a run finishing, a save failing) are invisible to a screen reader, and every animation plays at full motion regardless of OS preference.

## 2. Goals

- The context pane is resizable, with the width remembered across sessions.
- The inbox is scannable: you can tell at a glance what happened, when, and whether it needs you.
- Destructive actions are undoable for a few seconds and always acknowledged.
- Context panes share one set of primitives, so they look like one product.
- A basic accessibility floor: live regions for async events, reduced-motion respected, keyboard-operable resize.

## 3. Non-goals

- A full a11y audit or WCAG certification. This establishes a floor, not compliance.
- Undo for anything that isn't a simple delete. No undo stack, no multi-step history.
- Resizable *content* panes or a full drag-and-drop layout system. One handle, one pane.
- Mobile layout rework. The context pane is `hidden md:flex`; resize is a desktop affordance.
- Restyling the KB editor, agent designer, or chat surfaces. SP9 owns those.

## 4. Resizable context pane

`AppShell.tsx` renders the pane as a fixed `w-64 shrink-0` aside. It becomes width-controlled with a drag handle on its trailing edge.

- **Range:** 200px–560px, clamped. Below 200 the content is unusable; above 560 it crowds the main pane on a 1366px laptop (the operator's screen).
- **Default:** 256px — today's `w-64`, so nothing moves until the user drags.
- **Persistence:** one `localStorage` key, mirroring the existing `theme.tsx` pattern (read lazily in `useState`'s initializer, write on change). A corrupt or out-of-range stored value falls back to the default rather than throwing.
- **Reset:** double-click the handle restores 256px.
- **Keyboard:** the handle is a real `role="separator"` with `aria-orientation="vertical"`, `aria-valuenow/min/max`, and `tabIndex={0}`. Left/Right arrows resize by 16px, Home/End jump to min/max. This is the difference between an accessible control and a mouse-only one, and it costs a keydown handler.
- **Pointer:** uses Pointer Events (`setPointerCapture`) so a fast drag that leaves the handle doesn't drop the gesture. Dragging sets `user-select: none` on the body and clears it on release.

The width applies to the `aside` via inline style, not a Tailwind class — arbitrary pixel values can't be class names, and the value is dynamic.

## 5. Inbox redesign

### 5.1 What's wrong now

`HomePage.tsx:28-105`. Every message is the same weight: a 3.5px icon, the agent name in `font-semibold`, a 2-line-clamped body in `text-muted-2`, and a timestamp in `text-[10px] text-muted-2/70` — three sizes of grey in a 256px column. Unread is a 6px dot. Clicking expands, and the entire payoff is a Delete button. Messages from different days sit flush against each other with nothing marking the boundary.

Worse, the API already returns `trigger` and `status` (`web/api_home.go:57-66`) and the UI renders neither. `status` is the actionable signal — it distinguishes a routine notification from a failed run — and it is currently discarded.

### 5.2 The redesign

**Day grouping.** Messages group under sticky day headers: `Today`, `Yesterday`, then `Mon, 14 Jul`. This is the single highest-value change — it turns an undifferentiated stream into a timeline.

**Card anatomy.** Two rows instead of three sizes of grey:
- Row 1: source icon · name (agent name, or "Reminder" for reminder-sourced) · relative time, right-aligned and de-emphasized.
- Row 2: the body at `text-xs` in the **primary** foreground colour, clamped to 3 lines when collapsed.

**Unread** is a 2px left accent bar plus a medium-weight name — a whole-card signal, not a dot you have to hunt for. The dot is removed.

**Status.** `db.InboxMessage.Status` is exactly `"ok" | "error"`. An `"error"` message shows a small danger-toned badge next to the name; `"ok"` renders nothing — a badge on every card would be noise. `trigger` (`cron|manual|chat`, empty for reminders) is surfaced in the expanded view only: it is context, not a scanning aid.

**Expanded view** earns the click: full untruncated body, the trigger line, and actions — "View agent" (deep-links to `/agents/<id>`, when the message has an agent) and Delete.

**Header** shows `Inbox` with the unread count as a badge rather than inline text, and "Mark all read" as a proper small button.

The empty state and its "connect a chat app" nudge are preserved as-is; they already work.

### 5.3 Data

Rendering `status` and `trigger` requires no API change — both are already on `apiInboxMessage` (`web/api_home.go:57-66`) and already populated.

Deep-linking to the agent does require one small backend change: `db.InboxMessage` carries `AgentID` (`internal/db/models.go:174`) but `apiInboxMessage` does not expose it. Add `agent_id` to the DTO and its mapper. It is empty for reminder-sourced messages by design, and an empty id simply means the "View agent" action is not offered.

## 6. Toast and undo

### 6.1 Toasts

Add a toast system. Toasts are rendered from a single host mounted in `AppShell`, so any page can raise one.

Toasts are for **confirming an action**, not for reporting inline form errors — those stay inline where the user is looking (the existing `NoteEditor` error-banner pattern is correct and stays).

### 6.2 Undo — the delay pattern

Undo uses a **deferred-commit** model, not a server-side restore:

> On delete, the item disappears from the UI immediately and a toast appears with an Undo action. The API call is **not** made yet. If Undo is clicked, nothing was ever sent. If the toast expires (5 seconds), the delete is committed.

This is deliberate. A server-side undo would need a restore endpoint and soft-delete columns on every table — a schema change for a UI affordance. The deferred-commit pattern needs neither and is indistinguishable to the user for the window that matters.

Its one real hazard is losing the pending delete on navigation or reload. Rules:
- A pending delete **commits immediately** if the user navigates away or closes the tab (flush on unmount and on `beforeunload`). It is never silently dropped — a user who clicked Delete and left expects it gone.
- Pending deletes are keyed by item id; deleting the same item twice is idempotent.
- If the committed call fails, the item **returns** to the list and an error toast explains why. Failure must not look like success.

**Scope:** inbox messages and reminders. These are high-frequency, low-consequence deletes on the home page. KB note deletion is explicitly excluded — it already has a confirm dialog, its failure path was hardened during SP7, and layering deferred-commit onto a file operation with an unmount-flush interaction is exactly the shape of the four data-loss bugs the redesign already shipped once.

## 7. Context-pane consistency

Introduce two shared primitives and adopt them in every context pane:

- `ContextPaneHeader` — the pane title row (title, optional action slot).
- `ContextSection` — a titled section with consistent heading treatment and spacing.

Today `HomePage` writes `h2 className="px-4 pt-3 pb-1 text-sm font-bold"` for its title and `h3 className="mb-1.5 px-1 text-[11px] font-bold uppercase tracking-wide text-muted-2"` for sections, and other pages differ. The primitives fix the values in one place. This is a refactor with no visual change on the pages that already match the canonical treatment, and small corrections on those that drifted.

## 8. Accessibility floor

- **Live region.** One polite `aria-live` region in the shell announces async outcomes (toasts route their message through it). A screen-reader user currently gets no signal at all when a background action completes.
- **Reduced motion.** A global `@media (prefers-reduced-motion: reduce)` rule disables transitions and animations. Tailwind's `motion-safe:`/`motion-reduce:` variants are used for any new animated affordance.
- **Focus visibility.** The resize handle and the new inbox interactive elements carry visible focus rings; the handle is reachable in tab order.
- **Naming.** Icon-only buttons introduced by this work carry `aria-label`s.

## 9. Dark mode and contrast retune

The inbox's readability problem is partly a token problem: body text renders in `text-muted-2`, a colour intended for metadata. The retune is narrow and rule-based, not a repaint:

- Body/content text uses the primary foreground token. `muted-2` is reserved for metadata (timestamps, counts, hints).
- Verify the muted tokens against their backgrounds in both themes and lift them if they fall below a 4.5:1 contrast ratio for text-sized use.
- The accent colour is checked in dark mode for the same reason.

Any token value that changes is changed at its definition, not overridden per-component.

## 10. Testing

- **Resize:** drag changes width; the value clamps at both ends; it persists across a remount; a corrupt stored value falls back to the default; arrow keys resize and Home/End jump; double-click resets.
- **Inbox:** messages group under correct day headers (with a fixed clock, so "Today" is deterministic); a failure `status` renders the badge and a normal one does not; unread renders the accent treatment; expanding reveals the full body and the agent deep-link; the empty state is unchanged.
- **Undo:** delete removes the row and raises a toast without calling the API; Undo cancels the call entirely; toast expiry commits it; a failed commit restores the row and shows an error; navigating away commits a pending delete rather than dropping it.
- **Context primitives:** a snapshot or structural assertion that panes render the shared header/section shape.
- **A11y:** the live region exists and receives toast text; the resize handle exposes correct `role`/`aria-value*`; reduced-motion CSS is present.

The frontend suite is the gate (`npx vitest run`, 417 tests green at merge). Every behaviour above must be pinned by a test that fails without its implementation — the SP7 review found more than one test that passed either way.

## 11. Risks

| Risk | Mitigation |
|---|---|
| Deferred-commit undo drops a delete on navigation | Flush on unmount and `beforeunload`; never silently discard |
| A failed committed delete looks like success | Row returns to the list, error toast explains |
| Resize handle unusable by keyboard | `role="separator"` + arrow/Home/End handlers, pinned by test |
| Contrast retune regresses the whole palette | Change tokens at definition, rule-based (content vs metadata), verified in both themes |
| Context-pane refactor causes visual drift | Primitives encode today's canonical values; pages that already match are unchanged |
| Inbox `agent_id` missing from the DTO | Deep-link action is simply not offered when absent |
