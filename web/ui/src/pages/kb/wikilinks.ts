import { Node, mergeAttributes, nodeInputRule } from "@tiptap/core";

// Loose local shapes for the markdown-it instance tiptap-markdown hands us
// in parse.setup — markdown-it isn't a direct dependency of this project (it
// arrives transitively via tiptap-markdown) and ships no bundled types, so we
// type only the surface we touch rather than importing it for types alone.
interface MdInlineState {
  src: string;
  pos: number;
  posMax: number;
  push(type: string, tag: string, nesting: number): { meta?: Record<string, unknown> };
}
interface MdToken {
  meta?: { target?: string };
}
interface MdInstance {
  inline: { ruler: { before(name: string, ruleName: string, rule: (state: MdInlineState, silent: boolean) => boolean): void } };
  renderer: { rules: Record<string, (tokens: MdToken[], idx: number) => string> };
  utils: { escapeHtml(s: string): string };
}

// Matches `[[target]]` (or `[[target|alias]]`) typed at the cursor. Mirrors
// internal/vault/links.go's wikilinkRE shape (no `]` inside the target) —
// non-greedy, anchored to the end of the typed text so nodeInputRule fires
// as soon as the closing `]]` completes.
export const WIKILINK_RE = /\[\[([^[\]]+)\]\]$/;

// splitAlias pulls a "target|Alias" pair apart for display/navigation only.
// The raw string (alias included, if any) is always what's stored in
// attrs.target and written back out verbatim by the markdown serializer
// below — splitting it permanently here would risk losing/reordering
// content the user typed. See the "why not attrs.alias" note on
// `addAttributes` for why this stays a derived helper instead of a second
// node attribute.
export function splitAlias(raw: string): { target: string; alias: string | null } {
  const i = raw.indexOf("|");
  if (i === -1) return { target: raw, alias: null };
  const alias = raw.slice(i + 1).trim();
  return { target: raw.slice(0, i).trim(), alias: alias || null };
}

// wikilinkInlineRule is a markdown-it inline rule recognizing `[[...]]`
// (no nested `[`), pushed as a single self-closing "wikilink" token whose
// `meta.target` carries the raw inner text. Registered `before("text", …)`
// so it runs ahead of markdown-it's plain-text run collector at every
// position — same pattern tiptap-markdown itself uses for taskList
// (registering markdown-it-task-lists via parse.setup), just hand-rolled
// here since there's no off-the-shelf wikilink plugin already in the tree.
function wikilinkInlineRule(state: MdInlineState, silent: boolean): boolean {
  const { src, pos, posMax } = state;
  if (src.charCodeAt(pos) !== 0x5b /* [ */ || src.charCodeAt(pos + 1) !== 0x5b) return false;

  let end = -1;
  for (let i = pos + 2; i < posMax - 1; i++) {
    const c = src.charCodeAt(i);
    if (c === 0x5b /* [ */) return false; // no nested '[' — bail, let other rules handle it
    if (c === 0x5d /* ] */ && src.charCodeAt(i + 1) === 0x5d) {
      end = i;
      break;
    }
  }
  if (end === -1) return false;

  const target = src.slice(pos + 2, end);
  if (!target) return false;

  if (!silent) {
    const token = state.push("wikilink", "", 0);
    token.meta = { target };
  }
  state.pos = end + 2;
  return true;
}

// Wikilink is a custom TipTap inline atom node representing an Obsidian-style
// [[wikilink]]. It exists to fix a round-trip-safety gap: prosemirror-markdown's
// default text serializer backslash-escapes literal "[" on the way out
// ("[[x]]" -> "\[\[x\]\]"), which internal/vault's wikilinkRE (literal-bracket
// match only) can't see — so a plain-text "[[x]]" is NOT safe to edit in
// WYSIWYG (see editor.ts's long comment + editor.test.ts). Modeling the link
// as its own atom node with a custom `markdown.serialize` sidesteps the
// generic text escaping entirely: this node always writes "[[target]]"
// literally, so a note containing wikilinks now round-trips byte-for-byte
// and can safely reopen in WYSIWYG instead of being forced to raw mode.
//
// Click-to-navigate is deliberately NOT wired into this node (no options, no
// addProseMirrorPlugins/NodeView) — it lives in NoteEditor via the
// `editorProps.handleClickOn` prop TipTap's useEditor already accepts. That
// keeps this file schema-only (parse/serialize/render/input-rule) and
// testable without an onNavigate callback in the loop, and avoids a second
// "wikilink"-named extension instance colliding with the one buildExtensions
// registers by default (extension names must be unique per editor).
export const Wikilink = Node.create({
  name: "wikilink",
  inline: true,
  group: "inline",
  atom: true,

  addAttributes() {
    return {
      // Raw text between "[[" and "]]" (alias included, unparsed) — kept as
      // a single verbatim string, not split into {target, alias} attrs, so
      // the markdown serializer below can write back the exact original
      // bytes with no reconstruction logic that could drift from the input.
      target: {
        default: "",
        parseHTML: (el: HTMLElement) => el.getAttribute("data-target") ?? el.textContent ?? "",
        renderHTML: (attrs: { target: string }) => ({ "data-target": attrs.target }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'span[data-type="wikilink"]' }];
  },

  // Pill display choice: show the alias if the link has one, else the bare
  // target — never the literal "[[...]]" brackets (the pill's shape/color
  // already signals "this is a link", so echoing the markdown syntax inside
  // it would be redundant chrome).
  renderHTML({ node, HTMLAttributes }) {
    const { alias, target } = splitAlias(node.attrs.target as string);
    return [
      "span",
      mergeAttributes(HTMLAttributes, { "data-type": "wikilink", class: "wikilink-pill" }),
      alias ?? target,
    ];
  },

  addStorage() {
    return {
      markdown: {
        // state.write() emits text literally (unlike state.text(), which
        // would re-trigger the same backslash-escaping this node exists to
        // avoid) — this is the load-bearing line for lossless round-trip.
        serialize(state: { write: (s: string) => void }, node: { attrs: { target: string } }) {
          state.write("[[" + node.attrs.target + "]]");
        },
        parse: {
          setup(md: MdInstance) {
            md.inline.ruler.before("text", "wikilink", wikilinkInlineRule);
            md.renderer.rules.wikilink = (tokens, idx) => {
              const target = tokens[idx].meta?.target ?? "";
              const escaped = md.utils.escapeHtml(target);
              return `<span data-type="wikilink" data-target="${escaped}">${escaped}</span>`;
            };
          },
        },
      },
    };
  },

  addInputRules() {
    return [
      nodeInputRule({
        find: WIKILINK_RE,
        type: this.type,
        getAttributes: (match) => ({ target: match[1] }),
      }),
    ];
  },
});
