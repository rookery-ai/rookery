import { createContext, useContext, useEffect, useMemo, useState } from "react";
import {
  Folder, FolderOpen, FileText, Brain, Bot, MessageSquare, Sparkles,
  ChevronRight, MoreHorizontal, type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import {
  useKBTree, useNewNote, useDeleteNote, useRenameNote, useSaveKBOrder, type KBNode,
} from "@/lib/kb";
import { useToast } from "@/components/shell/Toast";
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
//
// `order` is the user's own drag-chosen sequence for this folder (by name,
// persisted server-side). It takes precedence, but only for names it actually
// lists: anything that appeared since the last drag — a note an agent just
// wrote — is NOT silently pinned to an arbitrary spot, it falls through to the
// derived rules below and sorts after the explicitly-ordered block.
function sortNodes(nodes: KBNode[], order: string[] = []): KBNode[] {
  const rank = new Map(order.map((name, i) => [name, i]));
  return [...nodes].sort((a, b) => {
    const ra = rank.get(a.name), rb = rank.get(b.name);
    if (ra !== undefined && rb !== undefined) return ra - rb;
    if (ra !== undefined) return -1;
    if (rb !== undefined) return 1;
    const sysA = isEffectivelySystem(a) ? 1 : 0, sysB = isEffectivelySystem(b) ? 1 : 0;
    if (sysA !== sysB) return sysA - sysB;
    const dirA = a.is_dir ? 0 : 1, dirB = b.is_dir ? 0 : 1;
    if (dirA !== dirB) return dirA - dirB;
    return a.name.localeCompare(b.name);
  });
}

// ── Drag and drop ────────────────────────────────────────────────────────────
//
// Two gestures, deliberately distinguished by WHERE in a row you drop:
//   - onto a folder row      → move the dragged node into that folder
//   - into the gap above or  → reorder within the folder both nodes already
//     below a sibling row      share (cross-folder reordering is not a thing;
//                               to change folder you drop on the folder)
// Dropping onto a FILE row is never a target — there is nothing sensible it
// could mean, and allowing it would make an off-by-a-few-pixels drag look like
// it did something.
//
// The dragged node has to be readable during `dragover` (to decide whether a
// target is legal and which indicator to draw), and the HTML5 DataTransfer is
// deliberately unreadable until `drop` — so it lives in context instead.
type DraggedNode = { path: string; name: string; parent: string; isDir: boolean };

const DragCtx = createContext<{
  dragged: DraggedNode | null;
  setDragged: (d: DraggedNode | null) => void;
}>({ dragged: null, setDragged: () => {} });

function parentOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i === -1 ? "" : path.slice(0, i);
}

// A folder can't be dropped inside itself or anything beneath it — that would
// ask os.Rename to move a directory into its own subtree.
function isSelfOrDescendant(target: string, dragged: string): boolean {
  return target === dragged || target.startsWith(dragged + "/");
}

type DropHint = "before" | "after" | "into" | null;

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
  onMoveInto,
  onReorder,
  onImportFiles,
}: {
  node: KBNode;
  depth: number;
  selectedPath: string | null;
  onSelect: (path: string, isDir: boolean, displayName?: string) => void;
  // Move the dragged node into this (folder) node.
  onMoveInto: (dragged: DraggedNode, folder: KBNode) => void;
  // Reorder within the level this row belongs to. Owned by TreeLevel, which
  // is the only component that knows the full sibling list.
  onReorder: (dragged: DraggedNode, targetName: string, position: "before" | "after") => void;
  // Fired when OS file(s) are dropped ON THIS ROW specifically — see the
  // dedicated OS-file-drag branch in handleDragOver/handleDrop below. Omitted
  // when the tree as a whole is drop-inert (FileTree's onImportFiles absent).
  onImportFiles?: (files: File[], dir: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const [dialog, setDialog] = useState<DialogKind>(null);
  const [hint, setHint] = useState<DropHint>(null);
  const { dragged, setDragged } = useContext(DragCtx);
  const selected = selectedPath === node.path;
  const Icon = iconFor(node, expanded);

  function handleClick() {
    if (node.is_dir) setExpanded((e) => !e);
    onSelect(node.path, node.is_dir, node.display_name);
  }

  // Which gesture, if any, a drop at this pointer position would perform.
  // Returns null for anything illegal so dragover can withhold the drop
  // affordance entirely rather than accepting and then quietly doing nothing.
  function hintFor(e: React.DragEvent): DropHint {
    if (!dragged || dragged.path === node.path) return null;

    // System dirs are excluded from the persisted order (see handleReorder),
    // so a reorder aimed at one could never be saved — don't offer the
    // affordance and then silently do nothing.
    const sameParent = dragged.parent === parentOf(node.path) && !isEffectivelySystem(node);
    const canMoveInto =
      node.is_dir &&
      !isEffectivelySystem(node) &&
      !isSelfOrDescendant(node.path, dragged.path) &&
      dragged.parent !== node.path;

    const rect = e.currentTarget.getBoundingClientRect();
    // Without usable geometry there's no way to tell an edge from the middle,
    // and guessing would silently pick "into" (NaN fails both edge tests) —
    // i.e. it would MOVE a file on a drop that was meant to reorder. Refuse
    // the drop instead.
    if (!rect.height || !Number.isFinite(e.clientY)) return null;
    const offset = (e.clientY - rect.top) / rect.height;
    // A folder row splits three ways (edges reorder, middle moves in); a file
    // row splits in two, since "into a file" is not a target.
    const edge = canMoveInto ? 0.3 : 0.5;
    if (offset < edge) return sameParent ? "before" : null;
    if (offset > 1 - edge) return sameParent ? "after" : null;
    return canMoveInto ? "into" : null;
  }

  // Both handlers stop propagation unconditionally for the tree's OWN
  // internal reorder/move drag, including when this row rejects the drop.
  // Rows nest (a folder's children render inside its own wrapper), so
  // without this an illegal drop on a child — say, onto a file — would
  // bubble to the enclosing folder row and be re-interpreted as "move into
  // that folder". A row's verdict on a drop aimed at it is final.
  //
  // That rule applies only while DragCtx's `dragged` is set (a row's own
  // onDragStart). A file dragged in from the OS sets no such state, so
  // `dragged` is null for the whole gesture:
  //   - dragover: still falls through untouched here, letting it bubble to
  //     the tree's root wrapper, which is what shows the "drop anywhere"
  //     ring highlight and calls preventDefault so a drop can land at all
  //     (the same underlying event; the browser only needs preventDefault
  //     called SOMEWHERE along its bubble path, not on this exact element).
  //   - drop: handled explicitly in handleDrop below — unlike dragover, drop
  //     needs to know WHICH row (i.e. which folder) the cursor was actually
  //     over, so it can no longer just fall through to the root wrapper the
  //     way it used to; see that handler's comment.
  function handleDragOver(e: React.DragEvent) {
    if (!dragged) return;
    e.stopPropagation();
    const next = hintFor(e);
    if (!next) {
      if (hint) setHint(null);
      return;
    }
    // Only preventDefault for a target we'll actually act on — that is what
    // tells the browser this is a valid drop zone and switches the cursor.
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (next !== hint) setHint(next);
  }

  function handleDrop(e: React.DragEvent) {
    if (!dragged) {
      // Native OS file drop landing ON this row (not the blank gap below the
      // last one) — captured here, rather than left to bubble to the tree's
      // root wrapper, so the folder actually under the cursor becomes the
      // import target instead of every drop always landing in notes/. A drop
      // onto a FOLDER row targets that folder; onto a FILE row, there's
      // nothing sensible "into a file" could mean, so its PARENT folder is
      // used instead. Only intercepted when the caller wired onImportFiles
      // AND the drag actually carries files — anything else (no
      // onImportFiles, or the tree's own internal reorder/move drag, which
      // carries no "Files" type and has dragged !== null anyway) falls
      // through untouched and keeps bubbling, exactly as before.
      if (!onImportFiles || !e.dataTransfer.types.includes("Files")) return;
      const files = Array.from(e.dataTransfer.files ?? []);
      if (files.length === 0) return;
      e.preventDefault();
      e.stopPropagation();
      onImportFiles(files, node.is_dir ? node.path : parentOf(node.path));
      return;
    }
    e.stopPropagation();
    const target = hintFor(e);
    setHint(null);
    if (!dragged || !target) return;
    e.preventDefault();
    if (target === "into") onMoveInto(dragged, node);
    else onReorder(dragged, node.name, target);
    setDragged(null);
  }

  return (
    <div
      draggable
      onDragStart={(e) => {
        e.stopPropagation();
        setDragged({
          path: node.path,
          name: node.name,
          parent: parentOf(node.path),
          isDir: node.is_dir,
        });
        // Payload is set for completeness/interop; the live drag state that
        // dragover needs comes from DragCtx (DataTransfer is write-only until
        // drop).
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", node.path);
      }}
      onDragEnd={() => {
        setDragged(null);
        setHint(null);
      }}
      onDragOver={handleDragOver}
      onDragLeave={() => setHint(null)}
      onDrop={handleDrop}
      className={cn(
        dragged?.path === node.path && "opacity-40",
        hint === "into" && "rounded ring-2 ring-ring",
      )}
    >
      {/* The insertion indicator is positioned against the ROW, not against
          this wrapper: an expanded folder's children render inside the wrapper
          too, so an "after" line anchored there would be drawn below the whole
          subtree instead of under the folder's own row. Absolute so it never
          shifts layout mid-drag — a 2px line that nudged its siblings would
          move the drop target out from under the cursor. */}
      <div className="relative">
        {(hint === "before" || hint === "after") && (
          <div
            aria-hidden="true"
            className={cn(
              "pointer-events-none absolute inset-x-0 z-10 h-0.5 bg-accent",
              hint === "before" ? "top-0" : "bottom-0",
            )}
          />
        )}
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
            "flex min-w-0 flex-1 items-center gap-1.5 rounded px-1.5 py-1 text-sm",
            dragged ? "cursor-grabbing" : "cursor-pointer",
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
      </div>
      {node.is_dir && expanded && (
        <TreeLevel
          path={node.path}
          depth={depth + 1}
          selectedPath={selectedPath}
          onSelect={onSelect}
          onMoveInto={onMoveInto}
          onImportFiles={onImportFiles}
        />
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
//
// Reordering is handled HERE rather than in TreeRow because a new order is a
// property of the whole sibling list, which only this component has: the row
// knows where the drop landed, this knows what the resulting sequence is.
function TreeLevel({
  path,
  depth,
  selectedPath,
  onSelect,
  onMoveInto,
  onImportFiles,
}: {
  path: string;
  depth: number;
  selectedPath: string | null;
  onSelect: (path: string, isDir: boolean, displayName?: string) => void;
  onMoveInto: (dragged: DraggedNode, folder: KBNode) => void;
  onImportFiles?: (files: File[], dir: string) => void;
}) {
  const { data, isLoading } = useKBTree(path);
  const saveOrder = useSaveKBOrder();
  const { toast } = useToast();
  const nodes = useMemo(
    () => sortNodes(data?.nodes ?? [], data?.order ?? []),
    [data?.nodes, data?.order],
  );

  function handleReorder(dragged: DraggedNode, targetName: string, position: "before" | "after") {
    // The persisted order is the full sequence of the USER's own rows, not a
    // sparse set of pins — so a later derived-sort change or a rename can't
    // silently shuffle rows they've already arranged.
    //
    // System dirs (chats/, agents/, …) are deliberately excluded even though
    // they're visible here. Ranked names sort ahead of unranked ones, so
    // including them would pin them above every note created after the drag —
    // i.e. reordering once would push each NEW note below the system dirs,
    // where it never belonged. Left unranked, they stay in the derived tail
    // and the "user content before system dirs" rule keeps holding.
    const names = nodes
      .filter((n) => !isEffectivelySystem(n))
      .map((n) => n.name)
      .filter((n) => n !== dragged.name);
    let at = names.indexOf(targetName);
    if (at === -1) return;
    if (position === "after") at += 1;
    names.splice(at, 0, dragged.name);
    saveOrder.mutate(
      { dir: path, names },
      {
        onError: (err) =>
          toast({
            message: err instanceof ApiError ? err.message : "Couldn't save the new order",
            variant: "error",
          }),
      },
    );
  }

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
        <TreeRow
          key={node.path}
          node={node}
          depth={depth}
          selectedPath={selectedPath}
          onSelect={onSelect}
          onMoveInto={onMoveInto}
          onReorder={handleReorder}
          onImportFiles={onImportFiles}
        />
      ))}
    </>
  );
}

export default function FileTree({
  selectedPath,
  onSelect,
  onMoved,
  onImportFiles,
}: {
  selectedPath: string | null;
  onSelect: (path: string, isDir: boolean, displayName?: string) => void;
  // Fired after a drag-move succeeds. KBPage uses it to follow the open note
  // to its new home — a move is a rename underneath, so without this the
  // `?path=` param still points at the old location and NoteEditor drops into
  // its "deleted elsewhere" state on the single most common drag there is.
  onMoved?: (from: string, to: string) => void;
  // Fired when the user drops OS file(s) on the tree. `dir` is the folder the
  // drop targeted: the row's own path when dropped on a folder row, that
  // row's parent when dropped on a file row (see TreeRow's handleDrop), or
  // omitted when dropped in the blank gap below the last row (this root
  // wrapper's own onDrop, below — no row was under the cursor to target).
  // Omitted entirely, the tree is drop-inert (upload is opt-in per caller).
  onImportFiles?: (files: File[], dir?: string) => void;
}) {
  const [dragged, setDragged] = useState<DraggedNode | null>(null);
  const [fileDragging, setFileDragging] = useState(false);
  const renameNote = useRenameNote();
  const { toast } = useToast();
  const dragCtx = useMemo(() => ({ dragged, setDragged }), [dragged]);

  // Moves are owned by the root so a drag can cross levels — the source row
  // and the destination folder are usually in different TreeLevels.
  function handleMoveInto(d: DraggedNode, folder: KBNode) {
    const to = folder.path ? `${folder.path}/${d.name}` : d.name;
    renameNote.mutate(
      { from: d.path, to },
      {
        onSuccess: () => onMoved?.(d.path, to),
        onError: (err) =>
          toast({
            message: err instanceof ApiError ? err.message : `Couldn't move “${d.name}”`,
            variant: "error",
          }),
      },
    );
  }

  return (
    <DragCtx.Provider value={dragCtx}>
      <div
        className={cn(
          "h-full px-1 py-1",
          fileDragging && "rounded ring-2 ring-inset ring-ring",
        )}
        // This wrapper's own onDrop only ever fires for a drop in the BLANK
        // GAP below the last row: a drop landing ON a row is now captured by
        // that row's own handleDrop (see TreeRow), which stopPropagation's it
        // so it never reaches here — that's what lets a folder row target
        // itself instead of every drop defaulting to notes/. dragover is
        // unaffected (rows deliberately let it keep bubbling here — see
        // TreeRow's handleDragOver comment) so the ring highlight and the
        // preventDefault that allows a drop at all still apply tree-wide,
        // gated on `onImportFiles` being provided AND the drag actually
        // carrying files (`types.includes("Files")`), so the tree's OWN
        // internal reorder/move drag — which carries no "Files" type — never
        // trips this.
        onDragOver={(e) => {
          if (!onImportFiles || !e.dataTransfer.types.includes("Files")) return;
          e.preventDefault();
          e.dataTransfer.dropEffect = "copy";
          setFileDragging(true);
        }}
        onDragLeave={(e) => {
          // A dragleave fires when moving from this wrapper onto one of its
          // own row children too — only clear the highlight once the pointer
          // has actually left the whole tree, or the ring would flicker on
          // every row boundary crossed while dragging.
          if (e.currentTarget.contains(e.relatedTarget as Node | null)) return;
          setFileDragging(false);
        }}
        onDrop={(e) => {
          if (!onImportFiles) return;
          const files = Array.from(e.dataTransfer.files ?? []);
          if (files.length === 0) return; // not a file drop — leave it to the row that's actually handling it
          e.preventDefault();
          setFileDragging(false);
          onImportFiles(files); // blank-gap drop: no folder was under the cursor, keep today's default (notes/)
        }}
      >
        <TreeLevel
          path=""
          depth={0}
          selectedPath={selectedPath}
          onSelect={onSelect}
          onMoveInto={handleMoveInto}
          onImportFiles={onImportFiles}
        />
      </div>
    </DragCtx.Provider>
  );
}
