package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/vault"
)

func refFixture(t *testing.T, files map[string]string) (*vault.Vault, string) {
	t.Helper()
	v := vault.New(t.TempDir())
	ws := "ws-1"
	for rel, body := range files {
		abs, err := v.Resolve(ws, rel)
		if err != nil {
			t.Fatalf("resolve %s: %v", rel, err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o640); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return v, ws
}

func TestReferencedFilesFindsAPathHoweverItIsWritten(t *testing.T) {
	v, ws := refFixture(t, map[string]string{"notes/trip.md": "Status: draft\n"})

	for name, msg := range map[string]string{
		"backticked":  "Update `notes/trip.md` — change Status: draft to final.",
		"bare":        "Update notes/trip.md — change Status: draft to final.",
		"trailing":    "Please look at notes/trip.md.",
		"parenthetic": "See the note (notes/trip.md) for details.",
	} {
		t.Run(name, func(t *testing.T) {
			got := ReferencedFiles(v, ws, msg)
			if !strings.Contains(got, "notes/trip.md") {
				t.Errorf("path not detected in %q; got:\n%s", msg, got)
			}
			if !strings.Contains(got, "Status: draft") {
				t.Errorf("content not inlined for %q; got:\n%s", msg, got)
			}
		})
	}
}

// Ordinary prose must not trigger a lookup. A wrong guess spends context and
// points the model at a file nobody asked about, which is worse than leaving
// the search tools to do their job.
func TestReferencedFilesIgnoresProse(t *testing.T) {
	v, ws := refFixture(t, map[string]string{"notes/trip.md": "x\n"})
	for _, msg := range []string{
		"should I use this and/or that?",
		"tell me about my trip note",
		"what is 3/4 of 12?",
		"",
	} {
		if got := ReferencedFiles(v, ws, msg); got != "" {
			t.Errorf("prose %q produced a block:\n%s", msg, got)
		}
	}
}

func TestReferencedFilesResolvesAWikilink(t *testing.T) {
	v, ws := refFixture(t, map[string]string{"notes/trip.md": "Status: draft\n"})
	got := ReferencedFiles(v, ws, "update [[trip]] please")
	if !strings.Contains(got, "notes/trip.md") || !strings.Contains(got, "Status: draft") {
		t.Errorf("wikilink not resolved; got:\n%s", got)
	}
}

// A message is untrusted text, so vault.Resolve is the only thing between a
// typed path and an arbitrary read.
func TestReferencedFilesRefusesEscapes(t *testing.T) {
	v, ws := refFixture(t, map[string]string{"notes/trip.md": "x\n"})
	for _, msg := range []string{
		"read ../../etc/passwd please",
		"read /etc/passwd please",
		"read notes/../../../etc/hosts",
	} {
		got := ReferencedFiles(v, ws, msg)
		if strings.Contains(got, "root:") || strings.Contains(got, "passwd") || strings.Contains(got, "hosts") {
			t.Errorf("escape %q was not refused; got:\n%s", msg, got)
		}
	}
}

// Skipped silently: the model still has search_files, and an error block would
// teach it to distrust a context that is right the rest of the time.
func TestReferencedFilesSkipsAMissingPath(t *testing.T) {
	v, ws := refFixture(t, map[string]string{"notes/trip.md": "x\n"})
	if got := ReferencedFiles(v, ws, "update notes/nope.md"); got != "" {
		t.Errorf("a missing path produced a block:\n%s", got)
	}
}

func TestReferencedFilesCapsTheCount(t *testing.T) {
	files := map[string]string{}
	var names []string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		rel := "notes/" + n + ".md"
		files[rel] = "body " + n + "\n"
		names = append(names, rel)
	}
	v, ws := refFixture(t, files)
	got := ReferencedFiles(v, ws, "compare "+strings.Join(names, " and "))
	if n := strings.Count(got, "--- notes/"); n != maxReferencedFiles {
		t.Errorf("included %d files, want the %d cap; got:\n%s", n, maxReferencedFiles, got)
	}
}

// Over the inline cap the block carries the file's SHAPE. The limitation is
// real and stated: a shape is not an exact old_string.
func TestReferencedFilesRendersAShapeForALargeFile(t *testing.T) {
	big := "# Big\n\n" + strings.Repeat("filler line to push this well past the inline cap\n", 400)
	v, ws := refFixture(t, map[string]string{"notes/big.md": big})
	got := ReferencedFiles(v, ws, "summarise notes/big.md")
	if strings.Contains(got, strings.Repeat("filler line to push this well past the inline cap\n", 50)) {
		t.Error("a large file was inlined verbatim instead of mapped")
	}
	if !strings.Contains(got, "shape only") {
		t.Errorf("the map path did not declare itself; got:\n%s", got)
	}
}

// The hazard this block creates for itself: handing the model a file's full
// text invites a whole-file write_file, which discards the rich-text
// constructs (callouts, toggles, column grids, alignment) that a surgical
// edit_file leaves untouched. A rewording that drops this steer reintroduces
// exactly the formatting loss the feature was meant to avoid.
func TestReferencedFilesTellsTheModelToEditNotRewrite(t *testing.T) {
	v, ws := refFixture(t, map[string]string{"notes/trip.md": "Status: draft\n"})
	got := ReferencedFiles(v, ws, "update notes/trip.md")
	for _, want := range []string{"edit_file", "old_string", "write_file"} {
		if !strings.Contains(got, want) {
			t.Errorf("the block does not mention %q; got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "not making it") {
		t.Errorf("the block does not say describing a change is not making it; got:\n%s", got)
	}
}
