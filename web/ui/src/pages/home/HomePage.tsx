import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Bell, Bot, Trash2, Clock, AlertTriangle, Plus } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { ContextPaneHeader, ContextSection } from "@/components/shell/ContextPaneParts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn, timeAgo } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useServices } from "@/lib/connections";
import { useDeferredDelete } from "@/lib/useDeferredDelete";
import {
  useDashboard,
  greeting,
  useReminders,
  useCreateReminder,
  useDeleteReminder,
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
// rendering. Assumes `messages` arrives newest-first (the API's order) —
// buckets are built by first-appearance rather than re-sorted, so a day's
// messages never get reordered relative to each other.
export function groupByDay(messages: InboxMessage[], now: Date): DayGroup[] {
  const todayMs = startOfDay(now);
  const yesterdayMs = todayMs - 24 * 60 * 60 * 1000;
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
  return order.map((key) => ({
    label: key === todayMs ? "Today" : key === yesterdayMs ? "Yesterday" : dayLabel(key),
    messages: buckets.get(key)!,
  }));
}

function DayHeader({ label }: { label: string }) {
  return (
    <div className="sticky top-0 z-10 mb-1 bg-chrome/95 px-1 py-1 text-[11px] font-semibold text-muted-2 backdrop-blur">
      {label}
    </div>
  );
}

function InboxCard({ msg, onDelete }: { msg: InboxMessage; onDelete: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const markRead = useMarkInboxRead();
  const Icon = msg.source === "reminder" ? Bell : Bot;
  const name = msg.agent_name || (msg.source === "reminder" ? "Reminder" : "Notification");

  function handleClick() {
    setExpanded((v) => !v);
    if (!msg.read) markRead.mutate(msg.id);
  }

  return (
    <div
      className={cn(
        "mb-1.5 rounded-lg border border-border bg-background px-2.5 py-2 text-xs",
        // Unread: a whole-card signal (a 2px accent bar), not the old 6px
        // dot you had to hunt for — spec §5.2.
        !msg.read && "border-l-2 border-l-primary",
      )}
    >
      <button type="button" onClick={handleClick} className="flex w-full flex-col gap-1 text-left">
        <span className="flex items-center gap-1.5">
          <Icon className="size-3.5 shrink-0 text-muted-2" />
          <span className={cn("truncate", !msg.read && "font-medium")}>{name}</span>
          {msg.status === "error" && (
            <span className="shrink-0 rounded-full bg-danger-soft px-1.5 py-0.5 text-[10px] font-medium text-danger">
              Failed
            </span>
          )}
          <span className="ml-auto shrink-0 text-[10px] text-muted-2">{timeAgo(msg.created_at)}</span>
        </span>
        {/* Body carries the primary foreground token, not muted-2 — spec §9:
            muted-2 is for metadata (timestamps, counts), not content. */}
        <p className={cn("text-foreground", !expanded && "line-clamp-3")}>{msg.body}</p>
      </button>
      {expanded && (
        <div className="mt-1.5">
          {msg.trigger && <p className="mb-1.5 text-[11px] text-muted-2">Trigger: {msg.trigger}</p>}
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
      className="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[10px] font-semibold leading-none text-accent-foreground"
    >
      {count > 9 ? "9+" : count}
    </span>
  );
}

function InboxSection() {
  const { data } = useInbox();
  const { data: dash } = useDashboard();
  const markAll = useMarkAllInboxRead();
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

  return (
    <div className="border-b border-border pb-3">
      <ContextSection
        title="Inbox"
        action={
          unread > 0 ? (
            <div className="flex items-center gap-2">
              <InboxCountBadge count={unread} />
              <Button variant="ghost" size="xs" onClick={() => markAll.mutate()}>
                Mark all read
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
          groups.map((g, i) => (
            <div key={`${g.label}-${i}`}>
              <DayHeader label={g.label} />
              {g.messages.map((m) => (
                <InboxCard key={m.id} msg={m} onDelete={() => schedule(m.id, "Notification deleted")} />
              ))}
            </div>
          ))
        )}
      </ContextSection>
    </div>
  );
}

// ── Context pane: Reminders ──────────────────────────────────────────────────

function ReminderRow({ r, onDelete }: { r: Reminder; onDelete: () => void }) {
  return (
    <div className="mb-1.5 flex items-start justify-between gap-2 rounded-lg border border-border bg-background px-2.5 py-2 text-xs">
      <div className="min-w-0">
        <p className="truncate font-medium">{r.message}</p>
        <p className="text-[10px] text-muted-2">{new Date(r.remind_at).toLocaleString()}</p>
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

function AddReminderForm() {
  const [message, setMessage] = useState("");
  const [when, setWhen] = useState("");
  const [error, setError] = useState("");
  const create = useCreateReminder();

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await create.mutateAsync({ message, when });
      setMessage("");
      setWhen("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <form onSubmit={handleSubmit} className="mt-1.5 space-y-1 px-1">
      <Input
        aria-label="Reminder message"
        placeholder="Remind me to…"
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        className="h-7 text-xs"
      />
      <Input
        aria-label="Reminder time"
        placeholder="in 10 minutes, tomorrow at 3pm…"
        value={when}
        onChange={(e) => setWhen(e.target.value)}
        className="h-7 text-xs"
      />
      {error && <p className="text-[11px] text-danger">{error}</p>}
      <Button
        type="submit"
        size="xs"
        variant="outline"
        disabled={!message.trim() || !when.trim() || create.isPending}
        className="w-full"
      >
        <Plus /> Add reminder
      </Button>
    </form>
  );
}

function RemindersSection() {
  const { data } = useReminders();
  const del = useDeleteReminder();
  const qc = useQueryClient();
  // Same deferred-commit undo pattern as the inbox — see InboxSection.
  const { schedule, pending } = useDeferredDelete({
    commit: (id) => del.mutateAsync(id),
    onRestore: () => qc.invalidateQueries({ queryKey: ["reminders"] }),
  });
  const reminders = (data?.reminders ?? []).filter((r) => !pending.has(r.id));
  return (
    <div className="pt-3">
      <ContextSection title="Reminders">
        {reminders.length === 0 ? (
          <p className="px-1 text-xs text-muted-2">No reminders yet.</p>
        ) : (
          reminders.map((r) => (
            <ReminderRow key={r.id} r={r} onDelete={() => schedule(r.id, "Reminder deleted")} />
          ))
        )}
        <AddReminderForm />
      </ContextSection>
    </div>
  );
}

// ── Content: stat tiles ──────────────────────────────────────────────────────

function StatTile({ value, label, badge }: { value: ReactNode; label: string; badge?: string }) {
  return (
    <div className="flex-1 rounded-lg border border-border p-3">
      <div className="flex items-baseline gap-1.5">
        <span className="text-xl font-extrabold">{value}</span>
        {badge && <span className="text-xs font-medium text-danger">{badge}</span>}
      </div>
      <p className="text-[11px] text-muted-2">{label}</p>
    </div>
  );
}

// ── Content: Next up ──────────────────────────────────────────────────────────

function NextUpCard({ upcoming }: { upcoming: DashboardUpcoming[] }) {
  return (
    <div className="rounded-lg border border-border p-3">
      <h3 className="mb-2 text-[11px] font-bold uppercase tracking-wide text-muted-2">Next up</h3>
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
    <div className="rounded-lg border border-border p-3">
      <h3 className="mb-2 text-[11px] font-bold uppercase tracking-wide text-muted-2">
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

// ── Page ─────────────────────────────────────────────────────────────────────

export default function HomePage() {
  const { data: dash } = useDashboard();
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
            <InboxSection />
            <RemindersSection />
          </div>
        </div>
      </ContextPane>

      <div className="p-6">
        <div className="mb-6 flex items-center justify-between gap-2">
          <h1 className="text-xl font-bold">
            {dash ? `${greeting(hour)}, ${dash.display_name}` : "Welcome"}
          </h1>
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

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <NextUpCard upcoming={dash?.upcoming ?? []} />
          <NeedsAttentionCard runs={runs} />
        </div>
      </div>
    </>
  );
}
