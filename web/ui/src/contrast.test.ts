/// <reference types="node" />
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

// index.css records that an earlier review measured --ok at 3.68:1 against its
// own -soft fill, and that --ok/--warn/--danger were darkened to fix it. That
// finding was a one-off manual check, so nothing stopped the next palette edit
// from undoing it. This file turns the check into a gate: the ratios are
// computed from the stylesheet itself, in both themes, on every run.
const cssPath = path.join(path.dirname(fileURLToPath(import.meta.url)), "index.css");
const css = readFileSync(cssPath, "utf8");

function channel(c: number): number {
  const s = c / 255;
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}

function luminance(hex: string): number {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

export function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((m, n) => n - m);
  return (hi + 0.05) / (lo + 0.05);
}

// Reads a token from one specific block so both themes can be checked from the
// single stylesheet. Slices to the block's closing brace so a later block's
// definition of the same token cannot be picked up by mistake.
function token(block: ":root" | ".dark", name: string): string {
  const start = css.indexOf(block + " {");
  if (start === -1) throw new Error(`block ${block} not found`);
  const body = css.slice(start, css.indexOf("\n}", start));
  const m = body.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`));
  if (!m) throw new Error(`token ${name} not found in ${block}`);
  return m[1];
}

describe.each([":root", ".dark"] as const)("contrast in %s", (block) => {
  const bg = token(block, "--background");
  const chrome = token(block, "--chrome");

  // Borders are non-text UI. WCAG 1.4.11 asks 3:1 for meaningful boundaries,
  // but a hairline card outline is decorative separation rather than the sole
  // carrier of information, so the floor here is "visibly darker than the
  // surface" — which is precisely what darkening --border bought.
  test("borders are visibly darker than the surfaces they sit on", () => {
    expect(ratio(token(block, "--border"), bg)).toBeGreaterThanOrEqual(1.25);
    expect(ratio(token(block, "--border-strong"), bg)).toBeGreaterThanOrEqual(1.6);
    expect(ratio(token(block, "--border-strong"), chrome)).toBeGreaterThanOrEqual(1.45);
  });

  test("--border-strong is a real step up from --border", () => {
    // If these converge, the "one step up" token is decorative and the
    // follow-up option the spec reserves (cards → border-strong) is worthless.
    expect(ratio(token(block, "--border-strong"), bg)).toBeGreaterThan(
      ratio(token(block, "--border"), bg),
    );
  });

  // The regression guard proper: status colours must hold 4.5:1 as text on all
  // three grounds they actually appear on — including their own -soft fill,
  // which is the tightest constraint and the one that slipped last time.
  test.each(["ok", "warn", "danger"])("--%s holds 4.5:1 on all three grounds", (name) => {
    const fg = token(block, `--${name}`);
    expect(ratio(fg, bg)).toBeGreaterThanOrEqual(4.5);
    expect(ratio(fg, chrome)).toBeGreaterThanOrEqual(4.5);
    expect(ratio(fg, token(block, `--${name}-soft`))).toBeGreaterThanOrEqual(4.5);
  });

  test.each(["--foreground", "--muted", "--muted-2"])(
    "%s holds 4.5:1 on background and chrome",
    (name) => {
      expect(ratio(token(block, name), bg)).toBeGreaterThanOrEqual(4.5);
      expect(ratio(token(block, name), chrome)).toBeGreaterThanOrEqual(4.5);
    },
  );
});
