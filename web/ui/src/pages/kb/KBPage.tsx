import { useRef, useState } from "react";
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
import SearchBox from "./SearchBox";

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
) {
  for (const file of files) {
    try {
      const res = await upload.mutateAsync({ file });
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

function KBPaneHeader({ onPickFiles }: { onPickFiles: (files: File[]) => void }) {
  const [newOpen, setNewOpen] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  return (
    <ContextPaneHeader
      title="Knowledge Base"
      action={
        <>
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
          <NewEntryDialog dirPath="" kind="note" open={newOpen} onOpenChange={setNewOpen} />
        </>
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

export default function KBPage() {
  const [params, setParams] = useSearchParams();
  const path = params.get("path");
  const isDirHint = params.get("dir") === "1";
  const isFile = !!path && !isDirHint;
  const isMarkdown = !!path && path.toLowerCase().endsWith(".md");

  const upload = useUploadKBFile();
  const { toast } = useToast();

  function openPath(p: string, isDir: boolean) {
    setParams(isDir ? { path: p, dir: "1" } : { path: p });
  }

  // A drag-move in the tree is a rename underneath, so the open document's
  // path is stale the moment it lands. Follow it rather than leaving the
  // editor pointed at a path that no longer exists (which reads as the note
  // having been deleted).
  function handleMoved(from: string, to: string) {
    if (path !== from) return;
    setParams(isDirHint ? { path: to, dir: "1" } : { path: to });
  }

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <KBPaneHeader onPickFiles={(files) => void importFiles(files, upload, toast)} />
          <div className="min-h-0 flex-1">
            <SearchBox onSelect={(p) => openPath(p, false)}>
              <FileTree
                selectedPath={path}
                onSelect={openPath}
                onMoved={handleMoved}
                onImportFiles={(files) => void importFiles(files, upload, toast)}
              />
            </SearchBox>
          </div>
        </div>
      </ContextPane>
      {isFile ? (
        isMarkdown ? (
          <NoteEditor path={path} key={path} />
        ) : (
          <FileViewer path={path} key={path} />
        )
      ) : (
        <KBEmptyState />
      )}
    </>
  );
}
