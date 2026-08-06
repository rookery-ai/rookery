import { clampImageWidth, splitAltWidth, joinAltWidth } from "./imageResize";

// jsdom cannot drive a pointer drag, so the maths is extracted and tested
// directly — the same tactic placeMenu in SlashMenu.tsx uses.
test("width is clamped to the column and a sane minimum", () => {
  expect(clampImageWidth(400, 800)).toBe(400);
  expect(clampImageWidth(2000, 800)).toBe(800);
  expect(clampImageWidth(10, 800)).toBe(80);
  expect(clampImageWidth(400.6, 800)).toBe(401);
});

test("splitAltWidth reads a trailing pipe width", () => {
  expect(splitAltWidth("Architecture|420")).toEqual({ alt: "Architecture", width: 420 });
  expect(splitAltWidth("Architecture")).toEqual({ alt: "Architecture", width: null });
  expect(splitAltWidth("")).toEqual({ alt: "", width: null });
});

test("an alt that genuinely contains a pipe is not corrupted", () => {
  // Split on the LAST pipe, and only when what follows is a bare integer.
  expect(splitAltWidth("a|b")).toEqual({ alt: "a|b", width: null });
  expect(splitAltWidth("a|b|300")).toEqual({ alt: "a|b", width: 300 });
});

test("joinAltWidth is the inverse", () => {
  expect(joinAltWidth("Architecture", 420)).toBe("Architecture|420");
  expect(joinAltWidth("Architecture", null)).toBe("Architecture");
  expect(joinAltWidth("", 420)).toBe("|420");
});
