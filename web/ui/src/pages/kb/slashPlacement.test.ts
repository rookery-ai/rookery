import { placeMenu } from "./SlashMenu";

// The real measured geometry: twelve items render 224x442 in a 1600x900
// viewport. 442px is more than half a laptop viewport, which is why "flip
// above" alone is not a sufficient fix.
const MENU = { width: 224, height: 442 };
const VIEW = { width: 1600, height: 900 };

test("opens below the caret when there is room", () => {
  const p = placeMenu({ top: 100, bottom: 120, left: 400 }, MENU, VIEW);
  expect(p.top).toBe(124);
  expect(p.left).toBe(400);
  expect(p.maxHeight).toBeNull();
});

test("flips above when it would overflow the bottom", () => {
  // The measured bug: caret on the last line of a long note. The popup used to
  // render at top 868 and run 410px below the fold.
  const p = placeMenu({ top: 864, bottom: 868, left: 1112 }, MENU, VIEW);
  expect(p.top).toBe(864 - 4 - MENU.height);
  expect(p.top).toBeGreaterThanOrEqual(0);
  expect(p.top + MENU.height).toBeLessThanOrEqual(VIEW.height);
  expect(p.maxHeight).toBeNull();
});

test("caps and scrolls when it fits on neither side, choosing the larger", () => {
  // A 500px viewport with the caret mid-screen: 200px below, 276px above.
  // Clipping would hide items with no way to reach them, so the list scrolls.
  const p = placeMenu(
    { top: 280, bottom: 300, left: 10 },
    MENU,
    { width: 1600, height: 500 },
  );
  expect(p.maxHeight).toBe(276);
  expect(p.top).toBe(4);
});

test("caps downward when below is the roomier side", () => {
  const p = placeMenu(
    { top: 60, bottom: 80, left: 10 },
    MENU,
    { width: 1600, height: 400 },
  );
  expect(p.top).toBe(84);
  expect(p.maxHeight).toBe(316);
});

test("clamps left so the menu never leaves the right edge", () => {
  const p = placeMenu({ top: 100, bottom: 120, left: 1560 }, MENU, VIEW);
  expect(p.left).toBe(VIEW.width - MENU.width - 4);
});

test("clamps left so the menu never leaves the left edge", () => {
  const p = placeMenu({ top: 100, bottom: 120, left: -50 }, MENU, VIEW);
  expect(p.left).toBe(4);
});

test("never returns a negative left on a viewport narrower than the menu", () => {
  // Degenerate, but a negative left is worse than an overhang: the first
  // characters of every item would be unreachable off the left edge.
  const p = placeMenu(
    { top: 10, bottom: 30, left: 5 },
    MENU,
    { width: 120, height: 900 },
  );
  expect(p.left).toBeGreaterThanOrEqual(0);
});
