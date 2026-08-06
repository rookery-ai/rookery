import { useState, type ReactNode } from "react";
import type { Editor } from "@tiptap/core";
import { BubbleMenu } from "@tiptap/react/menus";
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
} from "lucide-react";
import { cn } from "@/lib/utils";
import { TEXT_COLORS, HIGHLIGHT_COLORS, HIGHLIGHT_FG } from "./marks/colors";

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
export default function BubbleToolbar({ editor }: { editor: Editor | null }) {
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
    <BubbleMenu editor={editor} shouldShow={({ state }) => !state.selection.empty}>
      <div className="rounded-md border border-border bg-popover shadow-md">
        {colorsOpen ? (
          <ColorSwatches editor={editor} onDone={() => setColorsOpen(false)} />
        ) : (
          <div className="flex items-center gap-0.5 p-1">
        <ToolbarButton
          label="Bold"
          active={editor.isActive("bold")}
          onClick={() => editor.chain().focus().toggleBold().run()}
        >
          <Bold className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Italic"
          active={editor.isActive("italic")}
          onClick={() => editor.chain().focus().toggleItalic().run()}
        >
          <Italic className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Underline"
          active={editor.isActive("underline")}
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
          active={editor.isActive("strike")}
          onClick={() => editor.chain().focus().toggleStrike().run()}
        >
          <Strikethrough className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Code"
          active={editor.isActive("code")}
          onClick={() => editor.chain().focus().toggleCode().run()}
        >
          <Code className="size-4" />
        </ToolbarButton>
        <div className="mx-0.5 h-5 w-px bg-border" />
        <ToolbarButton
          label="Heading 1"
          active={editor.isActive("heading", { level: 1 })}
          onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}
        >
          <Heading1 className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Heading 2"
          active={editor.isActive("heading", { level: 2 })}
          onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
        >
          <Heading2 className="size-4" />
        </ToolbarButton>
        <ToolbarButton label="Link" active={editor.isActive("link")} onClick={setLink}>
          <LinkIcon className="size-4" />
        </ToolbarButton>
        <div className="mx-0.5 h-5 w-px bg-border" />
        <ToolbarButton
          label="Bullet list"
          active={editor.isActive("bulletList")}
          onClick={() => editor.chain().focus().toggleBulletList().run()}
        >
          <List className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="To-do list"
          active={editor.isActive("taskList")}
          onClick={() => editor.chain().focus().toggleTaskList().run()}
        >
          <ListTodo className="size-4" />
        </ToolbarButton>
        <ToolbarButton
          label="Quote"
          active={editor.isActive("blockquote")}
          onClick={() => editor.chain().focus().toggleBlockquote().run()}
        >
          <Quote className="size-4" />
        </ToolbarButton>
          </div>
        )}
      </div>
    </BubbleMenu>
  );
}
