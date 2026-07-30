#!/usr/bin/env node
// Generates src/pages/kb/emojiData.generated.ts from the vendored Unicode
// emoji data.
//
// Why a generator rather than a dependency: the KB icon picker used a
// hand-curated ~160-emoji table, which is far too small, but emoji-mart is a
// ~200 KB RUNTIME dependency in an SPA that ships embedded in a single binary,
// and it brings its own styling to theme. Generating at build time gets the full
// set with zero runtime cost and no new package.
//
// The source (unicode-emoji-json's data-by-group.json) is VENDORED beside this
// script rather than fetched, so `npm ci && vite build` needs no network.
//
// The output is COMMITTED, so the release build does not run this script at all.
// emojiData.test.ts re-runs the generator and compares, which is what makes a
// stale commit fail CI instead of silently shipping an old set.
//
// Usage:
//   node scripts/gen-emoji.mjs            # write the file
//   node scripts/gen-emoji.mjs --stdout   # print it (used by the test)

import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.join(here, "emoji-source.json");
const OUT = path.join(here, "../src/pages/kb/emojiData.generated.ts");

// Search keywords come from the emoji's name plus its slug. The slug adds the
// word-split form of names that read as one token ("thumbsup" → "thumbs up"),
// and de-duplicating keeps the table small — this data is shipped to the
// browser, so every repeated word costs bytes on every page load.
function keywordsFor(entry) {
  const words = `${entry.name} ${entry.slug.replace(/_/g, " ")}`
    .toLowerCase()
    .split(/[^a-z0-9+]+/)
    .filter(Boolean);
  return [...new Set(words)].join(" ");
}

function generate() {
  const groups = JSON.parse(readFileSync(SOURCE, "utf8"));

  const body = groups
    .map((g) => {
      const entries = g.emojis
        .map((e) => `    { emoji: ${JSON.stringify(e.emoji)}, keywords: ${JSON.stringify(keywordsFor(e))} },`)
        .join("\n");
      return `  {\n    name: ${JSON.stringify(g.name)},\n    emojis: [\n${entries}\n    ],\n  },`;
    })
    .join("\n");

  const total = groups.reduce((n, g) => n + g.emojis.length, 0);

  return `// GENERATED FILE — DO NOT EDIT BY HAND.
//
// Regenerate with:  node scripts/gen-emoji.mjs
//
// Source: scripts/emoji-source.json (unicode-emoji-json data-by-group.json),
// vendored so the build needs no network. ${total} emoji across the ${groups.length}
// standard Unicode groups, with search keywords derived from each emoji's name
// and slug.
//
// This file is committed on purpose: the release build must not have to run the
// generator. emojiData.test.ts re-runs it and compares against this file, so a
// stale commit fails CI rather than shipping an out-of-date set.
import type { EmojiGroup } from "./emojiData";

export const generatedEmojiGroups: EmojiGroup[] = [
${body}
];
`;
}

const out = generate();
if (process.argv.includes("--stdout")) {
  process.stdout.write(out);
} else {
  writeFileSync(OUT, out);
  process.stderr.write(`wrote ${path.relative(process.cwd(), OUT)}\n`);
}
