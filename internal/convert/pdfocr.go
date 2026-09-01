package convert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// OCR for PDFs with no usable text layer.
//
// A scanned PDF is the single most common thing a person uploads and cannot
// read back, and the converter used to answer it with the sentence "OCR is not
// available" — on hosts where it plainly was. Both tools this needs are already
// part of the platform's own dependency story: pdftoppm ships in the same
// poppler package as pdftotext, which is already a declared host tool, and
// tesseract is a declared host tool in its own right, whose recorded purpose is
// "OCR for scanned documents and images". Nothing new is required for this to
// work; the code to call them was simply missing.
//
// This is a FALLBACK, never the primary path. OCR is slow, costs CPU per page,
// and makes recognition errors a text layer would not — so it runs only when
// extraction produced nothing usable, and the note says so in its frontmatter.

// maxOCRPages bounds how many pages are rasterised and recognised. OCR costs
// roughly a second of CPU per page in the HOST process — the same process
// serving every request — so an unbounded run over a 400-page scan would pin a
// core for several minutes on an upload nobody is watching. The cap truncates
// with a warning rather than refusing: the first pages of a scan are still worth
// far more than an apology, and the original is preserved beside the note.
const maxOCRPages = 20

// ocrTimeout bounds the whole rasterise-and-recognise run from outside, in
// addition to the page cap, because a malformed PDF can make either tool spin.
const ocrTimeout = 5 * time.Minute

// ocrDPI is the rasterisation resolution. 150 is the usual floor for reliable
// recognition of body text and was verified against this repository's own
// fixture; 300 roughly quadruples both the time and the memory for a marginal
// gain on ordinary documents.
const ocrDPI = 150

// pdftoppmPath and tesseractPath resolve the two binaries, checking
// ~/.local/bin before PATH for the same reason pdftotextPath does: conversion
// runs in the host server process, which under systemd often has a bare PATH
// that omits the operator's own bin directory. Package variables so tests can
// describe a host they are not running on.
var (
	pdftoppmPath  = func() string { return lookupHostTool("pdftoppm") }
	tesseractPath = func() string { return lookupHostTool("tesseract") }
)

func lookupHostTool(bin string) string {
	if home, err := os.UserHomeDir(); err == nil {
		local := filepath.Join(home, ".local", "bin", bin)
		if fi, err := os.Stat(local); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return local
		}
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	return p
}

// ocrUnavailableWarning names what is actually missing, rather than asserting
// that OCR is impossible. The old wording said "OCR is not available" on every
// host, including those where it was — so the operator was told to stop looking
// at precisely the moment the fix was one package away.
func ocrUnavailableWarning() string {
	var missing []string
	if pdftoppmPath() == "" {
		missing = append(missing, "poppler-utils (for pdftoppm)")
	}
	if tesseractPath() == "" {
		missing = append(missing, "tesseract")
	}
	if len(missing) == 0 {
		// Both present, yet OCR produced nothing: the pages really are blank, or
		// carry no recognisable text. Saying a tool is missing here would send
		// the operator to install something they already have.
		return "no text extracted, and OCR of the page images found none either — the pages may be blank or contain only graphics"
	}
	return "no text extracted; the PDF is likely scanned images, and OCR is unavailable on this host — install " +
		strings.Join(missing, " and ") + " to read scanned documents"
}

// ocrPDF rasterises the PDF's pages and runs OCR over each, returning the
// recognised text. It reports an error only when the pipeline could not run;
// finding no text is a successful run that returns "".
func ocrPDF(data []byte, pages int) (string, error) {
	ppm, tess := pdftoppmPath(), tesseractPath()
	if ppm == "" || tess == "" {
		return "", fmt.Errorf("convert: pdf ocr: pdftoppm and tesseract are both required")
	}

	dir, err := os.MkdirTemp("", "sa-pdfocr-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), ocrTimeout)
	defer cancel()

	// -l/-r bound the work before any of it happens: rasterising 400 pages and
	// then discarding all but the first 20 would pay the whole cost anyway.
	limit := maxOCRPages
	if pages > 0 && pages < limit {
		limit = pages
	}
	raster := exec.CommandContext(ctx, ppm, "-png", "-r", fmt.Sprint(ocrDPI),
		"-f", "1", "-l", fmt.Sprint(limit), src, filepath.Join(dir, "page"))
	var rasterErr bytes.Buffer
	raster.Stderr = &rasterErr
	if err := raster.Run(); err != nil {
		return "", fmt.Errorf("convert: pdf ocr: pdftoppm: %w: %s", err, tailLines(rasterErr.String(), 3))
	}

	images, err := filepath.Glob(filepath.Join(dir, "page*.png"))
	if err != nil || len(images) == 0 {
		return "", fmt.Errorf("convert: pdf ocr: pdftoppm produced no page images")
	}
	// Glob returns lexical order, which puts page-10 before page-2. pdftoppm
	// zero-pads only when it knows the page count, so the order must be made
	// numeric explicitly or a 12-page scan comes back with its pages shuffled.
	sort.Slice(images, func(i, j int) bool {
		return pageIndexOf(images[i]) < pageIndexOf(images[j])
	})

	var sb strings.Builder
	for _, img := range images {
		var out, errBuf bytes.Buffer
		cmd := exec.CommandContext(ctx, tess, img, "stdout")
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err != nil {
			// One unreadable page must not discard the pages that did work.
			continue
		}
		if s := strings.TrimSpace(out.String()); s != "" {
			sb.WriteString(s)
			sb.WriteString("\n\n")
		}
	}
	return sb.String(), nil
}

// pageIndexOf reads the page number pdftoppm appended to a file name
// ("page-12.png" → 12), returning 0 when there is none.
func pageIndexOf(path string) int {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	i := len(base)
	for i > 0 && base[i-1] >= '0' && base[i-1] <= '9' {
		i--
	}
	n := 0
	for _, c := range base[i:] {
		n = n*10 + int(c-'0')
	}
	return n
}

// tailLines keeps the last n lines of a subprocess's stderr, which is where the
// actionable message almost always is.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
