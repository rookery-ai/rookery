import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// ── Dashboard ────────────────────────────────────────────────────────────────
// Mirrors web/api_dashboard.go's apiDashboardResponse.

export type DashboardRun = {
  id: string;
  agent_id: string;
  agent_name: string;
  status: "running" | "success" | "failed";
  trigger: string;
  started_at: string;
  finished_at: string | null;
};

export type DashboardUpcoming = {
  agent_id: string;
  agent_name: string;
  cron_expr: string;
  next_run_at: string | null;
};

export type Dashboard = {
  display_name: string;
  agent_count: number;
  active_agent_count: number;
  recent_runs: DashboardRun[];
  upcoming: DashboardUpcoming[];
  has_connector: boolean;
};

// normalizeDashboard guards against nil Go slices reaching a component as
// JSON null — same convention as lib/agents.ts's normalizeAgentDetail.
function normalizeDashboard(d: Dashboard): Dashboard {
  return { ...d, recent_runs: d.recent_runs ?? [], upcoming: d.upcoming ?? [] };
}

export function useDashboard() {
  return useQuery({
    queryKey: ["dashboard"],
    queryFn: () => api.get<Dashboard>("/api/v1/dashboard").then(normalizeDashboard),
  });
}

// greeting returns a time-of-day salutation for the given hour-of-day
// (0-23, local time). Split out as a pure function of `hour` (not `Date`)
// so it's trivially testable across all boundaries without faking the
// system clock.
export function greeting(hour: number): string {
  if (hour < 12) return "Good morning";
  if (hour < 18) return "Good afternoon";
  return "Good evening";
}

// ── Reminders ────────────────────────────────────────────────────────────────
// Mirrors web/api_home.go's apiReminder.

export type Reminder = {
  id: string;
  message: string;
  remind_at: string;
  sent: boolean;
};

// splitReminders partitions the list into what is still coming and what has
// already fired. Pending sorts by remind_at ASCENDING (the next one to fire is
// the one you care about); done sorts DESCENDING (most recently completed
// first), because an old completed reminder is the least interesting row on
// the page.
//
// Pure and exported so the ordering — the part that is easy to get subtly
// wrong and invisible in a rendered snapshot — is unit-testable on its own.
// The API already returns fired reminders (db.ListReminders has no `sent`
// filter and the DTO carries the flag); the UI simply ignored it until now.
export function splitReminders(list: Reminder[]): { pending: Reminder[]; done: Reminder[] } {
  const at = (r: Reminder) => new Date(r.remind_at).getTime();
  const pending = list.filter((r) => !r.sent).sort((a, b) => at(a) - at(b));
  const done = list.filter((r) => r.sent).sort((a, b) => at(b) - at(a));
  return { pending, done };
}

export function useReminders() {
  return useQuery({
    queryKey: ["reminders"],
    queryFn: () => api.get<{ reminders: Reminder[] }>("/api/v1/reminders").then((d) => ({
      reminders: d.reminders ?? [],
    })),
  });
}

export function useCreateReminder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ text }: { text: string }) =>
      api.post<Reminder>("/api/v1/reminders", { text }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["reminders"] }),
  });
}

export function useDeleteReminder() {
  const qc = useQueryClient();
  return useMutation({
    // keepalive: this mutation is also what useDeferredDelete's flushAll
    // fires from a `beforeunload` handler on tab close — without it, a
    // browser may abort the in-flight DELETE when the page is torn down,
    // silently losing the delete. Scoped to this call site only; see
    // RequestOptions in lib/api.ts for why it isn't the default.
    mutationFn: (id: string) =>
      api.del<{ ok: boolean }>(`/api/v1/reminders/${id}`, undefined, { keepalive: true }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["reminders"] }),
  });
}

// ── Inbox ────────────────────────────────────────────────────────────────────
// Mirrors web/api_home.go's apiInboxMessage.

export type InboxMessage = {
  id: string;
  source: string;
  agent_id: string;
  agent_name: string;
  trigger: string;
  status: string;
  body: string;
  read: boolean;
  created_at: string;
};

export function useInbox() {
  return useQuery({
    queryKey: ["inbox"],
    queryFn: () => api.get<{ messages: InboxMessage[]; unread: number }>("/api/v1/inbox").then((d) => ({
      messages: d.messages ?? [],
      unread: d.unread,
    })),
  });
}

// Both keys, deliberately. The inbox LIST is ["inbox"]; the rail's unread
// badge is ["inbox-poll"] (see useInboxPoll). Invalidating only the first is
// why reading a notification left the badge counting it for up to 30 seconds.
function invalidateInbox(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ["inbox"] });
  void qc.invalidateQueries({ queryKey: ["inbox-poll"] });
}

export function useMarkInboxRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.post<{ ok: boolean }>(`/api/v1/inbox/${id}/read`),
    onSuccess: () => invalidateInbox(qc),
  });
}

export function useMarkAllInboxRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ ok: boolean }>("/api/v1/inbox/read-all"),
    onSuccess: () => invalidateInbox(qc),
  });
}

export function useDeleteInboxMessage() {
  const qc = useQueryClient();
  return useMutation({
    // keepalive: see useDeleteReminder above — this is the other mutation
    // useDeferredDelete's `beforeunload` flush can fire on tab close.
    mutationFn: (id: string) =>
      api.del<{ ok: boolean }>(`/api/v1/inbox/${id}`, undefined, { keepalive: true }),
    onSuccess: () => invalidateInbox(qc),
  });
}

// Lightweight poll DTO — mirrors web/handlers_inbox.go's handleInboxPoll,
// a cheaper endpoint than useInbox's full list (unread count + a handful of
// recent items) meant to be polled on an interval for a rail badge.
export type InboxPollItem = {
  id: string;
  source: string;
  agent_name: string;
  trigger: string;
  status: string;
  read: boolean;
  preview: string;
  created_at: string;
};

export type InboxPoll = {
  unread: number;
  recent: InboxPollItem[];
};

// Own query key (["inbox-poll"], distinct from ["inbox"]) so its 30s
// refetchInterval doesn't bleed into the full inbox list/page query.
export function useInboxPoll() {
  return useQuery({
    queryKey: ["inbox-poll"],
    queryFn: () => api.get<InboxPoll>("/api/v1/inbox/poll").then((d) => ({
      unread: d.unread ?? 0,
      recent: d.recent ?? [],
    })),
    refetchInterval: 30_000,
  });
}
