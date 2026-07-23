package vault

import (
	"strings"
	"unicode/utf8"
)

// targetChunkChars is the size a chunk aims for. Big enough that a retrieved
// chunk answers the question on its own (the whole point of returning chunks
// rather than matching lines), small enough that several fit inside a tool
// result's byte cap.
//
// This is the HARD bound, not a target in the aspirational sense: chunks feed
// BM25 retrieval and are serialized into a byte-capped LLM tool result, so a
// chunk that exceeds it breaks the contract the caller depends on. Every path
// through splitOversized is built to guarantee no output ever exceeds it,
// however pathological the input (see hardSplitWindow).
const targetChunkChars = 1500

// Chunk is one retrievable passage of a note.
type Chunk struct {
	Path    string // vault-relative path of the source file
	Heading string // heading trail, e.g. "Trip plan > Flights" ("" if none)
	Text    string // the passage
	Line    int    // 1-based line where the section began; every fragment produced by splitting an oversized section reports this same line, not its own offset within the section
}

// ChunkMarkdown splits a markdown document at heading boundaries. Headings are
// the author's own structure, so a section is the natural unit of meaning — and
// carrying the heading trail means a retrieved chunk states where it came from
// without the reader needing the rest of the file.
//
// A section longer than the target is split further (see splitOversized for
// the guarantee that provides). A `#` line inside a fenced code block (``` or
// ~~~) is never treated as a heading, so a shell/Python snippet in a note
// can't fork off a spurious section or split the fence in half. A heading
// with no body still yields one chunk (carrying the heading text itself) so a
// stub section isn't invisible to retrieval.
//
// A leading YAML frontmatter block (see frontmatterEnd) is skipped entirely,
// never emitted as its own chunk: every imported document's frontmatter
// repeats the title/filename (see renderImportedNote), so left unstripped it
// becomes a passage that scores deceptively well on exactly the terms a
// filename-based query would use — competing with, and sometimes beating, the
// real content for the retrieval budget.
func ChunkMarkdown(path, content string) []Chunk {
	lines := strings.Split(normalizeLineEndings(content), "\n")
	fmEnd := frontmatterEnd(lines)
	lines = lines[fmEnd:]
	var (
		out       []Chunk
		trail     []string
		curLines  []string
		curStart  = fmEnd + 1
		curHead   string
		inFence   bool
		fenceChar byte
		fenceLen  int
	)

	flush := func() {
		text := strings.TrimSpace(strings.Join(curLines, "\n"))
		curLines = nil
		if text == "" {
			if curHead == "" {
				// No heading and no content — e.g. blank leading whitespace,
				// or an all-empty document. Nothing worth keeping.
				return
			}
			// A heading with no body (a "## TODO" stub, or a heading-only
			// document) would otherwise be entirely absent from retrieval.
			// Fall back to the heading text itself so it stays findable.
			text = curHead
		}
		for _, part := range splitOversized(text) {
			out = append(out, Chunk{Path: path, Heading: curHead, Text: part, Line: curStart})
		}
	}

	for i, line := range lines {
		if !inFence {
			if ch, n, ok := fenceMarker(line); ok {
				inFence, fenceChar, fenceLen = true, ch, n
				curLines = append(curLines, line)
				continue
			}
			if level, title, ok := parseHeading(line); ok {
				flush()
				trail = updateTrail(trail, level, title)
				curHead = joinTrail(trail)
				curStart = fmEnd + i + 1
				continue
			}
			curLines = append(curLines, line)
			continue
		}
		if fenceCloses(line, fenceChar, fenceLen) {
			inFence = false
		}
		curLines = append(curLines, line)
	}
	flush()
	return out
}

// frontmatterEnd returns the index of the first content line after a leading
// YAML frontmatter block, or 0 if lines does not start with one. Deliberately
// narrow, on purpose: only a "---" that is LITERALLY the first line of the
// file opens frontmatter, and only a later line that is EXACTLY "---" on its
// own closes it. A "---" used as a markdown horizontal rule mid-document is
// never mistaken for this, because this function only ever looks at line 0
// for the opening delimiter — it never scans for one. A leading "---" that
// never finds a closing "---" is left alone too (returns 0): guessing wrong
// here would silently eat real content as if it were metadata, which is worse
// than occasionally leaving a genuine frontmatter block unstripped.
func frontmatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return i + 1
		}
	}
	return 0
}

// ChunkPlain splits non-markdown text purely by size. Used for converted
// documents and other text files that carry no heading structure — PDF/DOCX
// extraction routinely yields one giant unwrapped paragraph, which is exactly
// the case splitOversized's hard fallback exists for.
func ChunkPlain(path, content string) []Chunk {
	text := strings.TrimSpace(normalizeLineEndings(content))
	if text == "" {
		return nil
	}
	var out []Chunk
	for _, part := range splitOversized(text) {
		out = append(out, Chunk{Path: path, Text: part, Line: 1})
	}
	return out
}

// normalizeLineEndings collapses CRLF (and lone CR, for old Mac-style text)
// to LF before chunking. Left alone, a stray \r survives stuck to the end of
// every non-final line reflected into a section's text.
func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
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

// fenceMarker detects an opening code-fence line — 3 or more of the same
// fence character (``` or ~~~), optionally followed by an info string like
// "python" — so a `#` comment inside the fenced block downstream isn't
// mistaken for a markdown heading.
func fenceMarker(line string) (ch byte, n int, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return 0, 0, false
	}
	ch = trimmed[0]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	return ch, n, true
}

// fenceCloses reports whether line closes a fence opened with the given
// character repeated minLen times: the same character, at least that many
// times, with nothing but whitespace after it — an info string only belongs
// on the opening fence, so a line still carrying one doesn't count as a close.
func fenceCloses(line string, ch byte, minLen int) bool {
	trimmed := strings.TrimLeft(line, " \t")
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	return n >= minLen && strings.TrimSpace(trimmed[n:]) == ""
}

// updateTrail replaces the trail from the given level down, so "# A / ## B /
// ## C" yields the trail A > C rather than A > B > C. Skipped levels leave an
// empty placeholder in the slice (so later level bookkeeping stays correct);
// joinTrail is responsible for dropping those placeholders when rendering.
func updateTrail(trail []string, level int, title string) []string {
	if level-1 < len(trail) {
		trail = trail[:level-1]
	}
	for len(trail) < level-1 {
		trail = append(trail, "")
	}
	return append(trail, title)
}

// joinTrail renders a heading trail, dropping empty placeholders left by
// skipped levels — otherwise "# A" then "### C" renders "A >  > C", and a
// document starting at "##" renders " > Only".
func joinTrail(trail []string) string {
	parts := make([]string, 0, len(trail))
	for _, t := range trail {
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " > ")
}

// splitOversized breaks text longer than the target into chunks that respect
// the bound. The size bound is the HARD invariant — a chunk is serialized
// into a byte-capped tool result downstream, so nothing may leave this
// function over target size, no matter how pathological the input.
//
// Splitting prefers, in priority order: paragraph boundaries, then line
// boundaries, then sentence boundaries ("KATEX_INLINE_OPEN. "), then a whitespace boundary
// found near the target cut point, and finally — only when none of those
// exist within reach, as with an unwrapped URL, minified JSON, a run of CJK
// text, or one giant token — a straight cut at the target size. That last
// cut always lands on a UTF-8 rune boundary, never mid-character, but it CAN
// land mid-word for pathological input. That is the intended trade-off: an
// oversized chunk breaks the contract the caller depends on, while a mid-word
// cut merely reads awkwardly.
func splitOversized(text string) []string {
	if len(text) <= targetChunkChars {
		return []string{text}
	}
	acc := &accumulator{}
	for _, para := range strings.Split(text, "\n\n") {
		if len(para) > targetChunkChars {
			for _, line := range strings.Split(para, "\n") {
				if len(line) > targetChunkChars {
					// A single physical line can itself be oversized —
					// unwrapped prose with no embedded newline at all (e.g. a
					// converted document, or the pathological case of one
					// giant line). Line boundaries alone can't help here, so
					// fall back to sentence boundaries, and hardSplitWindow
					// underneath that guarantees the bound even when no
					// sentence boundary exists either.
					for _, sentence := range splitSentences(line) {
						for _, piece := range hardSplitWindow(sentence) {
							acc.add(piece, "")
						}
					}
					continue
				}
				acc.add(line, "\n")
			}
			continue
		}
		acc.add(para, "\n\n")
	}
	acc.flush()
	return acc.out
}

// accumulator packs fragments (that are each individually within the bound)
// into as few chunks as possible, flushing before a fragment plus its
// trailing separator would push the running total over target. Budgeting the
// separator into the overflow check (not just the fragment) means the join
// character itself can never be what tips a chunk over the bound.
type accumulator struct {
	cur strings.Builder
	out []string
}

func (a *accumulator) add(fragment, sep string) {
	need := len(fragment) + len(sep)
	if a.cur.Len() > 0 && a.cur.Len()+need > targetChunkChars {
		a.flush()
	}
	a.cur.WriteString(fragment)
	a.cur.WriteString(sep)
}

func (a *accumulator) flush() {
	if s := strings.TrimSpace(a.cur.String()); s != "" {
		a.out = append(a.out, s)
	}
	a.cur.Reset()
}

// splitSentences splits s right after each ". " boundary, keeping the
// trailing space with the sentence it ends so words are never fused when
// parts are later joined. This is tried before the hard window fallback
// below — coarser fallbacks (paragraph, line) come before this, and the
// window cut comes after, only for whatever this can't bound on its own.
//
// When s contains no ". " at all (a URL, minified JSON, CJK text using a
// full-width terminator, one giant token), this returns s unsplit — it is
// deliberately NOT responsible for enforcing the size bound; hardSplitWindow
// is.
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

// hardSplitWindow is the terminal fallback: the last thing standing between
// arbitrary input and a chunk that violates the size bound. splitSentences
// hands it whatever it couldn't bound — a fragment with no ". " boundary at
// all — so this must handle the worst case: no whitespace, no punctuation,
// nothing but bytes.
//
// It prefers to back off to the nearest whitespace or ". " sentence boundary
// within a small window before the target cut point, so ordinary prose still
// breaks at a readable point even when hit by this fallback. When no such
// boundary exists in the window (a long URL, minified JSON, one giant token),
// it cuts anyway — but always on a UTF-8 rune boundary, since slicing a Go
// string by raw byte index can land inside a multi-byte character (any CJK
// or emoji rune) and corrupt it. A mid-word cut is an acceptable trade-off
// here; a chunk over the bound, or a mangled rune, is not.
func hardSplitWindow(s string) []string {
	if len(s) <= targetChunkChars {
		return []string{s}
	}
	const window = targetChunkChars / 4
	var out []string
	for len(s) > targetChunkChars {
		cut := runeSafeCut(s, targetChunkChars)
		if b := backOffToBoundary(s, cut, window); b > 0 {
			cut = b
		}
		if cut == 0 {
			// Defensive only: targetChunkChars is a package constant known to
			// be >0, so runeSafeCut always finds a boundary at index >=1 for a
			// non-empty s. Avoids an infinite loop if that ever changes.
			cut = runeSafeCut(s, 1)
			if cut == 0 {
				cut = len(s)
			}
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if len(s) > 0 {
		out = append(out, s)
	}
	return out
}

// backOffToBoundary scans backward from cut, within window bytes, for a
// nicer place to break: right after whitespace, or right after a ". "
// sentence boundary. Returns 0 (meaning "use the hard rune-boundary cut
// instead") when nothing suitable is found within the window.
func backOffToBoundary(s string, cut, window int) int {
	limit := cut - window
	if limit < 0 {
		limit = 0
	}
	for i := cut; i > limit; i-- {
		if !utf8.RuneStart(s[i]) {
			continue
		}
		switch {
		case s[i-1] == ' ' || s[i-1] == '\t' || s[i-1] == '\n':
			return i
		case i >= 2 && s[i-2] == '.' && s[i-1] == ' ':
			return i
		}
	}
	return 0
}
