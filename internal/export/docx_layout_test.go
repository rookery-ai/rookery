package export

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"
)

// pngDataURI builds a real PNG of the given size as a data: URI — the exact
// shape web/api_kb.go's inlineVaultAssets produces, which is what the DOCX
// writer actually receives.
//
// A real encoded image rather than a fixture blob because the writer calls
// image.DecodeConfig to derive the aspect ratio, so the bytes have to be
// genuinely decodable for the test to exercise the path at all.
func pngDataURI(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// docxParts unzips a generated document into a name→bytes map.
func docxParts(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("generated docx is not a valid zip: %v", err)
	}
	out := map[string][]byte{}
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
		out[f.Name] = b
	}
	return out
}

// An embedded image must be present as a real part, be reachable through a
// relationship of the IMAGE type, have its extension declared, and carry the
// requested size in EMU.
//
// Every one of those can be wrong while the file is still a well-formed zip of
// well-formed XML that Word opens — showing a missing-picture box, or a
// stretched image, with no error. So the assertions reach the actual values
// rather than stopping at well-formedness.
func TestDOCXEmbedsAnImageAtTheRequestedSize(t *testing.T) {
	md := fmt.Sprintf("![a diagram|420](%s)\n", pngDataURI(t, 800, 400))

	out, err := ToDOCX([]byte(md), Options{Title: "Doc"})
	if err != nil {
		t.Fatal(err)
	}
	parts := docxParts(t, out)

	media, ok := parts["word/media/image1.png"]
	if !ok {
		t.Fatalf("no media part was written; parts = %v", partNames(parts))
	}
	if !bytes.HasPrefix(media, []byte("\x89PNG")) {
		t.Error("the media part does not hold PNG bytes")
	}

	rels := string(parts["word/_rels/document.xml.rels"])
	if !strings.Contains(rels, `Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"`) {
		t.Error("no image-type relationship; Word resolves by type, so the picture would not be found")
	}
	if !strings.Contains(rels, `Target="media/image1.png"`) {
		t.Error("the relationship does not point at the media part")
	}
	// An image relationship carrying TargetMode="External" sends Word looking
	// outside the package for a part that is inside it.
	if strings.Contains(rels, `Target="media/image1.png" TargetMode="External"`) {
		t.Error("the image relationship is marked External")
	}

	if ct := string(parts["[Content_Types].xml"]); !strings.Contains(ct, `Extension="png"`) {
		t.Error("png is not declared in [Content_Types].xml; Word rejects the whole file, not just the part")
	}

	doc := string(parts["word/document.xml"])
	// 420px wide, and 210px tall because the source is 800x400 — the aspect
	// ratio must come from the real image, not be assumed square.
	wantCX, wantCY := 420*9525, 210*9525
	if !strings.Contains(doc, fmt.Sprintf(`<wp:extent cx="%d" cy="%d"/>`, wantCX, wantCY)) {
		t.Errorf("wrong extent: want cx=%d cy=%d (420px at the source's 2:1 ratio)", wantCX, wantCY)
	}
	if !strings.Contains(doc, `descr="a diagram"`) {
		t.Error("the alt text did not reach the picture description")
	}
	if strings.Contains(doc, "|420") {
		t.Error("the width marker leaked into the document as visible text")
	}
}

// The whole document must stay well-formed XML once drawings are in it. An
// undeclared namespace prefix is the easy mistake here, and Word reports it as
// a corrupt file rather than as a missing image.
func TestDOCXWithAnImageIsWellFormed(t *testing.T) {
	md := fmt.Sprintf("![x|100](%s)\n", pngDataURI(t, 100, 100))
	out, err := ToDOCX([]byte(md), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range docxParts(t, out) {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		var into any
		if err := xml.Unmarshal(data, &into); err != nil {
			t.Errorf("%s is not well-formed: %v", name, err)
		}
	}
}

// A grid becomes a borderless table — Word's only way to put blocks side by
// side. Without explicit `none` borders Word applies its document default and
// the layout arrives with visible gridlines nobody asked for.
func TestDOCXRendersColumnsAsABorderlessTable(t *testing.T) {
	md := "<div data-cols=\"2\">\n\nLeft cell.\n\nRight cell.\n\n</div>\n"

	out, err := ToDOCX([]byte(md), Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc := string(docxParts(t, out)["word/document.xml"])

	if !strings.Contains(doc, "<w:tbl>") {
		t.Fatal("no table was emitted, so the cells would stack instead of sitting side by side")
	}
	if strings.Count(doc, "<w:tc>") != 2 {
		t.Errorf("want 2 cells, got %d", strings.Count(doc, "<w:tc>"))
	}
	for _, edge := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		if !strings.Contains(doc, fmt.Sprintf(`<w:%s w:val="none"`, edge)) {
			t.Errorf("the %s border is not explicitly none, so Word would draw its default", edge)
		}
	}
	// Compared against the EXTRACTED text, not a raw substring: a paragraph is
	// legitimately split across several <w:r> runs (Word joins them), so
	// searching the markup for a contiguous sentence fails on a correct file.
	if txt := docxText(doc); !strings.Contains(txt, "Left cell.") || !strings.Contains(txt, "Right cell.") {
		t.Errorf("cell content was lost: %q", txt)
	}
}

// docxText concatenates every <w:t> in a document, which is the text a reader
// actually sees.
func docxText(doc string) string {
	var sb strings.Builder
	rest := doc
	for {
		i := strings.Index(rest, "<w:t")
		if i < 0 {
			return sb.String()
		}
		rest = rest[i:]
		open := strings.Index(rest, ">")
		if open < 0 {
			return sb.String()
		}
		rest = rest[open+1:]
		end := strings.Index(rest, "</w:t>")
		if end < 0 {
			return sb.String()
		}
		sb.WriteString(rest[:end])
		rest = rest[end:]
	}
}

// Alignment is a PARAGRAPH property in Word — there is no block-level container
// — so it has to reach each paragraph, and a <w:jc> outside <w:pPr> makes the
// document invalid rather than merely unaligned.
func TestDOCXAppliesAlignmentToEachParagraph(t *testing.T) {
	md := "<div align=\"center\">\n\nFirst.\n\nSecond.\n\n</div>\n"

	out, err := ToDOCX([]byte(md), Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc := string(docxParts(t, out)["word/document.xml"])

	if n := strings.Count(doc, `<w:jc w:val="center"/>`); n != 2 {
		t.Errorf("got %d centred paragraphs, want 2", n)
	}
	if strings.Contains(doc, `<w:p><w:jc`) {
		t.Error("a w:jc was emitted outside w:pPr, which makes the document invalid")
	}
	var into any
	if err := xml.Unmarshal(docxParts(t, out)["word/document.xml"], &into); err != nil {
		t.Errorf("document is not well-formed: %v", err)
	}
}

// An image whose bytes cannot be decoded degrades to its alt text rather than
// being embedded at a guessed size. DOCX requires an explicit extent, so a
// guess produces a visibly stretched picture — worse than an absent one, and
// much harder to attribute to the exporter.
func TestDOCXSkipsAnUndecodableImage(t *testing.T) {
	md := "![the alt text|420](data:image/png;base64,bm90YXBuZw==)\n"

	out, err := ToDOCX([]byte(md), Options{})
	if err != nil {
		t.Fatal(err)
	}
	parts := docxParts(t, out)

	for name := range parts {
		if strings.HasPrefix(name, "word/media/") {
			t.Errorf("an undecodable image was embedded as %s", name)
		}
	}
	if txt := docxText(string(parts["word/document.xml"])); !strings.Contains(txt, "the alt text") {
		t.Error("the alt text was not used as the fallback")
	}
}

// The reported case, in DOCX: two resized images inside a two-column grid. This
// exported with no images and no layout at all.
func TestDOCXHandlesTheReportedCase(t *testing.T) {
	md := fmt.Sprintf("<div data-cols=\"2\">\n\n![before|300](%s)\n\n![after|300](%s)\n\n</div>\n",
		pngDataURI(t, 600, 300), pngDataURI(t, 600, 300))

	out, err := ToDOCX([]byte(md), Options{Title: "Comparison"})
	if err != nil {
		t.Fatal(err)
	}
	parts := docxParts(t, out)

	if _, ok := parts["word/media/image1.png"]; !ok {
		t.Error("the first image was not embedded")
	}
	if _, ok := parts["word/media/image2.png"]; !ok {
		t.Error("the second image was not embedded")
	}
	doc := string(parts["word/document.xml"])
	if !strings.Contains(doc, "<w:tbl>") {
		t.Error("the images are not laid out side by side")
	}
	if n := strings.Count(doc, "<w:drawing>"); n != 2 {
		t.Errorf("got %d drawings, want 2", n)
	}
	// Two drawings must not share a docPr id — Word treats a duplicate as a
	// corrupt file.
	if strings.Count(doc, `<wp:docPr id="1"`) > 1 {
		t.Error("two drawings share a docPr id")
	}
}

func partNames(parts map[string][]byte) []string {
	var names []string
	for n := range parts {
		names = append(names, n)
	}
	return names
}
