package export

import (
	"bytes"
	"encoding/base64"
	"image"
	"strconv"
	"strings"

	// Registered for image.DecodeConfig only: the three formats the KB editor
	// can produce and OOXML can carry. A format absent from this list is not
	// guessed at — see imageDimensions.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// emuPerPixel is fixed by OOXML: English Metric Units are 914400 per inch, and
// a pixel is 1/96 inch, so 914400/96 = 9525. Not a tunable.
const emuPerPixel = 9525

func pixelsToEMU(px int) int { return px * emuPerPixel }

// SplitAltWidth separates an image's real alt text from the pixel width the
// editor stores alongside it in Obsidian's `![alt|420](src)` form.
//
// It must agree exactly with the editor's TypeScript splitAltWidth
// (web/ui/src/pages/kb/imageResize.ts), which is why the rule is stated the
// same way in both: split on the LAST pipe, and only when the tail is a bare
// integer, so an alt that genuinely contains a pipe survives.
//
// Returns a width of 0 when there is none. 0 rather than a pointer because
// "no width" and "zero width" are the same instruction here — render at the
// image's natural size — and a pointer would invite a nil check at every call
// site to express nothing extra.
func SplitAltWidth(alt string) (string, int) {
	i := strings.LastIndex(alt, "|")
	if i < 0 {
		return alt, 0
	}
	tail := alt[i+1:]
	if tail == "" {
		return alt, 0
	}
	// strconv.Atoi accepts a leading sign and surrounding text would already
	// have failed, but "-40" must read as alt text rather than a width, so the
	// digits are checked explicitly.
	for _, r := range tail {
		if r < '0' || r > '9' {
			return alt, 0
		}
	}
	n, err := strconv.Atoi(tail)
	if err != nil {
		return alt, 0
	}
	return alt[:i], n
}

// imageDimensions reports an image's natural pixel size.
//
// It returns ok=false for a format the stdlib decoders do not recognise, and
// callers must SKIP such an image rather than assume a shape for it: DOCX
// requires an explicit extent in EMU, so a guessed aspect ratio produces a
// visibly stretched picture — worse than an absent one, and much harder to
// attribute to the exporter.
func imageDimensions(data []byte) (w, h int, ok bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// scaleToWidth returns the display size for an image, honouring a requested
// width and deriving the height from the real aspect ratio. A zero or negative
// request means "natural size".
func scaleToWidth(naturalW, naturalH, requested int) (w, h int) {
	if requested <= 0 || naturalW <= 0 {
		return naturalW, naturalH
	}
	return requested, int(float64(naturalH) * float64(requested) / float64(naturalW))
}

// dataURIPayload splits a `data:<mime>;base64,<payload>` source into its media
// type and decoded bytes.
//
// Export needs this because web/api_kb.go's inlineVaultAssets has ALREADY
// rewritten every vault image into a data URI by the time the markdown reaches
// this package — that is what makes an exported HTML file self-contained. DOCX
// cannot render a data URI, so it has to decode back to bytes and store them as
// a real media part.
func dataURIPayload(src string) (mediaType string, data []byte, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(src, prefix) {
		return "", nil, false
	}
	rest := src[len(prefix):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", nil, false
	}
	meta, payload := rest[:comma], rest[comma+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, false
	}
	mediaType = strings.TrimSuffix(meta, ";base64")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, false
	}
	return mediaType, decoded, true
}

// imageExtension maps a media type to the file extension an OOXML media part
// must use. Word selects its decoder from the part name, so a .png holding JPEG
// bytes renders as a broken image placeholder.
func imageExtension(mediaType string) (string, bool) {
	switch mediaType {
	case "image/png":
		return "png", true
	case "image/jpeg":
		return "jpeg", true
	case "image/gif":
		return "gif", true
	}
	return "", false
}
