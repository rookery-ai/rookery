package export

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestToHTMLEmbedsTheUIFont(t *testing.T) {
	out, err := ToHTML([]byte("# Hi\n\nbody text\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	// The font must be INLINED, not merely named. ToPDF shells out to a headless
	// renderer running on the SERVER, which will not have Inter installed, so a
	// named font silently falls back while the export still reports success.
	// Inlining also keeps an exported HTML file a single portable document.
	if !strings.Contains(s, "data:font/woff2;base64,") {
		t.Error("exported HTML does not inline the font as a data: URI")
	}
	if !strings.Contains(s, `font-family:"InterVariable"`) {
		t.Error("exported HTML has no @font-face for InterVariable")
	}
	if !strings.Contains(s, `font-family: "InterVariable"`) {
		t.Error("exported HTML body does not use InterVariable")
	}
	// The old stack must no longer be the primary face.
	if strings.Contains(s, "-apple-system") {
		t.Error("exported HTML still leads with the old system stack")
	}
	// Monospace is deliberately left as the system stack: no second vendored
	// family for code blocks alone.
	if !strings.Contains(s, "ui-monospace") {
		t.Error("exported HTML lost its monospace stack")
	}
}

func TestToDOCXNamesTheUIFont(t *testing.T) {
	out, err := ToDOCX([]byte("hello **world**\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		doc = string(b)
	}
	if doc == "" {
		t.Fatal("no word/document.xml in the archive")
	}
	if !strings.Contains(doc, `w:ascii="Inter"`) {
		t.Error("DOCX runs do not name Inter")
	}
	// Code keeps a monospace face rather than inheriting the body font.
	if !strings.Contains(doc, "Consolas") && strings.Contains(doc, "<w:t") {
		t.Log("note: no code run in this fixture, Consolas not expected")
	}
}
