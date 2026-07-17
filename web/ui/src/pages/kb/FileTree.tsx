import { useEffect, useState } from "react";
import {
  Folder, FolderOpen, FileText, Brain, Bot, MessageSquare, Sparkles,
  ChevronRight, MoreHorizontal, type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useKBTree, useNewNote, useDeleteNote, useRenameNote, type KBNode } from "@/lib/kb";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

// The backend (web/api_kb.go's kbSystemDirs) marks the root-level `memory`
// dir system:true — it groups it with agents/chats/skills/reminders/inbox as
// "not user-authored knowledge". But spec §6 explicitly calls out memory/ as
// user-facing knowledge ("user notes and memory/ first") that should sort
// and style WITH user content, not muted alongside the others. Rather than
// special-casing memory/ in the backend's kbSystemDirs (which DOES want the
// system treatment for the rest of that set), override the flag here by
// name — memory/ stays fully editable and keeps its Brain icon either way.
function isEffectivelySystem(node: KBNode): boolean {
  return node.system && node.name !== "memory";
}

// Root-content-first ordering (spec §6): user content sorts before system
// dirs, dirs before files within a group, then alphabetically.
function sortNodes(nodes: KBNode[]): KBNode[] {
  return [...nodes].sort((a, b) => {
    const sysA = isEffectivelySystem(a) ? 1 : 0, sysB = isEffectivelySystem(b) ? 1 : 0;
    if (sysA !== sysB) return sysA - sysB;
    const dirA = a.is_dir ? 0 : 1, dirB = b.is_dir ? 0 : 1;
    if (dirA !== dirB) return dirA - dirB;
    return a.name.localeCompare(b.name);
  });
}

const SPECIAL_DIR_ICONS: Record<string, LucideIcon> = {
  memory: Brain,
  agents: Bot,
  chats: MessageSquare,
  skills: Sparkles,
};

function iconFor(node: KBNode, expanded: boolean): LucideIcon {
  if (!node.is_dir) return FileText;
  return SPECIAL_DIR_ICONS[node.name] ?? (expanded ? FolderOpen : Folder);
}

type NewKind = "note" | "folder";

// Shared by the per-row "New note…"/"New folder…" actions and the pane
// header's root-level "New note" button.
export function NewEntryDialog({
  dirPath,
  kind,
  open,
  onOpenChange,
}: {
  dirPath: string;
  kind: NewKind;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const newNote = useNewNote();

  useEffect(() => {
    if (open) {
      setName("");
      setError("");
    }
  }, [open]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    let n = name.trim();
    if (!n) return;
    if (kind === "note" && !n.endsWith(".md")) n += ".md";
    const path = dirPath ? `${dirPath}/${n}` : n;
    try {
      await newNote.mutateAsync({ path, is_dir: kind === "folder" });
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{kind === "note" ? "New note" : "New folder"}</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="new-entry-name">Name</Label>
            <Input id="new-entry-name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full">Create</Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RenameDialog({
  node,
  open,
  onOpenChange,
}: {
  node: KBNode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [value, setValue] = useState(node.path);
  const [error, setError] = useState("");
  const renameNote = useRenameNote();

  useEffect(() => {
    if (open) {
      setValue(node.path);
      setError("");
    }
  }, [open, node.path]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const to = value.trim();
    if (!to || to === node.path) return;
    try {
      await renameNote.mutateAsync({ from: node.path, to });
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Rename</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="rename-path">Path</Label>
            <Input id="rename-path" value={value} onChange={(e) => setValue(e.target.value)} autoFocus />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full">Rename</Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DeleteDialog({
  node,
  open,
  onOpenChange,
}: {
  node: KBNode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [error, setError] = useState("");
  const deleteNote = useDeleteNote();

  useEffect(() => {
    if (open) setError("");
  }, [open]);

  async function confirm() {
    try {
      await deleteNote.mutateAsync({ path: node.path });
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Delete “{node.path}”?</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-2">This can’t be undone.</p>
        {error && <p className="text-danger text-sm">{error}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="destructive" onClick={confirm}>Delete</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

type DialogKind = "new-note" | "new-folder" | "rename" | "delete" | null;

function TreeRow({
  node,
  depth,
  selectedPath,
  onSelect,
}: {
  node: KBNode;
  depth: number;
  selectedPath: string | null;
  onSelect: (path: string, isDir: boolean) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const [dialog, setDialog] = useState<DialogKind>(null);
  const selected = selectedPath === node.path;
  const Icon = iconFor(node, expanded);

  function handleClick() {
    if (node.is_dir) setExpanded((e) => !e);
    onSelect(node.path, node.is_dir);
  }

  return (
    <div>
      {/* The dropdown trigger is a SIBLING of the role=button row, not nested
          inside it (a11y fix — a <button> descendant of a role="button"
          element is an invalid/ambiguous nested-interactive structure, and
          it also meant the trigger's click had to stopPropagation to avoid
          re-triggering row selection). "group" lives on this wrapper so the
          existing hover-reveal styling (opacity-0 group-hover:opacity-100 on
          the trigger) keeps working — CSS :hover on a descendant still
          matches its ancestors, so hovering the row still reveals the
          trigger. */}
      <div className="group flex items-center gap-1.5 pr-1">
        <div
          role="button"
          tabIndex={0}
          onClick={handleClick}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              handleClick();
            }
          }}
          style={{ paddingLeft: 6 + depth * 14 }}
          className={cn(
            "flex min-w-0 flex-1 items-center gap-1.5 rounded px-1.5 py-1 text-sm cursor-pointer",
            selected ? "bg-border" : "hover:bg-chrome",
            isEffectivelySystem(node) && "text-muted-2",
          )}
        >
          {node.is_dir ? (
            <ChevronRight
              className={cn("size-3.5 shrink-0 text-muted-2 transition-transform", expanded && "rotate-90")}
            />
          ) : (
            <span className="size-3.5 shrink-0" />
          )}
          <Icon className="size-4 shrink-0" />
          <span className="flex-1 truncate">{node.display_name}</span>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              aria-label={`Actions for ${node.display_name}`}
              className="shrink-0 rounded p-0.5 opacity-0 group-hover:opacity-100 hover:bg-border focus-visible:opacity-100"
            >
              <MoreHorizontal className="size-3.5" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            {node.is_dir && (
              <>
                <DropdownMenuItem onSelect={() => setDialog("new-note")}>New note…</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setDialog("new-folder")}>New folder…</DropdownMenuItem>
              </>
            )}
            <DropdownMenuItem onSelect={() => setDialog("rename")}>Rename…</DropdownMenuItem>
            <DropdownMenuItem variant="destructive" onSelect={() => setDialog("delete")}>
              Delete…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {node.is_dir && expanded && (
        <TreeLevel path={node.path} depth={depth + 1} selectedPath={selectedPath} onSelect={onSelect} />
      )}
      {node.is_dir && (
        <>
          <NewEntryDialog
            dirPath={node.path} kind="note"
            open={dialog === "new-note"} onOpenChange={(o) => setDialog(o ? "new-note" : null)}
          />
          <NewEntryDialog
            dirPath={node.path} kind="folder"
            open={dialog === "new-folder"} onOpenChange={(o) => setDialog(o ? "new-folder" : null)}
          />
        </>
      )}
      <RenameDialog node={node} open={dialog === "rename"} onOpenChange={(o) => setDialog(o ? "rename" : null)} />
      <DeleteDialog node={node} open={dialog === "delete"} onOpenChange={(o) => setDialog(o ? "delete" : null)} />
    </div>
  );
}

// One useKBTree call per mounted level — expansion gates this component's
// render (not the hook), so lazy loading stays hook-rule-safe.
function TreeLevel({
  path,
  depth,
  selectedPath,
  onSelect,
}: {
  path: string;
  depth: number;
  selectedPath: string | null;
  onSelect: (path: string, isDir: boolean) => void;
}) {
  const { data, isLoading } = useKBTree(path);
  const nodes = sortNodes(data?.nodes ?? []);

  if (isLoading) {
    return (
      <div style={{ paddingLeft: 6 + depth * 14 }} className="py-1 text-xs text-muted-2">
        Loading…
      </div>
    );
  }
  return (
    <>
      {nodes.map((node) => (
        <TreeRow key={node.path} node={node} depth={depth} selectedPath={selectedPath} onSelect={onSelect} />
      ))}
    </>
  );
}

export default function FileTree({
  selectedPath,
  onSelect,
}: {
  selectedPath: string | null;
  onSelect: (path: string, isDir: boolean) => void;
}) {
  return (
    <div className="px-1 py-1">
      <TreeLevel path="" depth={0} selectedPath={selectedPath} onSelect={onSelect} />
    </div>
  );
}
