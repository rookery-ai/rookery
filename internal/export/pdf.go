package export

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/browser"
)

// pdfTimeout bounds a headless renderer subprocess. A browser or LibreOffice can
// hang on pathological input; the context kills it from outside.
const pdfTimeout = 30 * time.Second

// lookPath is exec.LookPath as a package variable so tests can force the
// "no engine" branch (and a specific engine) without touching the real PATH.
var lookPath = exec.LookPath

// bundledChromium resolves the Chromium this platform installs for itself. A
// package variable for the same reason lookPath is: a test must be able to
// describe a host it is not running on.
var bundledChromium = browser.ChromiumExecutable

// runEngine executes the chosen renderer. It is a package variable so tests can
// substitute a stub that writes fake PDF bytes to outPath, exercising the
// success branch without any engine installed. ctx carries the timeout, htmlPath
// is the input HTML file, outPath is where the PDF must land.
//
// Engine stderr is captured and folded into the error. It used to be discarded,
// so every renderer failure — a missing shared library, a sandbox refusal, a
// LaTeX engine pandoc could not find — surfaced to the operator as an identical
// bare 500 with nothing to act on.
var runEngine = func(ctx context.Context, eng pdfEngine, binPath, htmlPath, outPath string) error {
	name, args := eng.command(binPath, htmlPath, outPath)
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, lastLines(msg, 5))
		}
		return err
	}
	return nil
}

// lastLines keeps the tail of a subprocess's stderr. Renderers are verbose and
// the actionable line is almost always the last one; the whole stream would push
// the useful part out of a log line or an error message.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// pdfEngine describes one supported headless renderer: how to locate it and how
// to build its argv for an HTML→PDF conversion.
type pdfEngine struct {
	// bin is the executable name probed with lookPath, and the name reported in
	// an error. For the bundled Chromium it is a label, not a PATH entry.
	bin string
	// locate resolves the executable, or returns false when it is unavailable.
	// Most engines just look on PATH; the bundled Chromium looks in Playwright's
	// cache, which is the whole reason this is a function rather than a name.
	locate func() (string, bool)
	// command returns the resolved binary path and its arguments. Most engines
	// name the output file directly; libreoffice can only target a directory, so
	// each engine owns its own arg shape.
	command func(binPath, htmlPath, outPath string) (string, []string)
	// dirOutput is true for engines that write "<stem>.pdf" into the output
	// file's DIRECTORY rather than to the exact outPath (libreoffice).
	dirOutput bool
}

// onPath returns a locate function for an ordinary PATH lookup.
func onPath(bin string) func() (string, bool) {
	return func() (string, bool) {
		if p, err := lookPath(bin); err == nil {
			return p, true
		}
		return "", false
	}
}

// chromiumArgs is shared by every Chromium-family engine so the flags cannot
// drift between the bundled build and one found on PATH.
//
// --no-pdf-header-footer is not cosmetic: without it Chromium stamps the print
// date and the source file:// URL onto every page, so an exported note carried a
// temp-file path in its header.
//
// --disable-dev-shm-usage is here for the same reason internal/browser/host.go
// sets it: a CONTAINER's /dev/shm defaults to 64 MB, which a full Chromium
// exceeds, and it then crashes on content-heavy pages. Rookery ships a container
// image, so this is a real target rather than a hypothetical one.
//
// It is deliberately NOT the fix for the hang that findPDFEngine addresses — a
// GitHub-hosted VM runner sizes /dev/shm from RAM, so this flag is inert there.
// Adding it here on that theory was a wrong first diagnosis; it is kept because
// it is correct for the container case, not because it fixed anything observed.
func chromiumArgs(binPath, in, out string) (string, []string) {
	return binPath, []string{
		"--headless",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--no-pdf-header-footer",
		"--print-to-pdf=" + out,
		in,
	}
}

// pdfEngines is the detection order.
//
// The Chromium this platform installs for itself comes FIRST. It is the only
// engine whose presence Rookery controls, `rookery browser install` puts it
// there, and /healthz already reports it — while PDF export used to declare
// itself unavailable on that same host and advise installing a renderer the
// operator already had.
//
// pandoc was REMOVED, not reordered. `pandoc in.html -o out.pdf` needs a LaTeX
// engine pandoc does not bundle, so on a host with pandoc and no LaTeX the probe
// reported PDF as available, the UI offered the button, and the call then failed
// as an opaque 500. An honest "unavailable" is better than a promise that breaks
// at the moment of use; a host with pandoc almost certainly has one of the
// others, and if not, the message names what to install.
var pdfEngines = []pdfEngine{
	{
		bin:     "chromium (bundled)",
		locate:  func() (string, bool) { p := bundledChromium(); return p, p != "" },
		command: chromiumArgs,
	},
	{
		bin:    "weasyprint",
		locate: onPath("weasyprint"),
		command: func(binPath, in, out string) (string, []string) {
			return binPath, []string{in, out}
		},
	},
	{bin: "chromium", locate: onPath("chromium"), command: chromiumArgs},
	{bin: "chromium-browser", locate: onPath("chromium-browser"), command: chromiumArgs},
	// The Google-branded builds are the usual spelling on Debian/Ubuntu and
	// macOS and were simply never probed.
	{bin: "google-chrome", locate: onPath("google-chrome"), command: chromiumArgs},
	{bin: "google-chrome-stable", locate: onPath("google-chrome-stable"), command: chromiumArgs},
	{
		bin:    "wkhtmltopdf",
		locate: onPath("wkhtmltopdf"),
		command: func(binPath, in, out string) (string, []string) {
			return binPath, []string{in, out}
		},
	},
	{
		bin:    "libreoffice",
		locate: onPath("libreoffice"),
		command: func(binPath, in, out string) (string, []string) {
			return binPath, []string{"--headless", "--convert-to", "pdf", "--outdir", filepath.Dir(out), in}
		},
		dirOutput: true,
	},
	// soffice is the same program under the name most non-Debian distributions
	// and macOS actually install.
	{
		bin:    "soffice",
		locate: onPath("soffice"),
		command: func(binPath, in, out string) (string, []string) {
			return binPath, []string{"--headless", "--convert-to", "pdf", "--outdir", filepath.Dir(out), in}
		},
		dirOutput: true,
	},
}

// findPDFEngine returns the first supported renderer that is present AND can
// actually run, together with its resolved executable path.
//
// Being on PATH is not the same as working, and the difference is expensive
// here. A name on PATH can be a wrapper that never renders anything — on Ubuntu
// `/usr/bin/chromium` is frequently a snap shim — and the failure mode is the
// worst available: it does not error, it HANGS, so the export sits there until
// pdfTimeout kills it and reports `signal: killed` after 30 seconds. Nothing in
// that message suggests the engine was the wrong one, and because the check was
// "does the name resolve", the next export picks the same broken engine again.
//
// This is the same class of defect as the Windows Python launcher stub that
// `internal/onboard` rejects by running the candidate: existence is not
// evidence. Verifying costs one `--version` per export on the happy path (tens
// of milliseconds) and bounds the unhappy path at engineProbeTimeout instead of
// pdfTimeout — after which the NEXT engine gets a turn, which is the behaviour
// the ordered list was written for and never had.
func findPDFEngine() (pdfEngine, string, bool) {
	for _, eng := range pdfEngines {
		p, ok := eng.locate()
		if !ok || !engineRuns(p) {
			continue
		}
		return eng, p, true
	}
	return pdfEngine{}, "", false
}

// engineProbeTimeout bounds the "can you run at all" check. It is far shorter
// than pdfTimeout on purpose: this asks a binary to print its version, which is
// immediate for anything healthy, so a slow answer IS the answer.
const engineProbeTimeout = 5 * time.Second

// engineRuns is a package variable for the same reason lookPath and runEngine
// are: a test describing a host it is not running on has a path but no binary
// there to execute.
var engineRuns = engineRunsReal

// engineRunsReal reports whether a located renderer actually executes.
//
// Every engine in pdfEngines accepts --version, and none of them renders
// anything or touches the filesystem in response, which is what makes this safe
// to call before committing to one.
func engineRunsReal(binPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), engineProbeTimeout)
	defer cancel()

	return exec.CommandContext(ctx, binPath, "--version").Run() == nil
}

// ToPDF renders the note to HTML (the same document ToHTML produces) and converts
// it with the first headless renderer available. With none installed it returns
// ErrNoPDFEngine so the caller can prompt the operator to install one, rather
// than failing opaquely.
//
// The HTML is written to a temp file; the engine reads it and writes the PDF to a
// sibling temp file; both are cleaned up. The subprocess is bounded by
// pdfTimeout.
func ToPDF(md []byte, opts Options) ([]byte, error) {
	eng, binPath, ok := findPDFEngine()
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
	// The output deliberately does NOT share the input's stem. libreoffice names
	// its result after the input ("note.html" -> "note.pdf"), so writing to
	// "note.pdf" made the reconciliation below compare a path to itself — dead
	// code that hid the fact that nothing had ever exercised the libreoffice
	// path at all.
	outPath := filepath.Join(dir, "out.pdf")

	ctx, cancel := context.WithTimeout(context.Background(), pdfTimeout)
	defer cancel()
	if err := runEngine(ctx, eng, binPath, htmlPath, outPath); err != nil {
		return nil, fmt.Errorf("export: pdf: %s failed: %w", eng.bin, err)
	}

	if eng.dirOutput {
		produced := filepath.Join(dir, "note.pdf") // input stem "note" + .pdf
		if err := os.Rename(produced, outPath); err != nil {
			return nil, fmt.Errorf("export: pdf: locate %s output: %w", eng.bin, err)
		}
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("export: pdf: read output: %w", err)
	}
	// A renderer that exits 0 having written nothing usable is a failure the
	// caller must not hand to a browser as a download.
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		return nil, fmt.Errorf("export: pdf: %s produced %d bytes that are not a PDF", eng.bin, len(data))
	}
	return data, nil
}
