import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { checkFidelity, fidelityRoundTrip } from "./editor";

// The other half of internal/convert's fidelity corpus. See the long comment on
// TestFidelityCorpus (internal/convert/fidelity_test.go) for why this is split
// across two languages; briefly: the editor's round-trip check is the thing that
// decides whether an imported note is editable at all, it only runs in the
// browser, and nothing in Go can execute it.
//
// These files are REAL ToMarkdown output, written by the Go test, not fixtures
// written by hand to look like it. That distinction is the entire point — the
// frontend already had a hand-written approximation, and a converter could drift
// away from it without ever breaking it.
//
// A failure here means an imported document of that shape opens READ-ONLY in the
// knowledge base: not editable, no keystroke marks it dirty, no save path runs.
// Regenerate the corpus with:
//
//   go test ./internal/convert/ -run TestFidelityCorpus -update-fidelity
const corpusDir = join(process.cwd(), "..", "..", "internal", "convert", "testdata", "fidelity");

const files = readdirSync(corpusDir).filter((f) => f.endsWith(".md"));

describe("converter output opens in the rich text editor", () => {
  // An empty directory would make every assertion below vacuously pass, which
  // is the one way this suite could report success while testing nothing.
  it("finds the generated corpus", () => {
    expect(files.length).toBeGreaterThan(0);
  });

  for (const name of files) {
    it(name, () => {
      const md = readFileSync(join(corpusDir, name), "utf8");
      if (!checkFidelity(md)) {
        // Show the round trip, not just a boolean: the diff between these two
        // strings names the exact construct that failed.
        expect(fidelityRoundTrip(md)).toBe(md);
      }
      expect(checkFidelity(md)).toBe(true);
    });
  }
});
