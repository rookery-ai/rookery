# SP8 "everyday feel" — manual smoke checklist

Sub-plan 8 shipped six UI-polish changes to the React SPA: a resizable context pane, a
toast/undo system, deferred-commit deletes for inbox + reminders, shared context-pane
primitives, an inbox redesign, and a reduced-motion + contrast pass. This is a manual
checklist for the operator to run by hand after this branch is merged and deployed —
none of this is covered by integration/e2e tests (the repo has none; see CLAUDE.md
"Known gaps"), so it's the only verification these changes get beyond unit tests.

Run through this once against the deployed app (`http://<host>:8080/`), logged into a
workspace with at least one inbox notification and one reminder. Where a step needs
data that doesn't exist yet (e.g. a failed agent run), create it first (see notes inline).

## 1. Context-pane resize — drag

1. Open Home (or any page with a context pane — Chats/Connections/KB/Settings all
   share the same pane).
2. Hover the pane's left edge until the cursor becomes a column-resize cursor; drag it
   left or right.
   - Good result: the pane visibly resizes as you drag, clamped between roughly 200px
     and 560px (dragging past either extreme stops growing/shrinking rather than
     jumping or glitching).
3. Release the drag at a non-default width, then reload the page (F5).
   - Good result: the pane opens at the width you left it at, not back at the default.
     (It's stored in `localStorage` under `sa.paneWidth`, and applies globally — a
     width set on one page carries over when you navigate to another.)
4. Double-click the resize handle.
   - Good result: the pane snaps back to its default width (256px).

## 2. Context-pane resize — keyboard only

1. Tab through the page until focus lands on the resize handle (it's a
   `role="separator"` between the list panel and the context pane — you'll see a focus
   ring on the thin vertical strip).
2. Press the Left/Right arrow keys.
   - Good result: the pane width steps by a fixed increment each press, with no mouse
     involved.
3. Press Home, then End.
   - Good result: Home jumps the pane to its minimum width, End to its maximum.

## 3. Undo a delete (inbox)

1. On Home, find a notification in the Inbox section and delete it (expand the card,
   click Delete).
   - Good result: the row disappears from the list immediately, and a toast appears
     bottom-right saying something like "Notification deleted" with an **Undo** action.
2. Click **Undo** before the toast disappears (you have 5 seconds).
   - Good result: the row reappears in the inbox list. Confirm via a hard refresh that
     it's genuinely still there server-side too — the delete call is never supposed to
     fire when Undo is clicked, so nothing should have changed on the backend.

## 4. Let a delete expire (inbox or reminder)

1. Delete another inbox message (or a reminder from the Reminders section) and this
   time do **not** click Undo — just wait.
   - Good result: the toast disappears on its own after ~5 seconds without your input.
2. Refresh the page.
   - Good result: the message/reminder is gone for real — it was actually deleted on
     the server, not just hidden client-side. (This is the flip side of #3: the delete
     only commits once the undo window has fully expired.)
3. Optional: repeat step 1, but instead of waiting, immediately navigate away to
   another page (e.g. click Chats) before the 5 seconds are up.
   - Good result: the delete still goes through — navigating away flushes any pending
     delete rather than silently dropping it. Confirm with a refresh.

## 5. Inbox day grouping and status

1. Look at the Inbox section on Home with more than one day's worth of notifications
   (if everything is from today, this step just confirms the "Today" header; check
   back tomorrow, or use existing older test data, to see "Yesterday" and dated
   headers appear too).
   - Good result: notifications are grouped under day headers reading "Today",
     "Yesterday", and (for anything older) a `Weekday, D Mon` label like "Mon, 14 Jul" —
     newest day first, and messages within a day stay in arrival order.
2. Find (or trigger) a notification from a **failed** agent run.
   - Good result: that card shows a small "Failed" badge next to the agent name, distinct
     from ordinary notifications.
3. Look at an unread vs. a read notification side by side.
   - Good result: unread cards carry a visible left accent bar (not just a hard-to-spot
     dot); clicking a card marks it read and the bar goes away.
4. Expand a notification that has a source agent and click "View agent".
   - Good result: it navigates straight to that agent's detail page.

**If, after all of the above, the inbox still reads as an undifferentiated wall of grey
at a glance — say so.** The point of this redesign was to fix exactly that complaint;
day headers, the Failed badge, and the unread bar should all be visually obvious
without hunting.

## 6. Light and dark theme

1. Go to Settings and switch the theme to Light, then to Dark (System also picks up
   the OS setting).
2. In each theme, revisit Home's inbox and the resize handle from steps 1–5 above.
   - Good result: text stays legible against its background in both themes — in
     particular, check any small pill/badge/chip-style backgrounds (status badges,
     unread counts) aren't washed out or invisible against the surrounding surface in
     either theme. This was the subject of a contrast retune plus a same-day follow-up
     fix, so it's worth a deliberate look rather than a glance.

## 7. Reduced motion

1. Enable "reduce motion" in your OS accessibility settings (macOS: System Settings →
   Accessibility → Display → Reduce Motion; Windows: Settings → Accessibility → Visual
   effects → Animation effects off; Linux/GNOME: Settings → Accessibility → Seeing →
   Reduce Animation).
2. Reload the app and trigger a toast (delete something, as in step 3).
   - Good result: the toast appears without a slide/fade-in animation — it should just
     show up, not animate in.
3. Turn "reduce motion" back off afterward if you don't want it system-wide.
