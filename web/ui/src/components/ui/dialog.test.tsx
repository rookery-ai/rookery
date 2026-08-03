import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "./dialog";

// These pin the width and containment contract of DialogContent. Both were
// silently broken for the whole life of the component, and neither failure is
// visible in this file alone — you only see it when a caller's `max-w-*` has no
// effect, or when a wide child leaks out through the side of the modal.
//
// See the comment block above DialogContent for the mechanisms.

function open(className?: string) {
  render(
    <Dialog open>
      <DialogContent className={className}>
        <DialogHeader>
          <DialogTitle>Choose an icon</DialogTitle>
        </DialogHeader>
        <div>body</div>
      </DialogContent>
    </Dialog>,
  );
  const el = screen.getByRole("dialog");
  return (el.getAttribute("class") ?? "").split(/\s+/).filter(Boolean);
}

describe("DialogContent width contract", () => {
  it("caps width with an unprefixed utility so tailwind-merge can replace it", () => {
    // A responsive cap (`sm:max-w-lg`) is a DIFFERENT tailwind-merge conflict
    // group from a caller's `max-w-2xl`: both survive the merge and the
    // responsive one wins at ≥640px, pinning every dialog in the app to one
    // width regardless of what it asked for.
    const classes = open();
    expect(classes.filter((c) => /^sm:max-w-/.test(c))).toEqual([]);
    expect(classes).toContain("max-w-lg");
  });

  it("lets a caller replace the default cap outright", () => {
    const classes = open("max-w-2xl");
    expect(classes.filter((c) => /^max-w-/.test(c))).toEqual(["max-w-2xl"]);
  });

  it("keeps the small-viewport inset when a caller sets a cap", () => {
    // The inset must live on `w-`, not `max-w-`: as `max-w-[calc(100%-2rem)]`
    // it shared a group with the caller's cap and was merged away, leaving a
    // narrow viewport with a full-bleed dialog and no margin.
    expect(open("max-w-2xl")).toContain("w-[calc(100%-2rem)]");
  });

  it("uses a zero-minimum grid track so a wide child cannot blow the box out", () => {
    // grid-cols-1 → repeat(1, minmax(0, 1fr)). With the implicit `auto` track,
    // a grid item's automatic minimum size is content-based, so one wide
    // non-wrapping child stretches the track and every sibling with it.
    expect(open()).toContain("grid-cols-1");
  });

  it("never exceeds the viewport height", () => {
    expect(open()).toContain("max-h-[calc(100dvh-4rem)]");
  });
});
