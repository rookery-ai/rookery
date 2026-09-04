import { Editor, type AnyExtension } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import Link from "@tiptap/extension-link";
import Placeholder from "@tiptap/extension-placeholder";
import TaskList from "@tiptap/extension-task-list";
import TaskItem from "@tiptap/extension-task-item";
import TableRow from "@tiptap/extension-table-row";
import TableCell from "@tiptap/extension-table-cell";
import TableHeader from "@tiptap/extension-table-header";
import { Markdown, type MarkdownStorage } from "tiptap-markdown";
import { Wikilink } from "./wikilinks";
import { PipeSafeTable } from "./pipeSafeTable";
import { KBImage } from "./kbImage";
import { KBTextColor, KBBgColor } from "./marks/colors";
import { Callout } from "./nodes/callout";
import { Toggle, ToggleSummary } from "./nodes/toggle";
import { KBAlign } from "./nodes/align";
import { KBColumns } from "./nodes/columns";

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
    // openOnClick stays false so TipTap's own handler doesn't fire — NoteEditor's
    // editorProps.handleClick owns link opening instead, so a plain click on a
    // link (external OR a vault-relative attachment path) opens it in a new tab.
    // target/rel make an opened link safe.
    Link.configure({
      openOnClick: false,
      HTMLAttributes: { target: "_blank", rel: "noopener nofollow", title: "Click to open" },
    }),
    KBImage,
    // The defaults are load-bearing and deliberately not overridden.
    // showOnlyCurrent:true + includeChildren:false send the plugin down its
    // `useResolvedPath` branch, which decorates `resolved.node(1)` — the
    // TOP-LEVEL block holding the caret. So the hint follows the caret onto any
    // blank line, and an empty paragraph nested inside a callout, toggle,
    // column cell, blockquote or list item gets nothing, because node(1) there
    // is the wrapper and a wrapper is not a textblock. includeChildren:true
    // would reach those, and would also fire in every empty table cell and
    // toggle summary, which is noise on exactly the notes that have the most
    // of them.
    Placeholder.configure({ placeholder: "Press / for commands…" }),
    TaskList,
    TaskItem.configure({ nested: true }),
    PipeSafeTable.configure({ resizable: false }),
    TableRow,
    TableCell,
    TableHeader,
    // html:true lets inline HTML an agent or a document conversion leaves in a
    // note (a stray <br>, <sub>, <div>, or comment) render as its nearest
    // markdown/text instead of showing up as escaped "&lt;br&gt;" garbage. It is
    // safe for the fidelity contract: a note with NO html tags serializes
    // identically either way (verified — plain prose, wikilinks, and tables are
    // byte-for-byte unchanged), and a literal "a < b" in prose is still escaped
    // to "a &lt; b" exactly as before (markdown-it only treats "<" as markup
    // when it forms a real tag). So no note that opens in rich text today changes
    // behavior; only html-bearing notes (which already open raw) render better
    // once viewed as rich text.
    Markdown.configure({ html: true, linkify: false, breaks: false }),
    Wikilink,
    // Registration order sets mark rank, which sets DOM nesting order on
    // serialize: the lower-rank (earlier) mark renders as the OUTER span, the
    // higher-rank (later) mark as the INNER one. KBBgColor must come first so
    // KBTextColor is innermost — an element's own `color` isn't inherited,
    // it's applied directly, so whichever span sits closest to the text wins.
    // With KBTextColor innermost, a text colour applied inside a highlight
    // overrides the highlight's pinned foreground; a highlight with no text
    // colour still renders its own pinned foreground since nothing is nested
    // inside it to override it.
    KBBgColor,
    Callout,
    ToggleSummary,
    Toggle,
    KBAlign,
    KBColumns,
    KBTextColor,
    ...extra,
  ];
}

export function toMarkdown(editor: Editor): string {
  return editor.storage.markdown.getMarkdown();
}

// NOTE: prosemirror-markdown's serializer backslash-escapes CommonMark
// punctuation in literal text (e.g. "[" -> "\["), which is visually a no-op
// on render but is NOT safe to normalize away here in general: the vault's
// [[wikilink]] index (internal/vault/links.go, wikilinkRE) matches literal
// "[[...]]" and will NOT match an escaped "\[\[...\]\]". An earlier version
// of this file unescaped both sides before comparing to paper over exactly
// this for wikilinks, which made checkFidelity falsely report them as
// WYSIWYG-safe — the first WYSIWYG save of such a note silently broke every
// [[link]] in it into dead text. Do NOT add a blanket unescape step back
// here to "fix" some other punctuation-escaping mismatch without checking
// it can't paper over a real, vault-meaningful loss the way that did.
//
// Wikilinks specifically are now fixed correctly (not by normalizing this
// comparison, but by giving [[wikilink]] its own atom node — see
// wikilinks.ts — whose custom `markdown.serialize` writes the brackets back
// literally via `state.write`, never going through the escaping text
// serializer at all). See wikilinks.ts's top comment and editor.test.ts for
// the fidelity contract this establishes.

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
