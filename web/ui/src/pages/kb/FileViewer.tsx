import { useState } from "react";
import { useSearchParams } from "react-router";
import { AlertTriangle, Download, FileWarning, Loader2, MoreHorizontal } from "lucide-react";
import { ApiError } from "@/lib/api";
import { rawURL, useKBNote, useDeleteNote } from "@/lib/kb";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

// FileViewer opens any KB file the WYSIWYG/raw NoteEditor can't: a non-.md
// file whose kind (decided server-side by content sniffing, see
// web/api_kb.go's apiGetKBNote) is "code" (rendered read-only, monospace) or
// "binary" (Download-only — its bytes are never dumped into the DOM). It is
// deliberately read-only end to end: no save affordance exists here at all
// (not even a disabled one) — the designer is the sanctioned way to change
// agent code, since it re-tests the result; a hand-edited tools/*.py would
// silently drift from what was last verified.
//
// The header mirrors NoteHeader's breadcrumb/Download/Delete shape (see
// NoteHeader.tsx) but isn't the same component: NoteHeader's title field is
// hard-committed to markdown semantics (strips/reappends ".md", the rename
// mutation assumes that suffix) — reusing it as-is for a "script.py" would
// mangle the extension. This is the read-only subset of that pattern,
// without a title/rename control.
function FileViewerHeader({ path, onDelete }: { path: string; onDelete: () => void }) {
  const [, setParams] = useSearchParams();
  const [deleteOpen, setDeleteOpen] = useState(false);

  const segments = path.split("/");
  const filename = segments[segments.length - 1];

  return (
    <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2">
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        {segments.slice(0, -1).map((seg, i) => {
          const target = segments.slice(0, i + 1).join("/");
          return (
            <span key={target} className="flex shrink-0 items-center gap-1.5 text-xs text-muted-2">
              <button
                type="button"
                // Breadcrumb ancestors are always directories by
                // construction — carry that as an explicit `dir=1` hint
                // (review fix) so KBPage doesn't have to re-derive it from
                // the path string.
                onClick={() => setParams({ path: target, dir: "1" })}
                className="hover:text-foreground hover:underline"
              >
                {seg}
              </button>
              <span>/</span>
            </span>
          );
        })}
        <span className="min-w-0 flex-1 truncate text-sm font-semibold">{filename}</span>
      </div>

      <div className="flex shrink-0 items-center gap-3">
        <span className="text-xs text-muted-2">Read-only</span>
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
              aria-label="File actions"
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

export default function FileViewer({ path }: { path: string }) {
  const { data, isLoading, isError } = useKBNote(path);
  const [, setSearchParams] = useSearchParams();
  const deleteNote = useDeleteNote();
  // Review fix: the confirm dialog closes synchronously before the mutation
  // settles, and this had no onError — a failed DELETE looked identical to a
  // successful one (dialog closes, nothing else happens), so the user
  // reasonably believed the file was gone when it wasn't. Follows the
  // established pattern in NoteEditor.tsx's handleDelete (~:484-499) rather
  // than inventing a new one.
  const [deleteError, setDeleteError] = useState<string | null>(null);

  function handleDelete() {
    setDeleteError(null);
    deleteNote.mutate(
      { path },
      {
        onSuccess: () => {
          const parent = path.split("/").slice(0, -1).join("/");
          // `parent` is provably a directory when non-empty — carry the
          // `dir=1` hint like every other directory-targeting navigation in
          // this file (review fix: this site was missed when the default
          // flipped to "attempt to open the file", so deleting almost any
          // nested file landed on "Couldn't load this file.").
          setSearchParams(parent ? { path: parent, dir: "1" } : {});
        },
        onError: (err) => {
          setDeleteError(err instanceof ApiError ? `Delete failed: ${err.message}` : "Delete failed");
        },
      },
    );
  }

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-muted-2">
        <Loader2 className="size-5 animate-spin" />
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-sm text-danger">
        Couldn't load this file.
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <FileViewerHeader path={path} onDelete={handleDelete} />
      {deleteError && (
        <div className="flex items-center gap-2 border-b border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {deleteError}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto">
        {data.kind === "binary" ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center text-muted-2">
            <FileWarning className="size-8" />
            <p className="text-sm">
              Binary file — this format can't be previewed here. Use Download above to save it.
            </p>
          </div>
        ) : (
          <pre className="min-w-full whitespace-pre px-6 py-8 font-mono text-sm text-foreground">
            {data.content}
          </pre>
        )}
      </div>
    </div>
  );
}
