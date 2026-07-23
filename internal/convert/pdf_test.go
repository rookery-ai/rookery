package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadPDFFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/simple.pdf")
	if err != nil {
		t.Skipf("pdf fixture missing: %v", err)
	}
	return data
}

func TestPDFPureGo(t *testing.T) {
	data := loadPDFFixture(t)
	// Force the pure-Go path so this branch is covered even on a host that has
	// pdftotext installed.
	orig := pdftotextPath
	pdftotextPath = func() string { return "" }
	defer func() { pdftotextPath = orig }()

	got, err := ToMarkdown(data, Options{Filename: "report.pdf"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindPDF {
		t.Errorf("Kind = %q", got.Kind)
	}
	if got.Extractor != "pure-go" {
		t.Errorf("Extractor = %q, want pure-go", got.Extractor)
	}
	if !strings.Contains(strings.ToLower(got.Markdown), "revenue") {
		t.Errorf("expected extracted text, got:\n%s", got.Markdown)
	}
}

func TestPDFPrefersPdftotextWhenPresent(t *testing.T) {
	if pdftotextPath() == "" {
		t.Skip("pdftotext not installed on this host")
	}
	data := loadPDFFixture(t)
	got, err := ToMarkdown(data, Options{Filename: "report.pdf"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Extractor != "pdftotext" {
		t.Errorf("Extractor = %q, want pdftotext when it is installed", got.Extractor)
	}
	// This is the DEFAULT path on any host with poppler installed — asserting
	// only Extractor lets a regression that corrupts pdftotext's actual output
	// (bad flags, wrong encoding, truncation) pass silently. Mirror
	// TestPDFPureGo's content check so both extractors are pinned the same way.
	if !strings.Contains(strings.ToLower(got.Markdown), "revenue") {
		t.Errorf("expected extracted text, got:\n%s", got.Markdown)
	}
}

// loadTextlessFixture loads the committed textless PDF fixture. Unlike
// loadPDFFixture (which skips when simple.pdf is locally absent), a missing
// COMMITTED fixture here must fail loudly: silently skipping would recreate
// exactly the vacuous-pass defect this test exists to fix — a "PASS" that
// never actually exercised the code path under test.
func loadTextlessFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/textless.pdf")
	if err != nil {
		t.Fatalf("textless.pdf fixture missing (must be committed under testdata/): %v", err)
	}
	return data
}

// A PDF whose text layer yields almost nothing (scanned pages, CID fonts) must
// say so. Silently returning a near-empty body would let a failed extraction
// pass as a successful one — the single most likely way this converter misleads.
//
// testdata/textless.pdf is a genuinely valid, structurally complete PDF (real
// xref table, trailer, a content stream with drawing operators) that simply
// has no text operators on its one page — verified independently against the
// host's pdftotext (exit 0, empty stdout) and pdfinfo (1 page) before being
// committed. The previous fixture here was a 69-byte hand-rolled stub, below
// ledongthuc/pdf's ~100-byte read floor (NewReaderEncrypted seeks to
// end-100, which is negative for a file this small, errors, and leaves its
// scan buffer zeroed so the trailing "%%EOF" check always fails) — so
// pdfToMarkdown always errored and the test's own "if err != nil { return }"
// exited before ever checking Markdown or Warnings. This version asserts
// positively instead of tolerating either outcome.
func TestPDFThinExtractionWarns(t *testing.T) {
	orig := pdftotextPath
	pdftotextPath = func() string { return "" }
	defer func() { pdftotextPath = orig }()

	data := loadTextlessFixture(t)
	got, err := pdfToMarkdown(data, Options{Filename: "scan.pdf"})
	if err != nil {
		t.Fatalf("pdfToMarkdown: %v", err)
	}
	if strings.TrimSpace(got.Markdown) == "" {
		t.Fatal("Markdown must never be empty on a nil error")
	}
	if len(got.Warnings) == 0 {
		t.Fatal("a thin extraction must be recorded as a warning, not passed off as clean text")
	}
	found := false
	for _, w := range got.Warnings {
		if strings.Contains(w, "no text extracted") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a warning mentioning that no text was extracted, got: %v", got.Warnings)
	}
}

// pdftotext appends a form feed after EVERY page, including the last one —
// verified against pdfinfo on this package's own fixtures. Counting feeds
// directly (not +1) is what makes a 1-page document report 1.
func TestPdftotextPageCount(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"one page", "all the content\f", 1},
		{"multi page", "page one\fpage two\fpage three\f", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pdftotextPageCount(tc.text); got != tc.want {
				t.Errorf("pdftotextPageCount(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
}

// TestPdftotextPathPrefersLocalBin pins the SP24-T4 fix: a pdftotext installed
// in the operator's ~/.local/bin is resolved even when it is not on PATH, since
// a service-managed server often has a bare PATH. It shells nothing out — it only
// checks that the resolver returns the local-bin path when a fake executable is
// present there.
func TestPdftotextPathPrefersLocalBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "pdftotext")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := pdftotextPath(); got != fake {
		t.Errorf("pdftotextPath() = %q, want the ~/.local/bin executable %q", got, fake)
	}

	// A non-executable file in ~/.local/bin must NOT be picked (would fail to run).
	// Chmod explicitly — os.WriteFile does not re-apply perms to an existing file.
	if err := os.Chmod(fake, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pdftotextPath(); got == fake {
		t.Errorf("a non-executable ~/.local/bin/pdftotext must be skipped, got %q", got)
	}
}
