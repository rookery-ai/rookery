import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { it, expect } from "vitest";
import { Editor } from "@tiptap/core";
import { buildExtensions, toMarkdown } from "./editor";

// jsdom has no layout engine, so "does a marker appear" is not observable
// here. What IS observable is (a) that the stylesheet declares the rules at
// all, and (b) that the COMMANDS were never the problem — they always built a
// real list and serialized real markdown. Both halves matter: the second is
// what stops the next person from "fixing" the commands.
const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, "editor.css"), "utf8");

function headless(content: string) {
  return new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content,
  });
}

it("the stylesheet gives bullet lists a marker and an indent", () => {
  // Tailwind Preflight zeroes list-style/padding on ul. Without these rules a
  // bullet list is visually indistinguishable from consecutive paragraphs.
  expect(css).toMatch(/\.tiptap ul\b[^{]*\{[^}]*list-style:\s*disc/);
  expect(css).toMatch(/\.tiptap ul[\s\S]{0,200}padding-left/);
});

it("the stylesheet gives numbered lists a marker", () => {
  expect(css).toMatch(/\.tiptap ol\b[^{]*\{[^}]*list-style:\s*decimal/);
});

it("task lists stay unmarkered", () => {
  // The taskList rule is more specific than the new ul rule and must keep
  // winning — a checkbox list with a bullet next to every checkbox is wrong.
  expect(css).toMatch(
    /\.tiptap ul\[data-type="taskList"\][^{]*\{[^}]*list-style:\s*none/,
  );
});

it("toggleBulletList always produced real markdown — the bug was never the command", () => {
  const editor = headless("<p>alpha</p>");
  editor.commands.selectAll();
  editor.commands.toggleBulletList();
  expect(toMarkdown(editor)).toContain("- alpha");
  editor.destroy();
});

it("toggleOrderedList always produced real markdown", () => {
  const editor = headless("<p>alpha</p>");
  editor.commands.selectAll();
  editor.commands.toggleOrderedList();
  expect(toMarkdown(editor)).toContain("1. alpha");
  editor.destroy();
});
