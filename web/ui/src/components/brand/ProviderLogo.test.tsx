import { render, screen } from "@testing-library/react";
import { ProviderLogo } from "./ProviderLogo";
import { PROVIDER_LOGOS } from "./logos";

test("known slug renders an svg with the expected brand path", () => {
  render(<ProviderLogo name="telegram" />);
  const svg = document.querySelector("svg");
  expect(svg).not.toBeNull();
  const path = svg?.querySelector("path");
  expect(path?.getAttribute("d")).toBe(PROVIDER_LOGOS.telegram.path);
});

test("known slug is case-insensitive", () => {
  render(<ProviderLogo name="Telegram" />);
  const svg = document.querySelector("svg");
  expect(svg?.querySelector("path")?.getAttribute("d")).toBe(PROVIDER_LOGOS.telegram.path);
});

test("unknown slug renders the capitalized initial", () => {
  render(<ProviderLogo name="mattermost" />);
  expect(document.querySelector("svg")).toBeNull();
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

test("title attribute is always set", () => {
  render(<ProviderLogo name="github" />);
  expect(screen.getByTitle("GitHub")).toBeTruthy();
});

test("title attribute falls back to the raw name for unknown slugs", () => {
  render(<ProviderLogo name="mattermost" />);
  expect(screen.getByTitle("mattermost")).toBeTruthy();
});
