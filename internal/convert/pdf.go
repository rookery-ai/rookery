package convert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

// minTextPerPage is the byte threshold below which extraction is treated as
// suspect. Scanned pages and CID-encoded fonts commonly yield a handful of
// bytes per page; passing that off as the document's content would be a silent
// lie, so it becomes a warning the reader sees in the note's frontmatter.
const minTextPerPage = 40

// pdftotextPath resolves poppler's pdftotext, or "" when it is not installed.
// It is a package variable so tests can force the pure-Go branch on a host that
// happens to have poppler.
var pdftotextPath = func() string {
	p, err := exec.LookPath("pdftotext")
	if err != nil {
		return ""
	}
	return p
}

// pdfToMarkdown extracts a PDF's text layer. Two extractors, deliberately:
// pdftotext (poppler) handles layout and font encodings far better and is
// preferred whenever the host has it, and a pure-Go fallback guarantees the
// converter works on a host with nothing installed. Result.Extractor records
// which one ran, so output quality is always explainable.
func pdfToMarkdown(data []byte, opt Options) (Result, error) {
	res := Result{Kind: KindPDF, Title: titleFromFilename(opt.Filename)}

	text, extractor, pages, err := extractPDFText(data)
	if err != nil {
		return Result{}, err
	}
	res.Extractor = extractor

	text = strings.TrimSpace(text)
	if text == "" {
		res.Markdown = "(no text layer could be extracted from this PDF)\n"
		res.Warnings = append(res.Warnings,
			"no text extracted; the PDF is likely scanned images (OCR is not available)")
		return res, nil
	}
	if pages > 0 && len(text)/pages < minTextPerPage {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"only %d bytes of text across %d pages; the PDF may be scanned or use fonts this extractor cannot decode — treat the content as incomplete",
			len(text), pages))
	}
	if extractor == "pure-go" {
		res.Warnings = append(res.Warnings,
			"extracted without pdftotext; layout and column order may be imperfect")
	}
	res.Markdown = normalizeText(paragraphize(text))
	return res, nil
}

// extractPDFText returns the document text, the extractor that produced it, and
// the page count.
func extractPDFText(data []byte) (string, string, int, error) {
	if bin := pdftotextPath(); bin != "" {
		if text, pages, err := runPdftotext(bin, data); err == nil {
			return text, "pdftotext", pages, nil
		}
		// Fall through: a pdftotext failure should not fail the whole conversion
		// when a pure-Go extractor is available.
	}
	text, pages, err := extractPDFPureGo(data)
	if err != nil {
		return "", "", 0, fmt.Errorf("convert: pdf: %w", err)
	}
	return text, "pure-go", pages, nil
}

// runPdftotext writes the bytes to a temp file and shells out. -layout keeps
// columns and tables readable, which is the main reason to prefer poppler.
func runPdftotext(bin string, data []byte) (string, int, error) {
	dir, err := os.MkdirTemp("", "sa-pdf-")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return "", 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-layout", "-enc", "UTF-8", src, "-")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", 0, err
	}
	text := out.String()
	// pdftotext separates pages with a form feed.
	pages := strings.Count(text, "\f") + 1
	return strings.ReplaceAll(text, "\f", "\n\n"), pages, nil
}

// extractPDFPureGo is the dependency-only fallback.
func extractPDFPureGo(data []byte) (string, int, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", 0, err
	}
	pages := r.NumPage()
	var sb strings.Builder
	for i := 1; i <= pages; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue // one unreadable page must not lose the rest
		}
		sb.WriteString(text)
		sb.WriteString("\n\n")
	}
	return sb.String(), pages, nil
}

// paragraphize turns extracted text into markdown paragraphs: single newlines
// inside a paragraph are line wrapping from the PDF, not intentional breaks.
func paragraphize(text string) string {
	blocks := strings.Split(normalizeText(text), "\n\n")
	var out []string
	for _, b := range blocks {
		lines := strings.Split(b, "\n")
		var kept []string
		for _, l := range lines {
			if s := strings.TrimSpace(l); s != "" {
				kept = append(kept, s)
			}
		}
		if len(kept) > 0 {
			out = append(out, strings.Join(kept, " "))
		}
	}
	return strings.Join(out, "\n\n")
}
