import { render, screen } from "@testing-library/react";
import { Button } from "./button";

// The density floor. WCAG 2.2 AA's target-size minimum is 24px, so these clear
// it with margin without going to a touch-first 44px that would waste vertical
// room in a dense context pane. Asserted rather than eyeballed because the old
// sizes (h-9/h-8/h-6, size-9/8/6) are exactly what "you need to aim to click
// something" was about.
test.each([
  ["default", "h-10"],
  ["sm", "h-9"],
  ["xs", "h-7"],
  ["lg", "h-11"],
] as const)("text size %s meets the height floor (%s)", (size, cls) => {
  render(<Button size={size}>Go</Button>);
  expect(screen.getByRole("button").className).toContain(cls);
});

test.each([
  ["icon", "size-10"],
  ["icon-sm", "size-9"],
  ["icon-xs", "size-7"],
  ["icon-lg", "size-11"],
] as const)("icon size %s meets the target floor (%s)", (size, cls) => {
  render(<Button size={size} aria-label="act" />);
  expect(screen.getByRole("button").className).toContain(cls);
});

test("the four contract variants are all available", () => {
  for (const variant of ["default", "outline", "ghost", "destructive"] as const) {
    const { unmount } = render(<Button variant={variant}>x</Button>);
    expect(screen.getByRole("button")).toBeInTheDocument();
    unmount();
  }
});

test("every button carries a pointer cursor, because the browser gives none", () => {
  // A <button> gets cursor:default from the browser, and this build's Tailwind
  // preflight adds no button cursor rule — verified by grepping the emitted
  // CSS, which held only two cursor:pointer rules, neither of them for a
  // button. So every button in the app hovered as if it were inert.
  //
  // This is the same defect that made KB search results read as unclickable:
  // FileTree's rows opt into cursor-pointer explicitly, so in one pane the tree
  // felt interactive and the search results did not.
  render(<Button>Go</Button>);
  expect(screen.getByRole("button").className).toMatch(/cursor-pointer/);
});

test("a disabled button shows a not-allowed cursor rather than a pointer", () => {
  render(<Button disabled>Go</Button>);
  const btn = screen.getByRole("button");
  expect(btn.className).toMatch(/disabled:cursor-not-allowed/);
  expect(btn).toBeDisabled();
});
