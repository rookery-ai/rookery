import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

// Mirrors web/api_search.go's searchItem/searchGroup.
export type SearchItem = {
  title: string;
  id?: string;
  path?: string;
  line?: number;
  snippet?: string;
  url?: string;
};

export type SearchGroup = {
  kind: string;
  items: SearchItem[];
};

export type SearchResponse = {
  query: string;
  groups: SearchGroup[];
};

// The backend 400s on an empty q, so the query only runs once there's
// something to search for — the caller (CommandPalette) debounces `q`
// before it reaches here.
export function useGlobalSearch(q: string) {
  const query = q.trim();
  return useQuery({
    queryKey: ["global-search", query],
    queryFn: () => api.get<SearchResponse>(`/api/v1/search?q=${encodeURIComponent(query)}`),
    enabled: query.length > 0,
  });
}
