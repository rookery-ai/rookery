package convert

import (
	"os"
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
}

// A PDF whose text layer yields almost nothing (scanned pages, CID fonts) must
// say so. Silently returning a near-empty body would let a failed extraction
// pass as a successful one — the single most likely way this converter misleads.
func TestPDFThinExtractionWarns(t *testing.T) {
	orig := pdftotextPath
	pdftotextPath = func() string { return "" }
	defer func() { pdftotextPath = orig }()

	// A structurally valid but text-free PDF.
	minimal := []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
	got, err := pdfToMarkdown(minimal, Options{Filename: "scan.pdf"})
	if err != nil {
		// Erroring is an acceptable outcome; a blank success is not.
		return
	}
	if strings.TrimSpace(got.Markdown) == "" {
		t.Fatal("Markdown must never be empty on a nil error")
	}
	if len(got.Warnings) == 0 {
		t.Error("a thin extraction must be recorded as a warning, not passed off as clean text")
	}
}
