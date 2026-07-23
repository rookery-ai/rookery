package convert

import (
	"bytes"
	"path/filepath"
	"strings"
)

// Detect identifies the format of data. Magic bytes are checked FIRST and win
// outright: servers mislabel Content-Type and users rename files, but a PDF
// header is a PDF header. Only when the bytes are inconclusive do the filename
// extension and the reported MIME type get a say.
func Detect(data []byte, filename, mime string) Kind {
	if len(data) == 0 {
		return KindUnknown
	}
	if k := detectMagic(data); k != KindUnknown {
		return k
	}
	// A zip could be any OOXML format — they share magic bytes — so inspect the
	// archive's parts before falling back to the (possibly wrong) extension.
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		if k := detectOOXMLKind(data); k != KindUnknown {
			return k
		}
	}
	if k := kindFromExtension(filename); k != KindUnknown {
		return k
	}
	if k := kindFromMIME(mime); k != KindUnknown {
		return k
	}
	if looksHTML(data) {
		return KindHTML
	}
	if isMostlyText(data) {
		return KindText
	}
	return KindUnknown
}

// detectMagic recognizes formats by their leading bytes. A zip container is
// deliberately NOT resolved here — docx, pptx and xlsx share the same magic, so
// the caller falls through to the extension (and, in phase 2, to inspecting the
// archive's parts).
func detectMagic(data []byte) Kind {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return KindPDF
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(data, []byte("\xff\xd8\xff")),
		bytes.HasPrefix(data, []byte("GIF87a")),
		bytes.HasPrefix(data, []byte("GIF89a")),
		bytes.HasPrefix(data, []byte("BM")):
		return KindImage
	}
	if len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return KindImage
	}
	return KindUnknown
}

func kindFromExtension(filename string) Kind {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".md", ".markdown":
		return KindMarkdown
	case ".html", ".htm":
		return KindHTML
	case ".pdf":
		return KindPDF
	case ".docx":
		return KindDOCX
	case ".pptx":
		return KindPPTX
	case ".xlsx":
		return KindXLSX
	case ".csv":
		return KindCSV
	case ".tsv", ".tab":
		return KindTSV
	case ".json":
		return KindJSON
	case ".txt", ".log", ".text":
		return KindText
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return KindImage
	}
	return KindUnknown
}

func kindFromMIME(mime string) Kind {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	switch {
	case m == "":
		return KindUnknown
	case strings.Contains(m, "html"):
		return KindHTML
	case strings.Contains(m, "pdf"):
		return KindPDF
	case strings.Contains(m, "wordprocessingml"):
		return KindDOCX
	case strings.Contains(m, "presentationml"):
		return KindPPTX
	case strings.Contains(m, "spreadsheetml"):
		return KindXLSX
	case strings.Contains(m, "csv"):
		return KindCSV
	case strings.Contains(m, "tab-separated"):
		return KindTSV
	case strings.Contains(m, "json"):
		return KindJSON
	case strings.HasPrefix(m, "image/"):
		return KindImage
	case strings.Contains(m, "markdown"):
		return KindMarkdown
	case strings.HasPrefix(m, "text/"):
		return KindText
	}
	return KindUnknown
}

// looksHTML reports whether the first chunk of data contains an HTML marker.
// Only the head is scanned so a large body costs nothing.
func looksHTML(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	lower := bytes.ToLower(head)
	for _, marker := range [][]byte{[]byte("<!doctype html"), []byte("<html"), []byte("<head"), []byte("<body")} {
		if bytes.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// isMostlyText reports whether data decodes as text: no NUL bytes and few
// control characters in the sampled head.
func isMostlyText(data []byte) bool {
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	ctrl := 0
	for _, b := range head {
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			ctrl++
		}
	}
	return ctrl*100 <= len(head)
}

// IsTextual reports whether a body should be handed to a caller as plain text
// when no converter applies: a declared textual MIME type, or bytes that decode
// as text. Exported because web_fetch needs exactly this fallback decision.
func IsTextual(data []byte, mime string) bool {
	switch kindFromMIME(mime) {
	case KindText, KindJSON, KindCSV, KindTSV, KindMarkdown, KindHTML:
		return true
	}
	m := strings.ToLower(mime)
	if strings.Contains(m, "xml") || strings.Contains(m, "javascript") {
		return true
	}
	return isMostlyText(data)
}

// utf8BOM is the UTF-8 encoding of U+FEFF, which Excel and other Windows
// tools prepend to exported text by default. Left in place it becomes part of
// whatever text follows it — most damagingly the first header cell of a
// table, which is exactly what an agent keys, matches, or joins on.
const utf8BOM = "\xEF\xBB\xBF"

// normalizeText makes line endings uniform, strips a leading UTF-8 BOM, and
// trims trailing whitespace so converted output is byte-stable across
// sources. This only strips a BOM that is the very first thing in s: markdown
// and plain-text passthrough hand it raw file bytes, so a BOM there is always
// at position 0 and this is exactly the fix. The tabular and HTML renderers
// call this on their own already-rendered output (e.g. "| ... |\n" markdown),
// where a BOM from the source, if any survived, would be buried inside a
// cell rather than leading the string — so this alone would not have fixed a
// BOM'd CSV header (see tabularToMarkdown, which strips it from the raw bytes
// before parsing for exactly that reason). Stripping only a genuine leading
// BOM cannot affect any other converter's byte-exactness expectations: no
// legitimate output is supposed to start with U+FEFF.
func normalizeText(s string) string {
	s = strings.TrimPrefix(s, utf8BOM)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimRight(s, " \t\n") + "\n"
}

// codeFence returns a fence of backticks long enough that content cannot
// close it early. CommonMark treats any run of 3+ backticks as a fence, and a
// closing fence only needs to be *at least* as long as the opening one — so a
// content line of exactly ``` (three backticks) breaks a naive fixed "```"
// wrapper, dumping the remainder of the content as loose markdown plus a
// stray fence. Using longest-run-in-content + 1 (floored at 3) makes that
// impossible: no run inside content can ever reach the fence's own length.
func codeFence(content string) string {
	longest := 0
	run := 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// titleFromFilename derives a human title from a file name: "q3-sales.pdf" →
// "q3 sales". Returns "" when there is no usable name.
func titleFromFilename(filename string) string {
	base := filepath.Base(filename)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.NewReplacer("-", " ", "_", " ").Replace(base)
	return strings.TrimSpace(base)
}
