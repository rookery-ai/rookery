import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// Mirrors web/api_agents.go's apiAgent struct (toAPIAgent).
export type Agent = {
  id: string;
  name: string;
  description: string;
  active: boolean;
  created_at: string;
  running: boolean;
};

// Mirrors toAPIAgentDraft — nil draft serializes as JSON null.
export type AgentDraft = {
  agent_id?: string;
  agent_name?: string;
  is_edit?: boolean;
  state?: string;
  updated_at?: string;
  expires_at?: string;
} | null;

// Mirrors toAPISchedule — nil schedule serializes as JSON null.
export type AgentSchedule = {
  cron_expr: string;
  next_run_at: string | null;
  last_run_at: string | null;
  enabled: boolean;
} | null;

// Mirrors toAPIRun.
export type AgentRun = {
  id: string;
  trigger: string;
  status: "running" | "success" | "failed";
  exit_code: number | null;
  stdout: string;
  stderr: string;
  prompt_tokens: number | null;
  completion_tokens: number | null;
  total_tokens: number | null;
  started_at: string;
  finished_at: string | null;
};

// Mirrors toAPICoreSkill / toAPISkill.
export type CoreSkill = { name: string; description: string };
export type Skill = { id: string; name: string; description: string; installed_at: string };

// Mirrors toAPIConnection.
export type Connection = {
  id: string;
  provider: string;
  account_label: string;
  account_identity: string;
  status: string;
  created_at: string;
};

// Mirrors toAPIAgentDetail's map keys exactly (agentDetailData in
// web/handlers_agents.go: State/Logs/LastLog/AttachedSkills/MissingSecrets
// are plain string(s), not structured JSON).
export type AgentDetail = {
  agent: Agent;
  schedule: AgentSchedule;
  runs: AgentRun[];
  agent_md: string;
  state: string;
  logs: string[];
  last_log: string;
  attached_skills: string[];
  core_skills: CoreSkill[];
  all_skills: Skill[];
  workspace_connections: Connection[];
  attached_connection_ids: string[];
  missing_secrets: string[];
  running: boolean;
  live_run: boolean;
};

export function useAgents() {
  return useQuery({
    queryKey: ["agents"],
    queryFn: () => api.get<{ agents: Agent[]; draft: AgentDraft }>("/api/v1/agents"),
  });
}

// normalizeAgentDetail guards against nil Go slices reaching a component as
// JSON null: json.Marshal renders a nil []T as `null`, and every array field
// here is unconditionally `.length`/`.map`'d by AgentDetailPage and its
// cards. Belt-and-braces alongside the backend fix (web/api.go's orEmpty) —
// normalize once here so no component needs its own null guard.
function normalizeAgentDetail(d: AgentDetail): AgentDetail {
  return {
    ...d,
    runs: d.runs ?? [],
    logs: d.logs ?? [],
    attached_skills: d.attached_skills ?? [],
    core_skills: d.core_skills ?? [],
    all_skills: d.all_skills ?? [],
    workspace_connections: d.workspace_connections ?? [],
    attached_connection_ids: d.attached_connection_ids ?? [],
    missing_secrets: d.missing_secrets ?? [],
  };
}

export function useAgentDetail(id: string | null) {
  return useQuery({
    queryKey: ["agent", id],
    queryFn: () => api.get<AgentDetail>(`/api/v1/agents/${id}`).then(normalizeAgentDetail),
    enabled: !!id,
  });
}

export function useAgentActions() {
  const qc = useQueryClient();

  function invalidateAgent(id: string) {
    qc.invalidateQueries({ queryKey: ["agents"] });
    qc.invalidateQueries({ queryKey: ["agent", id] });
  }

  const delMut = useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/agents/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agents"] }),
  });

  const runMut = useMutation({
    mutationFn: (id: string) => api.post<{ status: string }>(`/api/v1/agents/${id}/run`),
    onSuccess: (_data, id) => invalidateAgent(id),
  });

  const saveScheduleMut = useMutation({
    mutationFn: ({ id, cron }: { id: string; cron: string }) =>
      api.put<AgentSchedule>(`/api/v1/agents/${id}/schedule`, { cron_expr: cron }),
    onSuccess: (_data, { id }) => invalidateAgent(id),
  });

  const deleteScheduleMut = useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/agents/${id}/schedule`),
    onSuccess: (_data, id) => invalidateAgent(id),
  });

  const saveAgentMDMut = useMutation({
    mutationFn: ({ id, content }: { id: string; content: string }) =>
      api.put<AgentDetail>(`/api/v1/agents/${id}/agent-md`, { content }),
    onSuccess: (_data, { id }) => invalidateAgent(id),
  });

  const saveSkillsMut = useMutation({
    mutationFn: ({ id, names }: { id: string; names: string[] }) =>
      api.put<AgentDetail>(`/api/v1/agents/${id}/skills`, { skill_names: names }),
    onSuccess: (_data, { id }) => invalidateAgent(id),
  });

  const saveConnectionsMut = useMutation({
    mutationFn: ({ id, ids }: { id: string; ids: string[] }) =>
      api.put<AgentDetail>(`/api/v1/agents/${id}/connections`, { connection_ids: ids }),
    onSuccess: (_data, { id }) => invalidateAgent(id),
  });

  return {
    del: (id: string) => delMut.mutateAsync(id),
    run: (id: string) => runMut.mutateAsync(id),
    saveSchedule: (id: string, cron: string) => saveScheduleMut.mutateAsync({ id, cron }),
    deleteSchedule: (id: string) => deleteScheduleMut.mutateAsync(id),
    saveAgentMD: (id: string, content: string) => saveAgentMDMut.mutateAsync({ id, content }),
    saveSkills: (id: string, names: string[]) => saveSkillsMut.mutateAsync({ id, names }),
    saveConnections: (id: string, ids: string[]) => saveConnectionsMut.mutateAsync({ id, ids }),
  };
}
