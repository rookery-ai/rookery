import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// Mirrors web/api_settings.go's DTOs (apiGetSettings).

export type Profile = {
  display_name: string;
  email: string;
  location: string;
  timezone: string;
  tone: string;
  language: string;
  notes: string;
};

export type WorkspaceMeta = { name: string; about: string };

export type CoderConfig = {
  kind: string;
  bin: string;
  timeout_s: number;
  provider: string;
  model: string;
  base_url: string;
  api_key_secret: string;
};

export type DetectedCoder = { name: string; bin: string; backend_type: string };

export type APIProvider = {
  name: string;
  label: string;
  schema: string;
  model_placeholder: string;
  docs_url: string;
  requires_key: boolean;
  custom: boolean;
};

// Mirrors apiCoderCatalogEntry — note the camelCase requiresKey/hasKey (matches
// the existing template-page JS contract, deliberately not snake_case).
export type CoderCatalogEntry = {
  name: string;
  base: string;
  model: string;
  docs: string;
  requiresKey: boolean;
  custom: boolean;
  hasKey: boolean;
};

export type Settings = {
  profile: Profile;
  workspace: WorkspaceMeta;
  coder: CoderConfig;
  detected_coders: DetectedCoder[];
  api_providers: APIProvider[];
  coder_catalog: CoderCatalogEntry[];
  secret_names: string[];
  // "slim" builds ship no CLI coder binary; the local engine is hidden.
  coder_mode?: "full" | "slim";
};

export function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<Settings>("/api/v1/settings"),
  });
}

export function useSaveProfile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (profile: Profile) =>
      api.put<{ ok: boolean }>("/api/v1/settings/profile", profile),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["settings"] }),
  });
}

// Also invalidates ["session"] so the rail's workspace name/initial updates
// immediately after a rename.
export function useSaveWorkspaceMeta() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (meta: WorkspaceMeta) =>
      api.put<{ ok: boolean }>("/api/v1/settings/workspace", meta),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings"] });
      qc.invalidateQueries({ queryKey: ["session"] });
    },
  });
}

// Mirrors apiCoderRequest (web/api_settings.go) field names exactly.
export type SaveCoderInput = {
  kind: string;
  bin: string;
  timeout_s: number;
  provider: string;
  model: string;
  base_url: string;
  api_key: string;
};

export function useSaveCoder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SaveCoderInput) =>
      api.put<{ ok: boolean }>("/api/v1/settings/coder", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["settings"] }),
  });
}

export type TestCoderResponse = { ok: boolean; reply?: string; error?: string };

export function useTestCoder() {
  return useMutation({
    mutationFn: () => api.post<TestCoderResponse>("/api/v1/settings/coder/test"),
  });
}

export type ChangeMasterPasswordInput = {
  current: string;
  new_password: string;
  confirm: string;
};

export function useChangeMasterPassword() {
  return useMutation({
    mutationFn: (input: ChangeMasterPasswordInput) =>
      api.put<{ ok: boolean }>("/api/v1/settings/master-password", input),
  });
}

// ── Owner sections (workspaces / system status / audit) ────────────────────
// Mirrors web/api_workspaces.go's DTOs. These endpoints are owner-gated
// (requireOwnerAPI, not requireActiveWorkspaceAPI) — no workspace-master-
// password re-entry is needed to read/write them.

// Read-only runtime status. The former claude_bin / coder_timeout /
// agent_timeout / memory_mb fields were removed: nothing on the server ever
// read them back, so the form only looked like it configured the system.
export type AdminSettings = {
  sandbox_on: boolean;
  landlock_ready: boolean;
};

export function useAdminSettings() {
  return useQuery({
    queryKey: ["admin-settings"],
    queryFn: () => api.get<AdminSettings>("/api/v1/admin/settings"),
  });
}

// Mirrors apiAuditLog.
export type AuditLogEntry = {
  workspace_id: string;
  action: string;
  target: string;
  detail: string;
  ip: string;
  created_at: string;
};

// Filters are sent to the server, not applied to the returned page: narrowing
// an already-truncated list of the most recent N events would report "no
// matches" for something that merely happened N+1 events ago.
export type AuditLogFilters = {
  workspace_id?: string;
  action?: string;
  q?: string;
  since_days?: number;
  limit?: number;
};

export function useAuditLog(filters: AuditLogFilters = {}) {
  const params = new URLSearchParams();
  params.set("limit", String(filters.limit ?? 100));
  if (filters.workspace_id) params.set("workspace_id", filters.workspace_id);
  if (filters.action) params.set("action", filters.action);
  if (filters.q) params.set("q", filters.q);
  if (filters.since_days) params.set("since_days", String(filters.since_days));
  const qs = params.toString();

  return useQuery({
    queryKey: ["audit-log", qs],
    // `actions` is the distinct set across the WHOLE log, not just this page,
    // so the action picker keeps offering a value even while it is selected
    // and has narrowed the results to a handful of rows.
    queryFn: () => api.get<{ logs: AuditLogEntry[]; actions: string[] }>(`/api/v1/admin/audit?${qs}`),
    placeholderData: (prev) => prev,
  });
}

export function useDeleteWorkspaceAdmin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/workspaces/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["session"] }),
  });
}

// ── Instance public URL ──────────────────────────────────────────────────────
// The base URL every OAuth redirect URI is built from. Instance-level, not
// per-workspace: it is a property of the deployment.

export type PublicURLState = {
  public_url: string; // configured value, "" when unset
  public_url_actual: string; // what is actually in use right now
  public_url_source: "configured" | "env" | "detected";
};

export function usePublicURL() {
  return useQuery({
    queryKey: ["admin", "public-url"],
    queryFn: () => api.get<PublicURLState>("/api/v1/admin/public-url"),
  });
}

export function useSavePublicURL() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (url: string) => api.put<PublicURLState>("/api/v1/admin/public-url", { url }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "public-url"] });
      // The redirect URI and every provider's preflight are derived from this.
      void qc.invalidateQueries({ queryKey: ["services"] });
    },
  });
}

export type PublicURLTestResult = { ok: boolean; warning?: boolean; error?: string };

export function useTestPublicURL() {
  return useMutation({
    mutationFn: (url: string) =>
      api.post<PublicURLTestResult>("/api/v1/admin/public-url/test", { url }),
  });
}
