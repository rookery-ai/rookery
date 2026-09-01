package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/scanned.pdf is a REAL scanned page: one rasterised image, no text
// layer at all (pdftotext returns a single newline from it). The pre-existing
// textless.pdf could not serve here — it is genuinely blank, so OCR of it
// correctly finds nothing and the test would pass whether or not OCR ran.
func scannedPDF(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "scanned.pdf"))
	if err != nil {
		t.Fatalf("read scanned fixture: %v", err)
	}
	return b
}

// The user-visible bug: a scanned PDF imported as a note containing nothing but
// an apology, on a host where both OCR tools were installed and one of them is a
// declared host tool whose stated purpose is reading scanned documents.
func TestScannedPDFIsReadByOCR(t *testing.T) {
	if pdftoppmPath() == "" || tesseractPath() == "" {
		t.Skip("pdftoppm and tesseract are required for OCR")
	}

	res, err := ToMarkdown(scannedPDF(t), Options{Filename: "scan.pdf"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(res.Markdown, "Quarterly Revenue Report") {
		t.Errorf("OCR did not recover the page text; got:\n%s", res.Markdown)
	}
	if !strings.HasSuffix(res.Extractor, "+ocr") {
		t.Errorf("extractor = %q, want it to record that OCR ran", res.Extractor)
	}
	// The reader must be told the text was guessed from pixels, because OCR
	// makes mistakes a text layer does not.
	if !hasWarningContaining(res.Warnings, "OCR") {
		t.Errorf("no warning declared that the text came from OCR: %v", res.Warnings)
	}
	// The old wording claimed OCR was unavailable. On this host it plainly is.
	if hasWarningContaining(res.Warnings, "OCR is not available") {
		t.Errorf("still claiming OCR is unavailable while using it: %v", res.Warnings)
	}
}

// With the tools absent, the message must name what to install rather than
// asserting that OCR is impossible — the old wording sent the operator away at
// exactly the moment the fix was one package install.
func TestScannedPDFWithoutOCRToolsNamesWhatIsMissing(t *testing.T) {
	defer stubTool(&pdftoppmPath, "")()
	defer stubTool(&tesseractPath, "")()

	res, err := ToMarkdown(scannedPDF(t), Options{Filename: "scan.pdf"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	w := strings.Join(res.Warnings, " ")
	for _, want := range []string{"poppler-utils", "tesseract", "install"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning does not mention %q: %v", want, res.Warnings)
		}
	}
	if strings.HasSuffix(res.Extractor, "+ocr") {
		t.Errorf("extractor claims OCR ran with no tools installed: %q", res.Extractor)
	}
}

// A PDF that HAS a good text layer must never be sent through OCR: it is slow,
// and its output is a guess where an exact reading was already available.
func TestTextPDFDoesNotUseOCR(t *testing.T) {
	if pdftotextPath() == "" {
		t.Skip("pdftotext is required to produce a text layer")
	}
	data, err := os.ReadFile(filepath.Join("testdata", "simple.pdf"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res, err := ToMarkdown(data, Options{Filename: "simple.pdf"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.HasSuffix(res.Extractor, "+ocr") {
		t.Errorf("OCR ran on a PDF with a usable text layer: %q", res.Extractor)
	}
	if !strings.Contains(res.Markdown, "Quarterly Revenue Report") {
		t.Errorf("text layer was not used; got:\n%s", res.Markdown)
	}
}

// pdftoppm names its output page-1.png, page-2.png … page-10.png, and lexical
// order puts page-10 before page-2. Without an explicit numeric sort a
// multi-page scan comes back with its pages shuffled — silently, since every
// page is present and only the order is wrong.
func TestPageIndexOrdersNumerically(t *testing.T) {
	cases := map[string]int{
		"/tmp/x/page-1.png":  1,
		"/tmp/x/page-2.png":  2,
		"/tmp/x/page-10.png": 10,
		"/tmp/x/page-07.png": 7,
		"/tmp/x/page.png":    0,
	}
	for path, want := range cases {
		if got := pageIndexOf(path); got != want {
			t.Errorf("pageIndexOf(%q) = %d, want %d", path, got, want)
		}
	}
}

func hasWarningContaining(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// stubTool swaps a tool-path resolver for the duration of a test, so a host that
// HAS the tool can still exercise the missing-tool branch.
func stubTool(v *func() string, path string) func() {
	prev := *v
	*v = func() string { return path }
	return func() { *v = prev }
}
