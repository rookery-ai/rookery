import { useState, type ReactNode } from "react";
import type { Editor } from "@tiptap/core";
import { BubbleMenu } from "@tiptap/react/menus";
import { useEditorState } from "@tiptap/react";
import {
  Bold,
  Italic,
  Underline as UnderlineIcon,
  Strikethrough,
  Code,
  Heading1,
  Heading2,
  Link as LinkIcon,
  List,
  ListTodo,
  Quote,
  Baseline,
  Ban,
  AlignLeft,
  AlignCenter,
  AlignRight,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { TEXT_COLORS, HIGHLIGHT_COLORS, HIGHLIGHT_FG } from "./marks/colors";
import { ALIGNMENTS } from "./nodes/align";
import AIActions, { type AIActionsState } from "./AIActions";

function ToolbarButton({
  label,
  active,
  onClick,
  children,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      aria-pressed={active}
      // Mousedown (not click) so the editor selection survives the click —
      // a plain click on a button steals focus/blur first, which can
      // collapse the selection the bubble menu is acting on.
      onMouseDown={(e) => {
        e.preventDefault();
        onClick();
      }}
      className={cn(
        "inline-flex size-7 items-center justify-center rounded-sm text-foreground hover:bg-accent",
        active && "bg-border",
      )}
    >
      {children}
    </button>
  );
}

// Fixed swatch grid — deliberately not a colour picker. Two rows of eight plus
// a "none" control per row.
function ColorSwatches({ editor, onDone }: { editor: Editor; onDone: () => void }) {
  return (
    <div className="w-56 space-y-2 p-2">
      <div>
        <div className="mb-1 text-xs text-muted-2">Text</div>
        <div className="flex flex-wrap gap-1">
          {TEXT_COLORS.map((c) => (
            <button
              key={c.name}
              type="button"
              title={`Text ${c.name}`}
              aria-label={`Text ${c.name}`}
              // Mousedown, not click: a click steals focus and collapses the
              // selection the toolbar is acting on. onClick is ALSO wired
              // (redundant on a mouse click, since preventDefault on
              // mousedown stops the click from doing anything harmful — but
              // it's the only event a keyboard activation (Tab, then Enter/
              // Space) fires, so without it the swatch is keyboard-dead.
              // setKBTextColor/unsetKBTextColor are idempotent, so both
              // handlers firing on one interaction is harmless.
              onMouseDown={(e) => {
                e.preventDefault();
                editor.chain().focus().setKBTextColor(c.hex).run();
                onDone();
              }}
              onClick={() => {
                editor.chain().focus().setKBTextColor(c.hex).run();
                onDone();
              }}
              className="size-5 rounded-sm border border-border"
              style={{ backgroundColor: c.hex }}
            />
          ))}
          <button
            type="button"
            title="No text colour"
            aria-label="No text colour"
            onMouseDown={(e) => {
              e.preventDefault();
              editor.chain().focus().unsetKBTextColor().run();
              onDone();
            }}
            onClick={() => {
              editor.chain().focus().unsetKBTextColor().run();
              onDone();
            }}
            className="flex size-5 items-center justify-center rounded-sm border border-border"
          >
            <Ban className="size-3 text-muted-2" />
          </button>
        </div>
      </div>
      <div>
        <div className="mb-1 text-xs text-muted-2">Highlight</div>
        <div className="flex flex-wrap gap-1">
          {HIGHLIGHT_COLORS.map((c) => (
            <button
              key={c.name}
              type="button"
              title={`Highlight ${c.name}`}
              aria-label={`Highlight ${c.name}`}
              // See the text-swatch comment above: onClick covers keyboard
              // activation, onMouseDown+preventDefault preserves the
              // selection on a mouse click. Both call the same idempotent
              // command.
              onMouseDown={(e) => {
                e.preventDefault();
                editor.chain().focus().setKBBgColor(c.hex).run();
                onDone();
              }}
              onClick={() => {
                editor.chain().focus().setKBBgColor(c.hex).run();
                onDone();
              }}
              className="size-5 rounded-sm border border-border"
              style={{ backgroundColor: c.hex, color: HIGHLIGHT_FG }}
            />
          ))}
          <button
            type="button"
            title="No highlight"
            aria-label="No highlight"
            onMouseDown={(e) => {
              e.preventDefault();
              editor.chain().focus().unsetKBBgColor().run();
              onDone();
            }}
            onClick={() => {
              editor.chain().focus().unsetKBBgColor().run();
              onDone();
            }}
            className="flex size-5 items-center justify-center rounded-sm border border-border"
          >
            <Ban className="size-3 text-muted-2" />
          </button>
        </div>
      </div>
    </div>
  );
}

// Shown as a floating menu over a non-empty text selection (TipTap
// BubbleMenu — v3 moved this from @tiptap/react's root export to the
// @tiptap/react/menus subpath; no separate package install needed, it
// ships inside @tiptap/react and depends on the already-installed
// @tiptap/extension-bubble-menu transitive dep).
// `aiActions` is owned by WysiwygEditor (via useAIActions) and passed down
// as a controlled prop — it must NOT be created here, since this whole
// component (and everything under it) unmounts whenever BubbleMenu hides,
// which would otherwise drop an in-flight or just-landed AI result the
// instant the selection collapses. See useAIActions's doc comment.
export default function BubbleToolbar({
  editor,
  aiActions,
}: {
  editor: Editor | null;
  aiActions: AIActionsState;
}) {
  // Pressed states are read through useEditorState, NOT by calling
  // editor.isActive() during render.
  //
  // @tiptap/react v3 changed useEditor's `shouldRerenderOnTransaction` to
  // default to FALSE, so a component that reads editor.isActive() in its body
  // computes it once per React render and never again — the toolbar's Bold,
  // Italic, heading, list and quote highlights were all silently frozen at
  // whatever the state was when the bubble menu mounted. Nothing failed: the
  // commands worked, only the indicators lied. useEditorState subscribes to the
  // transactions this component actually depends on and re-renders just this
  // subtree, which is the v3 idiom and confines the cost to a toolbar that only
  // exists while there is a selection.
  //
  // The selector tolerates a null editor because hooks cannot run after the
  // `if (!editor) return null` guard below.
  const flags = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: !!e?.isActive("bold"),
      italic: !!e?.isActive("italic"),
      underline: !!e?.isActive("underline"),
      strike: !!e?.isActive("strike"),
      code: !!e?.isActive("code"),
      h1: !!e?.isActive("heading", { level: 1 }),
      h2: !!e?.isActive("heading", { level: 2 }),
      link: !!e?.isActive("link"),
      bulletList: !!e?.isActive("bulletList"),
      taskList: !!e?.isActive("taskList"),
      blockquote: !!e?.isActive("blockquote"),
      // null means "no wrapper", which IS left — see the alignment buttons.
      align: e?.isActive("kbAlign")
        ? ((e.getAttributes("kbAlign").align as string | undefined) ?? "center")
        : null,
    }),
  })!;

  const [colorsOpen, setColorsOpen] = useState(false);

  if (!editor) return null;

  const setLink = () => {
    const previous = editor.getAttributes("link").href as string | undefined;
    const url = window.prompt("Link URL", previous ?? "");
    if (url === null) return;
    if (url === "") {
      editor.chain().focus().extendMarkRange("link").unsetLink().run();
      return;
    }
    editor.chain().focus().extendMarkRange("link").setLink({ href: url }).run();
  };

  return (
    <BubbleMenu
      editor={editor}
      // Restores the `editor.isEditable` term TipTap's own DEFAULT
      // shouldShow includes (see @tiptap/extension-bubble-menu's
      // BubbleMenuPlugin) — overriding shouldShow here entirely dropped it,
      // so the whole toolbar (underline, both colour swatch grids, and the
      // billable AI actions row) stayed live over a read-only note. Without
      // this, a selection on a read-only note could fire a paid coder call
      // via Improve/Explain/Reformat and then autosave the rewrite, closing
      // the spend BEFORE it happens rather than guarding accept() after.
      shouldShow={({ editor: e, state }) => e.isEditable && !state.selection.empty}
    >
      <div className="rounded-md border border-border bg-popover shadow-md">
        {colorsOpen ? (
          <ColorSwatches editor={editor} onDone={() => setColorsOpen(false)} />
        ) : (
          <div className="flex items-center gap-0.5 p-1">
        <ToolbarButton
          label="Bold"
          active={flags.bold}
          onClick={() => editor.chain().focus().toggleBold().run()}
        >
          <Bold className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Italic"
          active={flags.italic}
          onClick={() => editor.chain().focus().toggleItalic().run()}
        >
          <Italic className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Underline"
          active={flags.underline}
          onClick={() => editor.chain().focus().toggleUnderline().run()}
        >
          <UnderlineIcon className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Colour"
          // This whole button row unmounts whenever the swatch panel is open
          // (see the colorsOpen ? <ColorSwatches> : <row> branch below), so
          // this button can never be ON SCREEN while colorsOpen is true —
          // passing colorsOpen here would claim a pressed state that can
          // never actually render.
          active={false}
          onClick={() => setColorsOpen((v) => !v)}
        >
          <Baseline className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Strikethrough"
          active={flags.strike}
          onClick={() => editor.chain().focus().toggleStrike().run()}
        >
          <Strikethrough className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Code"
          active={flags.code}
          onClick={() => editor.chain().focus().toggleCode().run()}
        >
          <Code className="size-4" />
        </ToolbarButton>
        <div className="mx-0.5 h-5 w-px bg-border" />
        <ToolbarButton
          label="Heading 1"
          active={flags.h1}
          onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}
        >
          <Heading1 className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Heading 2"
          active={flags.h2}
          onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
        >
          <Heading2 className="size-4" />
        </ToolbarButton>
        <ToolbarButton label="Link" active={flags.link} onClick={setLink}>
          <LinkIcon className="size-4" />
        </ToolbarButton>
        <div className="mx-0.5 h-5 w-px bg-border" />
        <ToolbarButton
          label="Bullet list"
          active={flags.bulletList}
          onClick={() => editor.chain().focus().toggleBulletList().run()}
        >
          <List className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="To-do list"
          active={flags.taskList}
          onClick={() => editor.chain().focus().toggleTaskList().run()}
        >
          <ListTodo className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Quote"
          active={flags.blockquote}
          onClick={() => editor.chain().focus().toggleBlockquote().run()}
        >
          <Quote className="size-4" />
        </ToolbarButton>
        <div className="mx-0.5 h-5 w-px bg-border" />
        {/* Direct buttons rather than a ColorSwatches-style sub-panel: the
            pressed state IS the answer to "how is this aligned?", and a panel
            would hide the one thing the user opened it to find.

            These also serve IMAGES with no separate surface — the bubble
            menu's shouldShow only requires a non-empty selection, and a
            NodeSelection on an image is non-empty. An image lives in a
            paragraph, so centring the paragraph centres it. */}
        {ALIGNMENTS.map((a) => {
          const Icon = a === "left" ? AlignLeft : a === "center" ? AlignCenter : AlignRight;
          const label = a === "left" ? "Align left" : a === "center" ? "Align centre" : "Align right";
          // Unaligned IS left, so Left reads as pressed with no wrapper at all
          // — and clicking it lifts rather than wrapping in align="left",
          // which would be an extra div that changes nothing visible.
          const active = a === "left" ? flags.align === null || flags.align === "left" : flags.align === a;
          return (
            <ToolbarButton
              key={a}
              label={label}
              active={active}
              onClick={() => {
                const chain = editor.chain().focus();
                if (a === "left") chain.clearBlockAlign().run();
                else chain.setBlockAlign(a).run();
              }}
            >
              <Icon className="size-4" />
            </ToolbarButton>
          );
        })}
          </div>
        )}
        <AIActions state={aiActions} />
      </div>
    </BubbleMenu>
  );
}
