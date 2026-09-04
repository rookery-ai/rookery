package export

import (
	"strings"
	"testing"
)

var sampleAttachments = []Attachment{
	{Name: "Q3 report", Path: "uploads/q3.pdf"},
	{Name: "figures.xlsx", Path: "uploads/figures.xlsx"},
}

// A downloaded document must say what it referenced. Its relative links resolve
// to nothing once it leaves the vault, and without this section the reader has
// no way to know a file was even involved.
func TestHTMLListsAttachments(t *testing.T) {
	out, err := ToHTML([]byte("See the [Q3 report](uploads/q3.pdf).\n"),
		Options{Attachments: sampleAttachments})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if !strings.Contains(got, "<h2>Attachments</h2>") {
		t.Fatal("no attachments section")
	}
	for _, a := range sampleAttachments {
		if !strings.Contains(got, a.Name) {
			t.Errorf("attachment %q is not named", a.Name)
		}
		// The PATH as well as the name: a reader holding only this file cannot
		// follow the link, and the path is what lets them ask for the right
		// thing.
		if !strings.Contains(got, a.Path) {
			t.Errorf("attachment path %q is not shown", a.Path)
		}
	}
}

func TestDOCXListsAttachments(t *testing.T) {
	out, err := ToDOCX([]byte("See the [Q3 report](uploads/q3.pdf).\n"),
		Options{Attachments: sampleAttachments})
	if err != nil {
		t.Fatal(err)
	}
	parts := docxParts(t, out)
	text := docxText(string(parts["word/document.xml"]))

	if !strings.Contains(text, "Attachments") {
		t.Fatal("no attachments section")
	}
	for _, a := range sampleAttachments {
		if !strings.Contains(text, a.Name) {
			t.Errorf("attachment %q is not named", a.Name)
		}
		if !strings.Contains(text, a.Path) {
			t.Errorf("attachment path %q is not shown", a.Path)
		}
	}
}

// A note with no attachments must export exactly as before — an empty
// "Attachments" heading on every document would be worse than none.
func TestNoAttachmentsMeansNoSection(t *testing.T) {
	out, err := ToHTML([]byte("Just prose.\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "Attachments") {
		t.Error("an attachments section was emitted for a note with none")
	}

	docx, err := ToDOCX([]byte("Just prose.\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(docxText(string(docxParts(t, docx)["word/document.xml"])), "Attachments") {
		t.Error("an attachments section was emitted in DOCX for a note with none")
	}
}

// A grid must not be split across a page boundary in the PDF path: half a
// layout at the foot of one page and half at the head of the next reads as two
// broken layouts rather than one whole one.
func TestPrintCSSKeepsAGridWhole(t *testing.T) {
	out, err := ToHTML([]byte("x\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "@media print") {
		t.Fatal("no print stylesheet")
	}
	if !strings.Contains(got, "break-inside: avoid") {
		t.Error("a columns block can be split across a page boundary")
	}
}
