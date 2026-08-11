import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";

// Mirrors web.apiMCPServer. There is deliberately no credential field of any
// shape — not even a redacted one. A field that exists is a field a future handler
// can accidentally populate, and this one would be a server credential.
export type MCPServer = {
  id: string;
  name: string;
  slug: string;
  url: string;
  auth_kind: "none" | "bearer" | "header";
  header_name: string;
  has_token: boolean;
  enabled: boolean;
  status: "ACTIVE" | "NEEDS_AUTH" | "UNREACHABLE";
  last_error: string;
  synced_at: string;
  tool_count: number;
  active_tools: number;
};

export type MCPTool = {
  id: string;
  name: string;
  tool_name: string;
  title: string;
  description: string;
  read_only: boolean;
  approval_mode: "auto" | "approve";
  enabled: boolean;
  missing: boolean;
};

export type MCPSyncResult = {
  discovered?: number;
  added?: number;
  missing?: number;
  held_back?: number;
  status?: string;
  // A sync that RAN but whose server refused or was unreachable returns 200 with
  // this set, rather than a 5xx that would read as "Rookery is broken" and send the
  // owner looking in the wrong place.
  error?: string;
};

export type MCPServerInput = {
  name: string;
  url: string;
  auth_kind: string;
  header_name?: string;
  token?: string;
  enabled?: boolean;
};

export function useMCPServers() {
  return useQuery({
    queryKey: ["mcp", "servers"],
    // `?? []` guards the null a Go nil slice would marshal to. The server
    // initialises its slices, but a consumer that assumes non-null is one
    // regression away from unmounting the route.
    queryFn: async () => {
      const r = await api.get<{ servers: MCPServer[] }>("/api/v1/mcp/servers");
      return r.servers ?? [];
    },
  });
}

export function useMCPTools(serverId: string | null) {
  return useQuery({
    queryKey: ["mcp", "tools", serverId],
    enabled: !!serverId,
    queryFn: async () => {
      const r = await api.get<{ tools: MCPTool[]; cap: number }>(
        `/api/v1/mcp/servers/${serverId}/tools`,
      );
      return { tools: r.tools ?? [], cap: r.cap ?? 0 };
    },
  });
}

export function useCreateMCPServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: MCPServerInput) =>
      api.post<{ server: MCPServer }>("/api/v1/mcp/servers", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mcp"] }),
  });
}

export function useUpdateMCPServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...input }: MCPServerInput & { id: string }) =>
      api.put<{ server: MCPServer }>(`/api/v1/mcp/servers/${id}`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mcp"] }),
  });
}

export function useDeleteMCPServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.del<{ ok: boolean }>(`/api/v1/mcp/servers/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mcp"] }),
  });
}

export function useSyncMCPServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api.post<MCPSyncResult>(`/api/v1/mcp/servers/${id}/sync`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mcp"] }),
  });
}

export function useUpdateMCPTool() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      serverId,
      toolId,
      ...patch
    }: {
      serverId: string;
      toolId: string;
      enabled?: boolean;
      read_only?: boolean;
      approval_mode?: string;
    }) =>
      api.put<{ ok: boolean }>(
        `/api/v1/mcp/servers/${serverId}/tools/${toolId}`,
        patch,
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mcp"] }),
  });
}

export function useAgentMCPServers(agentId: string) {
  return useQuery({
    queryKey: ["mcp", "agent", agentId],
    queryFn: async () => {
      const r = await api.get<{ servers: MCPServer[]; attached: string[] }>(
        `/api/v1/agents/${agentId}/mcp`,
      );
      return { servers: r.servers ?? [], attached: r.attached ?? [] };
    },
  });
}

export function useSaveAgentMCPServers(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (serverIds: string[]) =>
      api.put<{ ok: boolean }>(`/api/v1/agents/${agentId}/mcp`, {
        server_ids: serverIds,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["mcp", "agent", agentId] }),
  });
}

// statusLabel keeps the two failure states distinguishable in the UI, because they
// call for different actions: NEEDS_AUTH means the credential was rejected and only
// the owner can fix it, while UNREACHABLE may well resolve on its own.
export function statusLabel(s: MCPServer["status"]): {
  text: string;
  tone: "ok" | "warn" | "danger";
} {
  switch (s) {
    case "NEEDS_AUTH":
      return { text: "Needs credential", tone: "danger" };
    case "UNREACHABLE":
      return { text: "Unreachable", tone: "warn" };
    default:
      return { text: "Connected", tone: "ok" };
  }
}
