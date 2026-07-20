import { useState } from "react";
import { useSearchParams } from "react-router";
import { Plus, FileText } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { Button } from "@/components/ui/button";
import FileTree, { NewEntryDialog } from "./FileTree";
import NoteEditor from "./NoteEditor";
import FileViewer from "./FileViewer";
import SearchBox from "./SearchBox";

function KBPaneHeader() {
  const [newOpen, setNewOpen] = useState(false);
  return (
    <div className="flex items-center justify-between gap-2 border-b border-border px-3 py-2.5">
      <h2 className="text-sm font-bold">Knowledge Base</h2>
      <Button
        variant="ghost" size="icon-sm" aria-label="New note"
        onClick={() => setNewOpen(true)}
      >
        <Plus className="size-4" />
      </Button>
      <NewEntryDialog dirPath="" kind="note" open={newOpen} onOpenChange={setNewOpen} />
    </div>
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

// A path "opens" as a document if its last segment looks like a file
// (contains a dot) rather than a bare directory name. Every top-level and
// agent/skill-scoped directory in the vault's layout (notes, memory, agents,
// <agentID>, tools, chats, …) is dot-free, so this is a safe, purely
// client-side approximation used ONLY to decide whether to render a content
// pane at all — it is not the kind decision. Which document component to use
// (the WYSIWYG/raw NoteEditor vs the read-only FileViewer) mirrors the
// backend's own unconditional rule (web/api_kb.go's apiGetKBNote): exactly
// ".md" is "markdown", everything else is content-sniffed server-side into
// "code" or "binary" by FileViewer's own fetch.
function looksLikeFile(path: string): boolean {
  const last = path.split("/").pop() ?? "";
  return last.includes(".");
}

export default function KBPage() {
  const [params, setParams] = useSearchParams();
  const path = params.get("path");
  const isFile = !!path && looksLikeFile(path);
  const isMarkdown = !!path && path.toLowerCase().endsWith(".md");

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <KBPaneHeader />
          <div className="min-h-0 flex-1">
            <SearchBox onSelect={(p) => setParams({ path: p })}>
              <FileTree selectedPath={path} onSelect={(p) => setParams({ path: p })} />
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
