// Package convert turns document bytes into markdown. It is a pure function of
// its input: no vault, no network, no LLM, no host state beyond an optional
// preference for a better external extractor when one happens to be installed.
// That purity is what makes it testable against golden fixtures and identical
// across hosts, and it is why conversion lives here rather than inside the tool
// layer — an LLM tool, a web fetch, an HTTP upload handler and a chat adapter
// all need it.
package convert

import "fmt"

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

// Result is a converted document.
type Result struct {
	Markdown  string   // the converted body; never empty on a nil error
	Title     string   // best-effort document title ("" if none could be derived)
	Kind      Kind     // the format that was detected and converted
	Extractor string   // which code path produced Markdown (e.g. "pure-go", "pdftotext")
	Warnings  []string // non-fatal quality notes, surfaced to the user in note frontmatter
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
		// OCR needs tesseract, which is not a pure-Go dependency and is out of
		// scope. Produce an honest stub rather than an error: the file is still
		// worth recording in the knowledge base, and the note says plainly that
		// no text was read from it.
		return Result{
			Markdown:  fmt.Sprintf("(image file, %d bytes — no text was extracted; OCR is not available)\n", len(data)),
			Title:     titleFromFilename(opt.Filename),
			Kind:      KindImage,
			Extractor: "none",
			Warnings:  []string{"image content is not searchable: no OCR"},
		}, nil
	case KindJSON:
		return Result{
			Markdown:  "```json\n" + normalizeText(string(data)) + "```\n",
			Title:     titleFromFilename(opt.Filename),
			Kind:      KindJSON,
			Extractor: "pure-go",
		}, nil
	case KindUnknown:
		return Result{}, fmt.Errorf("convert: unrecognized format (%d bytes); no converter applies", len(data))
	default:
		return Result{}, fmt.Errorf("convert: %s is not supported yet", kind)
	}
}

// passthrough normalizes already-textual input. Markdown is returned as-is;
// plain text is returned as-is too (it is valid markdown), so no information is
// invented or lost.
func passthrough(data []byte, kind Kind, opt Options) Result {
	return Result{
		Markdown:  normalizeText(string(data)),
		Title:     titleFromFilename(opt.Filename),
		Kind:      kind,
		Extractor: "pure-go",
	}
}
