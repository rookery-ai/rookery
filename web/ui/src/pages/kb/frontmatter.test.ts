import { describe, it, expect } from "vitest";
import { splitFrontmatter, joinFrontmatter, parseFrontmatterFields } from "./frontmatter";
import { checkFidelity } from "./editor";

// The real shapes internal/vault/reflect.go writes. These are the notes the
// user reported as unopenable in the rich text editor.
const INBOX_NOTE = `---
type: inbox
id: 0a107b38-db68-4666-a400-9e2bd663eb99
trigger: manual
status: ok
created_at: 2026-07-10T13:03:48Z
---

# 🤖 linkedin-post-scraper (manual)

❌ Could not check your LinkedIn posts today. The script needs a **LinkedIn** connection.
`;

const CHAT_NOTE = `---
type: chat
id: 33139123-6939-4d12-9bf8-409b2e042d24
platform: web
created_at: 2026-06-25T12:22:52Z
---

# Chat 2026-06-25 12:22

**User** · 2026-06-25T12:23:01Z

hey

**Assistant** · 2026-06-25T12:23:03Z

Hey Ilija! 👋
`;

const RUN_LOG = `---
type: agent-run
run_id: 1e12493f-b935-4cad-8386-876ceb41ce7c
agent_id: 0e71e9bf-97b6-4b40-9099-f3977e0384aa
status: ok
---

# Run of [[Daily Digest]] — ok

## Output sent to user

> all quiet
`;

describe("splitFrontmatter", () => {
  it("separates a reflected note's YAML block from its body", () => {
    const { frontmatter, body } = splitFrontmatter(INBOX_NOTE);
    expect(frontmatter).toContain("type: inbox");
    expect(frontmatter.startsWith("---")).toBe(true);
    expect(body.startsWith("# 🤖 linkedin-post-scraper")).toBe(true);
    // The block must not bleed into the body, or the editor is back to
    // rendering it as a horizontal rule plus a setext heading.
    expect(body).not.toContain("type: inbox");
  });

  it("leaves a note with no frontmatter completely untouched", () => {
    const plain = "# Dear Diary\n\nToday is a new day.\n";
    expect(splitFrontmatter(plain)).toEqual({ frontmatter: "", body: plain });
  });

  it("does not treat an unterminated --- as frontmatter", () => {
    // A horizontal rule with no closing fence is ordinary content. Swallowing
    // it as metadata would hide real text from the editor.
    const md = "---\n\nJust a rule above some text.\n";
    expect(splitFrontmatter(md).frontmatter).toBe("");
  });

  it("does not treat a mid-document --- as frontmatter", () => {
    const md = "# Title\n\n---\n\nAfter a rule.\n";
    expect(splitFrontmatter(md).frontmatter).toBe("");
  });

  // Regression guard. Without the key:value requirement, "Chapter 1" is
  // absorbed into the "block" — and then renders NOWHERE, because the metadata
  // strip only displays parsed pairs and the editor only ever sees the body.
  // That is worse than the old behaviour, where the note opened in raw mode
  // with every line visible.
  it("does not mistake a leading setext heading for frontmatter", () => {
    const md = "---\nChapter 1\n---\n\nContent\n";
    expect(splitFrontmatter(md)).toEqual({ frontmatter: "", body: md });
  });

  it("does not mistake a prose line containing a colon for a metadata pair", () => {
    const md = "---\nChapter 1: The Beginning\n---\n\nContent\n";
    expect(splitFrontmatter(md).frontmatter).toBe("");
  });

  it("still recognises a block with a single key", () => {
    expect(splitFrontmatter("---\ntype: chat\n---\n\nbody\n").frontmatter).toContain("type: chat");
  });

  it("handles an empty body after the block", () => {
    const { frontmatter, body } = splitFrontmatter("---\ntype: x\n---\n");
    expect(frontmatter).toContain("type: x");
    expect(body).toBe("");
  });
});

// The preservation contract: the block is opaque bytes, never parsed or
// re-serialized, so opening and saving without an edit is a no-op on disk.
describe("frontmatter round trip is byte-identical", () => {
  for (const [name, note] of [
    ["inbox note", INBOX_NOTE],
    ["chat transcript", CHAT_NOTE],
    ["agent run log", RUN_LOG],
    ["note without frontmatter", "# Plain\n\nbody\n"],
    ["frontmatter with no body", "---\ntype: x\n---\n"],
  ] as const) {
    it(name, () => {
      const { frontmatter, body } = splitFrontmatter(note);
      expect(joinFrontmatter(frontmatter, body)).toBe(note);
    });
  }
});

// The actual defect. Each of these notes failed checkFidelity as a whole and so
// opened in raw markdown; each body passes on its own. If this regresses,
// platform-written notes stop opening in the rich text editor again.
describe("reflected note bodies are rich-text-safe once split", () => {
  for (const [name, note] of [
    ["inbox note", INBOX_NOTE],
    ["chat transcript", CHAT_NOTE],
    ["agent run log", RUN_LOG],
  ] as const) {
    it(`${name}: whole note is lossy, body is not`, () => {
      expect(checkFidelity(note)).toBe(false);
      expect(checkFidelity(splitFrontmatter(note).body)).toBe(true);
    });
  }
});

describe("parseFrontmatterFields", () => {
  it("reads flat key/value pairs in document order", () => {
    expect(parseFrontmatterFields(splitFrontmatter(INBOX_NOTE).frontmatter)).toEqual([
      { key: "type", value: "inbox" },
      { key: "id", value: "0a107b38-db68-4666-a400-9e2bd663eb99" },
      { key: "trigger", value: "manual" },
      { key: "status", value: "ok" },
      { key: "created_at", value: "2026-07-10T13:03:48Z" },
    ]);
  });

  it("skips lines it cannot parse rather than throwing", () => {
    // Display-only: an unparseable line is still PRESERVED on save, because
    // saving uses the raw block and never this parse.
    expect(parseFrontmatterFields("---\nnot a pair\nkey: value\n---")).toEqual([
      { key: "key", value: "value" },
    ]);
  });

  it("returns nothing for an empty block", () => {
    expect(parseFrontmatterFields("")).toEqual([]);
  });
});
