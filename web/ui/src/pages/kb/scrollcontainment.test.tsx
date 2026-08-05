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
