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
    mutationFn: ({ message, when }: { message: string; when: string }) =>
      api.post<Reminder>("/api/v1/reminders", { message, when }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["reminders"] }),
  });
}

export function useDeleteReminder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/reminders/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["reminders"] }),
  });
}

// ── Inbox ────────────────────────────────────────────────────────────────────
// Mirrors web/api_home.go's apiInboxMessage.

export type InboxMessage = {
  id: string;
  source: string;
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

export function useMarkInboxRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.post<{ ok: boolean }>(`/api/v1/inbox/${id}/read`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["inbox"] }),
  });
}

export function useMarkAllInboxRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ ok: boolean }>("/api/v1/inbox/read-all"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["inbox"] }),
  });
}

export function useDeleteInboxMessage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/inbox/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["inbox"] }),
  });
}
