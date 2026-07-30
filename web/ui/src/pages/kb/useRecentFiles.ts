import { useCallback, useEffect, useRef, useState } from "react";
import { useSession } from "@/lib/session";

// Recently-viewed knowledge base files, most recent first.
//
// This is a VIEW HISTORY, written only when the user clicks a file in the UI —
// deliberately not an mtime listing of the vault. Agent run logs, reflected chat
// transcripts and inbox notifications are by far the most recently WRITTEN files
// in a working vault, so an mtime-ordered "recent" list would be a wall of
// run_20260624_101329.md with the user's own notes pushed off the end. Keying on
// what the user actually opened means agent churn can never enter the list, with
// no dir-exclusion list to keep in sync as the vault layout grows.
//
// Persisted in localStorage (same approach as rookery.paneWidth) rather than
// server-side: it is a per-browser convenience, not workspace state, and it must
// be readable synchronously on first paint to decide what to auto-open.

// Keyed PER WORKSPACE. Entries are workspace-relative vault paths
// ("notes/trip.md"), and this platform's whole model is one owner switching
// between fully isolated workspaces. A single shared key would carry workspace
// A's history into workspace B: the Recent strip would list A's notes while the
// user is in B, and the auto-open would either 404 its way down the list or —
// worse — silently open B's same-named file under A's title.
const RECENT_KEY_PREFIX = "rookery.kb.recent";

export function recentStorageKey(workspaceID?: string): string {
  return workspaceID ? `${RECENT_KEY_PREFIX}.${workspaceID}` : RECENT_KEY_PREFIX;
}

/** How many entries are shown in the context pane. */
export const RECENT_VISIBLE = 5;

// More are RETAINED than shown so the list survives a few stale entries (files
// deleted or renamed outside this UI) without immediately going short.
const RECENT_MAX = 20;

export type RecentFile = {
  path: string;
  /** Resolved display name, captured at click time from whichever surface the
   *  user clicked — the tree already knows it, so no extra request is needed. */
  title: string;
};

function isRecentFile(v: unknown): v is RecentFile {
  if (typeof v !== "object" || v === null) return false;
  const o = v as Record<string, unknown>;
  return typeof o.path === "string" && o.path !== "" && typeof o.title === "string";
}

/**
 * readRecent parses the stored list, tolerating anything.
 *
 * A corrupt or partially-corrupt value degrades to whatever entries are still
 * valid rather than throwing: this runs during render to decide what to
 * auto-open, so an exception here would blank the whole knowledge base page —
 * a far worse outcome than a short recents list.
 */
export function readRecent(workspaceID?: string): RecentFile[] {
  try {
    const raw = localStorage.getItem(recentStorageKey(workspaceID));
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(isRecentFile).slice(0, RECENT_MAX);
  } catch {
    return [];
  }
}

function writeRecent(workspaceID: string | undefined, list: RecentFile[]) {
  try {
    localStorage.setItem(recentStorageKey(workspaceID), JSON.stringify(list));
  } catch {
    // Storage full or blocked (private mode). The list is a convenience; losing
    // persistence is not worth breaking navigation over.
  }
}

/** push returns `list` with `entry` moved (or added) to the front, deduped by
 *  path so re-opening a note promotes it instead of adding a second row. */
export function pushRecent(list: RecentFile[], entry: RecentFile): RecentFile[] {
  return [entry, ...list.filter((e) => e.path !== entry.path)].slice(0, RECENT_MAX);
}

export function useRecentFiles() {
  // The workspace id gates everything below. It arrives asynchronously (the
  // session query), so it can legitimately be undefined on the first render —
  // hence the reload effect rather than reading once in the state initializer:
  // reading at mount would permanently bind this hook to the unscoped key.
  const workspaceID = useSession().data?.workspace?.id;
  const [recent, setRecent] = useState<RecentFile[]>([]);
  const loadedForRef = useRef<string | undefined>(undefined);

  // Re-read whenever the active workspace changes. This is what makes switching
  // workspaces swap the history rather than carry it across — and it must run
  // before any write, or the empty initial state would be flushed over the
  // stored list.
  useEffect(() => {
    if (!workspaceID) return;
    loadedForRef.current = workspaceID;
    setRecent(readRecent(workspaceID));
  }, [workspaceID]);

  useEffect(() => {
    // Only persist once the list belongs to the workspace we'd be writing to.
    // Without this guard the initial empty state, or the outgoing workspace's
    // list, would be written under the incoming workspace's key.
    if (!workspaceID || loadedForRef.current !== workspaceID) return;
    writeRecent(workspaceID, recent);
  }, [workspaceID, recent]);

  /** Record a file the user opened. Callers must not call this for directories
   *  or for programmatic navigation — the list's whole value is that every entry
   *  is something the user deliberately opened. */
  const record = useCallback((entry: RecentFile) => {
    setRecent((list) => pushRecent(list, entry));
  }, []);

  /** Drop an entry whose file no longer exists. Called lazily, when opening it
   *  turns out to 404, rather than by validating the whole list up front — that
   *  would cost one request per entry on every page load. */
  const forget = useCallback((path: string) => {
    setRecent((list) => list.filter((e) => e.path !== path));
  }, []);

  /** Follow a rename so the entry keeps pointing at the file the user knows. */
  const rename = useCallback((from: string, to: string, title?: string) => {
    setRecent((list) =>
      list.map((e) => (e.path === from ? { path: to, title: title ?? e.title } : e)),
    );
  }, []);

  return { recent, record, forget, rename };
}
