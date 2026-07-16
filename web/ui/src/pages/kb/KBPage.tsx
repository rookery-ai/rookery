import { useState } from "react";
import { useSearchParams } from "react-router";
import { Plus, FileText } from "lucide-react";
import { ContextPane } from "@/components/shell/AppShell";
import { Button } from "@/components/ui/button";
import FileTree, { NewEntryDialog } from "./FileTree";
import NoteEditor from "./NoteEditor";

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
      {/* Task 6 fills this pane with a KB SearchBox (ripgrep search-as-you-type). */}
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

export default function KBPage() {
  const [params, setParams] = useSearchParams();
  const path = params.get("path");
  const isFile = !!path && path.endsWith(".md");

  return (
    <>
      <ContextPane>
        <div className="flex h-full flex-col">
          <KBPaneHeader />
          <div className="flex-1 overflow-y-auto">
            <FileTree selectedPath={path} onSelect={(p) => setParams({ path: p })} />
          </div>
        </div>
      </ContextPane>
      {isFile ? <NoteEditor path={path} key={path} /> : <KBEmptyState />}
    </>
  );
}
