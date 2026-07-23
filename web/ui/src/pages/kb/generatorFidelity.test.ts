import { describe, it, expect } from "vitest";
import { checkFidelity } from "./editor";
import { splitFrontmatter } from "./frontmatter";

// Representative bodies from the platform's own note generators. The editor
// checks fidelity on the body AFTER frontmatter is split off (see NoteEditor),
// so these mirror that: split, then assert the body is WYSIWYG-safe.
const notes: Record<string, string> = {
  csvWithPipeAndOmitted:
    "---\ntype: upload\n---\n\n" +
    "| item | note |\n| --- | --- |\n| A | x \\| y |\n| B | ok |\n\n" +
    "*3 further rows omitted (7 total).*\n",
  reflectedChat:
    "---\ntype: chat\n---\n\n# Chat 2026-07-23 15:04\n\n**You:** hello\n\n**Assistant:** hi there\n",
  inboxNote:
    "---\ntype: inbox\n---\n\n# Agent notification\n\nYour agent finished a run and found 2 new items.\n",
  agentRunLog:
    "---\ntype: run\n---\n\n# Run 2026-07-23\n\n- started\n- fetched data\n- done\n",
  // A converted document (internal/convert) with headings, an escaped-pipe
  // table cell, a list with a link, a code fence, and a <br> line break —
  // emitted as a backslash HARD break so it round-trips (a bare "\n" soft break
  // used to collapse to a space and open the note in raw mode). Mirrors real
  // convert.ToMarkdown output; pins the html.go/ooxml.go atom.Br fix from this
  // side too.
  convertedDocument:
    "---\ntype: upload\n---\n\n" +
    "# Quarterly Report\n\nThis is **bold**, *italic*, and `inline code`.\n\n" +
    "## Metrics\n\n| Name | Value |\n| --- | --- |\n| Growth & churn | x \\| y |\n\n" +
    "- First point\n\n- Second with a [link](https://x.com)\n\n" +
    "```\nif a < b { return }\n```\n\n" +
    "Line one\\\nline two after a break.\n",
};

describe("generator output opens in rich text", () => {
  for (const [name, md] of Object.entries(notes)) {
    it(name, () => {
      const { body } = splitFrontmatter(md);
      expect(checkFidelity(body)).toBe(true);
    });
  }
});
