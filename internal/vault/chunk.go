package vault

import (
	"strings"
)

// targetChunkChars is the size a chunk aims for. Big enough that a retrieved
// chunk answers the question on its own (the whole point of returning chunks
// rather than matching lines), small enough that several fit inside a tool
// result's byte cap.
const targetChunkChars = 1500

// Chunk is one retrievable passage of a note.
type Chunk struct {
	Path    string // vault-relative path of the source file
	Heading string // heading trail, e.g. "Trip plan > Flights" ("" if none)
	Text    string // the passage
	Line    int    // 1-based line in the file where this passage starts
}

// ChunkMarkdown splits a markdown document at heading boundaries. Headings are
// the author's own structure, so a section is the natural unit of meaning — and
// carrying the heading trail means a retrieved chunk states where it came from
// without the reader needing the rest of the file.
//
// A section longer than the target is split further, on paragraph boundaries, so
// one long note cannot monopolise a result set. Splitting prefers not to cut
// mid-line, falling back to mid-line-but-never-mid-sentence only when a single
// physical line is itself oversized (unwrapped prose with no embedded newline).
func ChunkMarkdown(path, content string) []Chunk {
	lines := strings.Split(content, "\n")
	var (
		out      []Chunk
		trail    []string
		curLines []string
		curStart = 1
		curHead  string
	)

	flush := func() {
		text := strings.TrimSpace(strings.Join(curLines, "\n"))
		curLines = nil
		if text == "" {
			return
		}
		for _, part := range splitOversized(text) {
			out = append(out, Chunk{Path: path, Heading: curHead, Text: part, Line: curStart})
		}
	}

	for i, line := range lines {
		if level, title, ok := parseHeading(line); ok {
			flush()
			trail = updateTrail(trail, level, title)
			curHead = strings.Join(trail, " > ")
			curStart = i + 1
			continue
		}
		curLines = append(curLines, line)
	}
	flush()
	return out
}

// ChunkPlain splits non-markdown text purely by size. Used for converted
// documents and other text files that carry no heading structure.
func ChunkPlain(path, content string) []Chunk {
	text := strings.TrimSpace(content)
	if text == "" {
		return nil
	}
	var out []Chunk
	for _, part := range splitOversized(text) {
		out = append(out, Chunk{Path: path, Text: part, Line: 1})
	}
	return out
}

// parseHeading recognizes an ATX heading line ("## Title") and returns its
// level and text.
func parseHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

// updateTrail replaces the trail from the given level down, so "# A / ## B /
// ## C" yields the trail A > C rather than A > B > C.
func updateTrail(trail []string, level int, title string) []string {
	if level-1 < len(trail) {
		trail = trail[:level-1]
	}
	for len(trail) < level-1 {
		trail = append(trail, "")
	}
	return append(trail, title)
}

// splitOversized breaks text longer than the target into paragraph-aligned
// parts, falling back to line boundaries when a single paragraph is itself
// oversized. It never splits mid-line.
func splitOversized(text string) []string {
	if len(text) <= targetChunkChars {
		return []string{text}
	}
	var out []string
	var cur strings.Builder
	appendPart := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, para := range strings.Split(text, "\n\n") {
		if cur.Len() > 0 && cur.Len()+len(para) > targetChunkChars {
			appendPart()
		}
		if len(para) > targetChunkChars {
			for _, line := range strings.Split(para, "\n") {
				if len(line) > targetChunkChars {
					// A single physical line can itself be oversized — unwrapped
					// prose with no embedded newline at all (e.g. a converted
					// document, or the pathological case of one giant line). Line
					// boundaries alone can't help here, so fall back one level
					// further to sentence boundaries: the finest granularity we
					// split at, so a chunk edge always falls between complete
					// sentences and never inside a word.
					for _, sentence := range splitSentences(line) {
						if cur.Len() > 0 && cur.Len()+len(sentence) > targetChunkChars {
							appendPart()
						}
						cur.WriteString(sentence)
					}
					continue
				}
				if cur.Len() > 0 && cur.Len()+len(line) > targetChunkChars {
					appendPart()
				}
				cur.WriteString(line)
				cur.WriteString("\n")
			}
			continue
		}
		cur.WriteString(para)
		cur.WriteString("\n\n")
	}
	appendPart()
	return out
}

// splitSentences splits s right after each ". " boundary, keeping the
// trailing space with the sentence it ends so words are never fused when
// parts are later joined. This is the deepest fallback splitOversized uses —
// coarser fallbacks (paragraph, line) are tried first and only give way to
// this one when a single line is itself larger than the target.
func splitSentences(s string) []string {
	var out []string
	start := 0
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == ' ' {
			out = append(out, s[start:i+2])
			start = i + 2
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
