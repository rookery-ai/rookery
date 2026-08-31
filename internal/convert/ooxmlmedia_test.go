package convert

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildZipBinary is buildZip for parts that are not text. The existing helper
// takes map[string]string, which would corrupt image bytes on the way in.
func buildZipBinary(t *testing.T, parts map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// A 1x1 PNG. Real bytes, because the collector sniffs the content type from the
// data rather than trusting the archive's metadata — a fake payload would be
// rejected as "not an image", which is the correct behaviour and would make the
// test pass for the wrong reason.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
	0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

const docxWithImage = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <w:body>
  <w:p><w:r><w:t>Before the chart.</w:t></w:r></w:p>
  <w:p><w:r><w:drawing><a:blip r:embed="rId7"/></w:drawing></w:r></w:p>
  <w:p><w:r><w:t>After the chart.</w:t></w:r></w:p>
 </w:body>
</w:document>`

const docxImageRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`

// Embedded images were discarded from every binary format, SILENTLY — no code
// path read word/media/, and no warning was appended even though Warnings
// exists precisely to declare a lossy conversion. A report whose charts were
// pictures converted to a note with the charts simply gone.
func TestDocxExtractsEmbeddedImage(t *testing.T) {
	data := buildZipBinary(t, map[string][]byte{
		"word/document.xml":            []byte(docxWithImage),
		"word/_rels/document.xml.rels": []byte(docxImageRels),
		"word/media/image1.png":        onePixelPNG,
	})

	res, err := ToMarkdown(data, Options{Filename: "report.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if len(res.Assets) != 1 {
		t.Fatalf("got %d assets, want 1", len(res.Assets))
	}
	if got := res.Assets[0].ContentType; got != "image/png" {
		t.Errorf("content type = %q, want image/png", got)
	}
	if res.Assets[0].Name != "image1.png" {
		t.Errorf("name = %q, want image1.png", res.Assets[0].Name)
	}
	if !strings.Contains(res.Markdown, "![]("+AssetRefScheme+"0)") {
		t.Errorf("markdown carries no reference to the extracted image:\n%s", res.Markdown)
	}
	// Position matters: the picture must stay between the paragraphs it sat
	// between, not be appended in a gallery at the end.
	before := strings.Index(res.Markdown, "Before the chart")
	img := strings.Index(res.Markdown, AssetRefScheme)
	after := strings.Index(res.Markdown, "After the chart")
	if !(before < img && img < after) {
		t.Errorf("image is out of position:\n%s", res.Markdown)
	}
}

// The same image placed twice must be stored ONCE and referenced twice —
// otherwise a deck with a logo on every slide writes fifty copies of it.
func TestDocxDeduplicatesRepeatedImage(t *testing.T) {
	body := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <w:body>
  <w:p><w:r><w:drawing><a:blip r:embed="rId7"/></w:drawing></w:r></w:p>
  <w:p><w:r><w:drawing><a:blip r:embed="rId7"/></w:drawing></w:r></w:p>
 </w:body>
</w:document>`
	data := buildZipBinary(t, map[string][]byte{
		"word/document.xml":            []byte(body),
		"word/_rels/document.xml.rels": []byte(docxImageRels),
		"word/media/image1.png":        onePixelPNG,
	})

	res, err := ToMarkdown(data, Options{Filename: "report.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if len(res.Assets) != 1 {
		t.Fatalf("got %d assets, want 1 (the same image twice)", len(res.Assets))
	}
	if n := strings.Count(res.Markdown, AssetRefScheme+"0"); n != 2 {
		t.Errorf("got %d references, want 2", n)
	}
}

// A non-image embedded object (a spreadsheet, a font, an OLE blob) must not be
// written into the note as though it were a picture, and its absence must be
// reported rather than silent.
func TestDocxSkipsNonImageEmbeds(t *testing.T) {
	data := buildZipBinary(t, map[string][]byte{
		"word/document.xml":            []byte(docxWithImage),
		"word/_rels/document.xml.rels": []byte(docxImageRels),
		"word/media/image1.png":        []byte("this is not an image at all"),
	})

	res, err := ToMarkdown(data, Options{Filename: "report.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if len(res.Assets) != 0 {
		t.Errorf("got %d assets, want 0", len(res.Assets))
	}
	if strings.Contains(res.Markdown, AssetRefScheme) {
		t.Errorf("markdown references an asset that was not extracted:\n%s", res.Markdown)
	}
	if len(res.Warnings) == 0 {
		t.Error("dropping an embedded object was not reported")
	}
}

// An External relationship target is a URL. Following it would turn a document
// import into a network fetch, from a file the user was merely storing.
func TestDocxIgnoresExternalRelationshipTargets(t *testing.T) {
	rels := `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId7" Target="https://example.com/tracker.png" TargetMode="External"/>
</Relationships>`
	data := buildZipBinary(t, map[string][]byte{
		"word/document.xml":            []byte(docxWithImage),
		"word/_rels/document.xml.rels": []byte(rels),
	})

	res, err := ToMarkdown(data, Options{Filename: "report.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if len(res.Assets) != 0 {
		t.Errorf("got %d assets, want 0 for an external target", len(res.Assets))
	}
	if strings.Contains(res.Markdown, "example.com") {
		t.Errorf("external URL leaked into the note:\n%s", res.Markdown)
	}
}

// A slide picture becomes its own block, never a bullet or part of the title —
// an image reference wrapped in "- " renders inside that list item.
func TestPptxExtractsSlideImage(t *testing.T) {
	slide := `<?xml version="1.0"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <a:p><a:r><a:t>Results</a:t></a:r></a:p>
 <a:p><a:r><a:t>Revenue is up</a:t></a:r></a:p>
 <a:blip r:embed="rId3"/>
</p:sld>`
	rels := `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId3" Target="../media/image2.png"/>
</Relationships>`
	data := buildZipBinary(t, map[string][]byte{
		"ppt/slides/slide1.xml":            []byte(slide),
		"ppt/slides/_rels/slide1.xml.rels": []byte(rels),
		"ppt/media/image2.png":             onePixelPNG,
	})

	res, err := ToMarkdown(data, Options{Filename: "deck.pptx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if len(res.Assets) != 1 {
		t.Fatalf("got %d assets, want 1", len(res.Assets))
	}
	// The rels target climbs out of ppt/slides/ with "..", which must resolve
	// against the part's own directory.
	if res.Assets[0].Name != "image2.png" {
		t.Errorf("name = %q, want image2.png", res.Assets[0].Name)
	}
	if !strings.Contains(res.Markdown, "\n![]("+AssetRefScheme+"0)") {
		t.Errorf("image is not its own block:\n%s", res.Markdown)
	}
	if strings.Contains(res.Markdown, "- ![](") {
		t.Errorf("image was wrapped in a bullet:\n%s", res.Markdown)
	}
}
