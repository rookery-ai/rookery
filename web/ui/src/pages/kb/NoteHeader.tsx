import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { Download, FileCode, Link2, MoreHorizontal } from "lucide-react";
import { cn } from "@/lib/utils";
import { rawURL } from "@/lib/kb";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

// Redeclared locally (identical literal set to NoteEditor's `SaveState`)
// rather than imported from NoteEditor — NoteEditor renders NoteHeader, so
// importing the type back from there would be a needless (if type-only-safe)
// circular edge. See task-6-report.md for the note on this choice.
export type EditorSaveState = "saved" | "saving" | "dirty" | "error" | "raw";

const STATE_LABEL: Record<EditorSaveState, string> = {
  saved: "Saved ✓",
  saving: "Saving…",
  dirty: "Unsaved",
  error: "Save failed",
  raw: "Raw mode",
};

export default function NoteHeader({
  path,
  state,
  backlinksCount,
  onRename,
  onDelete,
  rawMode,
  onToggleRaw,
}: {
  path: string;
  state: EditorSaveState;
  backlinksCount: number;
  onRename: (to: string) => void;
  onDelete: () => void;
  rawMode: boolean;
  onToggleRaw: () => void;
}) {
  const [, setParams] = useSearchParams();
  const [deleteOpen, setDeleteOpen] = useState(false);

  const segments = path.split("/");
  const filename = segments[segments.length - 1];
  const dir = segments.slice(0, -1).join("/");
  const originalTitle = filename.replace(/\.md$/, "");

  const [value, setValue] = useState(originalTitle);
  // A commit already fired for the current value (Enter blurs the input,
  // which would otherwise fire the blur handler's commit a second time with
  // the same {to} — this dedupes that without needing the actual rename to
  // have completed yet, since `path` doesn't change until the mutation's
  // onSuccess navigates here).
  const committedRef = useRef(false);

  useEffect(() => {
    setValue(originalTitle);
    committedRef.current = false;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  function commit() {
    if (committedRef.current) return;
    const trimmed = value.trim();
    if (!trimmed || trimmed === originalTitle) return;
    committedRef.current = true;
    onRename(dir ? `${dir}/${trimmed}.md` : `${trimmed}.md`);
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter") {
      e.preventDefault();
      commit();
      e.currentTarget.blur();
    } else if (e.key === "Escape") {
      committedRef.current = true;
      setValue(originalTitle);
      e.currentTarget.blur();
    }
  }

  return (
    <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2">
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        {segments.slice(0, -1).map((seg, i) => {
          const target = segments.slice(0, i + 1).join("/");
          return (
            <span key={target} className="flex shrink-0 items-center gap-1.5 text-xs text-muted-2">
              <button
                type="button"
                onClick={() => setParams({ path: target })}
                className="hover:text-foreground hover:underline"
              >
                {seg}
              </button>
              <span>/</span>
            </span>
          );
        })}
        <input
          value={value}
          onChange={(e) => {
            committedRef.current = false;
            setValue(e.target.value);
          }}
          onKeyDown={handleKeyDown}
          onBlur={commit}
          className="min-w-0 flex-1 truncate rounded border border-transparent bg-transparent px-1 text-sm font-semibold outline-none hover:border-border focus:border-ring focus:ring-[3px] focus:ring-ring/50"
          aria-label="Note title"
        />
        <span className="shrink-0 text-xs text-muted-2">.md</span>
      </div>

      <div className="flex shrink-0 items-center gap-3">
        <span className={cn("text-xs", state === "error" ? "text-danger" : "text-muted-2")}>
          {STATE_LABEL[state]}
        </span>
        {backlinksCount > 0 && (
          <span className="flex items-center gap-1 text-xs text-muted-2">
            <Link2 className="size-3.5" />
            {backlinksCount} {backlinksCount === 1 ? "backlink" : "backlinks"}
          </span>
        )}
        <Button variant="ghost" size="sm" onClick={onToggleRaw}>
          <FileCode className="size-4" />
          {rawMode ? "Rich text" : "Raw"}
        </Button>
        <Button variant="ghost" size="sm" asChild>
          <a href={rawURL(path)} download>
            <Download className="size-4" />
            Download
          </a>
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label="Note actions"
              className="shrink-0 rounded p-1 hover:bg-border"
            >
              <MoreHorizontal className="size-4" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem variant="destructive" onSelect={() => setDeleteOpen(true)}>
              Delete…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete “{path}”?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-2">This can’t be undone.</p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)}>Cancel</Button>
            <Button
              variant="destructive"
              onClick={() => {
                setDeleteOpen(false);
                onDelete();
              }}
            >
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
