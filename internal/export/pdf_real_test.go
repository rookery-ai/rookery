package export

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

var execCommand = exec.Command

// Every other ToPDF test stubs runEngine, which is how PDF export shipped
// completely dead while this package stayed green: the plumbing was covered and
// no engine's argv had ever been executed once. This test runs a REAL renderer.
//
// It is skipped when no engine is available, so CI without one still passes —
// but on any developer machine or container that has one (including every host
// where `rookery browser install` has been run, which is the common case) it
// exercises the actual command line and asserts a real PDF comes back.
//
// The argv is the interesting part, and it is not obvious. Chromium needs
// --no-pdf-header-footer or it stamps the print date and the temp file:// path
// onto every page; libreoffice ignores the output path it is given and names
// the file after the input; pandoc was removed outright because its argv cannot
// work without a LaTeX engine it does not bundle. None of that is visible to a
// stub.
func TestToPDFWithARealEngine(t *testing.T) {
	eng, binPath, ok := findPDFEngine()
	if !ok {
		t.Skip("no PDF engine available on this host")
	}
	t.Logf("using engine %q at %s", eng.bin, binPath)

	md := []byte("# Quarterly Report\n\n" +
		"Revenue grew twelve percent, where a &lt; b.\n\n" +
		"| Name | Value |\n| --- | --- |\n| Ada | 42 |\n")

	out, err := ToPDF(md, Options{Title: "Quarterly Report"})
	if err != nil {
		t.Fatalf("ToPDF with %s: %v", eng.bin, err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("output is not a PDF (first bytes %q)", out[:min(16, len(out))])
	}
	if len(out) < 500 {
		t.Errorf("PDF is implausibly small (%d bytes) — the engine may have written a stub", len(out))
	}

	// A renderer that emits a valid but EMPTY PDF would pass every check above.
	// Read the text back when a extractor is available, so the assertion is
	// about the document's content and not merely its container.
	if text, ok := pdfTextForTest(t, out); ok {
		if !strings.Contains(text, "Quarterly Report") {
			t.Errorf("exported PDF does not contain the note's heading; got:\n%s", text)
		}
		if !strings.Contains(text, "Ada") {
			t.Errorf("exported PDF does not contain the table contents; got:\n%s", text)
		}
		// The header Chromium adds without --no-pdf-header-footer is the source
		// file:// URL, which would leak a server temp path into the user's file.
		if strings.Contains(text, "file://") || strings.Contains(text, ".html") {
			t.Errorf("exported PDF carries a print header naming the source file; got:\n%s", text)
		}
	}
}

// pdfTextForTest extracts text with pdftotext when it is installed. Reported as
// (text, false) rather than skipping, so the container assertions above still
// run on a host without poppler.
func pdfTextForTest(t *testing.T, pdf []byte) (string, bool) {
	t.Helper()
	bin, err := lookPath("pdftotext")
	if err != nil {
		return "", false
	}
	dir := t.TempDir()
	src := dir + "/out.pdf"
	if err := os.WriteFile(src, pdf, 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	var buf bytes.Buffer
	cmd := execCommand(bin, src, "-")
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return buf.String(), true
}
