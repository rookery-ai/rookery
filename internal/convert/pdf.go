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
//
// It checks $HOME/.local/bin BEFORE PATH: conversion runs in the HOST server
// process (ImportFile → convert are in-process, not in an agent sandbox), and a
// server started under systemd or a minimal service manager often has a bare
// PATH that omits the operator's ~/.local/bin — so poppler installed there by
// the operator would otherwise be invisible and every PDF would silently take
// the weaker pure-Go path.
var pdftotextPath = func() string {
	if home, err := os.UserHomeDir(); err == nil {
		local := filepath.Join(home, ".local", "bin", "pdftotext")
		if fi, err := os.Stat(local); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return local
		}
	}
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
		// Actionable, and aimed at who can actually fix it: pdftotext runs in the
		// host server process, so installing poppler on the HOST (dnf install
		// poppler-utils / apt install poppler-utils, or into the operator's
		// ~/.local/bin) is what upgrades this — an agent installing tools into its
		// own sandbox cannot help an in-process converter.
		res.Warnings = append(res.Warnings,
			"extracted without pdftotext; layout and column order may be imperfect — "+
				"install poppler-utils on the host for higher-fidelity PDF text")
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
	return strings.ReplaceAll(text, "\f", "\n\n"), pdftotextPageCount(text), nil
}

// pdftotextPageCount counts pages in pdftotext's output. Poppler appends a
// form feed after EVERY page, including the last one — verified against
// pdfinfo on both a 1-page and a multi-page fixture — so the count of form
// feeds already equals the page count. The previous "+1" assumed the form
// feed was a page *separator* (n pages -> n-1 feeds), which is wrong for this
// extractor and overcounted every document by one: a 1-page PDF reported 2
// pages. That wrong count both misstates the page number the warning gives
// the reader and, via len(text)/pages, skews the thin-extraction threshold
// toward false positives on genuine single-page documents.
func pdftotextPageCount(text string) int {
	return strings.Count(text, "\f")
}

// pureGoTimeout bounds extractPDFPureGo. runPdftotext's subprocess is already
// bounded by a 60s context (the OS can kill it from outside); the pure-Go
// path runs in-process, so nothing external can stop a pathological input
// from looping forever inside the library and hanging the caller. This gives
// it the same ceiling.
const pureGoTimeout = 60 * time.Second

type pureGoResult struct {
	text  string
	pages int
	err   error
}

// extractPDFPureGo is the dependency-only fallback, bounded by pureGoTimeout.
// The extraction runs in its own goroutine specifically so a panic from the
// library (it does panic on some malformed input) is recovered THERE: a
// goroutine's panic can never be caught by its caller, so once this moved off
// the calling goroutine to gain a timeout, an unrecovered library panic would
// have taken down the whole process instead of just failing this conversion —
// trading a hypothetical hang for a guaranteed crash. Recovering here keeps
// both failure modes as plain errors.
func extractPDFPureGo(data []byte) (string, int, error) {
	done := make(chan pureGoResult, 1)
	go func() {
		var res pureGoResult
		defer func() {
			if r := recover(); r != nil {
				res = pureGoResult{err: fmt.Errorf("pdf library panic: %v", r)}
			}
			done <- res
		}()
		res.text, res.pages, res.err = extractPDFPureGoUnbounded(data)
	}()

	select {
	case res := <-done:
		return res.text, res.pages, res.err
	case <-time.After(pureGoTimeout):
		return "", 0, fmt.Errorf("pdf extraction timed out after %s", pureGoTimeout)
	}
}

// extractPDFPureGoUnbounded does the actual pure-Go extraction. Callers must
// go through extractPDFPureGo for the timeout and panic recovery above.
func extractPDFPureGoUnbounded(data []byte) (string, int, error) {
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
