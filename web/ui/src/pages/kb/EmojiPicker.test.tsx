import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import EmojiPicker from "./EmojiPicker";

function open() {
  const onSelect = vi.fn();
  render(
    <EmojiPicker open onOpenChange={() => {}} onSelect={onSelect} />,
  );
  return onSelect;
}

describe("EmojiPicker", () => {
  it("sizes its grid by available width, not a fixed column count", () => {
    // A fixed `grid-cols-10` of 2.25rem cells assumes a dialog width the
    // component does not control — it was either cramped or overflowing at
    // every width but the one it happened to be measured at.
    open();
    const grid = screen.getAllByRole("button", { name: /^Set icon/ })[0]
      .parentElement;
    expect(grid?.className).toContain(
      "grid-cols-[repeat(auto-fill,minmax(2.25rem,1fr))]",
    );
    expect(grid?.className).not.toMatch(/grid-cols-\d/);
  });

  it("searches across every category and reports the match count", async () => {
    const user = userEvent.setup();
    open();
    // "rocket" lives under Travel & Places, which is not the tab open by
    // default — a search that only covered the visible group would miss it.
    await user.type(screen.getByLabelText("Search emoji"), "rocket");
    expect(await screen.findByRole("button", { name: "Set icon 🚀" })).toBeTruthy();
    expect(screen.getByText(/match(es)?$/)).toBeTruthy();
  });

  it("names the query when nothing matches", async () => {
    const user = userEvent.setup();
    open();
    await user.type(screen.getByLabelText("Search emoji"), "zzzznope");
    expect(await screen.findByText(/No emoji match/)).toBeTruthy();
  });

  it("picks an emoji", async () => {
    const user = userEvent.setup();
    const onSelect = open();
    await user.type(screen.getByLabelText("Search emoji"), "rocket");
    await user.click(await screen.findByRole("button", { name: "Set icon 🚀" }));
    expect(onSelect).toHaveBeenCalledWith("🚀");
  });
});
