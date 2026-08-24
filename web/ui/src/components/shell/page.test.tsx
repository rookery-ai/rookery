import { readFileSync, readdirSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { render, screen } from "@testing-library/react";
import { PageContainer } from "./PageContainer";
import { PageTitle } from "./PageTitle";

const here = path.dirname(fileURLToPath(import.meta.url));

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((e) => {
    const p = path.join(dir, e);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx$/.test(e) && !/\.test\.tsx$/.test(e) ? [p] : [];
  });
}

test("PageContainer is fluid and caps only on ultrawide", () => {
  const { container } = render(<PageContainer>x</PageContainer>);
  const el = container.firstElementChild!;
  expect(el.className).toContain("w-full");
  expect(el.className).toContain("max-w-[1600px]");
  // The complaint was centred 768px content on a 1920px screen.
  expect(el.className).not.toMatch(/max-w-(3xl|5xl)\b/);
});

test("PageContainer keeps px/py separate so a caller can override the gutter", () => {
  // tailwind-merge treats p and px as DIFFERENT groups, so a p-* shorthand
  // here would survive alongside a caller's px-[7%] and let generated
  // stylesheet order pick the winner. CLAUDE.md records this exact bug in
  // ChatScroll.
  const src = readFileSync(path.join(here, "PageContainer.tsx"), "utf8");
  expect(src).toMatch(/px-8 py-6/);
  expect(src).not.toMatch(/className=\{cn\("[^"]*\bp-\d/);
});

test("PageTitle renders an icon from the shared entity map", () => {
  const { container } = render(<PageTitle icon="agents" title="Agents" />);
  expect(screen.getByRole("heading", { name: "Agents", level: 1 })).toBeInTheDocument();
  expect(container.querySelector("svg")).toBeTruthy();
});

test("PageTitle shows a subtitle only when given one", () => {
  const { rerender, container } = render(<PageTitle icon="agents" title="Agents" />);
  expect(container.querySelectorAll("p")).toHaveLength(0);
  rerender(<PageTitle icon="agents" title="Agents" subtitle="3 agents configured" />);
  expect(screen.getByText("3 agents configured")).toBeInTheDocument();
});

test("an unknown icon key degrades instead of crashing the page", () => {
  const { container } = render(<PageTitle icon="not-a-kind" title="Something" />);
  expect(container.querySelector("svg")).toBeTruthy();
});

test("no page still hardcodes its own centred max width", () => {
  // Four pages each capped their content independently (Settings max-w-3xl,
  // Connections max-w-5xl, FolderPage, NoteEditor), which is what left ~900px
  // of dead margin on a wide display.
  const offenders = sources(path.join(here, "../../pages"))
    .filter((f) => /className="[^"]*mx-auto[^"]*max-w-(3xl|5xl)\b/.test(readFileSync(f, "utf8")))
    .map((f) => path.basename(f));
  expect(offenders).toEqual([]);
});

test("the page title and its icon are sized for a title bar", () => {
  // Round one left the title at the same text-xl the old <h1>s used, so after
  // the type scale rose it no longer read as the largest thing on the page.
  const { container } = render(<PageTitle icon="agents" title="Agents" />);
  expect(container.querySelector("h1")!.className).toContain("text-2xl");
  expect(container.querySelector("svg")!.getAttribute("class")).toContain("size-7");
});

test("the page-title icon is the same size as the title text", () => {
  // The icon was size-6 (24px) while --text-2xl is remapped to 28px, so it read
  // visibly small beside the title on all eight pages using this component.
  // Asserted against the TOKEN rather than restating 28px here: index.css is
  // the one place the scale is defined, and a test carrying its own copy of the
  // number would agree with itself while the interface drifted.
  const css = readFileSync(path.join(here, "..", "..", "index.css"), "utf8");
  const rem = /--text-2xl:\s*([\d.]+)rem/.exec(css)?.[1];
  expect(rem).toBeDefined();

  const { container } = render(<PageTitle icon="agents" title="Agents" />);
  const size = /\bsize-(\d+)\b/.exec(
    container.querySelector("svg")!.getAttribute("class")!,
  )?.[1];
  expect(size).toBeDefined();

  // Tailwind's size-N is N * 0.25rem.
  expect(Number(size) * 0.25).toBeCloseTo(Number(rem), 5);
});

test("the icon rail is wide enough for its larger targets", () => {
  const src = readFileSync(path.join(here, "IconRail.tsx"), "utf8");
  expect(src).toContain("md:w-20");
  expect(src).toContain("size-12");
  // Glyphs grow with the targets, or a size-12 button holds a size-5 icon
  // floating in whitespace.
  expect(src).toContain('className="size-6 stroke-[2.25]"');
  expect(src).not.toContain("md:w-16");
});

test("the workspace avatar matches the rail item size", () => {
  // It sits at the top of the same column; if it does not grow with the rail
  // it reads as a different control.
  const src = readFileSync(path.join(here, "WorkspaceMenu.tsx"), "utf8");
  expect(src).toContain("size-12");
  expect(src).not.toMatch(/className="[^"]*size-11/);
});
