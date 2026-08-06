import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { Extension, type AnyExtension, type Editor } from "@tiptap/core";
import Suggestion, {
  exitSuggestion,
  type SuggestionKeyDownProps,
  type SuggestionProps,
} from "@tiptap/suggestion";
import { ReactRenderer } from "@tiptap/react";
import {
  Heading1,
  Heading2,
  Heading3,
  List,
  ListOrdered,
  ListTodo,
  Quote,
  Code,
  Minus,
  Table as TableIcon,
  Image as ImageIcon,
  Paperclip,
  Info,
  Lightbulb,
  AlertTriangle,
  OctagonAlert,
  StickyNote,
  ChevronRight,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { filterSlashItems, type SlashItem } from "./slashItems";

export const ICONS: Record<string, LucideIcon> = {
  "Heading 1": Heading1,
  "Heading 2": Heading2,
  "Heading 3": Heading3,
  "Bullet list": List,
  "Numbered list": ListOrdered,
  "To-do list": ListTodo,
  Quote: Quote,
  "Callout: note": StickyNote,
  "Callout: tip": Lightbulb,
  "Callout: info": Info,
  "Callout: warning": AlertTriangle,
  "Callout: danger": OctagonAlert,
  "Toggle list": ChevronRight,
  "Code block": Code,
  Divider: Minus,
  Table: TableIcon,
  Image: ImageIcon,
  "File attachment": Paperclip,
};

// Deletes the "/query" range then runs the chosen item — the command every
// selection path (click or keyboard) funnels through.
function runSlashItem(editor: Editor, range: { from: number; to: number }, item: SlashItem) {
  editor.chain().focus().deleteRange(range).run();
  item.run(editor);
}

type ListHandle = {
  onKeyDown: (props: SuggestionKeyDownProps) => boolean;
};

const SlashList = forwardRef<ListHandle, SuggestionProps<SlashItem>>(function SlashList(
  { items, command },
  ref,
) {
  const [selected, setSelected] = useState(0);

  useEffect(() => setSelected(0), [items]);

  const select = (index: number) => {
    const item = items[index];
    if (item) command(item);
  };

  useImperativeHandle(ref, () => ({
    onKeyDown({ event }) {
      if (event.key === "ArrowDown") {
        setSelected((prev) => (items.length ? (prev + 1) % items.length : 0));
        return true;
      }
      if (event.key === "ArrowUp") {
        setSelected((prev) => (items.length ? (prev - 1 + items.length) % items.length : 0));
        return true;
      }
      if (event.key === "Enter") {
        select(selected);
        return true;
      }
      if (event.key === "Escape") {
        return true;
      }
      return false;
    },
  }));

  if (items.length === 0) {
    return (
      <div className="w-56 rounded-md border border-border bg-popover px-3 py-2 text-sm text-muted-2 shadow-md">
        No matching blocks
      </div>
    );
  }

  return (
    <div className="w-56 overflow-hidden rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-md">
      {items.map((item, index) => {
        const Icon = ICONS[item.title];
        return (
          <button
            key={item.title}
            type="button"
            className={cn(
              "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm",
              // Soft tint + normal foreground, matching CommandItem: the icon
              // inside each row is text-muted-2 and never flipped with the
              // fill, so a full bg-accent left it barely visible.
              index === selected
                ? "bg-accent-soft text-foreground ring-1 ring-accent/40"
                : "hover:bg-accent-soft",
            )}
            onMouseEnter={() => setSelected(index)}
            onClick={() => select(index)}
          >
            {Icon ? <Icon className="size-4 text-muted-2" /> : null}
            {item.title}
          </button>
        );
      })}
    </div>
  );
});

export type Placement = { left: number; top: number; maxHeight: number | null };

/**
 * Decide where the fixed-position slash popup goes.
 *
 * Pure, and measurements-in/measurements-out, so it is actually testable:
 * jsdom has no layout engine and returns 0 for every rect, so a test driving
 * the real popup can prove it OPENS but never prove WHERE it lands. That gap
 * is how the original shipped — it set `top = caret.bottom + 4` with no bounds
 * check at all, and with the caret on the last line of a long note the menu
 * rendered 410px below the fold with ~32px visible.
 *
 * The menu is ~442px tall at twelve items — more than half a laptop viewport —
 * so flipping above is not sufficient on its own: when neither side fits, the
 * list has to cap and scroll. Clipping instead would hide items with no way to
 * reach them.
 */
export function placeMenu(
  caret: { top: number; bottom: number; left: number },
  menu: { width: number; height: number },
  viewport: { width: number; height: number },
  gap = 4,
): Placement {
  const below = viewport.height - caret.bottom - gap;
  const above = caret.top - gap;

  let top: number;
  let maxHeight: number | null = null;

  if (menu.height <= below) {
    top = caret.bottom + gap;
  } else if (menu.height <= above) {
    top = caret.top - gap - menu.height;
  } else if (above >= below) {
    maxHeight = Math.max(above, 0);
    top = gap;
  } else {
    maxHeight = Math.max(below, 0);
    top = caret.bottom + gap;
  }

  // Math.max on the ceiling keeps `left` non-negative on a viewport narrower
  // than the menu: an overhang on the right is recoverable, a negative left
  // puts the start of every item permanently off-screen.
  const maxLeft = viewport.width - menu.width - gap;
  const left = Math.min(Math.max(caret.left, gap), Math.max(maxLeft, gap));

  return { left, top, maxHeight };
}

const SLASH_EXTENSION_NAME = "slashCommand";

// Suggestion-based extension: "/" triggers a floating block-type menu.
// Positioning is fixed + manual (via the suggestion's clientRect) — no
// popper/floating-ui dependency, per the task's binding constraint.
export function slashSuggestion(): AnyExtension {
  return Extension.create({
    name: SLASH_EXTENSION_NAME,

    addProseMirrorPlugins() {
      return [
        Suggestion({
          editor: this.editor,
          char: "/",
          // Default allowedPrefixes (`[' ']`) + startOfLine:false already
          // means "at the very start of a block OR right after a space" —
          // exactly the "startOfLine OR preceded by whitespace" contract.
          startOfLine: false,
          allowSpaces: false,
          command: ({ editor, range, props }) => runSlashItem(editor, range, props as SlashItem),
          items: ({ query }) => filterSlashItems(query),
          render: () => {
            let renderer: ReactRenderer<ListHandle, SuggestionProps<SlashItem>> | null = null;
            let el: HTMLDivElement | null = null;
            let reposition: (() => void) | null = null;
            let sizeObserver: ResizeObserver | null = null;
            // The caret moves as the query is typed, and @tiptap/suggestion
            // hands out a FRESH props object (with a fresh clientRect closure)
            // on every update. The listeners below fire outside those calls, so
            // they must read the latest props rather than closing over the ones
            // onStart happened to receive — otherwise a resize triggered by the
            // list filtering would re-place the menu against a stale caret.
            let latest: SuggestionProps<SlashItem> | null = null;

            const position = (props: SuggestionProps<SlashItem>) => {
              if (!el) return;
              const caret = props.clientRect?.();
              if (!caret) return;
              // Measured as rendered rather than assumed: the item count
              // changes as the query narrows, so a constant height would be
              // wrong the moment you type.
              const box = el.getBoundingClientRect();
              const p = placeMenu(
                { top: caret.top, bottom: caret.bottom, left: caret.left },
                { width: box.width, height: box.height },
                { width: window.innerWidth, height: window.innerHeight },
              );
              el.style.left = `${p.left}px`;
              el.style.top = `${p.top}px`;
              el.style.maxHeight = p.maxHeight === null ? "" : `${p.maxHeight}px`;
              el.style.overflowY = p.maxHeight === null ? "" : "auto";
            };

            return {
              onStart: (props) => {
                renderer = new ReactRenderer(SlashList, { props, editor: props.editor });
                el = document.createElement("div");
                el.style.position = "fixed";
                el.style.zIndex = "50";
                el.appendChild(renderer.element as HTMLElement);
                document.body.appendChild(el);
                latest = props;
                reposition = () => latest && position(latest);
                reposition();
                // Re-place whenever the popup's own size changes.
                //
                // Load-bearing, not defensive: ReactRenderer has not laid the
                // list out yet at appendChild time, so the measure above reads
                // height 0. Zero fits anywhere, so the very first placement
                // always chose "below" — and with nothing else to correct it,
                // a menu opened at the bottom of a long note stayed 410px off
                // the fold, which is the exact bug this was meant to fix.
                // The observer also handles the height changing as a query
                // narrows the item list.
                sizeObserver = new ResizeObserver(() => reposition?.());
                sizeObserver.observe(el);
                // The popup is position:fixed while the caret is not, so any
                // scroll desynchronises them. capture:true because scroll does
                // not bubble — without it a scroll of the editor pane (the one
                // that actually moves the caret) would never be seen.
                window.addEventListener("scroll", reposition, true);
                window.addEventListener("resize", reposition);
              },
              onUpdate: (props) => {
                latest = props;
                renderer?.updateProps(props);
                position(props);
              },
              onKeyDown: (props) => {
                if (props.event.key === "Escape") {
                  // @tiptap/suggestion's own plugin (createSuggestionProps,
                  // node_modules/@tiptap/suggestion) happens to force-exit on
                  // Escape internally too — but that's an implementation
                  // detail of the library, not a contract this component
                  // should depend on. Close it ourselves explicitly so the
                  // popup closing doesn't silently regress on a future
                  // @tiptap/suggestion upgrade that drops that behavior.
                  exitSuggestion(props.view);
                  return true;
                }
                return renderer?.ref?.onKeyDown(props) ?? false;
              },
              onExit: () => {
                sizeObserver?.disconnect();
                sizeObserver = null;
                if (reposition) {
                  window.removeEventListener("scroll", reposition, true);
                  window.removeEventListener("resize", reposition);
                  reposition = null;
                }
                el?.remove();
                renderer?.destroy();
                el = null;
                renderer = null;
                latest = null;
              },
            };
          },
        }),
      ];
    },
  });
}
