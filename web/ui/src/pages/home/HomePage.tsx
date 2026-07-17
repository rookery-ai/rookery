import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { Bell, Bot, Trash2, Clock, AlertTriangle, Plus } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn, timeAgo } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useServices } from "@/lib/connections";
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

function InboxCard({ msg }: { msg: InboxMessage }) {
  const [expanded, setExpanded] = useState(false);
  const markRead = useMarkInboxRead();
  const del = useDeleteInboxMessage();
  const Icon = msg.source === "reminder" ? Bell : Bot;

  function handleClick() {
    setExpanded((v) => !v);
    if (!msg.read) markRead.mutate(msg.id);
  }

  return (
    <div className="mb-1.5 rounded-lg border border-border bg-background px-2.5 py-2 text-xs">
      <button type="button" onClick={handleClick} className="flex w-full items-start gap-2 text-left">
        <Icon className="mt-0.5 size-3.5 shrink-0 text-muted-2" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-semibold">{msg.agent_name || "Notification"}</span>
            {!msg.read && <span className="size-1.5 shrink-0 rounded-full bg-primary" aria-label="unread" />}
          </div>
          <p className={cn("text-muted-2", !expanded && "line-clamp-2")}>{msg.body}</p>
          <span className="text-[10px] text-muted-2/70">{timeAgo(msg.created_at)}</span>
        </div>
      </button>
      {expanded && (
        <div className="mt-1 flex justify-end">
          <Button
            variant="ghost"
            size="xs"
            className="text-danger"
            onClick={() => del.mutate(msg.id)}
          >
            <Trash2 /> Delete
          </Button>
        </div>
      )}
    </div>
  );
}

function InboxSection() {
  const { data } = useInbox();
  const { data: dash } = useDashboard();
  const markAll = useMarkAllInboxRead();
  const messages = data?.messages ?? [];
  const unread = data?.unread ?? 0;

  return (
    <div className="border-b border-border pb-3">
      <div className="mb-1.5 flex items-center justify-between px-1">
        <h3 className="text-[11px] font-bold uppercase tracking-wide text-muted-2">
          Inbox{unread > 0 ? ` · ${unread} new` : ""}
        </h3>
        {unread > 0 && (
          <button
            type="button"
            className="text-[11px] text-muted-2 hover:text-foreground"
            onClick={() => markAll.mutate()}
          >
            Mark all read
          </button>
        )}
      </div>
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
        messages.map((m) => <InboxCard key={m.id} msg={m} />)
      )}
    </div>
  );
}

// ── Context pane: Reminders ──────────────────────────────────────────────────

function ReminderRow({ r }: { r: Reminder }) {
  const del = useDeleteReminder();
  return (
    <div className="mb-1.5 flex items-start justify-between gap-2 rounded-lg border border-border bg-background px-2.5 py-2 text-xs">
      <div className="min-w-0">
        <p className="truncate font-medium">{r.message}</p>
        <p className="text-[10px] text-muted-2">{new Date(r.remind_at).toLocaleString()}</p>
      </div>
      <button
        type="button"
        onClick={() => del.mutate(r.id)}
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
  const reminders = data?.reminders ?? [];
  return (
    <div className="pt-3">
      <h3 className="mb-1.5 px-1 text-[11px] font-bold uppercase tracking-wide text-muted-2">
        Reminders
      </h3>
      {reminders.length === 0 ? (
        <p className="px-1 text-xs text-muted-2">No reminders yet.</p>
      ) : (
        reminders.map((r) => <ReminderRow key={r.id} r={r} />)
      )}
      <AddReminderForm />
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
          <h2 className="px-4 pt-3 pb-1 text-sm font-bold">Home</h2>
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
