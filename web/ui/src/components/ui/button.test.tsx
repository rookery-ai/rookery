import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { render, screen } from "@testing-library/react";
import { Button } from "./button";

// See styles.test.ts: Vite's asset-URL transform rewrites
// new URL(relative, import.meta.url) into an http: URL even under vitest.
const here = path.dirname(fileURLToPath(import.meta.url));

function tsxSources(dir: string): string[] {
  return readdirSync(dir).flatMap((e) => {
    const p = path.join(dir, e);
    if (statSync(p).isDirectory()) return tsxSources(p);
    return /\.tsx$/.test(e) && !/\.test\.tsx$/.test(e) ? [p] : [];
  });
}

// Walks <Button ...>body</Button> occurrences. Hand-rolled rather than a regex
// because the opening tag contains arrow functions, so a naive [^>]* stops at
// the ">" inside "=>" and captures handler source as the label.
function buttonElements(src: string): { attrs: string; body: string }[] {
  const out: { attrs: string; body: string }[] = [];
  const re = /<Button\b/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src)) !== null) {
    let i = m.index + m[0].length;
    let depth = 0;
    let quote: string | null = null;
    let tagEnd = -1;
    for (; i < src.length; i++) {
      const c = src[i];
      if (quote) {
        if (c === quote && src[i - 1] !== "\\") quote = null;
      } else if (c === '"' || c === "'" || c === "`") quote = c;
      else if (c === "{") depth++;
      else if (c === "}") depth--;
      else if (c === ">" && depth === 0 && src[i - 1] !== "=") {
        tagEnd = i;
        break;
      }
    }
    if (tagEnd < 0 || src[tagEnd - 1] === "/") continue; // self-closing
    const close = src.indexOf("</Button>", tagEnd);
    if (close < 0) continue;
    out.push({ attrs: src.slice(m.index + m[0].length, tagEnd), body: src.slice(tagEnd + 1, close) });
  }
  return out;
}

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

test("only the documented carve-out is left without an icon", () => {
  // Contract: every ACTION button carries a leading icon. The carve-out is
  // deliberate and narrow — dialog footer PAIRS (Cancel/Save) read as a matched
  // pair, and an icon on "Cancel" is noise, not information.
  //
  // Pinned as a test because the rule is otherwise unenforceable: it lives in a
  // comment, and the next Button added anywhere would quietly break it.
  const CARVE_OUT = /^(Cancel|Close|Done|Skip for now)/i;

  const offenders: string[] = [];
  for (const file of tsxSources(path.resolve(here, "../.."))) {
    const src = readFileSync(file, "utf8");
    for (const { attrs, body } of buttonElements(src)) {
      if (/<[A-Z][A-Za-z0-9]*[\s/>]/.test(body)) continue; // has an icon
      if (attrs.includes('variant="link"')) continue; // link is text-only
      const label = body.replace(/\s+/g, " ").trim();
      if (CARVE_OUT.test(label)) continue;
      offenders.push(`${path.basename(file)}: ${label.slice(0, 40)}`);
    }
  }
  expect(offenders).toEqual([]);
});
