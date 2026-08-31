// Package convert turns document bytes into markdown. It is a pure function of
// its input: no vault, no network, no LLM, no host state beyond an optional
// preference for a better external extractor when one happens to be installed.
// That purity is what makes it testable against golden fixtures and identical
// across hosts, and it is why conversion lives here rather than inside the tool
// layer — an LLM tool, a web fetch, an HTTP upload handler and a chat adapter
// all need it.
package convert

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedFormat is returned by ToMarkdown when the input's format
// isn't recognized at all, or is recognized but has no converter yet. It is
// a property of the INPUT, never a server fault — callers (the KB upload
// handler, the CLI/coder bridges) use errors.Is against it to report "we
// can't read this kind of file" instead of collapsing every ToMarkdown
// failure (including a genuine disk/parse fault deeper in a specific
// converter) into the same client-facing bucket.
var ErrUnsupportedFormat = errors.New("convert: unsupported format")

// Kind is a detected document format.
type Kind string

const (
	KindMarkdown Kind = "markdown"
	KindHTML     Kind = "html"
	KindPDF      Kind = "pdf"
	KindDOCX     Kind = "docx"
	KindPPTX     Kind = "pptx"
	KindXLSX     Kind = "xlsx"
	KindCSV      Kind = "csv"
	KindTSV      Kind = "tsv"
	KindJSON     Kind = "json"
	KindText     Kind = "text"
	KindImage    Kind = "image"
	KindUnknown  Kind = "unknown"
)

// Options carries hints about the input. Every field is optional: detection
// falls back to the bytes themselves when a hint is missing or wrong.
type Options struct {
	Filename  string // original file name, used as an extension hint and a title fallback
	MIME      string // Content-Type as reported by the source, treated as a hint only
	SourceURL string // where the bytes came from, recorded in the result
}

// AssetRefScheme prefixes the destination of an image the converter extracted
// from inside a document. The bytes travel in Result.Assets; the markdown
// carries "rookery-asset:<index>" until a caller that owns storage rewrites it
// to a real path (vault.ImportFile does this, for every ingest door).
//
// A distinct scheme rather than a plausible relative path, deliberately: if a
// caller ignores Assets, the reference is VISIBLY unresolved instead of silently
// pointing at a file that does not exist — a broken link that names its own
// cause beats one that looks like an ordinary missing image.
const AssetRefScheme = "rookery-asset:"

// Asset is a file extracted from inside a converted document — an image
// embedded in a .docx, a picture on a slide.
//
// It exists because internal/convert must stay a pure function of its input: no
// vault, no filesystem, no host state. That purity is what makes it testable
// against golden fixtures and identical across hosts, so the converter cannot
// write these anywhere itself. It hands them back instead, and the caller that
// already owns storage decides where they land.
type Asset struct {
	// Name is a suggested file name, taken from the document's own part name
	// where there is one. Callers must not trust it as a path — it is sanitised
	// at the point of writing, like any other uploaded file name.
	Name string
	// ContentType is sniffed from the bytes, not read from the container's
	// metadata, which is frequently absent or wrong.
	ContentType string
	Data        []byte
}

// Result is a converted document.
type Result struct {
	Markdown  string   // the converted body; never empty on a nil error
	Title     string   // best-effort document title ("" if none could be derived)
	Kind      Kind     // the format that was detected and converted
	Extractor string   // which code path produced Markdown (e.g. "pure-go", "pdftotext")
	Warnings  []string // non-fatal quality notes, surfaced to the user in note frontmatter
	// Assets are files extracted from inside the document, referenced from
	// Markdown as AssetRefScheme + the asset's index in this slice.
	Assets []Asset
}

// ToMarkdown detects the format of data and converts it to markdown. It returns
// an error naming the format when conversion is not supported — never a silent
// empty result.
func ToMarkdown(data []byte, opt Options) (Result, error) {
	kind := Detect(data, opt.Filename, opt.MIME)
	switch kind {
	case KindHTML:
		return htmlToMarkdown(data, opt)
	case KindMarkdown, KindText:
		return passthrough(data, kind, opt), nil
	case KindCSV, KindTSV:
		return tabularToMarkdown(data, kind, opt)
	case KindDOCX:
		return docxToMarkdown(data, opt)
	case KindXLSX:
		return xlsxToMarkdown(data, opt)
	case KindPPTX:
		return pptxToMarkdown(data, opt)
	case KindPDF:
		return pdfToMarkdown(data, opt)
	case KindImage:
		return imageToMarkdown(data, opt)
	case KindJSON:
		// Detect classifies KindJSON from the extension/MIME hint alone, with no
		// JSON validation — so this content is arbitrary uploaded bytes, not
		// necessarily well-formed JSON. A fixed "```json" fence breaks the moment
		// that content contains its own backtick-fence line (or simply lacks a
		// trailing newline before the literal "```" this code appends), dumping
		// the remainder as loose markdown plus a stray fence. codeFence sizes the
		// fence past any run already in the content; normalizeText guarantees the
		// body ends in "\n" so the closing fence always starts its own line.
		body := normalizeText(string(data))
		fence := codeFence(body)
		return Result{
			Markdown:  fence + "json\n" + body + fence + "\n",
			Title:     titleFromFilename(opt.Filename),
			Kind:      KindJSON,
			Extractor: "pure-go",
		}, nil
	case KindUnknown:
		return Result{}, fmt.Errorf("%w: unrecognized format (%d bytes); no converter applies", ErrUnsupportedFormat, len(data))
	default:
		return Result{}, fmt.Errorf("%w: %s not yet implemented", ErrUnsupportedFormat, kind)
	}
}

// passthrough normalizes already-textual input.
//
// The two kinds are deliberately NOT treated the same, though both arrive here
// as text. A .md upload is markdown the user wrote: its asterisks and brackets
// are intended as syntax, so it is returned byte-for-byte and any fidelity
// problem in it is the author's own, exactly as for a note typed in the editor.
//
// A .txt upload is not markdown. Its characters are literal, and returning them
// unescaped was the reason a plain text file containing "a < b" or a "[12]"
// citation imported into a note that could not be edited: those round-trip
// through the editor as "a &lt; b" and "\[12\]", the check fails, and the note
// opens read-only. Escaping changes nothing a reader sees — it is the same text
// once rendered — and makes the note editable.
//
// This does not attempt to stop plain text being INTERPRETED as markdown (a
// line starting "# " still becomes a heading). That is long-standing behaviour,
// it is what makes a pasted-in text file useful, and changing it is a separate
// decision from making the result editable.
func passthrough(data []byte, kind Kind, opt Options) Result {
	body := normalizeText(string(data))
	if kind == KindText {
		body = escapeTextBlock(body)
	}
	return Result{
		Markdown:  body,
		Title:     titleFromFilename(opt.Filename),
		Kind:      kind,
		Extractor: "pure-go",
	}
}

// escapeTextBlock applies the inline rules line by line, so that a leading "-"
// is judged per line rather than once for the whole document. Blank lines are
// preserved exactly, since they are what separate paragraphs.
func escapeTextBlock(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		lines[i] = escapeLeadingMarker(EscapeInline(ln))
	}
	return strings.Join(lines, "\n")
}
