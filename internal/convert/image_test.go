package convert

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mustReadFixture reads a committed testdata file, failing the test if it is
// missing (mirrors the pattern used by pdf_test.go/ooxml_test.go).
func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// The no-tesseract fallback must always produce the honest stub, on every host.
func TestImageToMarkdown_FallbackWhenNoTesseract(t *testing.T) {
	res, err := imageToMarkdownWith(nil, Options{Filename: "photo.png"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Extractor != "none" {
		t.Errorf("extractor = %q, want none", res.Extractor)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a no-OCR warning")
	}
}

// The OCR path runs only when tesseract is installed.
func TestImageToMarkdown_OCR(t *testing.T) {
	bin, err := exec.LookPath("tesseract")
	if err != nil {
		t.Skip("tesseract not on PATH; skipping OCR path")
	}
	data := mustReadFixture(t, "ocr_sample.png") // small PNG containing the text "HELLO OCR"
	res, err := imageToMarkdownWith(data, Options{Filename: "ocr_sample.png"}, bin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(res.Markdown), "HELLO") {
		t.Errorf("OCR text missing; got: %q", res.Markdown)
	}
	if res.Extractor != "tesseract" {
		t.Errorf("extractor = %q, want tesseract", res.Extractor)
	}
}
