import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";

const SRC = __dirname;

function sources(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) sources(p, out);
    // Test files are excluded, this one included: it necessarily spells out
    // every banned class in order to ban it.
    else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) out.push(p);
  }
  return out;
}

// Tailwind generates a utility only for a token that exists. `--color-line` and
// `--color-warning` never did — the real tokens are `--color-border` and
// `--color-warn` — so these class names emitted NO CSS at all.
//
// That is not cosmetic drift. Tailwind v4's Preflight sets `border: 0 solid`
// globally, so `border border-line` produced a border of width ZERO: the backup
// section's four <select>s rendered with no border on a transparent background.
// Worse, `bg-warning-soft text-warning` on the pending-restore banner — the most
// alarming state in the app — rendered as transparent plain body text.
//
// A misspelt token is invisible in review and in the running app, which is
// exactly why it survived; only a guard like this catches it.
describe("colour tokens actually exist", () => {
  const files = sources(SRC);
  const banned = [
    { cls: "border-line", use: "border-border" },
    { cls: "divide-line", use: "divide-border" },
    { cls: "text-warning", use: "text-warn" },
    { cls: "bg-warning-soft", use: "bg-warn-soft" },
    { cls: "border-warning", use: "border-warn" },
  ];

  for (const { cls, use } of banned) {
    test(`no source uses the non-existent "${cls}" (use "${use}")`, () => {
      const re = new RegExp(`\\b${cls}\\b`);
      const offenders = files
        .filter((f) => re.test(readFileSync(f, "utf8")))
        .map((f) => f.replace(SRC, ""));
      expect(offenders).toEqual([]);
    });
  }
});
