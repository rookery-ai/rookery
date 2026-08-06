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
  // Tightened to the introduced `.tiptap ul, .tiptap ol { padding-left: 1.5em }`
  // rule specifically — the pre-existing taskList rule
  // (`ul[data-type="taskList"] { padding-left: 0 }`) also contains the
  // substring "padding-left" and would otherwise satisfy a looser match even
  // with the new rules deleted entirely.
  expect(css).toMatch(/\.tiptap ul,[\s\S]{0,120}padding-left:\s*1\.5em/);
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

it("task lists stay unspaced — li + li is scoped away from taskList", () => {
  // A checkbox list never had inter-item spacing before the list-marker fix;
  // an unscoped `li + li` rule would silently add it back since task-list
  // <li>s are still adjacent siblings under their <ul>.
  expect(css).toMatch(
    /\.tiptap ul:not\(\[data-type="taskList"\]\) > li \+ li/,
  );
  // Pin against reverting to the unscoped form specifically (as opposed to
  // just requiring the scoped selector above, which a sloppy revert could
  // add alongside the old rule and still pass).
  expect(css).not.toMatch(/\n\.note-editor-content \.tiptap li \+ li \{/);
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
