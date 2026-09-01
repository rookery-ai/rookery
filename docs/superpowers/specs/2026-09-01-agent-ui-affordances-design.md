# Four agent-surface affordances

Four independent defects, one theme: a surface reports state accurately and
still fails to be usable. Each ships as its own pull request off `main`.

| # | Surface | Defect |
|---|---------|--------|
| 1 | Home | *Start chat* lands on an empty chat list, not a chat |
| 2 | Agents list | Ordered by name; a card says only when it was created |
| 3 | Agent detail | Skills/Connections/MCP panels list the whole pool |
| 4 | Agent detail | The schedule is a raw cron expression |

---

## 1. *Start chat* opens a chat with the caret in the composer

`QuickActions` links to `/chats`, which renders `ChatsEmptyState` — "Select a
chat or start a new one" — so the button named *Start chat* starts nothing.
The user then has to find *New chat* in the context pane.

**The fix is a query parameter, not an onClick.** Home's control stays a
`<Link>`: `cards.tsx` records that these are links precisely so middle-click and
"open in new tab" work, and a button with an `onClick` navigate() silently
breaks both. So *Start chat* points at `/chats?new=1`, and `ChatsPage` creates a
chat when it sees that parameter, then replaces the URL with `?chat=<id>`.

**Auto-creation is opt-in, and that is what makes it safe.** The alternative —
"create a chat whenever `/chats` has no selection" — would fire on the icon
rail's own `/chats` entry, so merely navigating to Chats to *read* history would
mint an empty chat every time. Only the caller that means "start one" says so.

Three details:

- **The create is guarded by a ref.** The effect depends on the chat list, which
  refetches; without the guard every refetch creates another chat. Same shape as
  `streamOpenRef` in `ChatWindow`.
- **`setParams(..., { replace: true })`**, so Back leaves Chats rather than
  re-entering a URL that creates a second chat.
- **The caret is already handled.** `ChatsPage` passes `autoFocus`
  unconditionally and keys `ChatWindow` on the chat id, so the mount-time focus
  fires as soon as `?chat=` is set. No new focus code.

## 2. Agents newest-first, with the last run on the card

Two changes to the same surface.

**Ordering is applied in `apiListAgents`, never in `db.ListAgents`.** That query
has five other callers — the chat context builder, the gateway's `/run` listing,
the dashboard, the KB and global search — and every one of them wants a
name-ordered list. Re-ordering the shared query would change all five silently
to fix one page.

**`last_run_at` comes from one aggregate query**, not a per-agent lookup:
`SELECT agent_id, MAX(started_at) FROM agent_runs WHERE workspace_id=? GROUP BY
agent_id`. `started_at` rather than `finished_at` is deliberate — a run in
flight has no `finished_at`, and "last run" means the most recent one, not the
most recent *completed* one. The card does not have to distinguish them: the
existing `StatusChip` already renders `running`.

**The field must marshal as `null`, never be omitted.** A Go nil pointer
serializes as `null`, and a TypeScript default substitutes only for `undefined`
— this repository has shipped that exact bug twice (`flattenRequires`,
`plan_ready`), so the test asserts on raw response bytes rather than on a
decoded struct.

**The card replaces "Created" with "Last run", falling back to "Created" when
the agent has never run.** The request reads "display last run as they are
displaying created date", which is ambiguous between adding and replacing. It
replaces: a card is a summary, two timestamps on one line is worse than one, and
an agent that has never run has nothing else its date line could usefully say.

## 3. The attachment panels collapse to five rows

`SkillsCard` renders every core skill (21 of them) plus every user skill, in a
320px sidebar column, above `ConnectionsCard` and `MCPServersCard` doing the
same for their pools. The panel that tells you what an agent is bound to is
mostly a list of things it is not bound to.

**One shared component, used by all three cards.** The three are already near
copies; three collapse implementations would drift. `MCPServersCard` was not
named in the request but has the identical problem, and excluding it would leave
one panel in the same column behaving differently for no reason.

**Collapsed = every attached row, padded to five with the preferred
unattached.** The request describes three cases — show the selected ones; if
none, the user's own first five; if none of those, five unselected — which this
single ranking satisfies: attached, then user-owned, then core.

**One deliberate deviation: an attached row is never hidden.** Read literally,
"5 skills" caps the collapsed list at five even when six are attached. But a
panel of checkboxes that hides a *checked* one misrepresents the agent's
configuration, which is the defect class this codebase documents most often. In
practice agents bind two to four skills, so the panel still shrinks. Agents with
more than five attachments show all of them and no toggle, which is the honest
outcome.

**Save is unaffected.** `checked` is a `Set` held independently of what is
rendered, so a hidden row keeps its state and Save still PUTs the full set.
Collapsing changes display only — a test pins this, because the tempting
implementation filters the list *before* the Set and silently drops bindings.

The toggle reads *View all N skills…* / *Show fewer*, and is absent when the
pool already fits.

## 4. The schedule reads as a sentence

`ScheduleCard` shows the raw cron expression and nothing else, so the field that
decides when an agent runs is unreadable to the audience this product is built
for.

**`SpecPanel.parseSchedule` already does this**, for four cron shapes, and
carries the constraint that matters: *a plausible-but-wrong plain-language
schedule is worse than showing the raw cron, because the user has no way to tell
it is wrong.* That is the design, not incidental prose — and broadening a parser
is exactly when such a rule gets lost.

So the translator moves to `lib/cron.ts` as `describeCron`, `parseSchedule`
becomes a thin caller (the two must not drift), and the covered shapes widen to
include weekday lists and ranges, day-of-month, hour lists, and a plain
minute-past-the-hour. **Every branch re-checks its captured values against the
field's real range before emitting prose**, and anything outside the covered
shapes still falls back to the raw expression.

Two decisions worth pinning in tests:

- **Weekday lists are emitted in week order, not source order.** Cron
  `0 8 * * 1,6` and `0 8 * * 6,1` are the same schedule; rendering the second as
  "Saturday and Monday" and the first as "Monday and Saturday" would make an
  identical schedule read as two different ones.
- **The prose degrades to nothing mid-typing.** The input is live-editable, so a
  half-typed expression must show no description rather than flash a confident
  reading of an incomplete one.

The card renders the sentence under the input, in the existing muted line beside
the enabled state and next-run time.

## Testing

Vitest for all four surfaces; Go tests for the API half of item 2. Specifically:
the auto-create fires once and not on rail navigation (1); the list order and
the raw-bytes `null` assertion (2); a checked-but-hidden row survives Save, and
the ranking in each of the three cases (3); each covered cron shape, week
ordering, and the fallback for anything else (4).
