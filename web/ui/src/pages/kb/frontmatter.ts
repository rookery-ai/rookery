// YAML frontmatter handling for the note editor.
//
// WHY THIS EXISTS
//
// Every note the platform writes into the vault carries a YAML frontmatter
// block (internal/vault/reflect.go): reflected chats, inbox notifications,
// reminders and agent run logs all open with `---\ntype: …\n---`.
//
// That block is the ONLY reason those notes would not open in the rich text
// editor. Markdown parses a leading `---` as a horizontal rule, and the key
// lines that follow become a setext `##` heading terminated by the closing
// `---`, so a round trip through the editor rewrites the block into
// `---\n\n## type: inbox id: … created_at: …`. checkFidelity correctly refuses
// that, and NoteEditor falls back to raw markdown — which is what the user saw
// as "inbox items can't be represented by the rich text editor".
//
// The note BODIES are already fine: measured against the real reflected shapes,
// inbox notes, chat transcripts and agent run logs all pass checkFidelity once
// the frontmatter is removed. So the fix is to keep the block out of the
// editor's hands entirely rather than to change what the reflector writes —
// that also repairs every note ALREADY in the user's vault, with no migration,
// and covers notes imported from elsewhere that carry frontmatter of their own.
//
// PRESERVATION CONTRACT
//
// The block is treated as OPAQUE BYTES. It is never parsed as YAML,
// reformatted, reordered or re-serialized — it is sliced off as a string and
// concatenated back unchanged. Opening a note and saving it without an edit
// must produce a byte-identical file. frontmatter.test.ts pins this.

export type SplitNote = {
  /** The raw frontmatter block including both `---` fences and the trailing
   *  newline(s) that separated it from the body. Empty when there is none. */
  frontmatter: string;
  /** Everything after the block — what the editor actually edits. */
  body: string;
};

/**
 * splitFrontmatter separates a leading YAML frontmatter block from the note body.
 *
 * Recognises the block only when the document STARTS with a `---` fence line and
 * a later `---` fence line closes it, matching the CommonMark-adjacent
 * convention every tool in this codebase writes. Anything else — a horizontal
 * rule mid-document, a `---` that is never closed, a leading blank line — is
 * left in the body, because misidentifying content as metadata would hide real
 * text from the editor.
 */
export function splitFrontmatter(md: string): SplitNote {
  const none: SplitNote = { frontmatter: "", body: md };
  // No leading-whitespace tolerance: a document that starts with a blank line
  // is not carrying frontmatter, and treating it as though it did would swallow
  // real content on save.
  if (!md.startsWith("---")) return none;

  const lines = md.split("\n");
  if (lines[0].trim() !== "---") return none;

  let close = -1;
  for (let i = 1; i < lines.length; i++) {
    if (lines[i].trim() === "---") {
      close = i;
      break;
    }
  }
  // An unterminated `---` is a horizontal rule followed by ordinary text, not a
  // metadata block.
  if (close === -1) return none;

  // Require at least one `key: value` line before believing this is metadata.
  //
  // Without this, a note that merely OPENS with a setext heading —
  //
  //   ---
  //   Chapter 1
  //   ---
  //   Content
  //
  // — would have "Chapter 1" absorbed into the block. It would then render
  // nowhere: the strip only shows parsed key/value pairs, and the editor only
  // ever sees the body. That is strictly worse than the pre-existing behaviour,
  // where the note simply failed the fidelity check and opened in raw mode with
  // every line visible.
  //
  // Every block the platform writes qualifies (`type: inbox`, `id: …`), and a
  // setext heading never can, so this costs nothing on the notes being targeted.
  const inner = lines.slice(1, close);
  if (!inner.some(looksLikeYamlPair)) return none;

  // Consume the blank lines that separate the block from the body so the body
  // starts at real content; they belong to the block for round-trip purposes.
  let bodyStart = close + 1;
  while (bodyStart < lines.length && lines[bodyStart].trim() === "") bodyStart++;

  return {
    frontmatter: lines.slice(0, bodyStart).join("\n"),
    body: lines.slice(bodyStart).join("\n"),
  };
}

// looksLikeYamlPair recognises a `key: value` line — a leading key of
// unspaced word characters followed by a colon. Deliberately strict about the
// key so an ordinary prose line that happens to contain a colon
// ("Chapter 1: The Beginning") is not mistaken for metadata.
function looksLikeYamlPair(line: string): boolean {
  return /^\s*[\w.-]+\s*:/.test(line);
}

/**
 * joinFrontmatter re-attaches a block taken from splitFrontmatter to a body.
 *
 * Inverse of splitFrontmatter for any document it split: joining the two halves
 * of a split returns the original bytes exactly. The blank-line separator is
 * re-inserted because splitFrontmatter absorbed it into the block; without it a
 * saved note would drift toward `---# Heading` over repeated round trips.
 */
export function joinFrontmatter(frontmatter: string, body: string): string {
  if (!frontmatter) return body;
  const fm = frontmatter.replace(/\n+$/, "");
  const rest = body.replace(/^\n+/, "");
  // A note that is nothing but metadata gets no separator: appending one would
  // add a trailing blank line on every open/save cycle, so a file the user never
  // edited would keep growing. Round-trip identity is the contract here.
  if (rest === "") return `${fm}\n`;
  return `${fm}\n\n${rest}`;
}

/**
 * parseFrontmatterFields renders the block as ordered key/value pairs for the
 * read-only metadata strip shown above the document.
 *
 * Deliberately a shallow `key: value` scan, NOT a YAML parser: the values are
 * only ever displayed, the blocks this codebase writes are flat string maps
 * (see internal/vault/reflect.go's `frontmatter()`), and pulling in a YAML
 * dependency to render a caption would be the tail wagging the dog. A line this
 * cannot parse is skipped from the display — it is still preserved on save,
 * since saving uses the raw block, never this parse.
 */
export function parseFrontmatterFields(frontmatter: string): Array<{ key: string; value: string }> {
  const out: Array<{ key: string; value: string }> = [];
  for (const line of frontmatter.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed === "---") continue;
    const idx = trimmed.indexOf(":");
    if (idx <= 0) continue;
    const key = trimmed.slice(0, idx).trim();
    const value = trimmed.slice(idx + 1).trim();
    if (key) out.push({ key, value });
  }
  return out;
}
