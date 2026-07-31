/// <reference types="node" />
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.dirname(fileURLToPath(import.meta.url));

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((e) => {
    const p = path.join(dir, e);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(e) && !/\.test\.tsx?$/.test(e) ? [p] : [];
  });
}

// Arbitrary pixel font sizes are how the type scale drifted in the first
// place: they are immune to the --text-* token remap, so they stay small
// forever while everything around them grows. All 39 former uses were
// micro-labels (uppercase section headings, stat-tile captions, the "+N more"
// hint) that read correctly at the new text-xs (13px).
test("no source file hardcodes a pixel font size", () => {
  const offenders = sources(root)
    .map((f) => [path.relative(root, f), readFileSync(f, "utf8")] as const)
    .filter(([, src]) => /text-\[\d+px\]/.test(src))
    .map(([f]) => f);
  expect(offenders).toEqual([]);
});
