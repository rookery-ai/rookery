import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";

const src = readFileSync(join(__dirname, "FileTree.tsx"), "utf8");

describe("file tree folder colour", () => {
  // Agents, Chats and Skills used to render `text-muted-2` because the server
  // marks them system:true, while Memory (carved out by name) and Notes (never
  // in kbSystemDirs) rendered at full contrast. To a user all five are the same
  // kind of thing — a folder you open — so the muted three read as disabled.
  test("system folders are not muted in the tree row", () => {
    expect(src).not.toMatch(/isEffectivelySystem\(node\)\s*&&\s*"text-muted-2"/);
  });

  // The predicate must survive: it is what stops a DB-backed folder being
  // dragged, reordered, or used as a drop target. Deleting it to fix the colour
  // would silently unlock all three.
  test("the system predicate still gates the drag rules", () => {
    expect(src).toContain("function isEffectivelySystem");
    const uses = src.match(/isEffectivelySystem\(/g) ?? [];
    // definition + canMoveInto + sameParent + handleReorder filter
    expect(uses.length).toBeGreaterThanOrEqual(4);
  });
});
