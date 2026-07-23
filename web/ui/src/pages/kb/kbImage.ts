import Image from "@tiptap/extension-image";

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
    };
  },
});
