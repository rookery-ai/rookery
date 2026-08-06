import Image from "@tiptap/extension-image";
import { splitAltWidth, joinAltWidth, clampImageWidth } from "./imageResize";

// KB images are stored in the vault as raw asset files and referenced from a
// note's markdown by a PORTABLE vault-relative path — `![alt](assets/foo.png)`
// — so the markdown stays meaningful on disk and to agents, not tied to a URL.
// But the browser needs a real URL to display them, served by /api/v1/kb/raw.
//
// This extension keeps `node.attrs.src` as the portable path (so tiptap-markdown
// serializes `![](assets/foo.png)` unchanged, and the fidelity round-trip is
// unaffected) while RENDERING an <img> whose src points at the served URL. On
// parse it reverses a served URL back to the bare path, so a pasted/rendered
// served-URL still round-trips to a portable path.

// A src is "external" (used as-is) when it's an absolute URL or data URI; any
// other value is treated as a vault-relative asset path.
function isExternalSrc(src: string): boolean {
  return /^(https?:|data:|blob:)/i.test(src);
}

// The served URL a vault-relative path displays through.
export function assetDisplayURL(src: string): string {
  if (isExternalSrc(src)) return src;
  return `/api/v1/kb/raw?path=${encodeURIComponent(src)}`;
}

// Reverse of assetDisplayURL: turn a served URL back into the stored vault path
// so the markdown stays portable even if the editor ever sees the served form.
export function vaultPathFromSrc(src: string | null): string | null {
  if (!src) return src;
  const m = src.match(/^\/api\/v1\/kb\/raw\?path=([^&]+)/);
  if (m) {
    try {
      return decodeURIComponent(m[1]);
    } catch {
      return src;
    }
  }
  return src;
}

export const KBImage = Image.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      src: {
        default: null,
        parseHTML: (el) => vaultPathFromSrc(el.getAttribute("src")),
        // The stored attr value stays the portable path; only the rendered DOM
        // attribute is rewritten to the served URL.
        renderHTML: (attrs) => (attrs.src ? { src: assetDisplayURL(attrs.src as string) } : {}),
      },
      // The alt slot carries the width in Obsidian's ![alt|420](src) form, so
      // the ATTRIBUTE holds only the real alt text and the width is its own
      // attribute. Splitting happens on parse, rejoining on serialize.
      alt: {
        default: null,
        parseHTML: (el) => splitAltWidth(el.getAttribute("alt") || "").alt || null,
        renderHTML: (attrs) => (attrs.alt ? { alt: attrs.alt as string } : {}),
      },
      width: {
        default: null,
        parseHTML: (el) => splitAltWidth(el.getAttribute("alt") || "").width,
        // Only emit the `width` attribute here, not `style`: ProseMirror's
        // renderSpec normalizes an attrs key literally named "style" via
        // dom.style.cssText, which stomps whatever the NodeView sets on the
        // element directly. The NodeView (below) is the one that actually
        // applies the pixel width as inline style; this renderHTML path is
        // only exercised by the non-NodeView (e.g. export/serialize-to-HTML)
        // rendering.
        renderHTML: (attrs) => (attrs.width ? { width: String(attrs.width) } : {}),
      },
    };
  },

  addStorage() {
    return {
      markdown: {
        // tiptap-markdown's own image serializer writes ![alt](src) and knows
        // nothing about the width, so it is overridden here rather than
        // letting the width silently drop on every save. Mirrors
        // prosemirror-markdown's stock `image` serializer spec exactly (down
        // to escaping parens in the destination and quotes in the title) —
        // dropping those escapes would corrupt any src/title containing them
        // on the SAVE path, which checkFidelity's load-time gate can't catch.
        serialize(state: any, node: any) {
          const label = joinAltWidth(node.attrs.alt || "", node.attrs.width ?? null);
          const src = String(node.attrs.src ?? "").replace(/[()]/g, "\\$&");
          const title = node.attrs.title
            ? ` "${String(node.attrs.title).replace(/"/g, '\\"')}"`
            : "";
          state.write(`![${state.esc(label)}](${src}${title})`);
          // This node is BLOCK-level (@tiptap/extension-image's default
          // `inline: false` → group "block"), and prosemirror-markdown's
          // MarkdownSerializerState tracks block separation via a
          // write()/closeBlock() pair: closeBlock() marks this node as the
          // last-written block so the NEXT block's own write() flushes a
          // blank-line separator before it (state.flushClose, called from
          // write()). Every other block-level custom serializer in this file
          // set (code-block.js, html.js, table.js, toggle.ts) calls
          // closeBlock(); this one didn't, so "closed" was never set, the
          // next block never flushed a separator, and a block image followed
          // by ANY other block ("![a](x.png)\n\nText.\n") round-tripped to
          // "![a](x.png)Text." — silently glued together. Confirmed via
          // corpus.test.ts and editor.test.ts's image fixity cases: adding
          // this one call turns "image + trailing block" from lossy
          // (read-only note) into a fixed point.
          state.closeBlock(node);
        },
        parse: {
          // handled by markdown-it + parseHTML above
        },
      },
    };
  },

  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = document.createElement("span");
      dom.className = "kb-image";

      const img = document.createElement("img");
      img.src = assetDisplayURL(node.attrs.src as string);
      if (node.attrs.alt) img.alt = node.attrs.alt as string;
      if (node.attrs.width) img.style.width = `${node.attrs.width}px`;
      dom.appendChild(img);

      const handle = document.createElement("span");
      handle.className = "kb-image-handle";
      handle.setAttribute("aria-hidden", "true");
      dom.appendChild(handle);

      handle.addEventListener("pointerdown", (event) => {
        // A read-only note (failed checkFidelity, not yet opted into editing —
        // see NoteEditor's READONLY_BANNER) still mounts this NodeView, so the
        // handle must refuse to start a drag rather than rely on the CSS hover
        // reveal alone (defense in depth: see editor.css's matching gate on
        // `.kb-image:hover .kb-image-handle`, and NoteEditor's markDirty/flush
        // guard for the third layer).
        if (!editor.isEditable) return;
        event.preventDefault();
        const startX = event.clientX;
        const startWidth = img.getBoundingClientRect().width;
        const column = (dom.parentElement?.getBoundingClientRect().width ?? startWidth);

        const onMove = (e: PointerEvent) => {
          const next = clampImageWidth(startWidth + (e.clientX - startX), column);
          img.style.width = `${next}px`;
        };
        const onUp = () => {
          window.removeEventListener("pointermove", onMove);
          window.removeEventListener("pointerup", onUp);
          const final = parseInt(img.style.width, 10);
          const pos = typeof getPos === "function" ? getPos() : undefined;
          if (typeof pos === "number" && !Number.isNaN(final)) {
            editor.view.dispatch(
              editor.view.state.tr.setNodeMarkup(pos, undefined, {
                ...node.attrs,
                width: final,
              }),
            );
          }
        };
        window.addEventListener("pointermove", onMove);
        window.addEventListener("pointerup", onUp);
      });

      return { dom };
    };
  },
});
