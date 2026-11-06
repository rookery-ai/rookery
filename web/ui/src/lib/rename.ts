import type { QueryClient } from "@tanstack/react-query";

// Mirrors apiRenameResult (web/api_rename.go).
export type RenameResult = {
  id: string;
  name: string;
  still_mentions: string[];
  warnings: string[];
  env_removed: boolean;
};

// A rename rewrites files and rows the browser may hold. An open KB note matters most:
// keeping its old content would let an autosave write the old name back.
export function invalidateAfterRename(qc: QueryClient) {
  for (const key of ["agents", "agent", "skills", "skill", "kb-tree", "kb-note", "inbox", "inbox-poll"]) {
    void qc.invalidateQueries({ queryKey: [key] });
  }
}

// The toast's text, or null when the rename has nothing to add to the new name on screen.
export function renameNotice(r: RenameResult): string | null {
  const parts: string[] = [];
  if (r.env_removed) parts.push("Its Python packages will reinstall on the next run.");
  const n = r.still_mentions.length;
  if (n > 0) {
    parts.push(
      `${n === 1 ? "1 file still mentions" : `${n} files still mention`} the old name in prose, left as written: ${r.still_mentions[0]}${n > 1 ? " and others" : ""}.`,
    );
  }
  parts.push(...r.warnings);
  return parts.length > 0 ? parts.join(" ") : null;
}
