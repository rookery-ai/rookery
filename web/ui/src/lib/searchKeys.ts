import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

// Mirrors web/api_search_keys.go's DTOs. A workspace-wide Brave/Tavily API
// key upgrades web search (chat, agents, skills) from keyless scraping to a
// real search API — see internal/websearch.KeySecretNames/KeyedProvider.
// Configured state only: the value is never returned by the API.

export type SearchKeyProvider = "brave" | "tavily";

// Mirrors apiSearchKeysResponse. Note this reports CONFIGURED, not working —
// a key is proven at save time (see SaveSearchKeyResult), and the result is
// deliberately not persisted.
export type SearchKeysState = { brave: boolean; tavily: boolean };

// Mirrors apiPutSearchKeyResponse. `verified` is false when the provider could
// not be reached at all; the key is still stored in that case (an outage must
// not block a save) and `note` explains what to look at.
export type SaveSearchKeyResult = { ok: boolean; verified: boolean; note?: string };

export function useSearchKeys() {
  return useQuery({
    queryKey: ["search-keys"],
    queryFn: () => api.get<SearchKeysState>("/api/v1/search-keys"),
  });
}

export function useSaveSearchKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ provider, key }: { provider: SearchKeyProvider; key: string }) =>
      api.put<SaveSearchKeyResult>("/api/v1/search-keys", { provider, key }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["search-keys"] }),
  });
}

export function useDeleteSearchKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (provider: SearchKeyProvider) =>
      api.del<{ ok: boolean }>(`/api/v1/search-keys/${provider}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["search-keys"] }),
  });
}
