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
  category: string;
  kind: string;
  setup_url: string;
  setup_steps: string[];
  has_creds: boolean;
  action_count: number;
  connect_inputs: ServiceConnectInput[];
  connections: ServiceConnection[];
};

// CATEGORY_ORDER fixes the section order on the connections page. A fixed array
// rather than sorting alphabetically: the groups have a natural priority, and
// alphabetical order would reshuffle the page as providers are added.
export const CATEGORY_ORDER = [
  "Google",
  "Publishing & Media",
  "Advertising",
  "Productivity",
  "Communication",
  "Commerce",
  "Developer",
  "Support",
  "Other",
] as const;

/**
 * Groups providers into ordered [category, providers] pairs, preserving the
 * incoming order within each group. A provider whose category is empty or not
 * in CATEGORY_ORDER falls into "Other" — it must never disappear from a page
 * whose whole purpose is showing every available integration. Empty categories
 * are dropped so "Advertising" leaves no blank heading before those providers
 * exist.
 */
export function groupByCategory(
  providers: ServiceProvider[],
): Array<[string, ServiceProvider[]]> {
  const known = new Set<string>(CATEGORY_ORDER);
  const buckets = new Map<string, ServiceProvider[]>();

  for (const p of providers) {
    const key = known.has(p.category) ? p.category : "Other";
    const bucket = buckets.get(key);
    if (bucket) bucket.push(p);
    else buckets.set(key, [p]);
  }

  return CATEGORY_ORDER.filter((c) => (buckets.get(c)?.length ?? 0) > 0).map(
    (c) => [c, buckets.get(c)!] as [string, ServiceProvider[]],
  );
}

export function useServices() {
  return useQuery({
    queryKey: ["services"],
    queryFn: () => api.get<{ providers: ServiceProvider[] }>("/api/v1/services"),
  });
}

// Mirrors apiConnectorAction. `params` is the action's JSON Schema. Real
// manifests can nest schemas deeper than this (e.g. an array param's `items`
// — see internal/connectors/schema.go's propSchema), but that's intentionally
// not modeled here: the actions panel only ever renders a param's top-level
// `type`, so an array param just shows as "array" and the nested shape is
// never read.
export type ConnectorActionParams = {
  properties?: Record<string, { type?: string; description?: string }>;
  required?: string[];
};

export type ConnectorAction = {
  name: string;
  description: string;
  mutating: boolean;
  public_write: boolean;
  params: ConnectorActionParams;
};

/**
 * Fetches one provider's curated action list.
 *
 * The key root is "service-actions", NOT "services": React Query invalidates by
 * key prefix, so every connect/disconnect mutation's
 * invalidateQueries({queryKey:["services"]}) would evict these lists too.
 *
 * staleTime: Infinity because the manifests are compiled into the binary via
 * go:embed and cannot change while the server runs. Fetched lazily — only the
 * actions view mounts this hook, so opening the wizard costs nothing.
 */
export function useProviderActions(provider: string) {
  return useQuery({
    queryKey: ["service-actions", provider],
    queryFn: () =>
      api.get<{ actions: ConnectorAction[] }>(
        `/api/v1/services/${provider}/actions`,
      ),
    staleTime: Infinity,
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
    mutationFn: ({
      provider,
      label,
      inputs,
    }: {
      provider: string;
      label?: string;
      // connect_inputs collected before consent (e.g. a Google Ads developer token)
      // — they cannot be discovered from any API, so they ride the signed state.
      inputs?: Record<string, string>;
    }) =>
      api.post<{ redirect_url: string }>(`/api/v1/services/${provider}/connect`, {
        label: label ?? "",
        inputs: inputs ?? {},
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
