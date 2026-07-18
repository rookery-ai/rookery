import { useEffect, useState, type ReactNode } from "react";
import { Search, X } from "lucide-react";
import { Input } from "@/components/ui/input";
import { useKBSearch, type KBSearchHit } from "@/lib/kb";

const DEBOUNCE_MS = 300;

// Wraps the substring of `snippet` matching `query` (case-insensitive, first
// occurrence only) in <mark>. No match (shouldn't normally happen — the
// server only returns hits that matched) falls back to the plain snippet.
function highlight(snippet: string, query: string) {
  const idx = snippet.toLowerCase().indexOf(query.toLowerCase());
  if (idx === -1) return snippet;
  return (
    <>
      {snippet.slice(0, idx)}
      <mark>{snippet.slice(idx, idx + query.length)}</mark>
      {snippet.slice(idx + query.length)}
    </>
  );
}

// Renders under the KB pane title (KBPage's Task-6 slot). Owns the query
// input debouncing directly rather than lifting query state to KBPage: it
// takes the tree as `children` and swaps to search results in place of it
// once the (debounced) query reaches 2 chars — a self-contained
// "replace the tree" component instead of KBPage having to track a
// "is searching" flag itself.
export default function SearchBox({
  onSelect,
  children,
}: {
  onSelect: (path: string) => void;
  children: ReactNode;
}) {
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");

  useEffect(() => {
    const t = window.setTimeout(() => setQuery(input), DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [input]);

  const active = query.length >= 2;
  const { data, isLoading } = useKBSearch(query);

  function clear() {
    setInput("");
    setQuery("");
  }

  return (
    <div className="flex h-full flex-col">
      <div className="relative px-2 py-1.5">
        <Search className="pointer-events-none absolute left-4 top-1/2 size-3.5 -translate-y-1/2 text-muted-2" />
        <Input
          placeholder="Search notes…"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === "Escape" && clear()}
          className="h-8 pl-8 text-sm"
        />
        {input && (
          <button
            type="button"
            aria-label="Clear search"
            onClick={clear}
            className="absolute right-4 top-1/2 -translate-y-1/2 text-muted-2 hover:text-foreground"
          >
            <X className="size-3.5" />
          </button>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {active ? (
          <SearchResults hits={data?.hits ?? []} query={query} loading={isLoading} onSelect={onSelect} />
        ) : (
          children
        )}
      </div>
    </div>
  );
}

function SearchResults({
  hits,
  query,
  loading,
  onSelect,
}: {
  hits: KBSearchHit[];
  query: string;
  loading: boolean;
  onSelect: (path: string) => void;
}) {
  if (loading) return <div className="px-3 py-4 text-xs text-muted-2">Searching…</div>;
  if (hits.length === 0) return <div className="px-3 py-4 text-xs text-muted-2">No results</div>;
  return (
    <ul className="px-1 py-1">
      {hits.map((hit) => (
        <li key={`${hit.path}:${hit.line}`}>
          <button
            type="button"
            onClick={() => onSelect(hit.path)}
            className="block w-full rounded px-2 py-1.5 text-left hover:bg-chrome"
          >
            <div className="truncate text-xs font-medium text-foreground">{hit.path}</div>
            <div className="truncate text-xs text-muted-2">{highlight(hit.snippet, query)}</div>
          </button>
        </li>
      ))}
    </ul>
  );
}
