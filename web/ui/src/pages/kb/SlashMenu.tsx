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
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { filterSlashItems, type SlashItem } from "./slashItems";

const ICONS: Record<string, LucideIcon> = {
  "Heading 1": Heading1,
  "Heading 2": Heading2,
  "Heading 3": Heading3,
  "Bullet list": List,
  "Numbered list": ListOrdered,
  "To-do list": ListTodo,
  Quote: Quote,
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
              index === selected ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
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

            const position = (props: SuggestionProps<SlashItem>) => {
              if (!el) return;
              const rect = props.clientRect?.();
              if (!rect) return;
              el.style.left = `${rect.left}px`;
              el.style.top = `${rect.bottom + 4}px`;
            };

            return {
              onStart: (props) => {
                renderer = new ReactRenderer(SlashList, { props, editor: props.editor });
                el = document.createElement("div");
                el.style.position = "fixed";
                el.style.zIndex = "50";
                el.appendChild(renderer.element as HTMLElement);
                document.body.appendChild(el);
                position(props);
              },
              onUpdate: (props) => {
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
                el?.remove();
                renderer?.destroy();
                el = null;
                renderer = null;
              },
            };
          },
        }),
      ];
    },
  });
}
