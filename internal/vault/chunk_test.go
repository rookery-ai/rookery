package vault

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestChunkMarkdownSplitsOnHeadings(t *testing.T) {
	doc := `# Trip plan

Intro paragraph.

## Flights

Booked with Wizz on the 3rd.

## Hotels

Staying near the centre.
`
	chunks := ChunkMarkdown("notes/trip.md", doc)
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3 (one per heading section): %+v", len(chunks), chunks)
	}
	if chunks[1].Heading != "Trip plan > Flights" {
		t.Errorf("Heading = %q, want the full trail", chunks[1].Heading)
	}
	if !strings.Contains(chunks[1].Text, "Wizz") {
		t.Errorf("chunk text wrong: %q", chunks[1].Text)
	}
	if chunks[1].Line < 5 {
		t.Errorf("Line = %d, want the heading's line in the file", chunks[1].Line)
	}
	for _, c := range chunks {
		if c.Path != "notes/trip.md" {
			t.Errorf("Path = %q", c.Path)
		}
	}
}

func TestChunkMarkdownSplitsOversizedSection(t *testing.T) {
	// A long section with no subheadings must still be split, or one huge note
	// would monopolise every result.
	body := strings.Repeat("Sentence about budget planning. ", 200) // ~6400 chars
	chunks := ChunkMarkdown("notes/long.md", "# Budget\n\n"+body)
	if len(chunks) < 3 {
		t.Fatalf("a %d-char section should split into several chunks, got %d", len(body), len(chunks))
	}
	for _, c := range chunks {
		if len(c.Text) > targetChunkChars*2 {
			t.Errorf("chunk of %d chars exceeds the bound", len(c.Text))
		}
		if c.Heading != "Budget" {
			t.Errorf("split chunks must keep the section heading, got %q", c.Heading)
		}
	}
}

func TestChunkMarkdownNoHeadings(t *testing.T) {
	chunks := ChunkMarkdown("notes/flat.md", "just a line\nand another\n")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("Heading = %q, want empty for a note with no headings", chunks[0].Heading)
	}
}

func TestChunkSkipsEmpty(t *testing.T) {
	if got := ChunkMarkdown("notes/empty.md", "\n\n   \n"); len(got) != 0 {
		t.Errorf("an empty note should yield no chunks, got %+v", got)
	}
}

func TestChunkPlain(t *testing.T) {
	chunks := ChunkPlain("files/data.txt", strings.Repeat("row of data. ", 400))
	if len(chunks) < 2 {
		t.Fatalf("plain text should split by size, got %d", len(chunks))
	}
	if chunks[0].Path != "files/data.txt" {
		t.Errorf("Path = %q", chunks[0].Path)
	}
}

// TestSplitOversizedEnforcesHardBound pins Finding 1: no input, however
// pathological, may produce a chunk over the bound. Every row here failed
// before the hard rune-boundary fallback existed, because splitSentences (and
// before it, line/paragraph splitting) all bail out to "return the whole
// thing unsplit" when they find no boundary of their own kind.
func TestSplitOversizedEnforcesHardBound(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		// A long URL: no spaces, no ". ", nothing paragraph/line splitting
		// can use.
		{"long URL, no spaces", "https://example.com/path/" + strings.Repeat("a1b2c3d4e5", 700)},
		// Minified JSON: commas and colons but no spaces at all.
		{"minified JSON", strings.Repeat(`"key":"value",`, 1200)},
		// CJK prose using the full-width period — never matches the ASCII
		// ". " boundary splitSentences looks for.
		{"CJK text using full-width period", strings.Repeat("这是一段没有句号分隔符的中文文本。", 400)},
		// Quoted dialogue: has spaces, but every "." is immediately followed
		// by a closing quote, not a space, so splitSentences finds no match.
		{"quoted dialogue", strings.Repeat(`She said "done." `, 600)},
		// A single 100KB token: no whitespace, no punctuation, nothing at
		// all for any boundary-based split to find.
		{"single giant token, no whitespace", strings.Repeat("x", 100_000)},
		// Emoji are multi-byte runes; a naive byte-index cut would corrupt
		// them.
		{"emoji-dense string", strings.Repeat("😀🎉🚀🔥💯", 3000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks := ChunkPlain("files/pathological.txt", tc.text)
			if len(chunks) < 2 {
				t.Fatalf("a %d-byte pathological input must split into multiple chunks, got %d", len(tc.text), len(chunks))
			}
			var reconstructed strings.Builder
			for _, c := range chunks {
				if len(c.Text) > targetChunkChars {
					t.Errorf("chunk of %d bytes exceeds the %d-byte hard bound", len(c.Text), targetChunkChars)
				}
				if !utf8.ValidString(c.Text) {
					t.Errorf("chunk is not valid UTF-8 (a rune was split mid-character): %q", c.Text)
				}
				reconstructed.WriteString(c.Text)
			}
			// Chunk boundaries intentionally trim whitespace (accumulator.flush
			// exists to keep chunks tidy), so compare with whitespace stripped:
			// what this test guards is that no non-whitespace rune was dropped
			// or mangled by the split, not that formatting survives byte-exact.
			if got, want := stripWhitespace(reconstructed.String()), stripWhitespace(tc.text); got != want {
				t.Errorf("chunks do not losslessly reconstruct the input's characters")
			}
		})
	}
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// TestChunkMarkdownIgnoresHashInFencedCode pins Finding 3: a `#` line inside a
// fenced code block must not be read as a heading, split the fence in half,
// or orphan the closing marker.
func TestChunkMarkdownIgnoresHashInFencedCode(t *testing.T) {
	doc := "# Real\n\nintro\n\n```\n# not a heading\ncode line\n```\n\nmore text\n"
	chunks := ChunkMarkdown("notes/fenced.md", doc)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (one section, no spurious heading split): %+v", len(chunks), chunks)
	}
	if chunks[0].Heading != "Real" {
		t.Errorf("Heading = %q, want %q", chunks[0].Heading, "Real")
	}
	if !strings.Contains(chunks[0].Text, "# not a heading") {
		t.Errorf("the fenced # line should survive as literal text, got: %q", chunks[0].Text)
	}
	if !strings.Contains(chunks[0].Text, "```") {
		t.Errorf("fence markers should not be dropped, got: %q", chunks[0].Text)
	}
	if !strings.Contains(chunks[0].Text, "more text") {
		t.Errorf("content after the fence should not be orphaned, got: %q", chunks[0].Text)
	}
}

// TestChunkMarkdownFenceTildeWithInfoString covers the ~~~ fence variant and
// an opening info string ("~~~python"), both of which must still be tracked
// as fence state, not just the plain ``` case.
func TestChunkMarkdownFenceTildeWithInfoString(t *testing.T) {
	doc := "# Title\n\n~~~python\n# comment inside fence\n~~~\n\nafter\n"
	chunks := ChunkMarkdown("notes/tilde.md", doc)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1: %+v", len(chunks), chunks)
	}
	if chunks[0].Heading != "Title" {
		t.Errorf("Heading = %q, want %q", chunks[0].Heading, "Title")
	}
}

// TestChunkMarkdownTrailDropsEmptySegments pins Finding 4's skipped-level
// case: "# A" then "### C" must render "A > C", not "A >  > C".
func TestChunkMarkdownTrailDropsEmptySegments(t *testing.T) {
	doc := "# A\n\nintro\n\n### C\n\nbody\n"
	chunks := ChunkMarkdown("notes/skip.md", doc)
	var found bool
	for _, c := range chunks {
		if c.Heading == "A > C" {
			found = true
		}
		if strings.Contains(c.Heading, "  ") {
			t.Errorf("Heading %q has a double space from an empty skipped-level segment", c.Heading)
		}
	}
	if !found {
		t.Errorf("expected a chunk with Heading %q, got %+v", "A > C", chunks)
	}
}

// TestChunkMarkdownTrailStartingBelowTopLevel pins the other skipped-level
// case: a document starting at "##" must render "Only", not " > Only".
func TestChunkMarkdownTrailStartingBelowTopLevel(t *testing.T) {
	doc := "## Only\n\nbody\n"
	chunks := ChunkMarkdown("notes/deep.md", doc)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Heading != "Only" {
		t.Errorf("Heading = %q, want %q (no leading \" > \")", chunks[0].Heading, "Only")
	}
}

// TestChunkMarkdownHeadingWithNoBodyStillChunked pins Finding 4: a heading
// with no body (a stub section, or a heading-only document) must still
// produce a chunk, or it is invisible to retrieval entirely.
func TestChunkMarkdownHeadingWithNoBodyStillChunked(t *testing.T) {
	doc := "# Intro\n\nsome text\n\n## TODO\n"
	chunks := ChunkMarkdown("notes/stub.md", doc)
	var stub *Chunk
	for i := range chunks {
		if chunks[i].Heading == "Intro > TODO" {
			stub = &chunks[i]
		}
	}
	if stub == nil {
		t.Fatalf("expected a chunk for the heading-only stub section, got %+v", chunks)
	}
	if !strings.Contains(stub.Text, "TODO") {
		t.Errorf("stub chunk should carry the heading text as its body, got %q", stub.Text)
	}
}

// TestChunkMarkdownCRLFNormalized pins Finding 4: a stray \r must not survive
// into chunked text.
func TestChunkMarkdownCRLFNormalized(t *testing.T) {
	doc := "# Title\r\n\r\nline one\r\nline two\r\n"
	chunks := ChunkMarkdown("notes/crlf.md", doc)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if strings.Contains(chunks[0].Text, "\r") {
		t.Errorf("chunk text still contains a stray \\r: %q", chunks[0].Text)
	}
}

// TestChunkMarkdownStripsLeadingFrontmatter pins half of Fix 4: a leading YAML
// frontmatter block (as renderImportedNote writes for every imported document)
// must never become its own retrievable chunk — it repeats the title/filename,
// which otherwise lets it score deceptively well and compete with real content
// for the retrieval budget.
func TestChunkMarkdownStripsLeadingFrontmatter(t *testing.T) {
	doc := "---\n" +
		"title: \"Quarterly Expenses\"\n" +
		"source: \"expenses.csv\"\n" +
		"kind: csv\n" +
		"---\n\n" +
		"# Quarterly Expenses\n\n" +
		"rent,900\ngroceries,240\n"
	chunks := ChunkMarkdown("notes/expenses.md", doc)
	for _, c := range chunks {
		if strings.Contains(c.Text, "source:") || strings.Contains(c.Text, "kind: csv") {
			t.Errorf("frontmatter leaked into a chunk: %+v", c)
		}
	}
	var found bool
	for _, c := range chunks {
		if strings.Contains(c.Text, "rent,900") {
			found = true
		}
	}
	if !found {
		t.Errorf("real content must still be chunked, got %+v", chunks)
	}
}

// TestChunkMarkdownHorizontalRuleNotMistakenForFrontmatter proves the
// narrowness of frontmatterEnd: a "---" used as a markdown horizontal rule
// mid-document (never on line 0) must never cause content to be dropped.
func TestChunkMarkdownHorizontalRuleNotMistakenForFrontmatter(t *testing.T) {
	doc := "# Notes\n\nFirst part.\n\n---\n\nSecond part after the rule.\n"
	chunks := ChunkMarkdown("notes/rule.md", doc)
	var all strings.Builder
	for _, c := range chunks {
		all.WriteString(c.Text)
	}
	if !strings.Contains(all.String(), "First part") || !strings.Contains(all.String(), "Second part after the rule") {
		t.Errorf("content around a mid-document horizontal rule must be preserved, got %+v", chunks)
	}
}

// TestChunkMarkdownUnclosedLeadingDelimiterNotStripped proves a document that
// merely STARTS with "---" but never closes it is left alone rather than
// guessed at (guessing wrong would silently eat real content as metadata).
func TestChunkMarkdownUnclosedLeadingDelimiterNotStripped(t *testing.T) {
	doc := "---\n\nThis document starts with a rule but has no closing delimiter.\n"
	chunks := ChunkMarkdown("notes/unclosed.md", doc)
	var all strings.Builder
	for _, c := range chunks {
		all.WriteString(c.Text)
	}
	if !strings.Contains(all.String(), "starts with a rule") {
		t.Errorf("content must be preserved when no closing delimiter exists, got %+v", chunks)
	}
}
