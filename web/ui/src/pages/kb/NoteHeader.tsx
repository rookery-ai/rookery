import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import {
  AlertTriangle,
  Download,
  FileCode,
  Link2,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { rawURL, exportURL, useExportFormats, isProtectedPath } from "@/lib/kb";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { FileText } from "lucide-react";
import EmojiPicker from "./EmojiPicker";
import ChatAboutFileButton from "./ChatAboutFileButton";

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

// ExportMenu offers downloading the note as HTML / Word / PDF / Markdown. The
// formats endpoint says whether the host can produce a PDF (needs a headless
// renderer installed); when it can't, that item is disabled with an
// explanation. Downloads go through same-origin anchors — the export endpoint
// sends Content-Disposition: attachment, so the browser saves rather than
// navigates; Markdown reuses the existing raw endpoint.
function ExportMenu({ path }: { path: string }) {
  const { data: formats } = useExportFormats();
  const pdfOK = formats?.pdf ?? false;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm">
          <Download className="size-4" />
          Export
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem asChild>
          <a href={exportURL(path, "html")} download>
            HTML
          </a>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <a href={exportURL(path, "docx")} download>
            Word (.docx)
          </a>
        </DropdownMenuItem>
        {pdfOK ? (
          <DropdownMenuItem asChild>
            <a href={exportURL(path, "pdf")} download>
              PDF
            </a>
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            disabled
            onSelect={(e) => e.preventDefault()}
            title="PDF export needs a headless renderer on the server (weasyprint, chromium, wkhtmltopdf, libreoffice, or pandoc)."
          >
            PDF (unavailable)
          </DropdownMenuItem>
        )}
        <DropdownMenuItem asChild>
          <a href={rawURL(path)} download>
            Markdown (.md)
          </a>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// The title field is a filename, not a path — a typed ".md" (any case) is
// redundant with the fixed suffix we already append, not a second segment
// to keep (review fix: typing "summer.md" used to rename to "summer.md.md").
function stripMdExt(s: string): string {
  return /\.md$/i.test(s) ? s.slice(0, -3) : s;
}

export default function NoteHeader({
  path,
  state,
  backlinksCount,
  onRename,
  onDelete,
  rawMode,
  onToggleRaw,
  renameError,
  lossyInRichText,
  icon,
  onSetIcon,
}: {
  path: string;
  state: EditorSaveState;
  backlinksCount: number;
  onRename: (to: string) => void;
  onDelete: () => void;
  rawMode: boolean;
  onToggleRaw: () => void;
  // The note's custom emoji + a setter (null clears it). Notion-style icon that
  // sits before the title.
  icon?: string;
  onSetIcon?: (emoji: string | null) => void;
  // True when this note failed the round-trip fidelity check but is being
  // edited in rich text anyway (the user took the override). The one-time
  // banner is gone by then, so the risk needs a permanent home — saving will
  // rewrite formatting elsewhere in the note, and that has to stay visible
  // for as long as it's true, not just until the banner is dismissed.
  lossyInRichText?: boolean;
  // Fed back from NoteEditor when its rename mutation fails — resets
  // committedRef so a plain Enter retries instead of staying a no-op
  // (review fix — "minor": the alternative considered was onRename
  // returning the mutation promise, but that would widen the onRename
  // contract from `(to: string): void` to something Promise-aware for
  // every caller; a one-way error signal is a smaller, additive change).
  renameError?: string | null;
}) {
  const [, setParams] = useSearchParams();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [iconOpen, setIconOpen] = useState(false);
  // A DB-backed, system-managed note (a chat transcript, an agent file, an
  // inbox notification, …) — the title is read-only and Delete is withheld,
  // because renaming/deleting it here would orphan the backing record. The
  // backend refuses these mutations too; this just hides the affordances.
  const protectedNote = isProtectedPath(path);

  const segments = path.split("/");
  const filename = segments[segments.length - 1];
  const dir = segments.slice(0, -1).join("/");
  const originalTitle = filename.replace(/\.md$/, "");

  const [value, setValue] = useState(originalTitle);
  const [titleError, setTitleError] = useState<string | null>(null);
  // A commit already fired for the current value (Enter blurs the input,
  // which would otherwise fire the blur handler's commit a second time with
  // the same {to} — this dedupes that without needing the actual rename to
  // have completed yet, since `path` doesn't change until the mutation's
  // onSuccess navigates here).
  const committedRef = useRef(false);

  useEffect(() => {
    setValue(originalTitle);
    setTitleError(null);
    committedRef.current = false;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  // The rename this instance fired came back as an error (still the same
  // `path` — no remount happened) — let a plain Enter retry instead of
  // silently no-op'ing forever because committedRef is stuck true.
  useEffect(() => {
    if (renameError) committedRef.current = false;
  }, [renameError]);

  // Returns whether the field is now in a "resolved" state (nothing to fix
  // — either a rename fired, or there was nothing to do) vs. blocked on a
  // validation error the user still needs to correct. Callers use this to
  // decide whether blurring the input is appropriate.
  function commit(): boolean {
    if (committedRef.current) return true;
    const trimmed = stripMdExt(value.trim());
    if (!trimmed || trimmed === originalTitle) {
      setTitleError(null);
      return true;
    }
    if (trimmed.includes("/")) {
      setTitleError("Title can't contain /");
      return false;
    }
    setTitleError(null);
    committedRef.current = true;
    onRename(dir ? `${dir}/${trimmed}.md` : `${trimmed}.md`);
    return true;
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter") {
      e.preventDefault();
      if (commit()) e.currentTarget.blur();
    } else if (e.key === "Escape") {
      committedRef.current = true;
      setTitleError(null);
      setValue(originalTitle);
      e.currentTarget.blur();
    }
  }

  return (
    <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2">
      {onSetIcon && (
        <button
          type="button"
          aria-label="Change note icon"
          onClick={() => setIconOpen(true)}
          className="flex size-10 shrink-0 items-center justify-center rounded-md text-2xl hover:bg-chrome"
        >
          {icon ? (
            <span aria-hidden>{icon}</span>
          ) : (
            <FileText className="size-6 text-muted-2" />
          )}
        </button>
      )}
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex items-center gap-1.5">
          {segments.slice(0, -1).map((seg, i) => {
            const target = segments.slice(0, i + 1).join("/");
            return (
              <span
                key={target}
                className="flex shrink-0 items-center gap-1.5 text-xs text-muted-2"
              >
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
          <input
            value={value}
            readOnly={protectedNote}
            onChange={(e) => {
              committedRef.current = false;
              setTitleError(null);
              setValue(e.target.value);
            }}
            onKeyDown={protectedNote ? undefined : handleKeyDown}
            onBlur={protectedNote ? undefined : () => commit()}
            className={cn(
              "min-w-0 flex-1 truncate rounded border border-transparent bg-transparent px-1 text-sm font-semibold outline-none",
              protectedNote
                ? "cursor-default"
                : "hover:border-border focus:border-ring focus:ring-[3px] focus:ring-ring/50",
            )}
            aria-label="Note title"
          />
          <span className="shrink-0 text-xs text-muted-2">.md</span>
        </div>
        {titleError && (
          <span className="pl-1 text-xs text-danger">{titleError}</span>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-3">
        {lossyInRichText && (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="flex items-center gap-1 rounded-full bg-warn-soft px-2 py-0.5 text-xs text-warn">
                <AlertTriangle className="size-3" />
                May reformat
              </span>
            </TooltipTrigger>
            <TooltipContent side="bottom" className="max-w-64">
              Opened as raw markdown to preserve its exact formatting. Switch to
              rich text to edit visually — a few uncommon details would be
              reformatted if you do.
            </TooltipContent>
          </Tooltip>
        )}
        <span
          className={cn(
            "text-xs",
            state === "error" ? "text-danger" : "text-muted-2",
          )}
        >
          {STATE_LABEL[state]}
        </span>
        {backlinksCount > 0 && (
          <span className="flex items-center gap-1 text-xs text-muted-2">
            <Link2 className="size-3.5" />
            {backlinksCount} {backlinksCount === 1 ? "backlink" : "backlinks"}
          </span>
        )}
        <ChatAboutFileButton path={path} />
        <Button variant="ghost" size="sm" onClick={onToggleRaw}>
          <FileCode className="size-4" />
          {rawMode ? "Rich text" : "Raw"}
        </Button>
        <ExportMenu path={path} />
        {!protectedNote && (
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
              <DropdownMenuItem
                variant="destructive"
                onSelect={() => setDeleteOpen(true)}
              >
                Delete…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>

      {onSetIcon && (
        <EmojiPicker
          open={iconOpen}
          onOpenChange={setIconOpen}
          current={icon}
          onSelect={onSetIcon}
        />
      )}

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete “{path}”?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-2">This can’t be undone.</p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setDeleteOpen(false);
                onDelete();
              }}
            >
              <Trash2 />
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
