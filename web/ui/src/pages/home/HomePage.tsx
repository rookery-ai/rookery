import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { PageContainer } from "@/components/shell/PageContainer";
import { PageTitle } from "@/components/shell/PageTitle";
import { cardClass } from "@/components/ui/card";
import {
  AgentsAtAGlanceCard, QuickActions, RecentActivityCard, RecentNotesCard,
} from "./cards";
import { Bell, Bot, Trash2, Clock, AlertTriangle, Plus, Loader2, Check, CheckCheck } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { ContextPaneHeader, ContextSection } from "@/components/shell/ContextPaneParts";
import { useToast } from "@/components/shell/Toast";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { cn, timeAgo } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useServices } from "@/lib/connections";
import { useDeferredDelete } from "@/lib/useDeferredDelete";
import { useListNav } from "@/lib/useKeyboardNav";
import {
  useDashboard,
  greeting,
  useReminders,
  useCreateReminder,
  useDeleteReminder,
  splitReminders,
  useInbox,
  useMarkInboxRead,
  useMarkAllInboxRead,
  useDeleteInboxMessage,
  type InboxMessage,
  type Reminder,
  type DashboardRun,
  type DashboardUpcoming,
} from "@/lib/home";

// ── Context pane: Inbox ─────────────────────────────────────────────────────

export type DayGroup = { label: string; messages: InboxMessage[] };

const WEEKDAY_NAMES = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTH_NAMES = [
  "Jan", "Feb", "Mar", "Apr", "May", "Jun",
  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];

// Midnight (local time) of the given instant, as an epoch ms — the bucket
// key for "which calendar day does this message belong to."
function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

// Deliberately not Intl/toLocaleDateString: locale + ICU data availability
// varies by environment, and a test asserting an exact label (spec §5.2's
// "Mon, 14 Jul") needs a format that doesn't drift with the runtime's
// default locale.
function dayLabel(dayMs: number): string {
  const d = new Date(dayMs);
  return `${WEEKDAY_NAMES[d.getDay()]}, ${d.getDate()} ${MONTH_NAMES[d.getMonth()]}`;
}

// groupByDay buckets inbox messages under day headers — spec §5.2's single
// highest-value change: it turns an undifferentiated stream into a timeline.
// Pure and exported so it's unit-testable with a fixed clock, independent of
// rendering. Messages within a day keep the order they arrived in (the API's
// newest-first) — they are never re-sorted relative to each other. The day
// GROUPS are sorted newest-first explicitly: bucketing is keyed by day, so an
// out-of-order input could never split a day in two, but it could render the
// headers themselves inverted ("Yesterday" above "Today"). ListInboxMessages
// orders by created_at DESC today, so this is belt-and-braces.
export function groupByDay(messages: InboxMessage[], now: Date): DayGroup[] {
  const todayMs = startOfDay(now);
  // Not todayMs - 24h: on the day after a DST spring-forward the previous
  // local day is only 23h long, so the fixed-offset arithmetic misses and
  // "Yesterday" silently renders as a date. Europe/Skopje observes DST.
  const yesterdayMs = startOfDay(
    new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1),
  );
  const order: number[] = [];
  const buckets = new Map<number, InboxMessage[]>();
  for (const m of messages) {
    const key = startOfDay(new Date(m.created_at));
    let bucket = buckets.get(key);
    if (!bucket) {
      bucket = [];
      buckets.set(key, bucket);
      order.push(key);
    }
    bucket.push(m);
  }
  order.sort((a, b) => b - a);
  return order.map((key) => ({
    label: key === todayMs ? "Today" : key === yesterdayMs ? "Yesterday" : dayLabel(key),
    messages: buckets.get(key)!,
  }));
}

function DayHeader({ label }: { label: string }) {
  return (
    <div className="sticky top-0 z-10 mb-1 bg-chrome/95 px-1 py-1 text-xs font-semibold text-muted-2 backdrop-blur">
      {label}
    </div>
  );
}

function InboxCard({
  msg,
  expanded,
  highlighted,
  onActivate,
  onDelete,
}: {
  msg: InboxMessage;
  expanded: boolean;
  highlighted: boolean;
  onActivate: () => void;
  onDelete: () => void;
}) {
  const Icon = msg.source === "reminder" ? Bell : Bot;
  const name = msg.agent_name || (msg.source === "reminder" ? "Reminder" : "Notification");

  return (
    <div
      data-highlighted={highlighted}
      className={cn(
        "mb-1.5 rounded-lg border border-border bg-background px-2.5 py-2 text-xs",
        // Unread: a whole-card signal (a 2px accent bar), not the old 6px
        // dot you had to hunt for — spec §5.2.
        !msg.read && "border-l-2 border-l-primary",
        // The keyboard-nav highlight (j/k from useListNav) needs its own
        // visible signal distinct from the unread bar — an invisible
        // highlight isn't a feature. A ring reads at a glance even stacked
        // with the unread accent.
        highlighted && "ring-2 ring-primary ring-offset-1 ring-offset-background",
      )}
    >
      <button type="button" onClick={onActivate} className="flex w-full flex-col gap-1 text-left">
        <span className="flex items-center gap-1.5">
          <Icon className="size-3.5 shrink-0 text-muted-2" />
          <span className={cn("truncate", !msg.read && "font-medium")}>{name}</span>
          {msg.status === "error" && (
            <span className="shrink-0 rounded-full bg-danger-soft px-1.5 py-0.5 text-xs font-medium text-danger">
              Failed
            </span>
          )}
          <span className="ml-auto shrink-0 text-xs text-muted-2">{timeAgo(msg.created_at)}</span>
        </span>
        {/* Body carries the primary foreground token, not muted-2 — spec §9:
            muted-2 is for metadata (timestamps, counts), not content. */}
        <p className={cn("text-foreground", !expanded && "line-clamp-3")}>{msg.body}</p>
      </button>
      {expanded && (
        <div className="mt-1.5">
          {msg.trigger && <p className="mb-1.5 text-xs text-muted-2">Trigger: {msg.trigger}</p>}
          <div className="flex items-center justify-end gap-1">
            {msg.agent_id && (
              <Button variant="ghost" size="xs" asChild>
                <Link to={`/agents/${msg.agent_id}`}>View agent</Link>
              </Button>
            )}
            <Button variant="ghost" size="xs" className="text-danger" onClick={onDelete}>
              <Trash2 /> Delete
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

function InboxCountBadge({ count }: { count: number }) {
  return (
    <span
      aria-label={`${count} unread`}
      className="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-xs font-semibold leading-none text-accent-foreground"
    >
      {count > 9 ? "9+" : count}
    </span>
  );
}

function InboxSection() {
  const { data } = useInbox();
  const { data: dash } = useDashboard();
  const markAll = useMarkAllInboxRead();
  const markRead = useMarkInboxRead();
  const del = useDeleteInboxMessage();
  const qc = useQueryClient();
  // Deferred-commit undo (§6.2): delete hides the row immediately, but the
  // DELETE call itself only fires if the 5s toast expires without Undo.
  const { schedule, pending } = useDeferredDelete({
    commit: (id) => del.mutateAsync(id),
    onRestore: () => qc.invalidateQueries({ queryKey: ["inbox"] }),
  });
  const messages = (data?.messages ?? []).filter((m) => !pending.has(m.id));
  const unread = data?.unread ?? 0;
  const groups = groupByDay(messages, new Date());

  // Expansion used to live inside InboxCard as local state. It's lifted up
  // here so keyboard activation (Enter, via useListNav below) has somewhere
  // to call into — a card no longer decides its own open/closed state.
  const [expandedId, setExpandedId] = useState<string | null>(null);

  // The one action both a mouse click on a card AND a keyboard Enter run —
  // kept as a single function so the two input methods can't drift apart
  // (a card no longer has its own handleClick).
  function activate(msg: InboxMessage) {
    setExpandedId((id) => (id === msg.id ? null : msg.id));
    if (!msg.read) markRead.mutate(msg.id);
  }

  // The list renders grouped by day (groupByDay), but useListNav wants one
  // flat, index-addressable array — flatten the already-computed groups
  // (not `messages` directly) so the index this hook tracks always lines up
  // with render order group-by-group, regardless of how the grouping itself
  // sorts. When the highlighted item is deleted, `pending` above has
  // already filtered it out of `messages` (and so `flatMessages`) by the
  // time this re-renders — useListNav's own length-clamp effect then keeps
  // `highlightedIndex` in range, which in practice moves the highlight onto
  // whatever row shifted into that slot (the next row down), or the new
  // last row if the deleted item was last. See useKeyboardNav.ts for why
  // that's the deliberate policy, not an accident.
  const flatMessages = groups.flatMap((g) => g.messages);
  const { highlightedIndex } = useListNav(flatMessages, activate);

  let indexOffset = 0;

  return (
    <div className="pt-3">
      <ContextSection
        title="Inbox"
        action={
          // The endpoint, hook and tests for this all existed; the button was
          // a 24px, 12px grey GHOST TEXT button that only rendered while
          // unread > 0, which is why it read as missing. It is now a real
          // outlined action with an icon, present whenever there is anything
          // to mark and merely disabled when there is nothing — discoverable
          // before it is needed rather than appearing and vanishing.
          messages.length > 0 ? (
            <div className="flex items-center gap-2">
              {unread > 0 && <InboxCountBadge count={unread} />}
              <Button
                variant="outline"
                size="sm"
                disabled={unread === 0 || markAll.isPending}
                onClick={() => markAll.mutate()}
              >
                <CheckCheck /> Mark all read
              </Button>
            </div>
          ) : undefined
        }
      >
        {messages.length === 0 ? (
          <div className="px-1 text-xs text-muted-2">
            <p>No notifications yet.</p>
            {dash && !dash.has_connector && (
              <p className="mt-0.5 text-muted-2/70">
                Connect a chat app so agents can reach you here too.
              </p>
            )}
          </div>
        ) : (
          groups.map((g, gi) => {
            const startIndex = indexOffset;
            indexOffset += g.messages.length;
            return (
              <div key={`${g.label}-${gi}`}>
                <DayHeader label={g.label} />
                {g.messages.map((m, mi) => (
                  <InboxCard
                    key={m.id}
                    msg={m}
                    expanded={expandedId === m.id}
                    highlighted={startIndex + mi === highlightedIndex}
                    onActivate={() => activate(m)}
                    onDelete={() => schedule(m.id, "Notification deleted")}
                  />
                ))}
              </div>
            );
          })
        )}
      </ContextSection>
    </div>
  );
}

// ── Context pane: Reminders ──────────────────────────────────────────────────

// A fired reminder is struck through and dimmed, with a check replacing the
// bell — done, but still deletable. It is NOT hidden: seeing what already went
// off is how you tell "it fired and I missed it" from "it never fired".
function ReminderRow({ r, onDelete }: { r: Reminder; onDelete: () => void }) {
  const Icon = r.sent ? Check : Bell;
  return (
    <div className="mb-1.5 flex items-start justify-between gap-2 rounded-lg border border-border bg-background px-2.5 py-2 text-xs">
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", r.sent ? "text-ok" : "text-muted-2")} />
      <div className="min-w-0 flex-1">
        <p className={cn("truncate font-medium", r.sent && "text-muted-2 line-through")}>{r.message}</p>
        <p className="text-xs text-muted-2">
          {r.sent && "Done · "}
          {new Date(r.remind_at).toLocaleString()}
        </p>
      </div>
      <button
        type="button"
        onClick={onDelete}
        aria-label="Delete reminder"
        className="shrink-0 text-muted-2 hover:text-danger"
      >
        <Trash2 className="size-3.5" />
      </button>
    </div>
  );
}

// The full list, behind "View all reminders". A modal rather than a route:
// there is no /reminders route today (global search maps a reminder hit to
// "/"), and adding one would mean a rail entry and an API-parity row for what
// is typically a list of under a dozen items.
function RemindersDialog({
  open,
  onOpenChange,
  reminders,
  onDelete,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  reminders: Reminder[];
  onDelete: (id: string) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[80vh] max-w-md flex-col gap-0 overflow-hidden p-0">
        <DialogHeader className="border-b border-border px-4 py-3">
          <DialogTitle>All reminders</DialogTitle>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {reminders.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-2">No reminders yet.</p>
          ) : (
            reminders.map((r) => <ReminderRow key={r.id} r={r} onDelete={() => onDelete(r.id)} />)
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function AddReminderForm() {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const create = useCreateReminder();
  const { toast } = useToast();

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      const r = await create.mutateAsync({ text });
      setText("");
      // Echo back the time the parser resolved — the trust surface for what
      // may have been an LLM guess. The user can delete a wrong parse.
      const at = new Date(r.remind_at);
      // Include the date, not just weekday+time: a reminder set for "next week"
      // renders as an unambiguous "Mon, Jul 27, 09:00" rather than "which Monday?"
      toast({
        message: `Reminder set for ${at.toLocaleString([], {
          weekday: "short",
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        })}`,
      });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <form onSubmit={handleSubmit} className="mt-1.5 space-y-1 px-1">
      <Input
        aria-label="Reminder"
        placeholder="Remind me in 10 minutes to call the doctor…"
        value={text}
        onChange={(e) => setText(e.target.value)}
        disabled={create.isPending}
        className="h-7 text-xs"
      />
      {error && <p className="text-xs text-danger">{error}</p>}
      <Button
        type="submit"
        size="xs"
        variant="outline"
        disabled={!text.trim() || create.isPending}
        className="w-full"
      >
        {create.isPending ? (
          <>
            <Loader2 className="animate-spin" /> Setting…
          </>
        ) : (
          <>
            <Plus /> Add reminder
          </>
        )}
      </Button>
    </form>
  );
}

// How many reminders the context pane shows before collapsing behind
// "View all". The pane also holds the inbox; an unbounded reminder list pushes
// it off the screen entirely.
const PANE_REMINDER_LIMIT = 3;

// useRemindersView is called ONCE, by HomePage, and its result handed to both
// the context-pane section and the main-screen card. Two independent
// useDeferredDelete instances would each keep their own `pending` set, so a
// delete in the pane would hide the row there while the card kept rendering
// it for the full 5s undo window — the same reminder both gone and present on
// one screen.
function useRemindersView() {
  const { data } = useReminders();
  const del = useDeleteReminder();
  const qc = useQueryClient();
  // Same deferred-commit undo pattern as the inbox — see InboxSection.
  const { schedule, pending } = useDeferredDelete({
    commit: (id) => del.mutateAsync(id),
    onRestore: () => qc.invalidateQueries({ queryKey: ["reminders"] }),
  });
  const reminders = (data?.reminders ?? []).filter((r) => !pending.has(r.id));
  // Upcoming first (soonest at the top), completed after — so the pane's three
  // visible rows are the three that still matter, never three stale ones.
  const { pending: upcoming, done } = splitReminders(reminders);
  return {
    upcoming,
    ordered: [...upcoming, ...done],
    onDelete: (id: string) => schedule(id, "Reminder deleted"),
  };
}

export type RemindersView = ReturnType<typeof useRemindersView>;

function RemindersSection({ view }: { view: RemindersView }) {
  const { ordered, onDelete } = view;
  const [allOpen, setAllOpen] = useState(false);
  const shown = ordered.slice(0, PANE_REMINDER_LIMIT);

  return (
    <div className="border-b border-border pb-3">
      <ContextSection title="Reminders">
        {ordered.length === 0 ? (
          <p className="px-1 text-xs text-muted-2">No reminders yet.</p>
        ) : (
          shown.map((r) => <ReminderRow key={r.id} r={r} onDelete={() => onDelete(r.id)} />)
        )}
        {ordered.length > PANE_REMINDER_LIMIT && (
          <Button variant="ghost" size="xs" className="w-full" onClick={() => setAllOpen(true)}>
            View all reminders ({ordered.length})
          </Button>
        )}
        <AddReminderForm />
      </ContextSection>
      <RemindersDialog
        open={allOpen}
        onOpenChange={setAllOpen}
        reminders={ordered}
        onDelete={onDelete}
      />
    </div>
  );
}

// ── Content: stat tiles ──────────────────────────────────────────────────────

function StatTile({ value, label, badge }: { value: ReactNode; label: string; badge?: string }) {
  return (
    <div className={cn("flex-1", cardClass)}>
      <div className="flex items-baseline gap-1.5">
        <span className="text-xl font-extrabold">{value}</span>
        {badge && <span className="text-xs font-medium text-danger">{badge}</span>}
      </div>
      <p className="text-xs text-muted-2">{label}</p>
    </div>
  );
}

// ── Content: Next up ──────────────────────────────────────────────────────────

function NextUpCard({ upcoming }: { upcoming: DashboardUpcoming[] }) {
  return (
    <div className={cardClass}>
      <h3 className="mb-2 text-xs font-bold uppercase tracking-wide text-muted-2">Next up</h3>
      {upcoming.length === 0 ? (
        <p className="text-sm text-muted-2">Nothing scheduled.</p>
      ) : (
        <ul className="space-y-1.5 text-sm">
          {upcoming.map((u) => (
            <li key={u.agent_id} className="flex items-start gap-2 text-muted-2">
              <Clock className="mt-0.5 size-3.5 shrink-0" />
              <span>
                <Link to={`/agents/${u.agent_id}`} className="font-medium text-foreground hover:underline">
                  {u.agent_name}
                </Link>
                {" runs "}
                {u.next_run_at ? new Date(u.next_run_at).toLocaleString() : "soon"}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ── Content: Needs attention ─────────────────────────────────────────────────

function NeedsAttentionCard({ runs }: { runs: DashboardRun[] }) {
  const failed = runs.filter((r) => r.status === "failed");
  return (
    <div className={cardClass}>
      <h3 className="mb-2 text-xs font-bold uppercase tracking-wide text-muted-2">
        Needs attention
      </h3>
      {failed.length === 0 ? (
        <p className="text-sm text-muted-2">All caught up — no failed runs.</p>
      ) : (
        <ul className="space-y-2 text-sm">
          {failed.map((r) => (
            <li key={r.id} className="flex items-start gap-2">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-danger" />
              <span className="text-muted-2">
                <Link to={`/agents/${r.agent_id}`} className="font-medium text-foreground hover:underline">
                  {r.agent_name}
                </Link>
                {" failed — "}
                <Link to={`/agents/${r.agent_id}`} className="underline hover:text-foreground">
                  view log
                </Link>
                {" or "}
                <Link to={`/agents/${r.agent_id}/edit`} className="underline hover:text-foreground">
                  ask the designer to fix it
                </Link>
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ── Content: Reminders ───────────────────────────────────────────────────────

// How many upcoming reminders the main-screen card lists. It sits beside Next
// up / Needs attention in a fixed-height row, so it shows the near horizon and
// defers the rest to the pane's "View all".
const CARD_REMINDER_LIMIT = 4;

// The main-screen counterpart to the context pane's list — same shape as
// NextUpCard / NeedsAttentionCard so the three read as one row of "what's
// coming". Deliberately shows ONLY upcoming reminders: completed ones are
// managed in the pane, and struck-through rows on the dashboard would be
// noise, not status.
function RemindersCard({ view }: { view: RemindersView }) {
  const { upcoming } = view;
  const shown = upcoming.slice(0, CARD_REMINDER_LIMIT);
  return (
    // A named region: "Reminders" is also the context pane's section heading,
    // so the dashboard card needs an addressable identity of its own — for a
    // screen reader landing on it out of context as much as for a test.
    <section aria-label="Upcoming reminders" className={cardClass}>
      <h3 className="mb-2 text-xs font-bold uppercase tracking-wide text-muted-2">Reminders</h3>
      {shown.length === 0 ? (
        <p className="text-sm text-muted-2">No reminders set.</p>
      ) : (
        <ul className="space-y-1.5 text-sm">
          {shown.map((r) => (
            <li key={r.id} className="flex items-start gap-2 text-muted-2">
              <Bell className="mt-0.5 size-3.5 shrink-0" />
              <span className="min-w-0">
                <span className="font-medium text-foreground">{r.message}</span>
                {" — "}
                {new Date(r.remind_at).toLocaleString()}
              </span>
            </li>
          ))}
        </ul>
      )}
      {upcoming.length > shown.length && (
        <p className="mt-2 text-xs text-muted-2">
          +{upcoming.length - shown.length} more in the sidebar
        </p>
      )}
    </section>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────────

export default function HomePage() {
  const { data: dash } = useDashboard();
  // One reminders view, shared by the pane and the card — see useRemindersView.
  const remindersView = useRemindersView();
  const { data: servicesData } = useServices();

  // Tile 3 = connected services (per the validated mockup: "5 connected
  // services"), summed across every provider's active connections — not
  // `has_connector` (that field stays reserved for the chat-app nudge above).
  const connectedServices = (servicesData?.providers ?? []).reduce(
    (n, p) => n + p.connections.length,
    0,
  );

  const runs = dash?.recent_runs ?? [];
  const failedCount = runs.filter((r) => r.status === "failed").length;
  const hour = new Date().getHours();

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <ContextPaneHeader title="Home" />
          <div className="min-h-0 flex-1 overflow-y-auto p-3">
            <RemindersSection view={remindersView} />
            <InboxSection />
          </div>
        </div>
      </ContextPane>

      <PageContainer>
        <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <PageTitle
            icon="home"
            title={dash ? `${greeting(hour)}, ${dash.display_name}` : "Welcome"}
          />
          <QuickActions />
        </div>

        <div className="mb-6 flex flex-col gap-3 sm:flex-row">
          <StatTile value={dash?.active_agent_count ?? 0} label="active agents" />
          <StatTile
            value={runs.length}
            label="recent runs"
            badge={failedCount > 0 ? `${failedCount} failed` : undefined}
          />
          <StatTile value={connectedServices} label="connected services" />
        </div>

        {/* Left column carries the dense, scannable content; right column the
            short status cards. Collapses to one column below lg. */}
        <div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
          <div className="flex flex-col gap-4">
            <RecentActivityCard runs={runs} />
            <AgentsAtAGlanceCard upcoming={dash?.upcoming ?? []} />
          </div>
          <div className="flex flex-col gap-4">
            <NextUpCard upcoming={dash?.upcoming ?? []} />
            <NeedsAttentionCard runs={runs} />
            <RemindersCard view={remindersView} />
            <RecentNotesCard />
          </div>
        </div>
      </PageContainer>
    </>
  );
}
