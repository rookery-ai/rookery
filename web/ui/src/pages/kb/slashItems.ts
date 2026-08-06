import type { Editor } from "@tiptap/core";
import { CALLOUT_KINDS } from "./nodes/callout";

export type SlashItem = {
  title: string;
  keywords: string;
  run(editor: Editor): void;
};

// Order here is the display/filter-result order — filterSlashItems never
// reorders by relevance, only removes non-matches, so this list's order IS
// the contract.
export const slashItems: SlashItem[] = [
  {
    title: "Heading 1",
    keywords: "h1 heading title big section",
    run: (editor) => editor.chain().focus().toggleHeading({ level: 1 }).run(),
  },
  {
    title: "Heading 2",
    keywords: "h2 heading subtitle section",
    run: (editor) => editor.chain().focus().toggleHeading({ level: 2 }).run(),
  },
  {
    title: "Heading 3",
    keywords: "h3 heading subheading section",
    run: (editor) => editor.chain().focus().toggleHeading({ level: 3 }).run(),
  },
  {
    title: "Bullet list",
    keywords: "ul unordered bullet list",
    run: (editor) => editor.chain().focus().toggleBulletList().run(),
  },
  {
    title: "Numbered list",
    keywords: "ol ordered numbered list",
    run: (editor) => editor.chain().focus().toggleOrderedList().run(),
  },
  {
    title: "To-do list",
    keywords: "todo task checklist checkbox list",
    run: (editor) => editor.chain().focus().toggleTaskList().run(),
  },
  {
    title: "Quote",
    keywords: "blockquote quote citation",
    run: (editor) => editor.chain().focus().toggleBlockquote().run(),
  },
  ...CALLOUT_KINDS.map((kind) => ({
    title: `Callout: ${kind}`,
    keywords: `callout admonition ${kind} aside box`,
    run: (editor: Editor) => editor.chain().focus().setCallout(kind).run(),
  })),
  {
    title: "Code block",
    keywords: "code codeblock pre fenced snippet",
    run: (editor) => editor.chain().focus().toggleCodeBlock().run(),
  },
  {
    title: "Divider",
    keywords: "hr divider horizontal rule line separator",
    run: (editor) => editor.chain().focus().setHorizontalRule().run(),
  },
  {
    title: "Table",
    keywords: "table grid rows columns 3x3",
    run: (editor) =>
      editor.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run(),
  },
  {
    title: "Image",
    keywords: "image picture photo img upload embed media",
    // Opening the image picker is a React concern, so signal NoteEditor via a
    // window event rather than manipulating the editor here (the "/img" range
    // is already deleted and the cursor is at the insertion point).
    run: () => window.dispatchEvent(new CustomEvent("kb:insertImage")),
  },
  {
    title: "File attachment",
    keywords: "file attachment upload document link paperclip",
    run: () => window.dispatchEvent(new CustomEvent("kb:insertAttachment")),
  },
];

export function filterSlashItems(query: string): SlashItem[] {
  const q = query.trim().toLowerCase();
  if (!q) return slashItems;
  return slashItems.filter(
    (item) => item.title.toLowerCase().includes(q) || item.keywords.toLowerCase().includes(q),
  );
}
