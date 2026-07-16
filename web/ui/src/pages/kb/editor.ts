import { Editor, type AnyExtension } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import Link from "@tiptap/extension-link";
import Placeholder from "@tiptap/extension-placeholder";
import TaskList from "@tiptap/extension-task-list";
import TaskItem from "@tiptap/extension-task-item";
import { Table } from "@tiptap/extension-table";
import TableRow from "@tiptap/extension-table-row";
import TableCell from "@tiptap/extension-table-cell";
import TableHeader from "@tiptap/extension-table-header";
import { Markdown, type MarkdownStorage } from "tiptap-markdown";

// tiptap-markdown ships types for its own extension but doesn't merge them
// into @tiptap/core's Storage interface, so `editor.storage.markdown` is
// untyped without this augmentation.
declare module "@tiptap/core" {
  interface Storage {
    markdown: MarkdownStorage;
  }
}

export function buildExtensions(extra: AnyExtension[] = []): AnyExtension[] {
  return [
    // TipTap 3's StarterKit bundles Link (and Underline) itself; disable its
    // copy so our own Link.configure() below doesn't collide with it.
    StarterKit.configure({ link: false }),
    Link.configure({ openOnClick: false }),
    Placeholder.configure({ placeholder: "Type / for blocks…" }),
    TaskList,
    TaskItem.configure({ nested: true }),
    Table.configure({ resizable: false }),
    TableRow,
    TableCell,
    TableHeader,
    Markdown.configure({ html: false, linkify: false, breaks: false }),
    ...extra,
  ];
}

export function toMarkdown(editor: Editor): string {
  return editor.storage.markdown.getMarkdown();
}

// NOTE: prosemirror-markdown's serializer backslash-escapes CommonMark
// punctuation in literal text (e.g. "[" -> "\["), which is visually a no-op
// on render but is NOT safe to normalize away here: the vault's [[wikilink]]
// index (internal/vault/links.go, wikilinkRE) matches literal "[[...]]" and
// will NOT match the escaped "\[\[...\]\]" the editor would write back on
// save. An earlier version of this file unescaped both sides before
// comparing, which made checkFidelity report wikilink notes as safe for
// WYSIWYG — but the first WYSIWYG save of such a note silently breaks every
// [[link]] in it into dead text. Do not add an unescape step back here
// without also fixing (or verifying) the vault-side link matcher.

// tiptap-markdown's TaskItem serializer always renders "loose" (a blank line
// between sibling items), regardless of whether the source was tight — a
// deterministic, unconditional reformatting the library performs on every
// round-trip, not a loss of user content (no text/structure changes, only
// inter-item spacing). Collapse a single blank line between two consecutive
// task-item lines so tight-vs-loose spacing doesn't register as a fidelity
// failure.
const collapseLooseTaskSpacing = (md: string) =>
  md.replace(/^([ \t]*[-*+][ \t]+\[[ xX]\][ \t]+.*)\n\n(?=[ \t]*[-*+][ \t]+\[[ xX]\][ \t]+)/gm, "$1\n");

const normalize = (md: string) =>
  collapseLooseTaskSpacing(
    md
      .replace(/[ \t]+$/gm, "")
      .replace(/\n{3,}/g, "\n\n")
      .trim(),
  );

// fidelityRoundTrip: load md into a headless editor, serialize back.
export function fidelityRoundTrip(md: string): string {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: md,
  });
  const out = toMarkdown(editor);
  editor.destroy();
  return out;
}

export function checkFidelity(md: string): boolean {
  try {
    return normalize(fidelityRoundTrip(md)) === normalize(md);
  } catch {
    return false;
  }
}
