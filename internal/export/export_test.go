package export

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleMarkdown exercises every block/inline construct the exporters claim to
// support, so one fixture backs the HTML and DOCX assertions.
const sampleMarkdown = "# Project Plan\n\n" +
	"An **intro** paragraph with _italics_, `inline code`, and a [link](https://example.com).\n\n" +
	"## Tasks\n\n" +
	"- first bullet\n" +
	"- second bullet\n" +
	"  - nested bullet\n\n" +
	"1. step one\n" +
	"2. step two\n\n" +
	"> a quoted line\n\n" +
	"```\ncode block line 1\ncode block line 2\n```\n\n" +
	"| Name | Role |\n| --- | --- |\n| Ada | Engineer |\n\n" +
	"---\n\n" +
	"See also [[Other Note|the other note]] and [[Bare Target]].\n"

func TestToHTML_StructureAndEscaping(t *testing.T) {
	out, err := ToHTML([]byte(sampleMarkdown), Options{Title: "Project Plan"})
	if err != nil {
		t.Fatalf("ToHTML: %v", err)
	}
	s := string(out)

	// Self-contained document scaffolding.
	for _, want := range []string{
		"<!DOCTYPE html>",
		"<title>Project Plan</title>",
		"<style>", // inlined CSS, no external stylesheet
	} {
		if !strings.Contains(s, want) {
			t.Errorf("HTML missing %q", want)
		}
	}

	// Block structure from goldmark + GFM.
	for _, want := range []string{
		"<h1", "Project Plan",
		"<h2", "Tasks",
		"<ul>", "<li>", "first bullet",
		"<ol>", "step one",
		"<blockquote>", "quoted line",
		"<pre>", "<code>", "code block line 1",
		"<table>", "<th>", "Ada",
		"<hr", // thematic break
	} {
		if !strings.Contains(s, want) {
			t.Errorf("HTML missing expected structure %q", want)
		}
	}

	// External link preserved.
	if !strings.Contains(s, `href="https://example.com"`) {
		t.Errorf("external link not preserved: %s", s)
	}

	// Wikilinks flattened to display text, brackets gone.
	if strings.Contains(s, "[[") || strings.Contains(s, "]]") {
		t.Errorf("wikilink brackets leaked into HTML")
	}
	if !strings.Contains(s, "the other note") || !strings.Contains(s, "Bare Target") {
		t.Errorf("wikilink display text missing")
	}
}

func TestToHTML_InjectionIsNeutralized(t *testing.T) {
	// A raw <script> in the note must never appear as a live tag in the output.
	md := "# Note\n\nHello <script>alert('xss')</script> world.\n\n" +
		"<div onclick=\"steal()\">click</div>\n"
	out, err := ToHTML([]byte(md), Options{Title: "Note"})
	if err != nil {
		t.Fatalf("ToHTML: %v", err)
	}
	s := string(out)

	// The one true safety assertion: no live <script> tag survived. goldmark's
	// safe default drops raw HTML rather than entity-escaping it, so we assert
	// absence of the tag, not a specific escaped form.
	if strings.Contains(s, "<script>") {
		t.Errorf("live <script> tag survived into HTML: %s", s)
	}
	if strings.Contains(s, "onclick=") {
		t.Errorf("raw event-handler HTML survived into output: %s", s)
	}
}

func TestToDOCX_ValidPackageAndContent(t *testing.T) {
	out, err := ToDOCX([]byte(sampleMarkdown), Options{Title: "Project Plan"})
	if err != nil {
		t.Fatalf("ToDOCX: %v", err)
	}

	files := readZip(t, out)

	// All four required OOXML parts are present.
	for _, part := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/_rels/document.xml.rels",
		"word/document.xml",
	} {
		if _, ok := files[part]; !ok {
			t.Errorf("docx missing required part %q", part)
		}
	}

	doc := files["word/document.xml"]

	assertContains(t, doc, `<w:pStyle w:val="Heading1"/>`, "heading1 style")
	assertContains(t, doc, `<w:pStyle w:val="Heading2"/>`, "heading2 style")
	// Heading text may be split across runs at word boundaries, so assert a
	// single word rather than the whole title.
	assertContains(t, doc, "Project", "heading text")
	assertContains(t, doc, `<w:t xml:space="preserve">`, "space-preserving runs")
	assertContains(t, doc, "<w:b/>", "bold run")
	assertContains(t, doc, "<w:i/>", "italic run")
	assertContains(t, doc, "<w:tbl>", "table")
	assertContains(t, doc, "<w:tblGrid>", "table grid")
	assertContains(t, doc, "<w:hyperlink r:id=", "hyperlink run")
	assertContains(t, doc, "code block line 1", "code block text")
	assertContains(t, doc, "Ada", "table cell text")

	// Wikilink brackets must not survive into the doc.
	if strings.Contains(doc, "[[") {
		t.Errorf("wikilink brackets leaked into docx")
	}

	// The hyperlink relationship exists and is external.
	rels := files["word/_rels/document.xml.rels"]
	assertContains(t, rels, `Target="https://example.com"`, "hyperlink rel target")
	assertContains(t, rels, `TargetMode="External"`, "external target mode")

	// The r:id used in the body must be defined in the rels part.
	id := between(doc, `<w:hyperlink r:id="`, `"`)
	if id == "" {
		t.Fatalf("no hyperlink id in document.xml")
	}
	assertContains(t, rels, `Id="`+id+`"`, "matching rels id for "+id)
}

func TestToDOCX_WhitespacePreservedAcrossRuns(t *testing.T) {
	// "hello world" split by bold must not fuse into "helloworld".
	out, err := ToDOCX([]byte("hello **world** there\n"), Options{})
	if err != nil {
		t.Fatalf("ToDOCX: %v", err)
	}
	doc := readZip(t, out)["word/document.xml"]
	// The trailing space before the bold run is preserved as its own run.
	assertContains(t, doc, `<w:t xml:space="preserve">hello </w:t>`, "leading run keeps trailing space")
	assertContains(t, doc, `<w:t xml:space="preserve">world</w:t>`, "bold run text")
}

func TestToDOCX_XMLEscaping(t *testing.T) {
	out, err := ToDOCX([]byte("A & B < C > D\n"), Options{})
	if err != nil {
		t.Fatalf("ToDOCX: %v", err)
	}
	doc := readZip(t, out)["word/document.xml"]
	if strings.Contains(doc, "A & B") {
		t.Errorf("ampersand not escaped in run text")
	}
	assertContains(t, doc, "&amp;", "escaped ampersand")
}

func TestAvailableFormats(t *testing.T) {
	defer stubBundledChromium("")()
	// HTML and DOCX are always available regardless of host.
	restore := stubLookPath(func(string) (string, error) { return "", errors.New("not found") })
	defer restore()

	f := AvailableFormats()
	if !f.HTML || !f.DOCX {
		t.Errorf("HTML and DOCX must always be available, got %+v", f)
	}
	if f.PDF {
		t.Errorf("PDF must be false when no engine is on PATH")
	}

	// With an engine present, PDF flips true.
	restore2 := stubLookPath(func(bin string) (string, error) {
		if bin == "weasyprint" {
			return "/usr/bin/weasyprint", nil
		}
		return "", errors.New("not found")
	})
	defer restore2()
	if !AvailableFormats().PDF {
		t.Errorf("PDF must be true when weasyprint is on PATH")
	}
}

func TestToPDF_NoEngine(t *testing.T) {
	defer stubBundledChromium("")()
	restore := stubLookPath(func(string) (string, error) { return "", errors.New("not found") })
	defer restore()

	_, err := ToPDF([]byte("# Hi\n"), Options{Title: "Hi"})
	if !errors.Is(err, ErrNoPDFEngine) {
		t.Fatalf("expected ErrNoPDFEngine, got %v", err)
	}
}

func TestToPDF_SuccessWithStubEngine(t *testing.T) {
	defer stubBundledChromium("")()
	// Pretend chromium is installed.
	restoreLP := stubLookPath(func(bin string) (string, error) {
		if bin == "chromium" {
			return "/usr/bin/chromium", nil
		}
		return "", errors.New("not found")
	})
	defer restoreLP()

	// Stub the runner: verify it received the HTML we produced, then write fake
	// PDF bytes to the output path (the real engine's job).
	fakePDF := []byte("%PDF-1.7\nstub\n%%EOF\n")
	var gotEngine string
	restoreRun := stubRunEngine(func(_ context.Context, eng pdfEngine, _, htmlPath, outPath string) error {
		gotEngine = eng.bin
		html, err := os.ReadFile(htmlPath)
		if err != nil {
			return err
		}
		if !strings.Contains(string(html), "<title>Hi</title>") {
			t.Errorf("engine did not receive the rendered HTML doc")
		}
		return os.WriteFile(outPath, fakePDF, 0o600)
	})
	defer restoreRun()

	out, err := ToPDF([]byte("# Hi\n"), Options{Title: "Hi"})
	if err != nil {
		t.Fatalf("ToPDF: %v", err)
	}
	if gotEngine != "chromium" {
		t.Errorf("expected chromium engine, got %q", gotEngine)
	}
	if !bytes.Equal(out, fakePDF) {
		t.Errorf("ToPDF returned %q, want stub PDF bytes", out)
	}
}

// libreoffice cannot be told where to put its output: it writes
// "<input-stem>.pdf" beside the input. The output path is now deliberately a
// DIFFERENT stem ("out.pdf" against an input of "note.html"), so this test
// actually exercises the rename. It could not before — the output was also
// called "note.pdf", so the reconciliation compared a path to itself and the
// rename was unreachable dead code, on the one engine whose whole reason for
// having a special case is that it needs it.
func TestToPDF_LibreOfficeDirOutput(t *testing.T) {
	defer stubBundledChromium("")()
	restoreLP := stubLookPath(func(bin string) (string, error) {
		if bin == "libreoffice" {
			return "/usr/bin/libreoffice", nil
		}
		return "", errors.New("not found")
	})
	defer restoreLP()

	fakePDF := []byte("%PDF-1.4\nlo\n%%EOF\n")
	restoreRun := stubRunEngine(func(_ context.Context, eng pdfEngine, _, htmlPath, outPath string) error {
		if !eng.dirOutput {
			t.Errorf("expected libreoffice to be a dir-output engine")
		}
		// Write where libreoffice really would: the input's stem, in the input's
		// directory — NOT outPath.
		return os.WriteFile(filepath.Join(filepath.Dir(htmlPath), "note.pdf"), fakePDF, 0o600)
	})
	defer restoreRun()

	out, err := ToPDF([]byte("# Hi\n"), Options{Title: "Hi"})
	if err != nil {
		t.Fatalf("ToPDF: %v", err)
	}
	if !bytes.Equal(out, fakePDF) {
		t.Errorf("ToPDF returned %q, want stub PDF bytes", out)
	}
}

// --- helpers ---

func stubLookPath(f func(string) (string, error)) func() {
	prev := lookPath
	lookPath = f
	return func() { lookPath = prev }
}

func stubRunEngine(f func(context.Context, pdfEngine, string, string, string) error) func() {
	prev := runEngine
	runEngine = f
	return func() { runEngine = prev }
}

// stubBundledChromium forces the bundled-Chromium probe off (or on). Every test
// that exercises a PATH engine must call it: the bundled build is probed FIRST,
// so on a developer machine that has run `rookery browser install` it would win
// and the test would silently assert against the wrong engine.
func stubBundledChromium(path string) func() {
	prev := bundledChromium
	bundledChromium = func() string { return path }
	return func() { bundledChromium = prev }
}

func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(b)
	}
	return out
}

func assertContains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected %s (%q) in output", label, needle)
	}
}

// between returns the substring of s between the first occurrence of start and
// the next occurrence of end after it, or "" if not found.
func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	i += len(start)
	j := strings.Index(s[i:], end)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}
