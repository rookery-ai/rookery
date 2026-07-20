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
