import { useKBFolders } from "@/lib/kb";

// FolderSelect is a native <select> of every vault folder, used by the
// new-note "Location" field and the bulk-Move dialog. Value "" is the vault
// root. Kept as a native select (not a Radix menu) so it stays a small,
// dependency-light control — the folder list is flat and short.
export function FolderSelect({
  value,
  onChange,
  id,
  disabledPaths,
}: {
  value: string;
  onChange: (path: string) => void;
  id?: string;
  // Paths to exclude from the options (e.g. a folder being moved can't be its
  // own destination). Descendants of these are excluded too.
  disabledPaths?: string[];
}) {
  const { data: folders = [] } = useKBFolders();
  const blocked = (p: string) =>
    (disabledPaths ?? []).some((d) => p === d || p.startsWith(d + "/"));
  const options = folders.filter((f) => !blocked(f));

  return (
    <select
      id={id}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="h-9 w-full rounded border border-border bg-background px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      {options.map((f) => (
        <option key={f} value={f}>
          {f === "" ? "/ (root)" : f}
        </option>
      ))}
    </select>
  );
}
