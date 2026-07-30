/// <reference types="node" />
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

// NOTE: `readFileSync(new URL("./index.css", import.meta.url))` — the literal
// pattern from the task brief — is intercepted by Vite's static asset-URL
// transform (it rewrites `new URL(relative, import.meta.url)` into a public
// asset URL, even under vitest/jsdom), so it resolves to an http: URL instead
// of the file on disk. Building the path via fileURLToPath sidesteps that
// transform while reading the exact same file.
const cssPath = path.join(path.dirname(fileURLToPath(import.meta.url)), "index.css");
const css = readFileSync(cssPath, "utf8");

test("reduced-motion rule exists and disables animation", () => {
  expect(css).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)/);
  expect(css).toMatch(/animation-duration:\s*0\.01ms/);
});

const SCALE = ["xs", "sm", "base", "lg", "xl", "2xl"] as const;

test("Inter is declared as a self-hosted @font-face and wired to --font-sans", () => {
  expect(css).toMatch(/@font-face\s*\{[\s\S]*?InterVariable\.woff2/);
  expect(css).toMatch(/font-weight:\s*100 900/);
  expect(css).toMatch(/--font-sans:\s*"InterVariable"/);
  // body must inherit the token, not carry its own hardcoded stack.
  expect(css).toMatch(/font-family:\s*var\(--font-sans\)/);
});

test("no external font is fetched — offline/LAN installs must work", () => {
  expect(css).not.toMatch(/fonts\.googleapis\.com|fonts\.gstatic\.com/);
});

test("every --text-* token has a matching line-height token", () => {
  // Tailwind v4 pairs each --text-X with --text-X--line-height. Setting only
  // the size leaves line-height pinned to the OLD metric, which makes text
  // cramped rather than more readable — the opposite of the goal.
  for (const k of SCALE) {
    expect(css).toMatch(new RegExp(`--text-${k}:\\s*[\\d.]+rem`));
    expect(css).toMatch(new RegExp(`--text-${k}--line-height:\\s*[\\d.]+rem`));
  }
});

test("text-xs is 13px and text-sm is 15px", () => {
  expect(css).toMatch(/--text-xs:\s*0\.8125rem/);
  expect(css).toMatch(/--text-sm:\s*0\.9375rem/);
});

test("interactive controls get a pointer cursor back", () => {
  // Tailwind v4's Preflight dropped `button { cursor: pointer }` to match the
  // browser default — and a <button>'s browser default is `cursor: default`.
  // Without this rule, 54 raw <button> elements across the app hovered as if
  // they were inert text, which is how "the KB search results are not
  // clickable" was reported: FileTree's rows set cursor-pointer explicitly, so
  // the tree felt interactive and the results in the same pane did not.
  expect(css).toMatch(/button:not\(:disabled\)/);
  expect(css).toMatch(/button:disabled\s*\{\s*cursor:\s*not-allowed/);
});
