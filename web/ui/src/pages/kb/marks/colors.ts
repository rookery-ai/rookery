import { Mark, mergeAttributes } from "@tiptap/core";

// Fixed palette, no picker. Stored as an inline `style` attribute — the same
// mechanism Obsidian itself uses for inline colour, so a coloured note renders
// correctly there. That does NOT extend to this app's own HTML/PDF/DOCX
// export: internal/export/markdown.go and web/handlers_kb.go both build
// goldmark WITHOUT html.WithUnsafe (deliberately, so a note can never inject
// a <script>), which means raw HTML — this mark's `<span style="color:…">`,
// KBBgColor's highlight span, and the toggle's `<details>` — is dropped
// rather than rendered on every one of those paths. The trade-off is real,
// not just theoretical: portable to Obsidian, silently lost on export.
//
// Contrast is the cost of that choice and is stated honestly: no single hex
// reaches WCAG AA body text (4.5:1) against BOTH #ffffff and #191919. The best
// any candidate in the Tailwind mid-range manages is 4.15 (violet-500). These
// eight floor at 3.53:1 — AA for large text and for non-text contrast.
// kbPalette.test.ts computes and pins that floor.
export const TEXT_COLORS = [
  { name: "red", hex: "#ef4444" },      // 3.76 light / 4.67 dark
  { name: "orange", hex: "#ea580c" },   // 3.56 / 4.94
  { name: "green", hex: "#059669" },    // 3.77 / 4.67
  { name: "teal", hex: "#0d9488" },     // 3.74 / 4.70
  { name: "blue", hex: "#3b82f6" },     // 3.68 / 4.78
  { name: "violet", hex: "#8b5cf6" },   // 4.23 / 4.15
  { name: "pink", hex: "#ec4899" },     // 3.53 / 4.98  ← the floor
  { name: "grey", hex: "#71717a" },     // 4.83 / 3.64
];

// Pale tints. Each is paired with HIGHLIGHT_FG in the SAME span, which is what
// keeps it legible on the dark theme — an inherited near-white --foreground on
// a pale yellow is unreadable. Every pair measures >= 12.2:1.
export const HIGHLIGHT_COLORS = [
  { name: "yellow", hex: "#fef08a" },
  { name: "orange", hex: "#fed7aa" },
  { name: "red", hex: "#fecaca" },
  { name: "green", hex: "#bbf7d0" },
  { name: "teal", hex: "#99f6e4" },
  { name: "blue", hex: "#bfdbfe" },
  { name: "purple", hex: "#e9d5ff" },
  { name: "pink", hex: "#fbcfe8" },
];

export const HIGHLIGHT_FG = "#18181b";

// The DOM normalizes a style colour to rgb() on read, so a mark parsed straight
// out of el.style would serialize back as "rgb(239, 68, 68)" and no longer
// match the "#ef4444" the file carried — checkFidelity would fail and the note
// would open read-only. Normalizing both directions to lowercase hex is what
// makes the round trip exact.
export function toHex(css: string): string | null {
  if (!css) return null;
  const rgb = css.match(/^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i);
  if (rgb) {
    return (
      "#" +
      [rgb[1], rgb[2], rgb[3]]
        .map((n) => Number(n).toString(16).padStart(2, "0"))
        .join("")
    );
  }
  const hex = css.trim().match(/^#([0-9a-f]{6})$/i);
  if (hex) return "#" + hex[1].toLowerCase();
  const short = css.trim().match(/^#([0-9a-f]{3})$/i);
  if (short) {
    return "#" + short[1].toLowerCase().split("").map((c) => c + c).join("");
  }
  return null;
}

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbColors: {
      setKBTextColor: (hex: string) => ReturnType;
      unsetKBTextColor: () => ReturnType;
      setKBBgColor: (hex: string) => ReturnType;
      unsetKBBgColor: () => ReturnType;
    };
  }
}

export const KBTextColor = Mark.create({
  name: "kbTextColor",

  addAttributes() {
    return {
      color: {
        default: null,
        parseHTML: (el) => toHex((el as HTMLElement).style.color),
        renderHTML: (attrs) =>
          attrs.color ? { style: `color:${attrs.color}` } : {},
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: "span[style]",
        getAttrs: (el) => {
          const style = (el as HTMLElement).style;
          // A highlight span also carries a `color` (the pinned foreground).
          // Claiming it here would parse one span as both marks and duplicate
          // it on the way back out.
          if (style.backgroundColor) return false;
          const color = toHex(style.color);
          return color ? { color } : false;
        },
      },
    ];
  },

  // Returning a real DOM node (not a `["span", attrs, 0]` spec array) is
  // deliberate: prosemirror-model's renderSpec special-cases an attrs object
  // key literally named "style" by assigning it via `dom.style.cssText = …`
  // instead of `dom.setAttribute("style", …)`. cssText assignment round-trips
  // through the CSSOM, which canonicalizes any recognized colour into
  // rgb(...) — so "#ef4444" comes back as "rgb(239, 68, 68)" the moment this
  // mark is serialized, independent of parsing. Building the element
  // ourselves and calling setAttribute directly keeps the literal hex string
  // the note was authored with.
  renderHTML({ HTMLAttributes }) {
    const el = document.createElement("span");
    for (const [key, value] of Object.entries(mergeAttributes(HTMLAttributes))) {
      if (value != null) el.setAttribute(key, String(value));
    }
    return { dom: el, contentDOM: el };
  },

  addCommands() {
    return {
      setKBTextColor:
        (hex: string) =>
        ({ commands }) =>
          commands.setMark(this.name, { color: hex }),
      unsetKBTextColor:
        () =>
        ({ commands }) =>
          commands.unsetMark(this.name),
    };
  },
});

export const KBBgColor = Mark.create({
  name: "kbBgColor",

  addAttributes() {
    return {
      bg: {
        default: null,
        parseHTML: (el) => toHex((el as HTMLElement).style.backgroundColor),
        // The foreground rides along in the SAME declaration on purpose — see
        // HIGHLIGHT_FG above.
        renderHTML: (attrs) =>
          attrs.bg
            ? { style: `background-color:${attrs.bg};color:${HIGHLIGHT_FG}` }
            : {},
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: "span[style]",
        getAttrs: (el) => {
          const bg = toHex((el as HTMLElement).style.backgroundColor);
          return bg ? { bg } : false;
        },
      },
    ];
  },

  // See KBTextColor.renderHTML above for why this builds a real DOM node
  // instead of returning a `["span", attrs, 0]` spec array: prosemirror-model
  // assigns an attrs key named "style" via `dom.style.cssText = …`, which
  // canonicalizes both `background-color` and the pinned `color` into rgb()
  // on serialization. Setting the attribute directly preserves the hex.
  renderHTML({ HTMLAttributes }) {
    const el = document.createElement("span");
    for (const [key, value] of Object.entries(mergeAttributes(HTMLAttributes))) {
      if (value != null) el.setAttribute(key, String(value));
    }
    return { dom: el, contentDOM: el };
  },

  addCommands() {
    return {
      setKBBgColor:
        (hex: string) =>
        ({ commands }) =>
          commands.setMark(this.name, { bg: hex }),
      unsetKBBgColor:
        () =>
        ({ commands }) =>
          commands.unsetMark(this.name),
    };
  },
});
