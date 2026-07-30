import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Bot, Brain, ChevronRight, FilePlus, FileText, Folder, FolderInput, FolderOpen, FolderPlus, MessageSquare, MoreHorizontal, Pencil, Plus, Smile, Sparkles, Trash2, X, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import {
  useKBTree,
  useNewNote,
  useDeleteNote,
  useRenameNote,
  useSaveKBOrder,
  useSetKBIcon,
  isProtectedPath,
  type KBNode,
} from "@/lib/kb";
import { useToast } from "@/components/shell/Toast";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import EmojiPicker from "./EmojiPicker";
import { FolderSelect } from "./FolderSelect";

// The backend (web/api_kb.go's kbSystemDirs) marks the root-level `memory`
// dir system:true — it groups it with agents/chats/skills/reminders/inbox as
// "not user-authored knowledge". But memory/ is content the user writes and
// edits directly, so it should not be muted or drag-locked alongside the
// rest of that set. Rather than special-casing memory/ in the backend's
// kbSystemDirs (which DOES want the system treatment for the others),
// override the flag here by name.
//
// This now affects STYLING and drag rules only — root ordering is decided by
// ROOT_FOLDER_RANK below, which names its folders explicitly.
function isEffectivelySystem(node: KBNode): boolean {
  return node.system && node.name !== "memory";
}

// The vault's own default folders lead, in a fixed order that runs from "who
// you are" outwards to "what the system produced": memory, agents, chats,
// skills. `notes` deliberately sorts LAST among folders — it is the open-ended
// folder that grows without bound, so anchoring it at the bottom keeps the
// fixed set in a stable, learnable position instead of having it shift as
// notes/ fills up.
//
// This replaces an earlier "user content before system dirs" rule: putting
// notes/ first pushed the four folders a user actually navigates to by muscle
// memory down the list.
const ROOT_FOLDER_RANK = new Map([
  ["memory", 0],
  ["agents", 1],
  ["chats", 2],
  ["skills", 3],
  // anything else lands between these two — see rootFolderRank
  ["notes", 5],
]);
const OTHER_ROOT_FOLDER_RANK = 4;

function rootFolderRank(node: KBNode): number {
  return ROOT_FOLDER_RANK.get(node.name) ?? OTHER_ROOT_FOLDER_RANK;
}

// sortNodes orders one directory's children.
//
// `order` is the user's own drag-chosen sequence for this folder (by name,
// persisted server-side). It takes precedence, but only for names it actually
// lists: anything that appeared since the last drag — a note an agent just
// wrote — is NOT silently pinned to an arbitrary spot, it falls through to the
// derived rules below and sorts after the explicitly-ordered block.
//
// `isRoot` selects the fixed default-folder ordering above. It applies only to
// the vault root, because those names are only meaningful there — a folder
// someone happens to call "chats" inside notes/ is just a folder.
export function sortNodes(
  nodes: KBNode[],
  order: string[] = [],
  isRoot = false,
): KBNode[] {
  const rank = new Map(order.map((name, i) => [name, i]));
  return [...nodes].sort((a, b) => {
    const ra = rank.get(a.name),
      rb = rank.get(b.name);
    if (ra !== undefined && rb !== undefined) return ra - rb;
    if (ra !== undefined) return -1;
    if (rb !== undefined) return 1;

    // Directories always precede files, at every level.
    const dirA = a.is_dir ? 0 : 1,
      dirB = b.is_dir ? 0 : 1;
    if (dirA !== dirB) return dirA - dirB;

    if (isRoot && a.is_dir && b.is_dir) {
      const fa = rootFolderRank(a),
        fb = rootFolderRank(b);
      if (fa !== fb) return fa - fb;
    }
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
type DraggedNode = {
  path: string;
  name: string;
  parent: string;
  isDir: boolean;
  // When the dragged row is part of a multi-selection of 2+, this carries the
  // whole selection so a drop moves all of them. Absent for a single-row drag.
  selection?: string[];
};

const DragCtx = createContext<{
  dragged: DraggedNode | null;
  setDragged: (d: DraggedNode | null) => void;
}>({ dragged: null, setDragged: () => {} });

export function parentOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i === -1 ? "" : path.slice(0, i);
}

export function baseName(path: string): string {
  return path.split("/").pop() ?? path;
}

// ── Multi-select ─────────────────────────────────────────────────────────────
//
// Ctrl/Cmd-click toggles a path in the selection; Shift-click selects the range
// between the anchor and the clicked row over the currently-VISIBLE rows. The
// visible order is read straight from the DOM at click time (every row carries a
// `data-kb-path`), scoped to the tree container — so range selection follows
// exactly what the user sees, including expanded children, without maintaining a
// separate flattened registry across the lazily-loaded nested levels.
type SelectionCtxValue = {
  selected: Set<string>;
  isSelected: (path: string) => boolean;
  // Handle a row's click with modifier keys. Returns true when it consumed the
  // click as a selection gesture (caller should NOT then open/expand the row).
  onRowClick: (path: string, e: React.MouseEvent) => boolean;
  // Called on a plain (no-modifier) click so the anchor tracks the last simple
  // click even though the multi-selection is cleared.
  setAnchor: (path: string) => void;
  containerRef: React.RefObject<HTMLDivElement | null>;
};

const SelectionCtx = createContext<SelectionCtxValue>({
  selected: new Set(),
  isSelected: () => false,
  onRowClick: () => false,
  setAnchor: () => {},
  containerRef: { current: null },
});

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

// NodeIcon renders a node's custom emoji when one is set, otherwise the default
// lucide icon. Shared by the tree rows and (via the exported helper) the folder
// page / note header so the same node shows the same icon everywhere.
export function NodeIcon({
  node,
  expanded,
  className,
}: {
  node: KBNode;
  expanded?: boolean;
  className?: string;
}) {
  if (node.icon) {
    return (
      <span
        className={cn(
          "inline-flex items-center justify-center leading-none",
          className,
        )}
        aria-hidden
      >
        {node.icon}
      </span>
    );
  }
  const Icon = iconFor(node, !!expanded);
  return <Icon className={cn("size-4 shrink-0", className)} />;
}

type NewKind = "note" | "folder";

// Shared by the per-row "New note…"/"New folder…" actions and the pane
// header's root-level "New note" button. `dirPath` seeds the Location picker's
// default; when `pickLocation` is true (the pane-header + button, which isn't
// anchored to a specific folder) the user can retarget it to any folder.
export function NewEntryDialog({
  dirPath,
  kind,
  open,
  onOpenChange,
  pickLocation = false,
  onCreated,
}: {
  dirPath: string;
  kind: NewKind;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pickLocation?: boolean;
  // Called with the path that was actually created, so the caller can navigate
  // to it — creating a note and being left on the previous screen was the whole
  // complaint. Optional, so a call site that only wants the file to exist stays
  // unchanged.
  onCreated?: (path: string, isDir: boolean) => void;
}) {
  const [name, setName] = useState("");
  const [location, setLocation] = useState(dirPath);
  const [error, setError] = useState("");
  const newNote = useNewNote();

  // Reset on the OPEN TRANSITION only. Keying this on `dirPath` as well (as it
  // was) meant any change to the caller's current directory while the dialog
  // was open wiped the half-typed name — and KBPage's `currentDir` does change
  // underneath an open dialog, because it is derived from the open note's path.
  // The user then pressed Create on an empty field and submit() silently
  // returned on `if (!n) return`.
  const wasOpenRef = useRef(false);
  useEffect(() => {
    if (open && !wasOpenRef.current) {
      setName("");
      setLocation(dirPath);
      setError("");
    }
    wasOpenRef.current = open;
  }, [open, dirPath]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    let n = name.trim();
    if (!n) return;
    if (kind === "note" && !n.endsWith(".md")) n += ".md";
    const dir = pickLocation ? location : dirPath;
    const path = dir ? `${dir}/${n}` : n;
    try {
      await newNote.mutateAsync({ path, is_dir: kind === "folder" });
      // `path` IS the created path, so no round trip is needed to discover it:
      // the client appends ".md" when the name lacks it, and the server
      // (apiNewKBNote) only appends when the basename has no dot at all — so
      // for a plain name both produce the same result, and for a dotted name
      // the client already supplied the extension and the server writes it
      // verbatim.
      onCreated?.(path, kind === "folder");
      onOpenChange(false);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>
            {kind === "note" ? "New note" : "New folder"}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="new-entry-name">Name</Label>
            <Input
              id="new-entry-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
            />
          </div>
          {pickLocation && (
            <div className="space-y-1.5">
              <Label htmlFor="new-entry-location">Location</Label>
              <FolderSelect
                id="new-entry-location"
                value={location}
                onChange={setLocation}
              />
            </div>
          )}
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full">
            <Plus />
            Create
          </Button>
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
            <Input
              id="rename-path"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              autoFocus
            />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full">
            <Pencil />
            Rename
          </Button>
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
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={confirm}>
            <Trash2 />
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

type DialogKind =
  "new-note" | "new-folder" | "rename" | "delete" | "icon" | null;

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
  onReorder: (
    dragged: DraggedNode,
    targetName: string,
    position: "before" | "after",
  ) => void;
  // Fired when OS file(s) are dropped ON THIS ROW specifically — see the
  // dedicated OS-file-drag branch in handleDragOver/handleDrop below. Omitted
  // when the tree as a whole is drop-inert (FileTree's onImportFiles absent).
  onImportFiles?: (files: File[], dir: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const [dialog, setDialog] = useState<DialogKind>(null);
  const [hint, setHint] = useState<DropHint>(null);
  const { dragged, setDragged } = useContext(DragCtx);
  const selection = useContext(SelectionCtx);
  const setIcon = useSetKBIcon();
  const selected = selectedPath === node.path;
  const multiSelected = selection.isSelected(node.path);
  // Protected nodes can't be dragged, renamed, deleted, or multi-selected — a
  // bulk delete/move must never be able to include a DB-backed system node.
  const protectedNode = isProtectedPath(node.path);

  function handleClick(e?: React.MouseEvent) {
    // Ctrl/Cmd or Shift click is a multi-selection gesture — it must not open
    // or expand the row. Protected nodes are never multi-selectable, so the
    // gesture opens the row normally instead of adding it to a bulk selection.
    if (e && !protectedNode && selection.onRowClick(node.path, e)) return;
    selection.setAnchor(node.path);
    if (node.is_dir) setExpanded((x) => !x);
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
    const sameParent =
      dragged.parent === parentOf(node.path) && !isEffectivelySystem(node);
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
      draggable={!protectedNode}
      data-kb-path={node.path}
      onDragStart={(e) => {
        e.stopPropagation();
        setDragged({
          path: node.path,
          name: node.name,
          parent: parentOf(node.path),
          isDir: node.is_dir,
          selection:
            multiSelected && selection.selected.size > 1
              ? [...selection.selected]
              : undefined,
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
              "flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-2 text-sm",
              dragged ? "cursor-grabbing" : "cursor-pointer",
              multiSelected
                ? "bg-accent/20"
                : selected
                  ? "bg-border"
                  : "hover:bg-chrome",
              isEffectivelySystem(node) && "text-muted-2",
            )}
          >
            {node.is_dir ? (
              <ChevronRight
                className={cn(
                  "size-4 shrink-0 text-muted-2 transition-transform",
                  expanded && "rotate-90",
                )}
              />
            ) : (
              <span className="size-4 shrink-0" />
            )}
            <NodeIcon
              node={node}
              expanded={expanded}
              className="size-5 shrink-0 text-lg"
            />
            <span
              className={cn(
                "flex-1 truncate",
                depth === 0 && node.is_dir && "font-medium",
              )}
            >
              {node.display_name}
            </span>
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                aria-label={`Actions for ${node.display_name}`}
                className="flex size-7 shrink-0 items-center justify-center rounded-md opacity-0 transition-opacity group-hover:opacity-100 hover:bg-border focus-visible:opacity-100"
              >
                <MoreHorizontal className="size-4" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {node.is_dir && (
                <>
                  <DropdownMenuItem onSelect={() => setDialog("new-note")}>
                    <FilePlus /> New note…
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => setDialog("new-folder")}>
                    <FolderPlus /> New folder…
                  </DropdownMenuItem>
                </>
              )}
              <DropdownMenuItem onSelect={() => setDialog("icon")}>
                <Smile /> Change icon…
              </DropdownMenuItem>
              {/* Rename/Delete are withheld for system-managed, DB-backed nodes
                (agents/chats/inbox/skills/reminders): removing them here would
                orphan the backing record — delete from the item's own page. */}
              {!isProtectedPath(node.path) && (
                <>
                  <DropdownMenuItem onSelect={() => setDialog("rename")}>
                    <Pencil /> Rename…
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    variant="destructive"
                    onSelect={() => setDialog("delete")}
                  >
                    <Trash2 /> Delete…
                  </DropdownMenuItem>
                </>
              )}
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
          {/* Creating from a folder's menu opens the result, exactly as
              clicking it in the tree would — same onSelect, so the two paths
              can't drift. */}
          <NewEntryDialog
            dirPath={node.path}
            kind="note"
            open={dialog === "new-note"}
            onOpenChange={(o) => setDialog(o ? "new-note" : null)}
            onCreated={(p, isDir) => onSelect(p, isDir)}
          />
          <NewEntryDialog
            dirPath={node.path}
            kind="folder"
            open={dialog === "new-folder"}
            onOpenChange={(o) => setDialog(o ? "new-folder" : null)}
            onCreated={(p, isDir) => onSelect(p, isDir)}
          />
        </>
      )}
      <RenameDialog
        node={node}
        open={dialog === "rename"}
        onOpenChange={(o) => setDialog(o ? "rename" : null)}
      />
      <DeleteDialog
        node={node}
        open={dialog === "delete"}
        onOpenChange={(o) => setDialog(o ? "delete" : null)}
      />
      <EmojiPicker
        open={dialog === "icon"}
        onOpenChange={(o) => setDialog(o ? "icon" : null)}
        current={node.icon}
        onSelect={(emoji) =>
          setIcon.mutate({ path: node.path, icon: emoji ?? "" })
        }
      />
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
    // path === "" is the vault root, where the default-folder ordering applies.
    () => sortNodes(data?.nodes ?? [], data?.order ?? [], path === ""),
    [data?.nodes, data?.order, path],
  );

  function handleReorder(
    dragged: DraggedNode,
    targetName: string,
    position: "before" | "after",
  ) {
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
            message:
              err instanceof ApiError
                ? err.message
                : "Couldn't save the new order",
            variant: "error",
          }),
      },
    );
  }

  if (isLoading) {
    return (
      <div
        style={{ paddingLeft: 6 + depth * 14 }}
        className="py-2 text-xs text-muted-2"
      >
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

  // ── Multi-selection state ────────────────────────────────────────────────
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const anchorRef = useRef<string | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);

  const clearSelection = useCallback(() => setSelected(new Set()), []);

  // Reads the visible row paths straight from the DOM, in document (pre-order)
  // order — which is exactly what the user sees, including expanded children.
  const visiblePaths = useCallback((): string[] => {
    const el = containerRef.current;
    if (!el) return [];
    return Array.from(el.querySelectorAll<HTMLElement>("[data-kb-path]"))
      .map((n) => n.dataset.kbPath ?? "")
      .filter(Boolean);
  }, []);

  const onRowClick = useCallback(
    (path: string, e: React.MouseEvent): boolean => {
      if (e.metaKey || e.ctrlKey) {
        setSelected((prev) => {
          const next = new Set(prev);
          if (next.has(path)) next.delete(path);
          else next.add(path);
          return next;
        });
        anchorRef.current = path;
        return true;
      }
      if (e.shiftKey) {
        const order = visiblePaths();
        const anchor = anchorRef.current ?? path;
        const a = order.indexOf(anchor);
        const b = order.indexOf(path);
        if (a === -1 || b === -1) {
          setSelected(new Set([path]));
        } else {
          const [lo, hi] = a <= b ? [a, b] : [b, a];
          setSelected(new Set(order.slice(lo, hi + 1)));
        }
        return true;
      }
      return false; // plain click — caller proceeds to open/expand
    },
    [visiblePaths],
  );

  const setAnchor = useCallback((path: string) => {
    anchorRef.current = path;
    setSelected(new Set());
  }, []);

  const selectionCtx = useMemo<SelectionCtxValue>(
    () => ({
      selected,
      isSelected: (p: string) => selected.has(p),
      onRowClick,
      setAnchor,
      containerRef,
    }),
    [selected, onRowClick, setAnchor],
  );

  // Moves are owned by the root so a drag can cross levels — the source row
  // and the destination folder are usually in different TreeLevels. When the
  // drag carries a multi-selection, move every selected path into the folder.
  function moveOne(from: string, folderPath: string) {
    const to = folderPath ? `${folderPath}/${baseName(from)}` : baseName(from);
    if (to === from) return; // already there
    renameNote.mutate(
      { from, to },
      {
        onSuccess: () => onMoved?.(from, to),
        onError: (err) =>
          toast({
            message:
              err instanceof ApiError
                ? err.message
                : `Couldn't move “${baseName(from)}”`,
            variant: "error",
          }),
      },
    );
  }

  function handleMoveInto(d: DraggedNode, folder: KBNode) {
    const paths =
      d.selection && d.selection.length > 1 ? d.selection : [d.path];
    for (const p of paths) {
      // A folder can't be moved into itself or its own subtree.
      if (folder.path === p || folder.path.startsWith(p + "/")) continue;
      moveOne(p, folder.path);
    }
    clearSelection();
  }

  return (
    <DragCtx.Provider value={dragCtx}>
      <SelectionCtx.Provider value={selectionCtx}>
        <div
          ref={containerRef}
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
            if (!onImportFiles || !e.dataTransfer.types.includes("Files"))
              return;
            e.preventDefault();
            e.dataTransfer.dropEffect = "copy";
            setFileDragging(true);
          }}
          onDragLeave={(e) => {
            // A dragleave fires when moving from this wrapper onto one of its
            // own row children too — only clear the highlight once the pointer
            // has actually left the whole tree, or the ring would flicker on
            // every row boundary crossed while dragging.
            if (e.currentTarget.contains(e.relatedTarget as Node | null))
              return;
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
        <SelectionActionBar
          selected={selected}
          onClear={clearSelection}
          onMoved={onMoved}
        />
      </SelectionCtx.Provider>
    </DragCtx.Provider>
  );
}

// SelectionActionBar floats above the pane when 2+ items are multi-selected,
// offering bulk Move (into a chosen folder) and Delete. Move renames each item
// into the target folder; Delete uses a single confirm (matching the tree's
// existing per-row delete UX) then deletes each. Both clear the selection when
// done.
function SelectionActionBar({
  selected,
  onClear,
  onMoved,
}: {
  selected: Set<string>;
  onClear: () => void;
  onMoved?: (from: string, to: string) => void;
}) {
  const [moveOpen, setMoveOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const renameNote = useRenameNote();
  const deleteNote = useDeleteNote();
  const { toast } = useToast();

  if (selected.size < 2) return null;
  const paths = [...selected];
  const count = paths.length;

  async function doMove() {
    setBusy(true);
    for (const from of paths) {
      // Skip a folder being moved into itself/its subtree, and no-op moves.
      if (target === from || target.startsWith(from + "/")) continue;
      const to = target ? `${target}/${baseName(from)}` : baseName(from);
      if (to === from) continue;
      try {
        await renameNote.mutateAsync({ from, to });
        onMoved?.(from, to);
      } catch (err) {
        toast({
          message:
            err instanceof ApiError
              ? `Couldn't move “${baseName(from)}”: ${err.message}`
              : `Couldn't move “${baseName(from)}”`,
          variant: "error",
        });
      }
    }
    setBusy(false);
    setMoveOpen(false);
    onClear();
  }

  async function doDelete() {
    setBusy(true);
    for (const p of paths) {
      try {
        await deleteNote.mutateAsync({ path: p });
      } catch (err) {
        toast({
          message:
            err instanceof ApiError
              ? `Couldn't delete “${baseName(p)}”: ${err.message}`
              : `Couldn't delete “${baseName(p)}”`,
          variant: "error",
        });
      }
    }
    setBusy(false);
    setDeleteOpen(false);
    onClear();
  }

  return (
    <>
      <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-2 rounded-lg border border-border bg-chrome px-3 py-2 shadow-lg">
        <span className="text-sm text-muted-2">{count} selected</span>
        <div className="mx-1 h-4 w-px bg-border" />
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setTarget("");
            setMoveOpen(true);
          }}
        >
          <FolderInput className="mr-1 size-4" /> Move…
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-danger"
          onClick={() => setDeleteOpen(true)}
        >
          <Trash2 className="mr-1 size-4" /> Delete
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Clear selection"
          onClick={onClear}
        >
          <X className="size-4" />
        </Button>
      </div>

      <Dialog open={moveOpen} onOpenChange={setMoveOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Move {count} items</DialogTitle>
          </DialogHeader>
          <div className="space-y-1.5">
            <Label htmlFor="bulk-move-target">Destination folder</Label>
            <FolderSelect
              id="bulk-move-target"
              value={target}
              onChange={setTarget}
              disabledPaths={paths}
            />
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setMoveOpen(false)}
              disabled={busy}
            >
              Cancel
            </Button>
            <Button onClick={doMove} disabled={busy}>
            <FolderInput />
              Move
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete {count} items?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-2">This can’t be undone.</p>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setDeleteOpen(false)}
              disabled={busy}
            >
              Cancel
            </Button>
            <Button variant="destructive" onClick={doDelete} disabled={busy}>
              <Trash2 />
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
