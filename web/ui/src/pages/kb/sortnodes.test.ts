import { sortNodes } from "./FileTree";
import type { KBNode } from "@/lib/kb";

function dir(name: string, system = false): KBNode {
  return { name, display_name: name, path: name, is_dir: true, system };
}
function file(name: string): KBNode {
  return { name, display_name: name, path: name, is_dir: false, system: false };
}

const names = (nodes: KBNode[]) => nodes.map((n) => n.name);

test("at the vault root the default folders lead in a fixed order, notes last", () => {
  // Deliberately shuffled, and alphabetically notes/ would come before skills/.
  const nodes = [dir("notes"), dir("skills", true), dir("chats", true), dir("agents", true), dir("memory", true)];
  expect(names(sortNodes(nodes, [], true))).toEqual([
    "memory",
    "agents",
    "chats",
    "skills",
    "notes",
  ]);
});

test("a user's own root folder sorts after the fixed set but before notes", () => {
  const nodes = [dir("notes"), dir("memory", true), dir("projects"), dir("archive")];
  expect(names(sortNodes(nodes, [], true))).toEqual(["memory", "archive", "projects", "notes"]);
});

test("directories precede files at the root, and files stay alphabetical", () => {
  // localeCompare collates case-insensitively, so inbox.md precedes README.md.
  const nodes = [file("README.md"), dir("notes"), file("inbox.md"), dir("memory", true)];
  expect(names(sortNodes(nodes, [], true))).toEqual(["memory", "notes", "inbox.md", "README.md"]);
});

test("the fixed ordering applies only at the root", () => {
  // Inside a folder, a directory that happens to be called "notes" is just a
  // directory and sorts alphabetically.
  const nodes = [dir("notes"), dir("archive"), dir("memory")];
  expect(names(sortNodes(nodes, [], false))).toEqual(["archive", "memory", "notes"]);
});

test("a user's drag-chosen order still wins over the default folder ranking", () => {
  // Someone who dragged notes/ to the top keeps it there — the new default
  // must not silently re-shuffle an arrangement they made by hand.
  const nodes = [dir("memory", true), dir("notes"), dir("agents", true)];
  expect(names(sortNodes(nodes, ["notes", "agents", "memory"], true))).toEqual([
    "notes",
    "agents",
    "memory",
  ]);
});

test("names absent from the drag order fall in behind it, still default-ranked", () => {
  // A folder created since the last drag is not pinned to an arbitrary slot;
  // it sorts after the explicitly-ordered block by the derived rules.
  const nodes = [dir("memory", true), dir("notes"), dir("agents", true), dir("chats", true)];
  expect(names(sortNodes(nodes, ["notes"], true))).toEqual([
    "notes",
    "memory",
    "agents",
    "chats",
  ]);
});

test("sorting does not mutate the input array", () => {
  const nodes = [dir("notes"), dir("memory", true)];
  const before = names(nodes);
  sortNodes(nodes, [], true);
  expect(names(nodes)).toEqual(before);
});
