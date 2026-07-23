package export

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// pdfTimeout bounds a headless renderer subprocess. A browser or LibreOffice can
// hang on pathological input; the context kills it from outside.
const pdfTimeout = 30 * time.Second

// lookPath is exec.LookPath as a package variable so tests can force the
// "no engine" branch (and a specific engine) without touching the real PATH.
var lookPath = exec.LookPath

// runEngine executes the chosen renderer. It is a package variable so tests can
// substitute a stub that writes fake PDF bytes to outPath, exercising the
// success branch without any engine installed. ctx carries the timeout, htmlPath
// is the input HTML file, outPath is where the PDF must land.
var runEngine = func(ctx context.Context, eng pdfEngine, htmlPath, outPath string) error {
	name, args := eng.command(htmlPath, outPath)
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

// pdfEngine describes one supported headless renderer: the binary name to probe
// and how to build its argv for an HTML→PDF conversion.
type pdfEngine struct {
	// bin is the executable name probed with lookPath.
	bin string
	// command returns the resolved binary path and its arguments. Most engines
	// name the output file directly; libreoffice can only target a directory, so
	// each engine owns its own arg shape.
	command func(htmlPath, outPath string) (string, []string)
	// dirOutput is true for engines that write "<stem>.pdf" into the output
	// file's DIRECTORY rather than to the exact outPath (libreoffice). The
	// produced file is then moved to outPath.
	dirOutput bool
}

// pdfEngines is the detection order: fidelity-first (weasyprint/chromium render
// CSS faithfully), then the more widely-installed fallbacks. The FIRST one found
// on PATH wins — the same "prefer a better external tool when present" philosophy
// convert uses for pdftotext.
var pdfEngines = []pdfEngine{
	{
		bin: "weasyprint",
		command: func(in, out string) (string, []string) {
			return "weasyprint", []string{in, out}
		},
	},
	{
		bin: "chromium",
		command: func(in, out string) (string, []string) {
			return "chromium", []string{"--headless", "--no-sandbox", "--disable-gpu", "--print-to-pdf=" + out, in}
		},
	},
	{
		bin: "chromium-browser",
		command: func(in, out string) (string, []string) {
			return "chromium-browser", []string{"--headless", "--no-sandbox", "--disable-gpu", "--print-to-pdf=" + out, in}
		},
	},
	{
		bin: "wkhtmltopdf",
		command: func(in, out string) (string, []string) {
			return "wkhtmltopdf", []string{in, out}
		},
	},
	{
		bin: "libreoffice",
		command: func(in, out string) (string, []string) {
			return "libreoffice", []string{"--headless", "--convert-to", "pdf", "--outdir", filepath.Dir(out), in}
		},
		dirOutput: true,
	},
	{
		bin: "pandoc",
		command: func(in, out string) (string, []string) {
			return "pandoc", []string{in, "-o", out}
		},
	},
}

// findPDFEngine returns the first supported renderer on PATH. The resolved
// binary path from lookPath is discarded — runEngine re-resolves by name via
// exec.CommandContext — but the probe is what tells us the engine EXISTS.
func findPDFEngine() (pdfEngine, bool) {
	for _, eng := range pdfEngines {
		if _, err := lookPath(eng.bin); err == nil {
			return eng, true
		}
	}
	return pdfEngine{}, false
}

// ToPDF renders the note to HTML (the same document ToHTML produces) and converts
// it with the first headless renderer found on PATH. With none installed it
// returns ErrNoPDFEngine so the caller can prompt the operator to install one,
// rather than failing opaquely.
//
// The HTML is written to a temp file; the engine reads it and writes the PDF to a
// sibling temp file; both are cleaned up. The subprocess is bounded by
// pdfTimeout.
func ToPDF(md []byte, opts Options) ([]byte, error) {
	eng, ok := findPDFEngine()
	if !ok {
		return nil, ErrNoPDFEngine
	}

	htmlDoc, err := ToHTML(md, opts)
	if err != nil {
		return nil, fmt.Errorf("export: pdf: render html: %w", err)
	}

	dir, err := os.MkdirTemp("", "sa-export-")
	if err != nil {
		return nil, fmt.Errorf("export: pdf: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	htmlPath := filepath.Join(dir, "note.html")
	if err := os.WriteFile(htmlPath, htmlDoc, 0o600); err != nil {
		return nil, fmt.Errorf("export: pdf: write html: %w", err)
	}
	outPath := filepath.Join(dir, "note.pdf")

	ctx, cancel := context.WithTimeout(context.Background(), pdfTimeout)
	defer cancel()
	if err := runEngine(ctx, eng, htmlPath, outPath); err != nil {
		return nil, fmt.Errorf("export: pdf: %s failed: %w", eng.bin, err)
	}

	// libreoffice names its output "<input-stem>.pdf" in the target directory,
	// not the path we asked for — reconcile that to outPath before reading.
	if eng.dirOutput {
		produced := filepath.Join(dir, "note.pdf") // input stem "note" + .pdf
		if produced != outPath {
			if err := os.Rename(produced, outPath); err != nil {
				return nil, fmt.Errorf("export: pdf: locate %s output: %w", eng.bin, err)
			}
		}
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("export: pdf: read output: %w", err)
	}
	return data, nil
}
