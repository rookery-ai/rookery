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
  // A config blob the setup steps say to paste (Slack's app manifest). Empty
  // for every other platform; the wizard renders the block only when set.
  setup_manifest?: string;
  fields: ConnectorField[];
  connected: boolean;
  identity: string; // the BOT's username
  // `connected` means the token authenticates; `linked` means the operator
  // completed the /start handshake and the integration is actually usable.
  linked: boolean;
  linked_identity: string;
  primary: boolean;
  dm_url: string;
  invite_url: string;
  // Whether a live adapter is running server-side right now. Advisory: it
  // distinguishes "waiting for /start" from "nothing is listening", but only
  // `linked` ever proves the handshake completed.
  bot_online: boolean;
};

// Mirrors apiSaveConnectorResponse.
export type SaveConnectorResponse = { ok: boolean; identity?: string; warning?: string };

// Mirrors apiTestConnectorResponse.
export type TestConnectorResponse = { ok: boolean; identity?: string; error?: string };

// opts.refetchInterval lets the "Link your account" step poll for the
// operator's /start handshake while unlinked; every other caller omits it and
// gets today's non-polling behaviour.
export function useConnectors(opts?: { refetchInterval?: number | false }) {
  return useQuery({
    queryKey: ["connectors"],
    queryFn: () => api.get<{ platforms: ConnectorPlatform[] }>("/api/v1/connectors"),
    refetchInterval: opts?.refetchInterval,
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

// Marks a linked platform as the one that receives unprompted delivery. 400s
// server-side if the platform isn't linked yet.
export function useSetPrimaryConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (platform: string) =>
      api.put<{ ok: boolean }>(`/api/v1/connectors/${platform}/primary`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connectors"] }),
  });
}

// Removes the operator's /start link while keeping the saved bot credentials
// — the platform falls back to connected-but-unlinked, not disconnected.
export function useUnlinkConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (platform: string) =>
      api.del<{ ok: boolean }>(`/api/v1/connectors/${platform}/identity`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connectors"] }),
  });
}

// ── Services (self-managed-OAuth / API-key service connections) ────────────
// Mirrors web/api_services.go's DTOs.

// Mirrors apiServiceConnection.
export type ServiceConnection = { id: string; label: string; identity: string; status: string };

// Mirrors apiServiceConnectInput.
export type ServiceConnectInput = { key: string; label: string; hint: string; required: boolean };

// Mirrors apiOAuthCreds. What the provider's own developer console calls its OAuth app
// credentials — Meta says "App ID"/"App Secret", Salesforce "Consumer Key". Each field
// falls back independently to "Client ID"/"Client secret", which is correct for the
// eight providers whose console genuinely says that.
export type OAuthCreds = {
  id_label?: string;
  id_hint?: string;
  secret_label?: string;
  secret_hint?: string;
};

// Mirrors apiServiceProvider.
export type ServiceProvider = {
  name: string;
  label: string;
  category: string;
  // "oauth" | "api_key" | "keyless". Keyless providers (Open-Meteo) need no
  // credential: the wizard shows setup steps and a bare Connect button.
  kind: string;
  setup_url: string;
  setup_steps: string[];
  // What the paste form should call the credential, from the provider YAML. Providers
  // differ a lot here ("AdGuard Home password", "Nextcloud app password"), so the form
  // must not assume "API key". Empty falls back to a generic label.
  key_label?: string;
  key_hint?: string;
  // Optional so existing test fixtures that omit it still typecheck.
  oauth_creds?: OAuthCreds;
  has_creds: boolean;
  // The provider owning the OAuth application this one authenticates through —
  // itself, or its auth_parent when aliased (google_calendar → google). The
  // wizard names it so the user knows WHICH app to edit; a child has no app of
  // its own in the provider's console. Optional so older fixtures typecheck.
  app_provider?: string;
  app_label?: string;
  // "create" (no stored credentials) or "update" (credentials exist, possibly
  // inherited from the parent). Drives whether the guidance says create an
  // application or update the existing one.
  setup_mode?: string;
  action_count: number;
  connect_inputs: ServiceConnectInput[];
  // The exact URI to register with the provider. Empty for api_key providers,
  // which never leave the browser.
  redirect_uri: string;
  // Why the current instance URL will not work with this provider. "hard"
  // disables Connect; "soft" warns only.
  preflight: PreflightProblem[];
  connections: ServiceConnection[];
};

// Mirrors apiPreflightProblem.
export type PreflightProblem = {
  severity: "hard" | "soft";
  code: string;
  message: string;
  fix: string;
};

// Mirrors apiPublicURLSummary — "what does my current instance URL cost me?"
export type PublicURLSummary = {
  base_url: string;
  oauth_providers: number;
  clean_providers: number;
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
  "Self-hosted",
  "Health & Fitness",
  "Finance",
  "Data & Reference",
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
    queryFn: () =>
      api.get<{ providers: ServiceProvider[]; summary: PublicURLSummary }>("/api/v1/services"),
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
