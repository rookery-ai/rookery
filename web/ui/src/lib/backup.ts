import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// Mirrors web/api_backup.go's backupConfigDTO. Every encrypted field is
// deliberately absent: the API reports only whether a secret is set, never its
// value.

export type BackupS3 = {
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key: string;
  secret_key_set: boolean;
  path_style: boolean;
};

export type BackupConfig = {
  enabled: boolean;
  destination: "local" | "s3";
  schedule: "daily" | "weekly";
  hour: number;
  weekday: number;
  retention: number;
  passphrase_set: boolean;
  local_dir: string;
  s3: BackupS3;
  last_run_at: string;
  last_status: string;
  last_error: string;
  last_size: number;
  next_run_at: string;
  pending_restore: boolean;
};

export type Snapshot = { name: string; size: number; mod_time: string };

export type SaveBackupConfig = {
  enabled: boolean;
  destination: string;
  schedule: string;
  hour: number;
  weekday: number;
  retention: number;
  passphrase?: string;
  local: { dir: string };
  s3: {
    endpoint: string;
    region: string;
    bucket: string;
    prefix: string;
    access_key: string;
    secret_key?: string;
    path_style: boolean;
  };
};

export function useBackupConfig() {
  return useQuery({
    queryKey: ["backup", "config"],
    queryFn: () => api.get<BackupConfig>("/backup/config"),
  });
}

// Listing hits the configured destination, so it is only attempted once one is
// actually configured — otherwise every unconfigured install shows an error.
export function useSnapshots(enabled: boolean) {
  return useQuery({
    queryKey: ["backup", "snapshots"],
    queryFn: () => api.get<Snapshot[]>("/backup/snapshots"),
    enabled,
    retry: false,
  });
}

export function useSaveBackupConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: SaveBackupConfig) => api.put<BackupConfig>("/backup/config", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup"] }),
  });
}

export function useRunBackup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ name: string }>("/backup/run", {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup"] }),
  });
}

export function useDeleteSnapshot() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.del<void>(`/backup/snapshots/${encodeURIComponent(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup", "snapshots"] }),
  });
}

export function useVerifySnapshot() {
  return useMutation({
    mutationFn: (body: { name: string; passphrase: string }) =>
      api.post<{ ok: boolean; files: number; workspaces: number }>("/backup/verify", body),
  });
}

export function useRestoreSnapshot() {
  return useMutation({
    mutationFn: (body: { name: string; passphrase: string; confirm: string }) =>
      api.post<{ status: string; message: string }>("/backup/restore", body),
  });
}

// formatBytes renders a snapshot size compactly. Sizes here span kilobytes to
// gigabytes, so a fixed unit would be unreadable at one end or the other.
export function formatBytes(n: number): string {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}
