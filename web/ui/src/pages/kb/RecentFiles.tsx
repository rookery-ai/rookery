import { FileText } from "lucide-react";
import { RECENT_VISIBLE, type RecentFile } from "./useRecentFiles";

// The "Recently viewed" strip at the top of the knowledge base context pane.
//
// Renders nothing at all until the user has opened something. An empty
// "Recent" heading over blank space is worse than the file tree simply starting
// where it always did, and a first-time user has no history to show.
export default function RecentFiles({
  recent,
  selectedPath,
  onSelect,
}: {
  recent: RecentFile[];
  selectedPath: string | null;
  onSelect: (path: string, title: string) => void;
}) {
  if (recent.length === 0) return null;

  return (
    // shrink-0: this is a fixed block above the tree's scroll region, so it must
    // not be compressed when the tree is long.
    <div className="shrink-0 border-b border-border px-2 py-2">
      <h2 className="px-2 pb-1 text-xs font-medium tracking-wide text-muted-2 uppercase">
        Recent
      </h2>
      <ul>
        {recent.slice(0, RECENT_VISIBLE).map((entry) => {
          const selected = entry.path === selectedPath;
          return (
            <li key={entry.path}>
              <button
                type="button"
                onClick={() => onSelect(entry.path, entry.title)}
                aria-current={selected ? "true" : undefined}
                // title carries the full path: the visible label is the resolved
                // display name, which for a reflected note (a chat transcript, an
                // inbox notification) says nothing about WHERE the file lives.
                title={entry.path}
                className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left text-sm transition-colors ${
                  selected ? "bg-accent-soft text-foreground" : "text-muted hover:bg-chrome"
                }`}
              >
                <FileText className="size-4 shrink-0 text-muted-2" />
                <span className="truncate">{entry.title}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
