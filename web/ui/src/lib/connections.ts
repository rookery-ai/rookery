import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// ── Connectors (chat-platform connections: Telegram/Discord/Slack) ─────────
// Mirrors web/api_connectors.go's DTOs.

export type ConnectorField = { name: string; label: string; secret: boolean };

// Mirrors apiConnectorPlatform.
export type ConnectorPlatform = {
  platform: string;
  label: string;
  blurb: string;
  setup_steps: string[];
  fields: ConnectorField[];
  connected: boolean;
  identity: string;
};

// Mirrors apiSaveConnectorResponse.
export type SaveConnectorResponse = { ok: boolean; identity?: string; warning?: string };

// Mirrors apiTestConnectorResponse.
export type TestConnectorResponse = { ok: boolean; identity?: string; error?: string };

export function useConnectors() {
  return useQuery({
    queryKey: ["connectors"],
    queryFn: () => api.get<{ platforms: ConnectorPlatform[] }>("/api/v1/connectors"),
  });
}

export function useSaveConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ platform, values }: { platform: string; values: Record<string, string> }) =>
      api.post<SaveConnectorResponse>("/api/v1/connectors", { platform, values }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connectors"] }),
  });
}

export function useDeleteConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (platform: string) => api.del<{ ok: boolean }>(`/api/v1/connectors/${platform}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connectors"] }),
  });
}

export function useTestConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (platform: string) =>
      api.post<TestConnectorResponse>(`/api/v1/connectors/${platform}/test`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connectors"] }),
  });
}

// ── Services (self-managed-OAuth / API-key service connections) ────────────
// Mirrors web/api_services.go's DTOs.

// Mirrors apiServiceConnection.
export type ServiceConnection = { id: string; label: string; identity: string; status: string };

// Mirrors apiServiceConnectInput.
export type ServiceConnectInput = { key: string; label: string; hint: string; required: boolean };

// Mirrors apiServiceProvider.
export type ServiceProvider = {
  name: string;
  label: string;
  kind: string;
  setup_url: string;
  setup_steps: string[];
  has_creds: boolean;
  connect_inputs: ServiceConnectInput[];
  connections: ServiceConnection[];
};

export function useServices() {
  return useQuery({
    queryKey: ["services"],
    queryFn: () => api.get<{ providers: ServiceProvider[] }>("/api/v1/services"),
  });
}

export function useSaveProviderCreds() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      provider,
      clientId,
      clientSecret,
    }: {
      provider: string;
      clientId: string;
      clientSecret: string;
    }) =>
      api.post<{ ok: boolean }>(`/api/v1/services/${provider}/creds`, {
        client_id: clientId,
        client_secret: clientSecret,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["services"] }),
  });
}

// useConnectService only builds the OAuth consent URL — it does not
// perform navigation itself. The caller (e.g. a "Connect" button handler)
// takes the resolved redirect_url and does `window.location.href = ...`.
export function useConnectService() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ provider, label }: { provider: string; label?: string }) =>
      api.post<{ redirect_url: string }>(`/api/v1/services/${provider}/connect`, {
        label: label ?? "",
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["services"] }),
  });
}

export function useConnectAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      provider,
      key,
      label,
      inputs,
    }: {
      provider: string;
      key: string;
      label?: string;
      inputs?: Record<string, string>;
    }) =>
      api.post<{ ok: boolean }>(`/api/v1/services/${provider}/apikey`, {
        key,
        label: label ?? "",
        inputs: inputs ?? {},
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["services"] }),
  });
}

export function useDeleteServiceConnection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/services/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["services"] }),
  });
}
