import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { Plus, FileText, Upload } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { ContextPaneHeader } from "@/components/shell/ContextPaneParts";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/shell/Toast";
import { ApiError } from "@/lib/api";
import { useUploadKBFile } from "@/lib/kb";
import FileTree, { NewEntryDialog } from "./FileTree";
import NoteEditor from "./NoteEditor";
import FileViewer from "./FileViewer";
import FolderPage from "./FolderPage";
import SearchBox from "./SearchBox";
import RecentFiles from "./RecentFiles";
import { useRecentFiles } from "./useRecentFiles";

// importFiles uploads each dropped/picked file in turn (not concurrently — a
// user dragging a handful of files in expects predictable one-at-a-time
// feedback, and the shared vault.ImportFile is globally mutex-serialized
// server-side anyway) and toasts the outcome of each. A conversion warning
// (e.g. a scanned PDF with no extractable text) is surfaced in the toast
// rather than treated as a silent success — see vault.ImportFile's doc
// comment: conversion is lossy by nature, so Warnings must reach the user.
async function importFiles(
  files: File[],
  upload: ReturnType<typeof useUploadKBFile>,
  toast: ReturnType<typeof useToast>["toast"],
  dir?: string,
) {
  for (const file of files) {
    try {
      const res = await upload.mutateAsync({ file, dir });
      toast({
        message: res.warnings?.length
          ? `Imported ${file.name} — ${res.warnings.join("; ")}`
          : `Imported ${file.name} as ${res.note_path}`,
      });
    } catch (err) {
      toast({
        message: err instanceof ApiError ? `Couldn't import ${file.name}: ${err.message}` : `Couldn't import ${file.name}`,
        variant: "error",
      });
    }
  }
}

function KBPaneHeader({
  onPickFiles,
  currentDir,
  newOpen,
  setNewOpen,
}: {
  onPickFiles: (files: File[]) => void;
  // The folder the new-note picker defaults to (the active folder, or the open
  // file's parent, or root). The picker still lets the user retarget it.
  currentDir: string;
  // The new-note dialog is CONTROLLED by KBPage rather than owned here, so the
  // ⌘K palette's "New note" action can open it by navigating to /kb?new=note.
  newOpen: boolean;
  setNewOpen: (open: boolean) => void;
}) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  return (
    <ContextPaneHeader
      title="Knowledge Base"
      action={
        // Both actions in one flex row with a consistent gap so the import and
        // new-note buttons align (previously they sat unwrapped and drifted).
        <div className="flex items-center gap-0.5">
          <Button
            variant="ghost" size="icon-sm" aria-label="Import file"
            onClick={() => fileInputRef.current?.click()}
          >
            <Upload className="size-4" />
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="sr-only"
            aria-label="Import file"
            onChange={(e) => {
              onPickFiles(Array.from(e.target.files ?? []));
              e.target.value = ""; // allow re-picking the same file(s) again
            }}
          />
          <Button
            variant="ghost" size="icon-sm" aria-label="New note"
            onClick={() => setNewOpen(true)}
          >
            <Plus className="size-4" />
          </Button>
          <NewEntryDialog
            dirPath={currentDir}
            kind="note"
            open={newOpen}
            onOpenChange={setNewOpen}
            pickLocation
          />
        </div>
      }
    />
  );
}

function KBEmptyState() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center text-muted-2">
      <FileText className="size-8" />
      <p className="text-sm">Select a note or create one.</p>
    </div>
  );
}

// Whether a path "opens" as a document (vs. staying on the empty state) is
// decided from a `dir=1` query-param hint that travels WITH the navigation
// event, never guessed from the filename (review fix: a "last segment
// contains a dot" heuristic silently refused to open real, legitimately
// extensionless files an agent writes — a skill script named `run`,
// Makefile, Dockerfile, LICENSE, a shebang shim — because the backend sniffs
// content, not extensions, and the frontend never even asked).
//
// Every navigation call site knows on the spot whether it points at a
// directory, so each one carries that fact explicitly instead of KBPage
// inferring it after the fact:
//   - FileTree.onSelect(path, isDir) already has the truth from the tree data.
//   - SearchBox only ever matches file *content* (internal/vault/search.go
//     skips directories), so its onSelect is always isDir=false.
//   - Breadcrumb clicks in NoteHeader/FileViewerHeader target only
//     *ancestors* of the currently open file, which are directories by
//     construction — they set `dir=1` themselves in the same setParams call.
// Residual case: a hand-typed or bookmarked URL with no `dir` hint at all
// defaults to ATTEMPTING TO OPEN the file, not the empty state — a stray
// directory hit already degrades gracefully (the backend's os.ReadFile on a
// directory errors and FileViewer renders "Couldn't load this file."), which
// is a strictly better failure mode than silently refusing to open a real
// file.
//
// Which document component to use (the WYSIWYG/raw NoteEditor vs the
// read-only FileViewer) is unrelated to this and still mirrors the backend's
// own unconditional rule (web/api_kb.go's apiGetKBNote): exactly ".md" is
// "markdown", everything else is content-sniffed server-side into "code" or
// "binary" by FileViewer's own fetch.

// fileTitle is the fallback label for a recents entry when the caller had no
// resolved display name to offer (search results carry only a path). The
// filename stem is right for a user-authored note, whose filename IS its title;
// a reflected note clicked from search keeps its UUID here, which is still more
// informative than the full path in a narrow pane.
function fileTitle(path: string): string {
  const name = path.split("/").pop() ?? path;
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(0, dot) : name;
}

export default function KBPage() {
  const [params, setParams] = useSearchParams();
  const path = params.get("path");
  const [newNoteOpen, setNewNoteOpen] = useState(false);
  const isDirHint = params.get("dir") === "1";
  const isFile = !!path && !isDirHint;
  const isDir = !!path && isDirHint;
  const isMarkdown = !!path && path.toLowerCase().endsWith(".md");

  // The folder the pane-header "+" defaults to: the open folder itself, the open
  // file's parent, or root.
  const currentDir = !path ? "" : isDir ? path : path.split("/").slice(0, -1).join("/");

  const upload = useUploadKBFile();
  const { toast } = useToast();
  const { recent, record, forget, rename } = useRecentFiles();

  // The ⌘K palette's "New note" action navigates to /kb?new=note. It used to
  // point at bare /kb, which opens the knowledge base and creates nothing —
  // the action looked broken because the only real new-note affordance is the
  // dialog below, owned by this page's local state.
  //
  // The intent is captured into state and the param stripped in the SAME
  // effect: the resume-last-note effect further down rewrites the query
  // wholesale (setParams({path}), no merge) and would drop `new` regardless,
  // so consuming it up front makes the ordering between the two irrelevant.
  // Stripping also stops a reload or a Back from reopening the dialog.
  const wantsNewNote = params.get("new") === "note";
  useEffect(() => {
    if (!wantsNewNote) return;
    setNewNoteOpen(true);
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("new");
        return next;
      },
      { replace: true },
    );
  }, [wantsNewNote, setParams]);

  // Recording happens HERE, in the one place every open funnels through, rather
  // than at each call site — so the tree, search and the recents list itself all
  // stay consistent, and a directory click can never enter the history.
  function openPath(p: string, isDir: boolean, displayName?: string) {
    setParams(isDir ? { path: p, dir: "1" } : { path: p });
    if (!isDir) record({ path: p, title: displayName || fileTitle(p) });
  }

  // Landing on /kb with no path opens the most recently viewed file, so the
  // knowledge base resumes where the user left it instead of showing an empty
  // pane. `replace` keeps this out of history: without it, Back from the note
  // would return to the bare /kb route, which would immediately re-open the same
  // note and trap the user.
  //
  // Deliberately NOT recorded via openPath — this is programmatic navigation,
  // and re-recording would only rewrite the entry that caused it.
  const topRecent = recent.length > 0 ? recent[0] : null;
  useEffect(() => {
    if (path === null && topRecent) {
      setParams({ path: topRecent.path }, { replace: true });
    }
  }, [path, topRecent, setParams]);

  // A drag-move in the tree is a rename underneath, so the open document's
  // path is stale the moment it lands. Follow it rather than leaving the
  // editor pointed at a path that no longer exists (which reads as the note
  // having been deleted).
  function handleMoved(from: string, to: string) {
    rename(from, to, fileTitle(to));
    if (path !== from) return;
    setParams(isDirHint ? { path: to, dir: "1" } : { path: to });
  }

  // A recents entry whose file has since been deleted or renamed outside this
  // UI would otherwise sit in the list forever, and — worse — be auto-opened on
  // every visit. Dropping it when the document reports it missing also unblocks
  // the auto-open, which falls through to the next entry.
  function handleMissing(missing: string) {
    forget(missing);
    if (path === missing) setParams({}, { replace: true });
  }

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <KBPaneHeader
            onPickFiles={(files) => void importFiles(files, upload, toast, currentDir)}
            currentDir={currentDir}
            newOpen={newNoteOpen}
            setNewOpen={setNewNoteOpen}
          />
          {/* Recent is a fixed block above the tree, NOT inside a scroll
              container of its own: SearchBox is `h-full` and owns the pane's
              only scroll region (for the tree/results). Wrapping both in a
              second scroller would give the pane two nested scrollbars and
              collapse SearchBox's height calculation. */}
          <div className="flex min-h-0 flex-1 flex-col">
            <RecentFiles
              recent={recent}
              selectedPath={path}
              onSelect={(p, title) => openPath(p, false, title)}
            />
            <div className="min-h-0 flex-1">
              <SearchBox onSelect={(p) => openPath(p, false)}>
                <FileTree
                  selectedPath={path}
                  onSelect={openPath}
                  onMoved={handleMoved}
                  onImportFiles={(files, dir) => void importFiles(files, upload, toast, dir)}
                />
              </SearchBox>
            </div>
          </div>
        </div>
      </ContextPane>
      {isFile ? (
        isMarkdown ? (
          <NoteEditor path={path} key={path} onMissing={() => handleMissing(path)} />
        ) : (
          <FileViewer path={path} key={path} />
        )
      ) : isDir ? (
        <FolderPage path={path!} key={path} onOpen={openPath} />
      ) : (
        <KBEmptyState />
      )}
    </>
  );
}
