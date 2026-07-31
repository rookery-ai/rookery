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
