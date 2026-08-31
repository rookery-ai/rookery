package convert

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"net/http"
	"path"
	"strings"
)

// Extracting the images embedded in an OOXML document.
//
// Every binary-document converter used to discard embedded media entirely, and
// SILENTLY — no code path read word/media/, ppt/media/ or xl/media/, and no
// warning was appended even though Warnings exists precisely to declare a lossy
// conversion. A report whose every chart lived in a picture converted to a note
// with the charts simply gone, and nothing said so.
//
// The reference from the document body to the bytes runs through the OPC
// relationship table, which is why this needs a part the converters never
// opened: <a:blip r:embed="rId7"> names a relationship id, and
// <part>/_rels/<name>.rels maps that id to a file inside the archive.

// maxAssetBytes caps a single extracted image. The whole upload is already
// bounded by internal/iolimit at ingest; this is the per-asset guard, so one
// enormous embedded bitmap cannot dominate the note or the vault.
const maxAssetBytes = 8 << 20

// maxAssets caps how many images are pulled out of one document. A deck of a
// hundred slides can carry a hundred pictures, and each becomes a file in the
// user's knowledge base; past this point the note is a gallery rather than a
// document, and the preserved original still holds everything.
const maxAssets = 50

// relTarget maps a relationship id to the archive path it points at.
type relTarget map[string]string

// readRels parses the .rels part that belongs to a document part, returning
// relationship id -> archive path. A missing rels part is not an error: a
// document with no relationships legitimately has none.
func readRels(zr *zip.Reader, partName string) relTarget {
	relName := path.Join(path.Dir(partName), "_rels", path.Base(partName)+".rels")
	data, err := readZipPart(zr, relName)
	if err != nil {
		return relTarget{}
	}
	var doc struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return relTarget{}
	}
	out := relTarget{}
	base := path.Dir(partName)
	for _, r := range doc.Rels {
		// An External target is a URL, not a part in this archive — following
		// it would turn a document import into a network fetch.
		if strings.EqualFold(r.Mode, "External") || r.Target == "" {
			continue
		}
		// Targets are relative to the part's own directory, and commonly climb
		// out of it ("../media/image1.png" from ppt/slides/slide1.xml).
		out[r.ID] = path.Clean(path.Join(base, r.Target))
	}
	return out
}

// assetCollector accumulates extracted media across the parts of one document,
// de-duplicating by archive path so an image placed on ten slides is stored
// once and referenced ten times.
type assetCollector struct {
	assets  []Asset
	byPath  map[string]int // archive path -> index in assets
	skipped int
	overCap bool
	zr      *zip.Reader
}

func newAssetCollector(zr *zip.Reader) *assetCollector {
	return &assetCollector{byPath: map[string]int{}, zr: zr}
}

// ref returns the markdown destination for the media behind a relationship id,
// reading and storing the bytes on first use. It returns "" when the id names
// nothing usable, so the caller can fall back to emitting nothing rather than a
// dangling reference.
func (c *assetCollector) ref(rels relTarget, id string) string {
	target := rels[id]
	if target == "" {
		return ""
	}
	if idx, ok := c.byPath[target]; ok {
		return fmt.Sprintf("%s%d", AssetRefScheme, idx)
	}
	if len(c.assets) >= maxAssets {
		c.overCap = true
		return ""
	}
	data, err := readZipPart(c.zr, target)
	if err != nil || len(data) == 0 {
		c.skipped++
		return ""
	}
	if len(data) > maxAssetBytes {
		c.skipped++
		return ""
	}
	ct := http.DetectContentType(data)
	// Only images are extracted. A document can embed a spreadsheet, a font or
	// an OLE object, and none of those belongs inline in a note; the original is
	// preserved beside it for anything this declines.
	if !strings.HasPrefix(ct, "image/") {
		c.skipped++
		return ""
	}
	c.byPath[target] = len(c.assets)
	c.assets = append(c.assets, Asset{
		Name:        path.Base(target),
		ContentType: ct,
		Data:        data,
	})
	return fmt.Sprintf("%s%d", AssetRefScheme, len(c.assets)-1)
}

// warnings reports what the collector could not take, so a lossy conversion
// still declares itself rather than quietly dropping pictures.
func (c *assetCollector) warnings() []string {
	var out []string
	if c.overCap {
		out = append(out, fmt.Sprintf(
			"this document embeds more than %d images; the rest are not in this note — open the preserved original for the full set",
			maxAssets))
	}
	if c.skipped > 0 {
		out = append(out, fmt.Sprintf(
			"%d embedded object(s) could not be included (too large, unreadable, or not an image)", c.skipped))
	}
	return out
}
