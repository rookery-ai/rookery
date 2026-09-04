package export

import (
	"strings"
	"testing"
)

// The reported case, end to end: two resized images inside a two-column grid.
//
// Before this change the exported HTML was two full-size images stacked, with
// the literal "|420" sitting in each alt attribute and the grid replaced by
// `<!-- raw HTML omitted -->`.
func TestTheReportedCaseRendersAsAGridOfSizedImages(t *testing.T) {
	out, err := ToHTML([]byte("<div data-cols=\"2\">\n\n"+
		"![before|420](before.png)\n\n"+
		"![after|420](after.png)\n\n"+
		"</div>\n"), Options{Title: "Comparison"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if strings.Contains(got, "raw HTML omitted") {
		t.Error("the grid wrapper was still dropped as raw HTML")
	}
	if !strings.Contains(got, "grid-template-columns:repeat(2,minmax(0,1fr))") {
		t.Error("no two-column grid was emitted")
	}
	// A bare 1fr lets one wide cell stretch its track and push the others out
	// of the page — CSS Grid §6.6, recorded twice already in this repo.
	if strings.Contains(got, "repeat(2,1fr)") {
		t.Error("a bare 1fr was emitted; the automatic minimum size must be pinned with minmax(0, …)")
	}
	if !strings.Contains(got, `width="420"`) {
		t.Error("the image width stored in the alt slot was not honoured")
	}
	if strings.Contains(got, "|420") {
		t.Error("the width marker leaked into the exported document as visible alt-text noise")
	}
	if !strings.Contains(got, `alt="before"`) {
		t.Error("the real alt text was lost while splitting the width off")
	}
	if !strings.Contains(got, "max-width:100%") {
		t.Error("no max-width: an image wider than the page would overflow it")
	}
}

// Alignment survives to the exported document.
func TestAlignmentReachesTheExportedDocument(t *testing.T) {
	out, err := ToHTML([]byte("<div align=\"center\">\n\nCentred **bold** text.\n\n</div>\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if !strings.Contains(got, "text-align:center") {
		t.Error("the alignment was dropped")
	}
	// The body must still be parsed as MARKDOWN inside the wrapper — that is
	// the property the editor's blank-line-separated form buys, and losing it
	// would turn every mark inside an aligned block into literal asterisks.
	if !strings.Contains(got, "<strong>bold</strong>") {
		t.Error("inline marks inside an aligned block were not parsed as markdown")
	}
}

// An image with no width behaves exactly as before, so notes that never used
// the resize handle export unchanged.
func TestAnUnsizedImageIsUnchanged(t *testing.T) {
	out, err := ToHTML([]byte("![a picture](pic.png)\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// Scoped to the <img> tag: the document template carries a viewport meta
	// whose content is literally "width=device-width", so a document-wide
	// search for "width=" matches every export ever produced.
	img := got[strings.Index(got, "<img"):]
	img = img[:strings.Index(img, ">")+1]
	if strings.Contains(img, "width=\"") {
		t.Errorf("a width was invented for an image that specified none: %s", img)
	}
	if !strings.Contains(got, `alt="a picture"`) {
		t.Errorf("alt text was mangled: %s", got)
	}
}
