package vault

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateImportFidelity = flag.Bool("update-fidelity", false,
	"rewrite the import-* files in internal/convert/testdata/fidelity from current ImportFile output")

// The fidelity corpus used to hold converter output only — never the note
// ImportFile actually writes, which wraps that output in a title heading, an
// optional conversion-notes line and a provenance line. That wrapper is what
// made EVERY imported note open read-only: the provenance line was written with
// "_…_" emphasis, which the editor re-serializes as "*…*", and a filename with a
// space produced a link destination CommonMark does not accept. internal/convert
// was green, the editor test was green, and no imported note was editable.
//
// So this writes the BODY of real ImportFile notes (frontmatter stripped — the
// editor never round-trips it) into the shared corpus, where
// web/ui/src/pages/kb/convertFidelity.test.ts runs the real checkFidelity over
// them. Regenerate with:
//
//	go test ./internal/vault/ -run TestImportedNoteFidelityCorpus -update-fidelity
func TestImportedNoteFidelityCorpus(t *testing.T) {
	cases := []struct {
		name, filename, title, data string
	}{
		// The shape that broke on the demo install: spaces and a dash in the name.
		{"import-spaced-filename", "Halcyon Labs - Brand refresh brief.csv", "", "Deliverable,Owner\nPricing page,Alex\n"},
		// Characters that are significant in a link label and a destination.
		{"import-awkward-filename", "Q3 report (final) [v2].csv", "", "Region,Sales\nEMEA,120\n"},
		// A caller-supplied title carrying markdown-significant characters.
		{"import-special-title", "notes.csv", "Report: a < b *draft*", "A,B\n1,2\n"},
	}
	corpus := filepath.Join("..", "convert", "testdata", "fidelity")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := New(t.TempDir())
			const ws = "ws1"
			if err := v.EnsureScaffold(ws); err != nil {
				t.Fatalf("scaffold: %v", err)
			}
			res, err := v.ImportFile(ws, ImportInput{Data: []byte(tc.data), Filename: tc.filename, Title: tc.title, DestDir: "notes"})
			if err != nil {
				t.Fatalf("ImportFile: %v", err)
			}
			raw, err := v.ReadNote(ws, res.NotePath)
			if err != nil {
				t.Fatalf("ReadNote: %v", err)
			}
			body := stripFrontmatter(string(raw))
			path := filepath.Join(corpus, tc.name+".md")
			if *updateImportFidelity {
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("write corpus: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s (regenerate with -update-fidelity): %v", path, err)
			}
			if string(want) != body {
				t.Errorf("corpus file %s is stale.\n got: %q\nwant: %q", path, body, want)
			}
		})
	}
}

// The Go half can still pin the two specific shapes, so a regression is caught
// here even on a machine that never runs vitest.
func TestImportedNoteProvenanceLineIsEditorCanonical(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	res, err := v.ImportFile(ws, ImportInput{Data: []byte("A,B\n1,2\n"), Filename: "my report.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	raw, _ := v.ReadNote(ws, res.NotePath)
	body := string(raw)
	if strings.Contains(body, "_Converted from") {
		t.Errorf("provenance still uses underscore emphasis, which the editor rewrites to *: %s", body)
	}
	if !strings.Contains(body, "*Converted from [my report.csv](uploads/my%20report.csv).*") {
		t.Errorf("provenance line is not the editor-canonical form with an encoded destination:\n%s", body)
	}
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
		return strings.TrimLeft(s[4+i+5:], "\n")
	}
	return s
}
