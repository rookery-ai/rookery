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
)

// imageToMarkdown extracts text from an image via tesseract when it is on PATH,
// falling back to an honest "no OCR" stub otherwise. This mirrors pdf.go's
// "prefer the local CLI, degrade gracefully" shape and keeps convert a pure
// function: it shells to a present-or-absent local tool, never the network.
func imageToMarkdown(data []byte, opt Options) (Result, error) {
	bin, _ := exec.LookPath("tesseract")
	return imageToMarkdownWith(data, opt, bin)
}

// imageToMarkdownWith is the testable core: bin == "" forces the fallback path.
func imageToMarkdownWith(data []byte, opt Options, bin string) (Result, error) {
	if bin == "" {
		return Result{
			Markdown:  fmt.Sprintf("(image file, %d bytes — no text was extracted; OCR is not available)\n", len(data)),
			Title:     titleFromFilename(opt.Filename),
			Kind:      KindImage,
			Extractor: "none",
			Warnings:  []string{"image content is not searchable: no OCR"},
		}, nil
	}

	text, err := runTesseract(bin, data)
	if err != nil {
		// A tool failure degrades to the same honest stub rather than erroring
		// the whole import — the file is still worth recording.
		return Result{
			Markdown:  fmt.Sprintf("(image file, %d bytes — OCR failed: %v)\n", len(data), err),
			Title:     titleFromFilename(opt.Filename),
			Kind:      KindImage,
			Extractor: "none",
			Warnings:  []string{"image OCR failed: " + err.Error()},
		}, nil
	}

	res := Result{
		Kind:      KindImage,
		Extractor: "tesseract",
		Title:     titleFromFilename(opt.Filename),
		// OCR output is document text like any other, and is if anything more
		// likely to contain stray markdown-significant characters, since the
		// engine guesses at glyphs it cannot read.
		Markdown: escapeTextBlock(normalizeText(text)),
	}
	if strings.TrimSpace(text) == "" {
		res.Warnings = append(res.Warnings, "OCR found no text in the image (it may be a photo or diagram)")
	}
	return res, nil
}

// runTesseract writes the image to a temp file and runs `tesseract <file> stdout`.
// A temp file (rather than stdin) is the portable invocation across tesseract
// builds.
func runTesseract(bin string, data []byte) (string, error) {
	dir, err := os.MkdirTemp("", "sa-ocr-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "img")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, in, "stdout")
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
