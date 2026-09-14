package chat

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/vault"
)

// maxReferencedFiles bounds how many files one message can pull into context.
//
// Three is enough for "compare notes/a.md with notes/b.md" and small enough
// that a message listing a folder's worth of paths cannot spend the whole
// context on files nobody asked about.
const maxReferencedFiles = 3

// maxInlinedNote is the largest file included verbatim; above it the block
// carries the file's SHAPE (vault.MapFile) instead.
//
// Anchored to what read_file itself returns in a single call: inlining more
// than the tool would makes this block a worse version of the tool, and the
// model can still page the rest. The cost of the map path is stated rather
// than hidden — a shape gives column names and an outline, not an exact
// old_string, so an edit to a large file can still fail.
const maxInlinedNote = 8 << 10

// pathRefRE matches an explicit knowledge-base path: at least one slash and a
// file extension, optionally wrapped in backticks or ordinary quotes.
//
// Requiring BOTH a separator and an extension is what keeps this from firing
// on ordinary prose. "notes/trip.md" is a path; "and/or" is not, and neither
// is a bare title — those stay the search tools' job, because a wrong guess
// here spends context and points the model at the wrong file, which is worse
// than not guessing at all.
var pathRefRE = regexp.MustCompile(`[A-Za-z0-9_./-]+/[A-Za-z0-9_.-]+\.[A-Za-z0-9]{1,8}`)

// ReferencedFiles renders the knowledge-base files a chat message names, so a
// turn that asks for an edit does not have to read and edit in the same breath.
//
// WHY THIS EXISTS. Two turns on one install, same model, same file, minutes
// apart: a cold turn asking "change Status: draft to Status: final" answered
// "Done!" and wrote nothing, while a turn whose PREVIOUS message had made the
// model read the file performed the identical edit correctly. The measured
// difference was whether the content was already in the conversation. The KB's
// "Chat about this file" and "Edit with AI" buttons get that for free, because
// their opening message makes the model read first; a message typed on the chat
// page does not.
//
// Returns "" when the message names nothing resolvable, so callers append it
// unconditionally.
func ReferencedFiles(v *vault.Vault, workspaceID, message string) string {
	if v == nil || strings.TrimSpace(message) == "" {
		return ""
	}
	paths := resolveReferences(v, workspaceID, message)
	if len(paths) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n[Files you referenced]\n")
	// The instruction is load-bearing, not framing. Handing the model a file's
	// full text invites it to answer with a rewritten copy, or to call
	// write_file with the WHOLE document — and a whole-file rewrite is what
	// flattens the rich-text constructs (callouts, toggles, column grids,
	// alignment) that a surgical edit_file leaves untouched. Saying what a
	// change REQUIRES, and that describing one is not making one, is the part
	// that must survive any rewording of this block.
	sb.WriteString("This is the current on-disk content of the file(s) named in the message above.\n")
	sb.WriteString("To CHANGE one, you must call edit_file with an old_string that appears exactly\n")
	sb.WriteString("once — prefer a targeted edit_file over rewriting the whole file with write_file,\n")
	sb.WriteString("which discards formatting you were not asked to touch. Describing the change in\n")
	sb.WriteString("your reply is not making it: if you did not call a tool, the file is unchanged.\n")

	for _, rel := range paths {
		sb.WriteString("\n--- " + rel + " ---\n")
		sb.WriteString(renderReference(v, workspaceID, rel))
	}
	return sb.String()
}

// renderReference returns a file's content, or its shape when it is too large
// to inline.
func renderReference(v *vault.Vault, workspaceID, rel string) string {
	data, err := v.ReadNote(workspaceID, rel)
	if err != nil {
		// Resolution already stat'd the file, so this is a race or a
		// permission fault. Say so plainly rather than dropping the entry:
		// the heading is already written, and a bare heading reads as an
		// empty file.
		return fmt.Sprintf("(could not be read: %v)\n", err)
	}
	if len(data) <= maxInlinedNote {
		out := string(data)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return out
	}
	shape, err := vault.MapFile(v, workspaceID, rel)
	if err != nil {
		return fmt.Sprintf("(too large to inline: %d bytes; use read_file with offset/limit)\n", len(data))
	}
	return shape.Render(maxInlinedNote) +
		"\n(shape only — this file is too large to inline. Use read_file, or\n" +
		"search_files scoped to this path, to get the exact text before editing.)\n"
}

// resolveReferences extracts candidate references and keeps the ones that
// resolve to a real file inside the vault.
func resolveReferences(v *vault.Vault, workspaceID, message string) []string {
	var out []string
	seen := map[string]bool{}

	add := func(rel string) bool {
		rel = strings.Trim(rel, "`'\"")
		if rel == "" || seen[rel] {
			return false
		}
		// vault.Resolve is the security primitive every read path uses: it
		// rejects "..", absolute escapes and anything outside this
		// workspace's vault. A message is untrusted text, so it is the only
		// thing standing between a typed path and an arbitrary read.
		abs, err := v.Resolve(workspaceID, rel)
		if err != nil {
			return false
		}
		// A path that does not exist is skipped SILENTLY. The model still has
		// search_files and glob, and an error block here would teach it to
		// distrust a context that is right the rest of the time.
		fi, err := os.Stat(abs)
		if err != nil || fi.IsDir() {
			return false
		}
		seen[rel] = true
		out = append(out, rel)
		return len(out) >= maxReferencedFiles
	}

	for _, m := range pathRefRE.FindAllString(message, -1) {
		if add(strings.TrimRight(m, ".,;:)]}")) {
			return out
		}
	}

	// Wikilinks cost a full vault walk to resolve, so that index is built only
	// when the message actually contains one.
	if strings.Contains(message, "[[") {
		idx, err := v.BuildLinkIndex(workspaceID)
		if err == nil {
			for _, l := range vault.ParseWikilinks(message) {
				if rel := idx.Resolve(l.Target); rel != "" {
					if add(rel) {
						return out
					}
				}
			}
		}
	}
	return out
}
