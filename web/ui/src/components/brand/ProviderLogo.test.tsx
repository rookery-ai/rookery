import { render, screen } from "@testing-library/react";
import { ProviderLogo } from "./ProviderLogo";
import { PROVIDER_LOGOS, isMonochrome, lookupLogo } from "./logos";

test("known slug renders the vendored svg inline", () => {
  const { container } = render(<ProviderLogo name="telegram" />);
  const svg = container.querySelector("svg");
  expect(svg).not.toBeNull();
  // Inline, not an <img> — currentColor in a monochrome mark cannot resolve
  // against the app's theme across an <img> boundary.
  expect(container.querySelector("img")).toBeNull();
});

test("known slug is case-insensitive", () => {
  const { container } = render(<ProviderLogo name="Telegram" />);
  expect(container.querySelector("svg")).not.toBeNull();
});

test("unknown slug renders the capitalized initial", () => {
  const { container } = render(<ProviderLogo name="mattermost" />);
  expect(container.querySelector("svg")).toBeNull();
  expect(screen.getByText("M")).toBeTruthy();
});

test("unknown name maps to a deterministic fallback color across renders", () => {
  const { container: c1 } = render(<ProviderLogo name="acme-widgets" />);
  const { container: c2 } = render(<ProviderLogo name="acme-widgets" />);
  const cls1 = c1.firstElementChild?.className;
  const cls2 = c2.firstElementChild?.className;
  expect(cls1).toBe(cls2);
  expect(cls1).toMatch(/bg-/);
});

test("different unknown names can map to different fallback colors", () => {
  // Not guaranteed for every pair (hash collisions are possible), but this
  // specific pair should differ — guards against a hash that always returns 0.
  const { container: c1 } = render(<ProviderLogo name="alpha" />);
  const { container: c2 } = render(<ProviderLogo name="zzz-totally-different" />);
  expect(c1.firstElementChild?.className).not.toBe(c2.firstElementChild?.className);
});

test("size prop is respected on the tile", () => {
  const { container } = render(<ProviderLogo name="discord" size={48} />);
  const tile = container.firstElementChild as HTMLElement;
  expect(tile.style.width).toBe("48px");
  expect(tile.style.height).toBe("48px");
});

test("default size is 32", () => {
  const { container } = render(<ProviderLogo name="discord" />);
  const tile = container.firstElementChild as HTMLElement;
  expect(tile.style.width).toBe("32px");
});

test("title and aria-label are always set, for known and unknown slugs alike", () => {
  render(<ProviderLogo name="github" />);
  expect(screen.getByTitle("github")).toBeTruthy();
  render(<ProviderLogo name="mattermost" />);
  expect(screen.getByTitle("mattermost")).toBeTruthy();
});

test("a monochrome mark gets an explicit color so currentColor resolves", () => {
  // GitHub ships as a fill="currentColor" path. Without a color on the tile it
  // would inherit whatever the surrounding text colour happens to be, which in
  // dark mode is near-white on a white tile — i.e. invisible.
  expect(isMonochrome(lookupLogo("github")!)).toBe(true);
  const { container } = render(<ProviderLogo name="github" />);
  const tile = container.firstElementChild as HTMLElement;
  expect(tile.style.color).toBe("rgb(24, 24, 27)");
});

test("a full-colour mark is left to its own fills", () => {
  // Slack is a multi-colour logo; forcing a colour on the tile would do
  // nothing here but would be a bug if the mark ever mixed currentColor in.
  expect(isMonochrome(lookupLogo("slack")!)).toBe(false);
  const { container } = render(<ProviderLogo name="slack" />);
  const tile = container.firstElementChild as HTMLElement;
  expect(tile.style.color).toBe("");
});

test("every vendored asset is a well-formed svg with a viewBox", () => {
  // A logo without a viewBox cannot scale to the tile and would render at its
  // published pixel size, blowing out the layout.
  const entries = Object.entries(PROVIDER_LOGOS);
  expect(entries.length).toBeGreaterThan(40);
  for (const [slug, svg] of entries) {
    expect(svg.startsWith("<svg"), `${slug} should start with <svg`).toBe(true);
    expect(svg.includes("viewBox"), `${slug} should carry a viewBox`).toBe(true);
    expect(/<script/i.test(svg), `${slug} must not contain <script>`).toBe(false);
  }
});
