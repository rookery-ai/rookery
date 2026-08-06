import { TEXT_COLORS, HIGHLIGHT_COLORS, HIGHLIGHT_FG, toHex } from "./colors";

// Computed from the values, never trusted — the same approach contrast.test.ts
// takes for the design tokens. A palette edit that drops below these floors
// fails the build instead of shipping unreadable text.
const LIGHT_BG = "#ffffff";
const DARK_BG = "#191919";

function lin(c: number) {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}
function luminance(hex: string) {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}
function ratio(a: string, b: string) {
  const [la, lb] = [luminance(a), luminance(b)];
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

test("every text colour holds 3.5:1 in BOTH themes", () => {
  // 3.5 is the honest floor, not 4.5. A single fixed hex cannot reach AA body
  // text against both #ffffff and #191919 — the best any candidate manages is
  // 4.15 — and a fixed hex is what makes the note portable to Obsidian.
  for (const { name, hex } of TEXT_COLORS) {
    const light = ratio(hex, LIGHT_BG);
    const dark = ratio(hex, DARK_BG);
    expect(light, `${name} (${hex}) light-bg ratio ${light.toFixed(2)} < 3.5`).toBeGreaterThanOrEqual(3.5);
    expect(dark, `${name} (${hex}) dark-bg ratio ${dark.toFixed(2)} < 3.5`).toBeGreaterThanOrEqual(3.5);
  }
});

test("every highlight tint carries legible text in both themes", () => {
  // The pinned foreground is why this works at all: the pair is
  // self-contained and never inherits the page's --foreground.
  for (const { name, hex } of HIGHLIGHT_COLORS) {
    const r = ratio(hex, HIGHLIGHT_FG);
    expect(r, `${name} (${hex}) vs ${HIGHLIGHT_FG} ratio ${r.toFixed(2)} < 4.5`).toBeGreaterThanOrEqual(4.5);
  }
});

test("no yellow or amber in the text set", () => {
  // Both measure under 3.2 on white. They belong as backgrounds only.
  expect(TEXT_COLORS.map((c) => c.name)).not.toContain("yellow");
  expect(TEXT_COLORS.map((c) => c.name)).not.toContain("amber");
});

test("toHex normalizes what the DOM gives back", () => {
  // Reading el.style.color returns a normalized rgb() string, NOT the hex the
  // markdown carried. Without this the round trip would rewrite every
  // "#ef4444" as "rgb(239, 68, 68)" and fail checkFidelity, dropping the note
  // into a read-only view.
  expect(toHex("rgb(239, 68, 68)")).toBe("#ef4444");
  expect(toHex("#EF4444")).toBe("#ef4444");
  expect(toHex("#ef4444")).toBe("#ef4444");
  expect(toHex("")).toBe(null);
  expect(toHex("not-a-colour")).toBe(null);
});
