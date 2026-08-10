import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RookeryLogo, RookeryMark, RookeryTile } from "./RookeryMark";
import {
  DEFAULT_WORKSPACE_ICON,
  WORKSPACE_ICONS,
  WorkspaceAvatar,
  findWorkspaceIcon,
} from "@/lib/workspaceIcons";

describe("RookeryMark", () => {
  // The whole reason the mark is a component and not an <img>: an image cannot
  // inherit currentColor, which is exactly how the documentation site's mark
  // ended up painting black and vanishing on the dark theme.
  it("strokes in currentColor so it themes with its surroundings", () => {
    const { container } = render(<RookeryMark />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("stroke")).toBe("currentColor");
    expect(svg.getAttribute("fill")).toBe("none");
  });

  it("is labelled for screen readers", () => {
    render(<RookeryMark />);
    expect(screen.getByRole("img", { name: "Rookery" })).toBeTruthy();
  });

  // A tile paints its own background, so its glyph must NOT be currentColor —
  // it would take the page's foreground and disappear against the fill.
  it("paints the tile glyph explicitly rather than inheriting", () => {
    const { container } = render(<RookeryTile id="t" />);
    const path = container.querySelector("path")!;
    expect(path.getAttribute("stroke")).toBe("#ece5db");
    expect(container.querySelector("linearGradient")!.id).toBe("t");
  });

  // Two tiles on one screen with the same gradient id would make the second
  // reference the first one's gradient.
  it("takes a gradient id so two tiles can coexist", () => {
    const { container } = render(
      <>
        <RookeryTile id="a" from="#111111" />
        <RookeryTile id="b" from="#222222" />
      </>,
    );
    const ids = [...container.querySelectorAll("linearGradient")].map((g) => g.id);
    expect(new Set(ids).size).toBe(2);
  });

  it("locks the wordmark up tight against the mark", () => {
    const { container } = render(<RookeryLogo />);
    // Mark and word read as one logo only while they sit closer to each other
    // than to anything else. A wide gap makes them two adjacent objects.
    expect(container.firstElementChild!.className).toContain("gap-2");
  });
});

describe("workspace presets", () => {
  it("offers the mark in eight hues, with the default first", () => {
    const marks = WORKSPACE_ICONS.filter((i) => i.slug.startsWith("rookery"));
    expect(marks).toHaveLength(8);
    expect(WORKSPACE_ICONS[0].slug).toBe(DEFAULT_WORKSPACE_ICON);
  });

  // The default must also be a storable choice, or picking the tile a workspace
  // already displays would be rejected by the server's validator.
  it("has a default that resolves to a real preset", () => {
    expect(findWorkspaceIcon(DEFAULT_WORKSPACE_ICON)).toBeTruthy();
  });

  it("keeps every slug unique", () => {
    const slugs = WORKSPACE_ICONS.map((i) => i.slug);
    expect(new Set(slugs).size).toBe(slugs.length);
  });

  // Renaming or dropping a slug orphans every workspace that stored it. The
  // palette pass deliberately changed only colours.
  it("still carries every original motif slug", () => {
    const original = [
      "aurora", "orbit", "prism", "meadow", "ember", "tide", "dusk", "grove",
      "signal", "quartz", "bloom", "slate", "cascade", "lattice", "forum",
      "spring", "nova", "eclipse", "surge", "venn", "summit", "monolith",
      "waning", "clinic", "strata", "beacon", "sprout", "voyage",
    ];
    const have = new Set(WORKSPACE_ICONS.map((i) => i.slug));
    for (const slug of original) expect(have.has(slug)).toBe(true);
  });

  it("renders the mark when a workspace has chosen no icon", () => {
    const { container } = render(<WorkspaceAvatar name="Personal" />);
    expect(container.querySelector("svg")).toBeTruthy();
    expect(container.textContent).not.toContain("P");
  });

  // An unknown slug is a workspace configured by a NEWER build. Falling back to
  // the initial is honest about not recognising it; rendering the default would
  // silently present it as the user's choice.
  it("falls back to the initial for a slug it does not know", () => {
    const { container } = render(
      <WorkspaceAvatar name="Personal" icon="from-the-future" />,
    );
    expect(container.querySelector("svg")).toBeNull();
    expect(container.textContent).toBe("P");
  });
});
