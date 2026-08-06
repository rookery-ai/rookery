import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

// jsdom has no layout engine and no scrolling, so the behaviour these
// declarations produce cannot be asserted here — scripts/verify-kb-layout.py
// does that against a real browser. What this file protects is that the
// declarations do not silently disappear in a refactor.
const here = dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(join(here, p), "utf8");

test("the editor scroll pane contains its overscroll", () => {
  // Without this, wheeling past the end of a long note chains out to the
  // document and scrolls the entire h-screen shell — icon rail and file tree
  // included — out of the viewport. Measured: documentElement.scrollTop went
  // 0 -> 1459 and the rail's top went 0 -> -1459.
  expect(src("NoteEditor.tsx")).toMatch(
    /min-h-0 flex-1 overflow-y-auto overscroll-contain/,
  );
});

test("the app shell root cannot scroll", () => {
  // The shell is a fixed-height frame and every scrolling region inside it is
  // explicit. Without overflow-hidden the tall editor content propagates its
  // scrollable overflow to the initial containing block, which is what made
  // the document scrollable at all — setting documentElement.scrollTop
  // directly moved the rail even with overscroll contained.
  expect(src("../../components/shell/AppShell.tsx")).toMatch(
    /h-screen overflow-hidden flex flex-col md:flex-row bg-background/,
  );
});

test("the app shell root is a containing block", () => {
  // `relative` on a flex container looks like a no-op and is exactly the kind
  // of class a cleanup removes. It is load-bearing: `overflow` only clips a
  // descendant when the clipping element is in that descendant's
  // containing-block chain, and the shell is otherwise position:static — so
  // the editor's overflowing content escaped it entirely and landed in the
  // ROOT element's scroll box. Measured before the fix: <html> clientHeight
  // 900 vs scrollHeight 13425, and wheeling over the icon rail moved
  // documentElement.scrollTop 0 -> 3200 with the rail's top 0 -> -3200.
  // With the fix, scrollHeight is 900 and nothing scrolls but the note.
  expect(src("../../components/shell/AppShell.tsx")).toMatch(
    /className="relative h-screen overflow-hidden/,
  );
});
