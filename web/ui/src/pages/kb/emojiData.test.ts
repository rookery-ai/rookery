import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { emojiGroups, filterEmojis } from "./emojiData";

// See styles.test.ts: Vite's asset-URL transform rewrites
// new URL(relative, import.meta.url) into an http: URL even under vitest, so
// paths are built through fileURLToPath instead.
const here = path.dirname(fileURLToPath(import.meta.url));
const uiRoot = path.resolve(here, "../../..");

test("the set covers the standard Unicode groups at full size", () => {
  // The picker used to ship a hand-curated ~160-emoji table.
  expect(emojiGroups).toHaveLength(9);
  const total = emojiGroups.reduce((n, g) => n + g.emojis.length, 0);
  expect(total).toBeGreaterThan(1500);
});

test("every entry carries search keywords", () => {
  // Keywords are the whole point of the generator: without them the only way
  // to find an emoji is to recognise its glyph in a grid of 1906.
  for (const g of emojiGroups) {
    for (const e of g.emojis) {
      expect(e.keywords.length, `${e.emoji} in ${g.name} has no keywords`).toBeGreaterThan(0);
    }
  }
});

test("search matches on keyword, not just the glyph", () => {
  const hits = filterEmojis("book");
  expect(hits.length).toBeGreaterThan(3);
  expect(hits.some((h) => h.emoji === "📚")).toBe(true);
});

test("search returns nothing for a non-word rather than everything", () => {
  expect(filterEmojis("zzzznotathing")).toEqual([]);
});

test("search finds an emoji by a word only its slug supplies", () => {
  // "thumbs up" comes from the slug's underscore split; the display name alone
  // would not match a two-word query typed this way.
  expect(filterEmojis("thumbs up").length).toBeGreaterThan(0);
});

test("the committed generated file matches the generator's output", () => {
  // The generated file is committed so the release build never runs the
  // generator. That only stays safe if a stale commit fails CI rather than
  // silently shipping an out-of-date set.
  const fresh = execFileSync("node", ["scripts/gen-emoji.mjs", "--stdout"], {
    cwd: uiRoot,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  const onDisk = readFileSync(path.join(here, "emojiData.generated.ts"), "utf8");
  expect(onDisk.trim()).toBe(fresh.trim());
});
