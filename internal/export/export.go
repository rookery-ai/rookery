// Package export turns a KB markdown note into a downloadable document —
// HTML, DOCX, or PDF. It is the sanctioned reverse of internal/convert (which
// is one-directional, into markdown), and is kept in its own package precisely
// so that convert stays into-markdown-only.
//
// Like convert, this is a pure function of its input for the always-available
// formats: HTML and DOCX need no host tools and no network, which is what makes
// them testable against golden fixtures and identical across hosts. PDF is the
// one best-effort format — it shells out to whichever headless renderer happens
// to be on PATH — mirroring how convert prefers an external pdftotext when one
// is installed but never requires it.
//
// The package renders markdown as-is: splitting frontmatter off the note, or
// choosing a filename, is the caller's job (the KB export handler). Callers pass
// the note body they want in the document.
package export

import "errors"

// ErrNoPDFEngine is returned by ToPDF when no supported headless renderer is on
// PATH. It is a property of the HOST (nothing is installed), never a fault of
// the input, so the handler can turn it into a "install one of these tools for
// PDF export" message rather than a generic 500. Callers use errors.Is against
// it.
var ErrNoPDFEngine = errors.New("export: no PDF engine available")

// Options carries rendering hints. Every field is optional; a zero Options
// produces a valid document with generic defaults.
type Options struct {
	// Title is used for the HTML document's <title> and the DOCX document
	// title. When empty a neutral default ("Note") is used so the output is
	// never headless.
	Title string
}

// Formats reports which export formats are usable right now. HTML and DOCX are
// always true (pure-Go, no host deps); PDF is true only when a headless
// renderer is on PATH. The UI uses this to grey out PDF when it can't be
// produced.
type Formats struct {
	HTML bool `json:"html"`
	DOCX bool `json:"docx"`
	PDF  bool `json:"pdf"`
}

// AvailableFormats reports the formats this host can currently produce.
func AvailableFormats() Formats {
	_, ok := findPDFEngine()
	return Formats{HTML: true, DOCX: true, PDF: ok}
}

// docTitle returns the effective document title, falling back to a neutral
// default so neither the HTML <title> nor the DOCX title is ever blank.
func (o Options) docTitle() string {
	if o.Title == "" {
		return "Note"
	}
	return o.Title
}
