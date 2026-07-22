package vault

import (
	"strings"
	"testing"
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
