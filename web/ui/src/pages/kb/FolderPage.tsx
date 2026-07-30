import { useState } from "react";
import { FolderPlus, FilePlus } from "lucide-react";
import { useKBTree, useSetKBIcon, type KBNode } from "@/lib/kb";
import { Button } from "@/components/ui/button";
import { NodeIcon, NewEntryDialog, sortNodes, parentOf, baseName } from "./FileTree";
import EmojiPicker from "./EmojiPicker";

// FolderPage is the main-pane view of a folder (Notion-style): the folder's own
// icon + name as a title, then its children as a clickable list. Opening a
// folder in the tree still expands it there AND opens this page. Reads the
// folder's own icon/display name from its PARENT listing (there's no single-node
// endpoint) and its children from its own listing.
export default function FolderPage({
  path,
  onOpen,
}: {
  path: string;
  onOpen: (path: string, isDir: boolean, displayName?: string) => void;
}) {
  const parent = parentOf(path);
  const { data: parentTree } = useKBTree(parent);
  const selfNode = parentTree?.nodes?.find((n) => n.path === path);

  const { data: tree, isLoading } = useKBTree(path);
  const setIcon = useSetKBIcon();
  const [iconOpen, setIconOpen] = useState(false);
  const [newKind, setNewKind] = useState<null | "note" | "folder">(null);

  const displayName = selfNode?.display_name ?? baseName(path);
  const folderNode: KBNode = selfNode ?? {
    name: baseName(path),
    display_name: displayName,
    path,
    is_dir: true,
    system: false,
  };
  const children = sortNodes(tree?.nodes ?? [], tree?.order ?? [], path === "");

  return (
    <div className="mx-auto flex h-full w-full max-w-[1600px] flex-col px-8 py-8">
      <div className="mb-6 flex items-center gap-3">
        <button
          type="button"
          aria-label="Change folder icon"
          onClick={() => setIconOpen(true)}
          className="flex size-10 items-center justify-center rounded text-2xl hover:bg-chrome"
        >
          <NodeIcon node={folderNode} expanded className="size-7 text-2xl" />
        </button>
        <h1 className="min-w-0 flex-1 truncate text-xl font-bold">{displayName}</h1>
        <Button variant="outline" size="sm" onClick={() => setNewKind("note")}>
          <FilePlus className="mr-1 size-4" /> New note
        </Button>
        <Button variant="outline" size="sm" onClick={() => setNewKind("folder")}>
          <FolderPlus className="mr-1 size-4" /> New folder
        </Button>
      </div>

      {isLoading ? (
        <p className="text-sm text-muted-2">Loading…</p>
      ) : children.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 text-center text-muted-2">
          <p className="text-sm">This folder is empty.</p>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setNewKind("note")}>New note</Button>
            <Button variant="outline" size="sm" onClick={() => setNewKind("folder")}>New folder</Button>
          </div>
        </div>
      ) : (
        <ul className="min-h-0 flex-1 space-y-0.5 overflow-y-auto">
          {children.map((child) => (
            <li key={child.path}>
              <button
                type="button"
                onClick={() => onOpen(child.path, child.is_dir, child.display_name)}
                className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-chrome"
              >
                <NodeIcon node={child} className="size-4 shrink-0 text-base" />
                <span className="min-w-0 flex-1 truncate">{child.display_name}</span>
              </button>
            </li>
          ))}
        </ul>
      )}

      <EmojiPicker
        open={iconOpen}
        onOpenChange={setIconOpen}
        current={folderNode.icon}
        onSelect={(emoji) => setIcon.mutate({ path, icon: emoji ?? "" })}
      />
      <NewEntryDialog
        dirPath={path}
        kind={newKind ?? "note"}
        open={newKind !== null}
        onOpenChange={(o) => setNewKind(o ? newKind : null)}
      />
    </div>
  );
}
