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

export function useAuditLog(limit = 100) {
  return useQuery({
    queryKey: ["audit-log", limit],
    queryFn: () => api.get<{ logs: AuditLogEntry[] }>(`/api/v1/admin/audit?limit=${limit}`),
  });
}

export function useDeleteWorkspaceAdmin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/workspaces/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["session"] }),
  });
}
