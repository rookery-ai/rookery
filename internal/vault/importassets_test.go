package vault

import (
	"archive/zip"
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/convert"
)

func timeAfterShort() <-chan time.Time { return time.After(2 * time.Second) }

func newTestVault(t *testing.T) (*Vault, string) {
	t.Helper()
	return New(t.TempDir()), "ws1"
}

// A 1x1 PNG — real bytes, because the extractor sniffs the content type from
// the data and would correctly reject a fake payload as "not an image", making
// the test pass for the wrong reason.
var testPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
	0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func docxWithOneImage(t *testing.T) []byte {
	t.Helper()
	parts := map[string][]byte{
		"word/document.xml": []byte(`<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <w:body>
  <w:p><w:r><w:t>Before the chart.</w:t></w:r></w:p>
  <w:p><w:r><w:drawing><a:blip r:embed="rId7"/></w:drawing></w:r></w:p>
 </w:body>
</w:document>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId7" Target="media/image1.png"/>
</Relationships>`),
		"word/media/image1.png": testPNG,
	}
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

var noteImageRE = regexp.MustCompile(`!\[[^\]]*\]\((` + FilesDir + `/[^)]+)\)`)

func imageRefIn(t *testing.T, body string) string {
	t.Helper()
	m := noteImageRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no image reference in note:\n%s", body)
	}
	return m[1]
}

func TestRewriteAssetRefs(t *testing.T) {
	paths := map[int]string{0: "uploads/image1-1.png", 1: "uploads/my pic-2.png"}
	cases := []struct{ name, in, want string }{
		{
			name: "single reference",
			in:   "Before\n\n![](" + convert.AssetRefScheme + "0)\n\nAfter\n",
			want: "Before\n\n![](uploads/image1-1.png)\n\nAfter\n",
		},
		{
			name: "keeps a label",
			in:   "![A chart](" + convert.AssetRefScheme + "0)",
			want: "![A chart](uploads/image1-1.png)",
		},
		{
			name: "two references to the same asset",
			in:   "![](" + convert.AssetRefScheme + "0) and ![](" + convert.AssetRefScheme + "0)",
			want: "![](uploads/image1-1.png) and ![](uploads/image1-1.png)",
		},
		{
			// A space in the destination would end it and turn the whole
			// construct back into literal text, so the image would stop being
			// an image.
			name: "escapes the stored path",
			in:   "![](" + convert.AssetRefScheme + "1)",
			want: "![](uploads/my%20pic-2.png)",
		},
		{
			// An index with no stored path means the write failed. A dangling
			// "rookery-asset:" renders as a broken image and explains nothing;
			// removing it leaves the prose intact and the warning does the
			// explaining.
			name: "drops an unresolved reference",
			in:   "Before ![](" + convert.AssetRefScheme + "9) after",
			want: "Before  after",
		},
		{
			name: "leaves ordinary markdown alone",
			in:   "![real](uploads/other.png) and [a link](https://x.com)",
			want: "![real](uploads/other.png) and [a link](https://x.com)",
		},
		{
			name: "no references at all",
			in:   "Just prose.\n",
			want: "Just prose.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteAssetRefs(tc.in, paths); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A malformed reference must not send the scanner into a loop — it walks the
// string looking for a closing paren, and a missing one used to be the shape
// most likely to hang it.
func TestRewriteAssetRefsTerminates(t *testing.T) {
	for _, in := range []string{
		"![](" + convert.AssetRefScheme + "0",
		"](" + convert.AssetRefScheme + "0)",
		"![](" + convert.AssetRefScheme + "notanumber)",
		"![](" + convert.AssetRefScheme + ")",
	} {
		done := make(chan string, 1)
		go func() { done <- rewriteAssetRefs(in, map[int]string{0: "uploads/a.png"}) }()
		select {
		case <-done:
		case <-timeAfterShort():
			t.Fatalf("rewriteAssetRefs did not terminate on %q", in)
		}
	}
}

// ImportFile is the single choke point every ingest door funnels through, so an
// image embedded in a document must land in uploads/ and be referenced by a
// path the editor and the export inliner can both resolve.
func TestImportFileStoresExtractedImages(t *testing.T) {
	v, ws := newTestVault(t)

	data := docxWithOneImage(t)
	res, err := v.ImportFile(ws, ImportInput{Data: data, Filename: "report.docx"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}

	note, err := v.ReadNote(ws, res.NotePath)
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	body := string(note)
	if strings.Contains(body, convert.AssetRefScheme) {
		t.Errorf("note still carries an unrewritten asset reference:\n%s", body)
	}
	if !strings.Contains(body, "]("+FilesDir+"/") {
		t.Errorf("note does not reference the stored image:\n%s", body)
	}

	// The referenced file must actually exist, or the note renders a broken
	// image and the export inliner finds nothing to inline.
	rel := imageRefIn(t, body)
	stored, err := v.ReadNote(ws, rel)
	if err != nil {
		t.Fatalf("stored image %q is not readable: %v", rel, err)
	}
	if len(stored) == 0 {
		t.Errorf("stored image %q is empty", rel)
	}
}
