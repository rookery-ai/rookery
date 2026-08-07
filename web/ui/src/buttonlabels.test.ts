import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";

const SRC = join(__dirname);

function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) {
      sourceFiles(p, out);
    } else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(p);
    }
  }
  return out;
}

// Every action button carries a leading lucide icon (see components/ui/button.tsx).
// An emoji baked into the label string therefore renders a SECOND icon beside it
// — "🔨 Build it" next to <Hammer/>, "Continue →" next to <ArrowRight/>. The same
// class of bug was already cleaned out of SettingsPage's section nav, so this
// guards the whole app rather than the three sites that were reported.
describe("button labels carry no glyph of their own", () => {
  const files = sourceFiles(SRC);

  test("no label string starts with an emoji", () => {
    const offenders: string[] = [];
    // Matches a quoted string opening with one of the glyphs that were used as
    // pseudo-icons. Deliberately narrow: emoji appear legitimately in prose,
    // status lines and chat protocol markers.
    const re = /(?:buildButton|saveButton|label|title)\s*:\s*"(?:🔨|✅|🚀|⚙️|🔑)/g;
    for (const f of files) {
      const src = readFileSync(f, "utf8");
      if (re.test(src)) offenders.push(f.replace(SRC, ""));
      re.lastIndex = 0;
    }
    expect(offenders).toEqual([]);
  });

  test("no button label ends with a text arrow", () => {
    const offenders: string[] = [];
    // A trailing "→" duplicates the lucide ArrowRight the button already has.
    // Requires a letter before the arrow so a bare "→" used as a separator in
    // a code comment (toggle.ts explains a round-trip with one) is not a hit.
    const re = /["`][^"`\n]*[A-Za-z][^"`\n]*\s→\s*["`]/g;
    for (const f of files) {
      const src = readFileSync(f, "utf8");
      const hits = src.match(re);
      if (hits) offenders.push(`${f.replace(SRC, "")}: ${hits.join(", ")}`);
    }
    expect(offenders).toEqual([]);
  });
});
