# Tool Reliability Implementation Plan (SP23)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the platform's three most-used tool surfaces reliable — web fetch/search, document→markdown conversion, and knowledge-base retrieval — by moving each behind a plain Go package the application can call directly.

**Architecture:** Three new units. `internal/convert` turns bytes into markdown (pure function, no vault/network/LLM). `internal/websearch` turns a query into results via a provider cascade. `internal/vault/index.go` adds BM25 chunk retrieval behind the existing `vault.Searcher` interface. Every LLM tool, HTTP handler, and chat adapter is a thin adapter over these.

**Tech Stack:** Go 1.26.4, stdlib `archive/zip` + `encoding/xml` for OOXML, `golang.org/x/net/html` (already indirect — promote to direct) for HTML, `github.com/yuin/goldmark` (already direct) for markdown chunking, one new pure-Go PDF text extractor.

**Spec:** `docs/superpowers/specs/2026-07-22-tool-reliability-design.md`

## Global Constraints

- **CGo-free.** Every dependency must build without CGo (this codebase uses `modernc.org/sqlite` for exactly this reason). Verify with `CGO_ENABLED=0 go build ./...`.
- **No new heavy modules.** OOXML via stdlib `archive/zip` + `encoding/xml`. The only permitted new module is one pure-Go PDF text extractor.
- **No tool ever returns an empty string.** An empty result is an explicit, non-error notice (e.g. `(no matches for "x")`). An empty tool result breaks strict provider serializers and trips the oscillation guard.
- **Transient failures never surface as `error:`.** 429/5xx/network/timeout are retried internally. `executeOrNudge` treats every `error:` result as a repeat-worthy failure, so a blip that clears on its own must not reach it.
- **Existing tool names are preserved.** `search_files`, `web_fetch`, `web_search` keep their names; only behaviour and descriptions change. Models and prompts are already primed on them.
- **Byte caps.** `maxToolResult` = 8 KiB (existing, unchanged). Designer KB context cap = 6 KiB. `web_search` returns at most 6 results (existing `maxWebSearchResults`).
- **Tests are hermetic.** No network, no reliance on host binaries. Where a host binary is optional (`pdftotext`), test both the present and absent branches.
- **Run `go test ./... -count=1 -timeout 120s` before every commit.**

## File Structure

**Phase 1 — web**

| File | Responsibility |
|---|---|
| `internal/convert/convert.go` (create) | `Kind`, `Options`, `Result`, `ToMarkdown` dispatch |
| `internal/convert/detect.go` (create) | Magic-byte sniffing, extension fallback |
| `internal/convert/html.go` (create) | `golang.org/x/net/html` walker → markdown |
| `internal/coder/netguard.go` (create) | Private/loopback/metadata address denial via dialer control |
| `internal/websearch/websearch.go` (create) | `Provider`, `Result`, `Search` cascade |
| `internal/websearch/ddg.go` (create) | DuckDuckGo html + lite providers |
| `internal/websearch/engines.go` (create) | Mojeek + Bing providers |
| `internal/websearch/keyed.go` (create) | Brave/Tavily keyed providers |
| `internal/coder/hosttools.go` (modify) | `webSearch` delegates to `websearch`; `webFetch` uses `convert` + netguard; un-gate both |
| `internal/prompts/prompts.go` (modify) | Chat prompt advertises web tools |
| `cmd/simple-agents/main.go` (modify) | CLI chat allowed-tools gains `WebFetch,WebSearch` |

**Phase 2 — conversion** (`internal/convert/{tabular,ooxml,pdf}.go`, `internal/vault/import.go`, `internal/coder/hosttools.go`, `cmd/simple-agents/main.go`, `web/api_kb.go`, `web/ui/src/pages/kb/`, `internal/gateway/telegram.go`)

**Phase 3 — retrieval** (`internal/vault/index.go`, `internal/vault/chunk.go`, `internal/vault/search.go`, `internal/coder/hosttools.go`, `internal/agentdesigner/flow.go`, `internal/skilldesigner/flow.go`, `internal/prompts/prompts.go`)

---

# PHASE 1 — Web tools

Ends mergeable: web search survives any single engine failing, fetch parses HTML properly, chat gets web access, and private address space is unreachable.

---

### Task 1: Convert package skeleton + format detection

**Files:**
- Create: `internal/convert/convert.go`
- Create: `internal/convert/detect.go`
- Test: `internal/convert/detect_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `convert.Kind` (string enum), `convert.Options{Filename, MIME, SourceURL string}`, `convert.Result{Markdown, Title string; Kind Kind; Extractor string; Warnings []string}`, `convert.Detect(data []byte, filename, mime string) Kind`, `convert.ToMarkdown(data []byte, opt Options) (Result, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/convert/detect_test.go`:

```go
package convert

import "testing"

func TestDetect(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		filename string
		mime     string
		want     Kind
	}{
		{"pdf by magic", []byte("%PDF-1.7\n..."), "", "", KindPDF},
		{"pdf magic beats wrong extension", []byte("%PDF-1.7\n..."), "report.txt", "", KindPDF},
		{"zip is unknown without ooxml part", []byte("PK\x03\x04rest"), "", "", KindUnknown},
		{"html by doctype", []byte("<!DOCTYPE html><html>"), "", "", KindHTML},
		{"html by tag", []byte("\n  <html lang=\"en\">"), "", "", KindHTML},
		{"html by mime", []byte("no markers here"), "", "text/html; charset=utf-8", KindHTML},
		{"markdown by extension", []byte("# Title"), "notes/a.md", "", KindMarkdown},
		{"csv by extension", []byte("a,b\n1,2"), "data.csv", "", KindCSV},
		{"tsv by extension", []byte("a\tb"), "data.tsv", "", KindTSV},
		{"json by mime", []byte(`{"a":1}`), "", "application/json", KindJSON},
		{"png by magic", []byte("\x89PNG\r\n\x1a\n"), "", "", KindImage},
		{"jpeg by magic", []byte("\xff\xd8\xff\xe0"), "", "", KindImage},
		{"plain text default", []byte("just some words"), "", "", KindText},
		{"empty is unknown", nil, "", "", KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(tc.data, tc.filename, tc.mime); got != tc.want {
				t.Errorf("Detect() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectOOXMLByExtension(t *testing.T) {
	// A real OOXML file is a zip; without unzipping we fall back to the extension.
	zip := []byte("PK\x03\x04something")
	for _, tc := range []struct {
		filename string
		want     Kind
	}{
		{"a.docx", KindDOCX},
		{"a.xlsx", KindXLSX},
		{"a.pptx", KindPPTX},
	} {
		if got := Detect(zip, tc.filename, ""); got != tc.want {
			t.Errorf("Detect(%s) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/user/simple-agents-v2 && go test ./internal/convert/ -run TestDetect -v`
Expected: FAIL — `no required module provides package .../internal/convert` (package does not exist yet).

- [ ] **Step 3: Write the types**

Create `internal/convert/convert.go`:

```go
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
```

- [ ] **Step 4: Write detection**

Create `internal/convert/detect.go`:

```go
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
	if len(data) > 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return KindImage
	}
	if looksHTML(data) {
		return KindHTML
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

// normalizeText makes line endings uniform and trims trailing whitespace so
// converted output is byte-stable across sources.
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimRight(s, " \t\n") + "\n"
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/convert/ -v`
Expected: PASS — `TestDetect` and `TestDetectOOXMLByExtension` both ok.

- [ ] **Step 6: Commit**

```bash
git add internal/convert/
git commit -m "feat(convert): add format detection and conversion skeleton"
```

---

### Task 2: HTML → markdown

**Files:**
- Create: `internal/convert/html.go`
- Test: `internal/convert/html_test.go`
- Modify: `go.mod` (promote `golang.org/x/net` to a direct dependency)

**Interfaces:**
- Consumes: `Result`, `Options`, `Kind` (Task 1)
- Produces: `htmlToMarkdown(data []byte, opt Options) (Result, error)` — called by `ToMarkdown`'s `KindHTML` branch, and in Task 4 by `web_fetch`

- [ ] **Step 1: Write the failing test**

Create `internal/convert/html_test.go`:

```go
package convert

import "strings"

import "testing"

func TestHTMLToMarkdown(t *testing.T) {
	doc := `<!DOCTYPE html><html><head><title>Q3 Report</title>
	<style>.x{color:red}</style><script>var a=1;</script></head>
	<body>
	  <nav>Home About Contact</nav>
	  <header>Site banner</header>
	  <main>
	    <h1>Revenue</h1>
	    <p>Revenue grew by <strong>12%</strong> this quarter.</p>
	    <ul><li>EMEA up</li><li>APAC flat</li></ul>
	    <a href="https://example.com/detail">Full detail</a>
	  </main>
	  <footer>Copyright 2026</footer>
	</body></html>`

	got, err := ToMarkdown([]byte(doc), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindHTML {
		t.Errorf("Kind = %q, want html", got.Kind)
	}
	if got.Title != "Q3 Report" {
		t.Errorf("Title = %q, want %q", got.Title, "Q3 Report")
	}
	for _, want := range []string{
		"# Revenue",
		"**12%**",
		"- EMEA up",
		"- APAC flat",
		"[Full detail](https://example.com/detail)",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("markdown missing %q, got:\n%s", want, got.Markdown)
		}
	}
	// <main> is present, so chrome outside it is dropped entirely.
	for _, unwanted := range []string{"Home About Contact", "Site banner", "Copyright 2026", "var a=1", "color:red"} {
		if strings.Contains(got.Markdown, unwanted) {
			t.Errorf("markdown should not contain %q, got:\n%s", unwanted, got.Markdown)
		}
	}
}

func TestHTMLToMarkdownWithoutMain(t *testing.T) {
	// No <main>/<article>: fall back to <body> but still drop nav/footer/script.
	doc := `<html><body><nav>Skip me</nav><p>Keep this.</p><footer>Not this</footer></body></html>`
	got, err := ToMarkdown([]byte(doc), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "Keep this.") {
		t.Errorf("missing body text, got:\n%s", got.Markdown)
	}
	if strings.Contains(got.Markdown, "Skip me") || strings.Contains(got.Markdown, "Not this") {
		t.Errorf("chrome leaked, got:\n%s", got.Markdown)
	}
}

func TestHTMLTable(t *testing.T) {
	doc := `<table>
	  <tr><th>Region</th><th>Sales</th></tr>
	  <tr><td>EMEA</td><td>120</td></tr>
	</table>`
	got, err := ToMarkdown([]byte(doc), Options{MIME: "text/html"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	for _, want := range []string{"| Region | Sales |", "| --- | --- |", "| EMEA | 120 |"} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("table missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestHTMLNeverEmpty(t *testing.T) {
	// A document with no extractable text must still not produce an empty body.
	got, err := ToMarkdown([]byte("<html><body><script>x=1</script></body></html>"), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.TrimSpace(got.Markdown) == "" {
		t.Error("Markdown must never be empty on a nil error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/convert/ -run TestHTML -v`
Expected: FAIL — `undefined: htmlToMarkdown` (the `KindHTML` branch in `ToMarkdown` references it).

- [ ] **Step 3: Promote the x/net dependency**

Run:

```bash
go get golang.org/x/net@v0.55.0
```

Expected: `golang.org/x/net v0.55.0` moves from the indirect `require` block to the direct one. No version change — it is already in the module graph via echo.

- [ ] **Step 4: Write the converter**

Create `internal/convert/html.go`:

```go
package convert

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlToMarkdown converts an HTML document to markdown using a real parser.
// The previous approach (four regexes over the raw source) collapsed an entire
// page — nav, cookie banner, body, footer — into one whitespace-run with no
// structure. A parse tree lets us do the two things that actually matter for
// model context: drop chrome, and preserve headings, lists, links and tables.
func htmlToMarkdown(data []byte, opt Options) (Result, error) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return Result{}, fmt.Errorf("convert: parse html: %w", err)
	}
	res := Result{Kind: KindHTML, Extractor: "pure-go"}
	res.Title = strings.TrimSpace(textOf(findFirst(doc, atom.Title)))
	if res.Title == "" {
		res.Title = titleFromFilename(opt.Filename)
	}

	// Prefer the semantic content root when the page provides one; a page that
	// marks up <main> or <article> is telling us exactly where the content is.
	root := findFirst(doc, atom.Main)
	if root == nil {
		root = findFirst(doc, atom.Article)
	}
	if root == nil {
		root = findFirst(doc, atom.Body)
	}
	if root == nil {
		root = doc
	}

	var w mdWriter
	w.walk(root)
	body := normalizeText(collapseBlankLines(w.sb.String()))

	if strings.TrimSpace(body) == "" {
		body = "(no readable text content)\n"
		res.Warnings = append(res.Warnings, "no readable text extracted from HTML")
	}
	res.Markdown = body
	return res, nil
}

// skipTags are elements whose subtree is never content: page chrome, and code
// the browser executes rather than displays.
var skipTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Nav: true, atom.Header: true, atom.Footer: true, atom.Aside: true,
	atom.Form: true, atom.Svg: true,
}

// mdWriter accumulates markdown while walking the parse tree.
type mdWriter struct {
	sb       strings.Builder
	listItem bool // currently inside an <li>, so text is emitted after a bullet
}

func (w *mdWriter) walk(n *html.Node) {
	if n == nil {
		return
	}
	if n.Type == html.TextNode {
		w.text(n.Data)
		return
	}
	if n.Type != html.ElementNode && n.Type != html.DocumentNode {
		return
	}
	if skipTags[n.DataAtom] {
		return
	}

	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		w.block()
		w.sb.WriteString(strings.Repeat("#", level) + " " + squeeze(textOf(n)))
		w.block()
		return
	case atom.P, atom.Div, atom.Section, atom.Blockquote:
		w.block()
		w.children(n)
		w.block()
		return
	case atom.Br:
		w.sb.WriteString("\n")
		return
	case atom.Hr:
		w.block()
		w.sb.WriteString("---")
		w.block()
		return
	case atom.Li:
		w.block()
		w.sb.WriteString("- ")
		w.listItem = true
		w.children(n)
		w.listItem = false
		w.sb.WriteString("\n")
		return
	case atom.Strong, atom.B:
		w.inline("**", n)
		return
	case atom.Em, atom.I:
		w.inline("*", n)
		return
	case atom.Code:
		w.inline("`", n)
		return
	case atom.Pre:
		w.block()
		w.sb.WriteString("```\n" + strings.TrimSpace(textOf(n)) + "\n```")
		w.block()
		return
	case atom.A:
		text := squeeze(textOf(n))
		href := attr(n, "href")
		if text == "" {
			return
		}
		if href == "" || strings.HasPrefix(href, "javascript:") {
			w.sb.WriteString(text)
			return
		}
		fmt.Fprintf(&w.sb, "[%s](%s)", text, href)
		return
	case atom.Table:
		w.block()
		w.table(n)
		w.block()
		return
	case atom.Img:
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			fmt.Fprintf(&w.sb, "![%s](%s)", alt, attr(n, "src"))
		}
		return
	}
	w.children(n)
}

func (w *mdWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
}

func (w *mdWriter) inline(marker string, n *html.Node) {
	text := squeeze(textOf(n))
	if text == "" {
		return
	}
	w.sb.WriteString(marker + text + marker)
}

// text writes a text node, collapsing whitespace. Leading whitespace directly
// after a block break is dropped so paragraphs do not start with a space.
func (w *mdWriter) text(s string) {
	s = squeeze(s)
	if s == "" {
		return
	}
	cur := w.sb.String()
	if cur != "" && !strings.HasSuffix(cur, "\n") && !strings.HasSuffix(cur, " ") &&
		!strings.HasPrefix(s, " ") {
		w.sb.WriteString(" ")
	}
	w.sb.WriteString(s)
}

// block ensures the output is at a blank-line boundary before the next block.
func (w *mdWriter) block() {
	cur := w.sb.String()
	if cur == "" {
		return
	}
	if !strings.HasSuffix(cur, "\n\n") {
		if strings.HasSuffix(cur, "\n") {
			w.sb.WriteString("\n")
		} else {
			w.sb.WriteString("\n\n")
		}
	}
}

// table renders a <table> as a markdown table. The first row becomes the header
// (whether it uses <th> or <td>), which is what nearly every real table means.
func (w *mdWriter) table(n *html.Node) {
	var rows [][]string
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Tr {
				var cells []string
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.DataAtom == atom.Td || cell.DataAtom == atom.Th {
						cells = append(cells, squeeze(textOf(cell)))
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
				continue
			}
			collect(c)
		}
	}
	collect(n)
	if len(rows) == 0 {
		return
	}
	writeRow := func(cells []string) {
		w.sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	writeRow(rows[0])
	sep := make([]string, len(rows[0]))
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(sep)
	for _, r := range rows[1:] {
		writeRow(r)
	}
}

// findFirst returns the first element with the given tag, depth-first.
func findFirst(n *html.Node, a atom.Atom) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirst(c, a); found != nil {
			return found
		}
	}
	return nil
}

// textOf returns the concatenated text of a subtree, skipping chrome elements.
func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
			return
		}
		if node.Type == html.ElementNode && skipTags[node.DataAtom] {
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return sb.String()
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// squeeze collapses all runs of whitespace to single spaces and trims.
func squeeze(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// collapseBlankLines reduces runs of 3+ newlines to exactly two, so block
// handling that over-produces separators still yields clean markdown.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/convert/ -v`
Expected: PASS — all four HTML tests plus the detection tests.

- [ ] **Step 6: Verify CGo-free build**

Run: `CGO_ENABLED=0 go build ./...`
Expected: no output (success).

- [ ] **Step 7: Commit**

```bash
git add internal/convert/ go.mod go.sum
git commit -m "feat(convert): HTML to markdown via a real parser"
```

---

### Task 3: Web search provider cascade

**Files:**
- Create: `internal/websearch/websearch.go`
- Create: `internal/websearch/ddg.go`
- Create: `internal/websearch/engines.go`
- Test: `internal/websearch/websearch_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `websearch.Result{Title, URL, Snippet string}`, `websearch.Provider` interface, `websearch.Client{HTTP *http.Client, RetryBase time.Duration, Providers []Provider}`, `(*Client).Search(ctx, query) ([]Result, error)`, `websearch.DefaultProviders(baseOverride map[string]string) []Provider`

- [ ] **Step 1: Write the failing test**

Create `internal/websearch/websearch_test.go`:

```go
package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ddgPage renders a minimal DuckDuckGo html result page.
func ddgPage(title, target, snippet string) string {
	return `<html><body><div class="result">` +
		`<a class="result__a" href="//duckduckgo.com/l/?uddg=` + target + `">` + title + `</a>` +
		`<a class="result__snippet">` + snippet + `</a>` +
		`</div></body></html>`
}

func testClient(providers ...Provider) *Client {
	return &Client{HTTP: &http.Client{Timeout: 5 * time.Second}, RetryBase: time.Millisecond, Providers: providers}
}

func TestSearchFirstProviderWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage("Weather Skopje", "https%3A%2F%2Fexample.com%2Fwx", "Sunny, 24C")))
	}))
	defer srv.Close()

	c := testClient(&ddgProvider{name: "ddg-html", base: srv.URL})
	got, err := c.Search(context.Background(), "weather skopje")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].URL != "https://example.com/wx" {
		t.Errorf("URL = %q, want the decoded redirect target", got[0].URL)
	}
	if got[0].Title != "Weather Skopje" {
		t.Errorf("Title = %q", got[0].Title)
	}
}

func TestSearchFallsThroughOnZeroResults(t *testing.T) {
	// First engine returns 200 with a JS challenge page (no result blocks) —
	// the exact real-world failure that made single-engine search unreliable.
	challenge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><noscript>Please enable JavaScript</noscript></body></html>`))
	}))
	defer challenge.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage("Second engine", "https%3A%2F%2Fexample.org%2Fb", "from the fallback")))
	}))
	defer good.Close()

	c := testClient(
		&ddgProvider{name: "ddg-html", base: challenge.URL},
		&ddgProvider{name: "ddg-lite", base: good.URL},
	)
	got, err := c.Search(context.Background(), "anything")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Second engine" {
		t.Fatalf("expected fallback engine result, got %+v", got)
	}
}

func TestSearchFallsThroughOnHardFailure(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage("Alive", "https%3A%2F%2Fexample.org%2Fc", "ok")))
	}))
	defer good.Close()

	c := testClient(&ddgProvider{name: "a", base: dead.URL}, &ddgProvider{name: "b", base: good.URL})
	got, err := c.Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Alive" {
		t.Fatalf("expected second engine, got %+v", got)
	}
}

func TestSearchRetriesTransientWithinProvider(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(ddgPage("Recovered", "https%3A%2F%2Fexample.org%2Fd", "ok")))
	}))
	defer srv.Close()

	c := testClient(&ddgProvider{name: "a", base: srv.URL})
	got, err := c.Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Recovered" {
		t.Fatalf("429 should be retried inside the provider, got %+v", got)
	}
	if calls < 2 {
		t.Errorf("expected a retry, saw %d calls", calls)
	}
}

func TestSearchAllEnginesFailReturnsEmptyNotError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()

	c := testClient(&ddgProvider{name: "a", base: dead.URL}, &ddgProvider{name: "b", base: dead.URL})
	got, err := c.Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("all-engines-fail must NOT be an error (it would trip the tool oscillation guard): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

func TestSearchDedupesByURL(t *testing.T) {
	page := `<html><body>` +
		`<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fx">One</a><a class="result__snippet">a</a>` +
		`<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fx%2F">Dup</a><a class="result__snippet">b</a>` +
		`</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(page))
	}))
	defer srv.Close()

	c := testClient(&ddgProvider{name: "a", base: srv.URL})
	got, _ := c.Search(context.Background(), "q")
	if len(got) != 1 {
		t.Errorf("trailing-slash duplicate should collapse, got %d: %+v", len(got), got)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	c := testClient(&ddgProvider{name: "a", base: "http://unused"})
	if _, err := c.Search(context.Background(), "  "); err == nil {
		t.Error("empty query must be an error (it is a caller bug, not a transient condition)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/websearch/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the cascade**

Create `internal/websearch/websearch.go`:

```go
// Package websearch turns a query into web results using a cascade of
// providers. A single keyless scrape is structurally unreliable — one layout
// change or JS-challenge interstitial and it silently returns nothing — so this
// package treats "this engine produced no parseable results" as a reason to try
// the next engine rather than as an answer.
package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Result is one search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Provider is one search backend. Name identifies it in logs. Search returns
// results, or an error; returning zero results with a nil error is a valid
// outcome that the cascade treats as "try the next provider".
type Provider interface {
	Name() string
	Search(ctx context.Context, hc *http.Client, query string) ([]Result, error)
}

// maxAttempts bounds the per-provider transient-retry loop.
const maxAttempts = 3

// Client runs providers in order.
type Client struct {
	HTTP      *http.Client
	RetryBase time.Duration
	Providers []Provider
}

// transientError marks a failure worth retrying against the SAME provider
// (429, 5xx, network, timeout) rather than moving on to the next one.
type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }
func (e transientError) Unwrap() error { return e.err }

// Transient wraps err as a retryable failure. Providers use it to opt into the
// per-provider retry loop.
func Transient(err error) error { return transientError{err} }

func isTransient(err error) bool {
	var t transientError
	return errorsAs(err, &t)
}

// Search tries each provider in order and returns the first non-empty result
// set. A provider that errors, or that returns zero results, falls through to
// the next one — the single most important reliability property here, because
// every keyless engine fails in both of those ways routinely.
//
// Exhausting every provider is NOT an error: it returns an empty slice with a
// nil error. The caller renders that as an explicit "no results" notice. An
// error result would be treated by the coder's oscillation guard as a failing
// call worth blocking, which is wrong for a query that simply matched nothing.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	base := c.RetryBase
	if base <= 0 {
		base = 500 * time.Millisecond
	}

	for _, p := range c.Providers {
		results, err := c.runProvider(ctx, hc, base, p, query)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // hard failure for this engine — try the next
		}
		if len(results) > 0 {
			return dedupe(results), nil
		}
	}
	return nil, nil
}

// runProvider retries one provider's transient failures with exponential backoff.
func (c *Client) runProvider(ctx context.Context, hc *http.Client, base time.Duration, p Provider, query string) ([]Result, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if !sleepCtx(ctx, base<<(attempt-1)) {
				return nil, ctx.Err()
			}
		}
		results, err := p.Search(ctx, hc, query)
		if err == nil {
			return results, nil
		}
		lastErr = err
		if !isTransient(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// dedupe collapses results whose URLs differ only in trailing slash, scheme
// case, or a leading "www." — the same page surfaced twice is noise.
func dedupe(in []Result) []Result {
	seen := make(map[string]bool, len(in))
	out := make([]Result, 0, len(in))
	for _, r := range in {
		key := normalizeURL(r.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	path := strings.TrimSuffix(u.Path, "/")
	return host + path + "?" + u.RawQuery
}

// errorsAs is a tiny local shim so this file needs no errors import in the
// hot path; it exists only for isTransient.
func errorsAs(err error, target *transientError) bool {
	for err != nil {
		if t, ok := err.(transientError); ok {
			*target = t
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
```

- [ ] **Step 4: Write the DuckDuckGo providers**

Create `internal/websearch/ddg.go`:

```go
package websearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// browserUA is a browser-like User-Agent. Keyless search endpoints serve a
// JS-challenge interstitial (200 OK, zero result blocks) to unfamiliar agents,
// which is indistinguishable from "no results" — so every provider sends this.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// maxBody bounds how much of a result page is read.
const maxBody = 2 << 20

// ddgProvider scrapes a DuckDuckGo HTML endpoint. Both the full and lite
// endpoints use the same result markup, so one implementation covers both.
type ddgProvider struct {
	name string
	base string
}

func (p *ddgProvider) Name() string { return p.name }

func (p *ddgProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	body, err := getPage(ctx, hc, p.base+"?q="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	return parseDDG(body), nil
}

// reDDGBlock matches one result block: the redirect anchor followed by the
// snippet anchor.
var reDDGBlock = regexp.MustCompile(`(?is)<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>.*?<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)

func parseDDG(doc string) []Result {
	var out []Result
	for _, m := range reDDGBlock.FindAllStringSubmatch(doc, -1) {
		target := decodeDDGRedirect(m[1])
		if target == "" {
			continue
		}
		out = append(out, Result{
			Title:   stripTags(m[2]),
			URL:     target,
			Snippet: stripTags(m[3]),
		})
	}
	return out
}

// decodeDDGRedirect recovers the real URL from DuckDuckGo's
// "//duckduckgo.com/l/?uddg=<urlencoded>" redirect wrapper.
func decodeDDGRedirect(href string) string {
	href = strings.TrimSpace(html.UnescapeString(href))
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	uddg := u.Query().Get("uddg")
	if uddg == "" {
		// Some endpoints link directly rather than via the redirect.
		if u.Scheme == "http" || u.Scheme == "https" {
			return u.String()
		}
		return ""
	}
	if real, err := url.QueryUnescape(uddg); err == nil {
		return real
	}
	return uddg
}

// getPage performs one GET and classifies the outcome. 429/5xx/network are
// wrapped as transient so the caller retries the same provider; a definitive
// 4xx is a hard failure that moves on to the next provider.
func getPage(ctx context.Context, hc *http.Client, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := hc.Do(req)
	if err != nil {
		return "", Transient(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", Transient(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(data), nil
}

var reAnyTag = regexp.MustCompile(`(?s)<[^>]*>`)

func stripTags(s string) string {
	s = reAnyTag.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Join(strings.Fields(html.UnescapeString(s)), " "))
}
```

- [ ] **Step 5: Write the remaining engines and the default set**

Create `internal/websearch/engines.go`:

```go
package websearch

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Production endpoints. Each is overridable in tests via DefaultProviders'
// baseOverride map, keyed by provider name.
const (
	ddgHTMLEndpoint = "https://html.duckduckgo.com/html/"
	ddgLiteEndpoint = "https://lite.duckduckgo.com/lite/"
	mojeekEndpoint  = "https://www.mojeek.com/search"
	bingEndpoint    = "https://www.bing.com/search"
)

// linkProvider scrapes an engine whose results are plain anchors matched by a
// regexp over the result list. Mojeek and Bing both fit this shape.
type linkProvider struct {
	name    string
	base    string
	param   string
	pattern *regexp.Regexp // submatch 1 = href, 2 = title html
}

func (p *linkProvider) Name() string { return p.name }

func (p *linkProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	body, err := getPage(ctx, hc, p.base+"?"+p.param+"="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, m := range p.pattern.FindAllStringSubmatch(body, -1) {
		href := strings.TrimSpace(html.UnescapeString(m[1]))
		if !strings.HasPrefix(href, "http") {
			continue
		}
		title := stripTags(m[2])
		if title == "" {
			continue
		}
		out = append(out, Result{Title: title, URL: href})
	}
	return out, nil
}

// DefaultProviders returns the keyless cascade in priority order. baseOverride
// maps a provider name to a replacement base URL (tests point these at httptest
// servers); a nil or missing entry uses the production endpoint.
func DefaultProviders(baseOverride map[string]string) []Provider {
	pick := func(name, prod string) string {
		if b, ok := baseOverride[name]; ok && b != "" {
			return b
		}
		return prod
	}
	return []Provider{
		&ddgProvider{name: "ddg-html", base: pick("ddg-html", ddgHTMLEndpoint)},
		&ddgProvider{name: "ddg-lite", base: pick("ddg-lite", ddgLiteEndpoint)},
		&linkProvider{
			name: "mojeek", base: pick("mojeek", mojeekEndpoint), param: "q",
			pattern: regexp.MustCompile(`(?is)<a[^>]*class="ob"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`),
		},
		&linkProvider{
			name: "bing", base: pick("bing", bingEndpoint), param: "q",
			pattern: regexp.MustCompile(`(?is)<h2><a[^>]*href="([^"]+)"[^>]*>(.*?)</a></h2>`),
		},
	}
}

// ensure the context import is used by the interface assertions below.
var _ Provider = (*ddgProvider)(nil)
var _ Provider = (*linkProvider)(nil)
var _ = context.Background
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/websearch/ -v`
Expected: PASS — all seven tests.

- [ ] **Step 7: Commit**

```bash
git add internal/websearch/
git commit -m "feat(websearch): provider cascade with per-provider transient retry"
```

---

### Task 4: Keyed search providers (Brave, Tavily)

**Files:**
- Create: `internal/websearch/keyed.go`
- Test: `internal/websearch/keyed_test.go`

**Interfaces:**
- Consumes: `Provider`, `Result`, `Transient`, `browserUA`, `maxBody` (Task 3)
- Produces: `websearch.KeyedProvider(name, apiKey, baseOverride string) Provider` returning nil when apiKey is empty; `websearch.KeySecretNames() []string` = `["SEARCH_KEY_BRAVE", "SEARCH_KEY_TAVILY"]`

- [ ] **Step 1: Write the failing test**

Create `internal/websearch/keyed_test.go`:

```go
package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBraveProvider(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Subscription-Token")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[
			{"title":"Brave hit","url":"https://example.com/b","description":"desc here"}
		]}}`))
	}))
	defer srv.Close()

	p := KeyedProvider("brave", "secret-key", srv.URL)
	if p == nil {
		t.Fatal("KeyedProvider returned nil for a non-empty key")
	}
	got, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "secret-key" {
		t.Errorf("api key header = %q, want the configured key", gotKey)
	}
	if len(got) != 1 || got[0].URL != "https://example.com/b" || got[0].Snippet != "desc here" {
		t.Fatalf("unexpected results: %+v", got)
	}
}

func TestTavilyProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"Tavily hit","url":"https://example.com/t","content":"body"}]}`))
	}))
	defer srv.Close()

	p := KeyedProvider("tavily", "k", srv.URL)
	got, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Tavily hit" {
		t.Fatalf("unexpected results: %+v", got)
	}
}

func TestKeyedProviderNilWithoutKey(t *testing.T) {
	if p := KeyedProvider("brave", "", ""); p != nil {
		t.Error("KeyedProvider must return nil when no key is configured")
	}
	if p := KeyedProvider("unknown-engine", "k", ""); p != nil {
		t.Error("KeyedProvider must return nil for an unknown engine")
	}
}

func TestKeyedProviderTransientOn429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := KeyedProvider("brave", "k", srv.URL)
	_, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !isTransient(err) {
		t.Errorf("429 must be transient so the provider is retried, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/websearch/ -run TestBrave -v`
Expected: FAIL — `undefined: KeyedProvider`.

- [ ] **Step 3: Write the keyed providers**

Create `internal/websearch/keyed.go`:

```go
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Production endpoints for the keyed providers.
const (
	braveEndpoint  = "https://api.search.brave.com/res/v1/web/search"
	tavilyEndpoint = "https://api.tavily.com/search"
)

// KeySecretNames are the secret names a workspace can set to upgrade search
// from scraping to a real API. They follow the CODER_KEY_<PROVIDER> convention
// already used for coder provider keys.
func KeySecretNames() []string { return []string{"SEARCH_KEY_BRAVE", "SEARCH_KEY_TAVILY"} }

// KeyedProvider returns a provider for a supported keyed engine, or nil when no
// key is configured or the engine is unknown. Returning nil (rather than an
// erroring provider) means an unconfigured key simply leaves the keyless
// cascade in place.
func KeyedProvider(engine, apiKey, baseOverride string) Provider {
	if apiKey == "" {
		return nil
	}
	switch engine {
	case "brave":
		return &braveProvider{key: apiKey, base: orDefault(baseOverride, braveEndpoint)}
	case "tavily":
		return &tavilyProvider{key: apiKey, base: orDefault(baseOverride, tavilyEndpoint)}
	}
	return nil
}

func orDefault(override, prod string) string {
	if override != "" {
		return override
	}
	return prod
}

type braveProvider struct {
	key  string
	base string
}

func (p *braveProvider) Name() string { return "brave" }

func (p *braveProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"?q="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.key)
	data, err := doJSON(hc, req)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("brave: decode: %w", err)
	}
	out := make([]Result, 0, len(payload.Web.Results))
	for _, r := range payload.Web.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: stripTags(r.Description)})
	}
	return out, nil
}

type tavilyProvider struct {
	key  string
	base string
}

func (p *tavilyProvider) Name() string { return "tavily" }

func (p *tavilyProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	body, _ := json.Marshal(map[string]any{"api_key": p.key, "query": query, "max_results": 6})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	data, err := doJSON(hc, req)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("tavily: decode: %w", err)
	}
	out := make([]Result, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: stripTags(r.Content)})
	}
	return out, nil
}

// doJSON performs the request and applies the same transient/definitive split
// the scraping providers use, so keyed engines participate in the retry loop
// identically.
func doJSON(hc *http.Client, req *http.Request) ([]byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, Transient(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, Transient(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errSnippet(data))
	}
	return data, nil
}

// errSnippet returns a short, safe excerpt of an error body for the message.
func errSnippet(data []byte) string {
	const limit = 200
	if len(data) > limit {
		return string(data[:limit]) + "…"
	}
	return string(data)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/websearch/ -v`
Expected: PASS — all eleven tests.

- [ ] **Step 5: Commit**

```bash
git add internal/websearch/
git commit -m "feat(websearch): optional Brave and Tavily keyed providers"
```

---

### Task 5: SSRF containment for web_fetch

**Files:**
- Create: `internal/coder/netguard.go`
- Test: `internal/coder/netguard_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `guardedHTTPClient(timeout time.Duration) *http.Client`, `denyPrivateAddr(network, address string, c syscall.RawConn) error`, `isBlockedIP(ip net.IP) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/coder/netguard_test.go`:

```go
package coder

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.53.0.1", "::1",
		"10.0.0.5", "172.16.4.1", "192.168.1.50",
		"169.254.169.254", // cloud metadata
		"fd00::1",         // unique local
		"fe80::1",         // link-local
		"0.0.0.0",
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = true, want false", s)
		}
	}
}

// TestGuardedClientBlocksLoopback is the load-bearing test: chat previously had
// no network at all, and the connector bridge listens on loopback holding
// per-run bearer tokens. A guarded client must not be able to reach it.
func TestGuardedClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret bridge response"))
	}))
	defer srv.Close()

	client := guardedHTTPClient(5 * time.Second)
	_, err := client.Get(srv.URL) // httptest always binds 127.0.0.1
	if err == nil {
		t.Fatal("guarded client reached a loopback address")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should name the block, got: %v", err)
	}
}

// TestGuardedClientBlocksRedirectToPrivate proves the guard is applied per
// connection, so a public URL cannot redirect into private space.
func TestGuardedClientBlocksRedirectToPrivate(t *testing.T) {
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should be unreachable"))
	}))
	defer private.Close()

	// The dialer control runs on every connection, including the redirect hop,
	// so pointing a redirect at loopback is blocked at dial time.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := guardedHTTPClient(5 * time.Second)
	if _, err := client.Get(redirector.URL); err == nil {
		t.Fatal("expected the redirect target to be blocked")
	}
}

func TestGuardedClientAllowsPublicDial(t *testing.T) {
	// Dial control is what enforces policy; verify a public IP passes the check
	// itself rather than making a real network call.
	if err := denyPrivateAddr("tcp4", "93.184.216.34:443", nil); err != nil {
		t.Errorf("public address should dial: %v", err)
	}
	if err := denyPrivateAddr("tcp4", "127.0.0.1:8080", nil); err == nil {
		t.Error("loopback address must be refused")
	}
}

var _ = context.Background
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coder/ -run TestIsBlockedIP -v`
Expected: FAIL — `undefined: isBlockedIP`.

- [ ] **Step 3: Write the guard**

Create `internal/coder/netguard.go`:

```go
package coder

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Private and special-purpose ranges web_fetch must never reach. Blocking is
// enforced at DIAL time rather than by inspecting the URL, which is the only
// way to catch the two cases that matter: a hostname that resolves to a private
// address, and a redirect hop into private space (the dialer control runs on
// every connection the client makes, redirects included).
//
// This became load-bearing when web_fetch was un-gated for chat: chat had no
// network at all before, and the loopback interface hosts the connector bridge,
// which holds per-run bearer tokens for the workspace's connected accounts.
var blockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",          // this host
		"10.0.0.0/8",         // RFC1918
		"127.0.0.0/8",        // loopback — the connector bridge lives here
		"169.254.0.0/16",     // link-local, incl. 169.254.169.254 cloud metadata
		"172.16.0.0/12",      // RFC1918
		"192.168.0.0/16",     // RFC1918
		"100.64.0.0/10",      // carrier-grade NAT / tailscale range
		"192.0.0.0/24",       // IETF protocol assignments
		"198.18.0.0/15",      // benchmarking
		"::1/128",            // IPv6 loopback
		"fc00::/7",           // IPv6 unique local
		"fe80::/10",          // IPv6 link-local
		"::/128",             // unspecified
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// isBlockedIP reports whether ip falls in private, loopback, link-local, or
// otherwise non-public space.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true // un-parseable is not provably public
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// denyPrivateAddr is a net.Dialer Control function: it runs after DNS
// resolution with the concrete address about to be connected to, for every
// connection including redirect hops.
func denyPrivateAddr(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("blocked: cannot parse address %q", address)
	}
	ip := net.ParseIP(host)
	if isBlockedIP(ip) {
		return fmt.Errorf("blocked: %s is a private or loopback address; web_fetch may only reach public hosts", host)
	}
	return nil
}

// guardedHTTPClient returns an HTTP client that cannot open a connection to
// private address space.
func guardedHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: denyPrivateAddr}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          10,
		},
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/coder/ -run 'TestIsBlockedIP|TestGuarded' -v`
Expected: PASS — all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/netguard.go internal/coder/netguard_test.go
git commit -m "feat(coder): deny private address space for web_fetch"
```

---

### Task 6: Wire the cascade, the parser, and the guard into the host tools

**Files:**
- Modify: `internal/coder/hosttools.go` (struct fields ~52-60; `webFetch` ~948; `webSearchOnce`/`parseDDGResults`/`decodeDDGRedirect` ~1112-1250; `renderWebBody` ~1033)
- Test: `internal/coder/hosttools_web_test.go` (existing — extended, not rewritten)

**Interfaces:**
- Consumes: `websearch.Client`, `websearch.DefaultProviders`, `websearch.KeyedProvider` (Tasks 3-4); `convert.ToMarkdown`, `convert.Options` (Tasks 1-2); `guardedHTTPClient` (Task 5)
- Produces: unchanged tool names `web_fetch` / `web_search`; `hostToolSet.ddgBaseURL` keeps its meaning as a test override (when set, it is the ONLY provider used, so existing tests stay deterministic and offline)

- [ ] **Step 1: Write the failing tests**

Append to `internal/coder/hosttools_web_test.go`:

```go
func TestWebFetchRendersHTMLAsMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><nav>Menu</nav><main><h1>Title</h1>
			<p>Body text with <a href="https://example.com/x">a link</a>.</p></main>
			<footer>Legal</footer></body></html>`))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out, err := h.webFetch(context.Background(), srv.URL, "GET", nil, "")
	if err != nil {
		t.Fatalf("webFetch: %v", err)
	}
	if !strings.Contains(out, "# Title") {
		t.Errorf("expected a markdown heading, got:\n%s", out)
	}
	if !strings.Contains(out, "[a link](https://example.com/x)") {
		t.Errorf("expected the link preserved as markdown, got:\n%s", out)
	}
	if strings.Contains(out, "Menu") || strings.Contains(out, "Legal") {
		t.Errorf("page chrome should be dropped, got:\n%s", out)
	}
}

func TestWebFetchMemoizesWithinToolset(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	for i := 0; i < 3; i++ {
		if _, err := h.webFetch(context.Background(), srv.URL, "GET", nil, ""); err != nil {
			t.Fatalf("webFetch %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("identical GETs in one toolset should hit the network once, saw %d", calls)
	}
}

func TestWebFetchBlocksPrivateAddressByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bridge secrets"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond} // guard ON
	if _, err := h.webFetch(context.Background(), srv.URL, "GET", nil, ""); err == nil {
		t.Fatal("web_fetch must not reach a loopback address")
	}
}

// A JSON API response is web_fetch's most common target and its own tool-
// description example. It must survive Phase 1, where no JSON converter exists.
func TestWebFetchJSONPassesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"current":{"temperature_2m":24.1}}`))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out, err := h.webFetch(context.Background(), srv.URL, "GET", nil, "")
	if err != nil {
		t.Fatalf("webFetch: %v", err)
	}
	if !strings.Contains(out, `"temperature_2m":24.1`) {
		t.Errorf("json body must pass through verbatim, got:\n%s", out)
	}
}

func TestWebFetchPDFIsConverted(t *testing.T) {
	// Phase 1 has no PDF converter yet, so a PDF must fail LOUDLY with a clear
	// message rather than returning the old "[not text]" dead end silently.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.7\nnot a real pdf"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out, err := h.webFetch(context.Background(), srv.URL, "GET", nil, "")
	if err == nil && !strings.Contains(out, "pdf") {
		t.Errorf("a pdf response should mention the format either way, got err=%v out=%q", err, out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/coder/ -run TestWebFetch -v`
Expected: FAIL — `unknown field allowPrivateHosts in struct literal`.

- [ ] **Step 3: Add the new struct fields**

In `internal/coder/hosttools.go`, replace the `ddgBaseURL string` field declaration and its comment with:

```go
	// ddgBaseURL, when set, overrides the search endpoint AND collapses the
	// provider cascade to that single endpoint. Tests point it at an httptest
	// server so the scraper is exercised deterministically and offline; in
	// production it is empty and the full cascade (see websearch.DefaultProviders)
	// applies.
	ddgBaseURL string

	// allowPrivateHosts disables web_fetch's private-address guard. It exists
	// ONLY for tests, which serve fixtures from httptest servers bound to
	// 127.0.0.1. It is never set in production: the guard is what stops a chat
	// coder from reaching the loopback connector bridge and its bearer tokens.
	allowPrivateHosts bool

	// fetchMemo caches web_fetch results within this toolset (one run/loop).
	// A weak model re-fetches the same URL repeatedly; the memo makes that free
	// and bounded, with no cross-run invalidation problem to get wrong.
	fetchMemo map[string]string
```

- [ ] **Step 4: Rewrite webFetch's client selection, rendering, and memo**

In `internal/coder/hosttools.go`, inside `webFetch`, replace the client-selection block:

```go
	client := h.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
```

with:

```go
	// Memo: an identical GET within one toolset costs one request.
	memoKey := method + " " + u.String() + " " + body
	if h.fetchMemo == nil {
		h.fetchMemo = map[string]string{}
	}
	if cached, ok := h.fetchMemo[memoKey]; ok {
		return cached, nil
	}

	client := h.httpClient
	if client == nil {
		// The guarded client refuses to dial private/loopback/link-local space,
		// enforced at dial time so it also covers a hostname that resolves into
		// private space and every redirect hop.
		if h.allowPrivateHosts {
			client = &http.Client{Timeout: 30 * time.Second}
		} else {
			client = guardedHTTPClient(30 * time.Second)
		}
	}
```

and, at the successful return inside the retry loop, replace:

```go
		text, retryable, err := h.webFetchOnce(ctx, client, method, u.String(), headers, body)
		if err == nil {
			return text, nil
		}
```

with:

```go
		text, retryable, err := h.webFetchOnce(ctx, client, method, u.String(), headers, body)
		if err == nil {
			h.fetchMemo[memoKey] = text
			return text, nil
		}
```

- [ ] **Step 5: Route body rendering through convert**

In `internal/coder/hosttools.go`, replace the whole `renderWebBody` function (and delete the now-unused `stripHTML`, `reScript`, `reStyle`, `reTag`, `reWS` vars) with:

```go
// renderWebBody turns a response body into text the model can use. HTML and any
// convertible document format go through internal/convert — so a fetched page
// keeps its headings, lists, links and tables instead of collapsing into one
// whitespace-run, and a PDF/DOCX URL yields readable text instead of a dead end.
// A format convert cannot handle degrades to a short note naming the type rather
// than dumping raw bytes into the model context.
func renderWebBody(contentType, sourceURL string, data []byte) string {
	res, err := convert.ToMarkdown(data, convert.Options{MIME: contentType, SourceURL: sourceURL})
	if err == nil && strings.TrimSpace(res.Markdown) != "" {
		return res.Markdown
	}
	// convert could not handle this type. If the body is textual, hand it back
	// AS-IS rather than discarding it: a JSON API response is the single most
	// common web_fetch target, and returning "no text could be extracted" for
	// one would be a regression. This branch also keeps Phase 1 shippable on
	// its own — the JSON/CSV/PDF converters land in Phase 2, and until they do
	// every textual body still flows through here unchanged.
	if convert.IsTextual(data, contentType) {
		return string(data)
	}
	kind := convert.Detect(data, "", contentType)
	return fmt.Sprintf("[web_fetch: %s response (%s), %d bytes — no text could be extracted; if you need to process it, use run_script or bash]",
		contentTypeMain(contentType), kind, len(data))
}
```

Update the one call site in `webFetchOnce`:

```go
	ct := resp.Header.Get("Content-Type")
	header := fmt.Sprintf("[web_fetch %d %s %s]\n", resp.StatusCode, contentTypeMain(ct), u)
	return header + renderWebBody(ct, u, data), false, nil
```

Add the import `"github.com/ilijad1/simple-agents/internal/convert"` to the file's import block.

- [ ] **Step 6: Replace webSearch with the cascade**

In `internal/coder/hosttools.go`, delete `webSearchOnce`, `ddgResult`, `reDDGBlock`, `parseDDGResults`, and `decodeDDGRedirect` (all now living in `internal/websearch`), and replace the body of `webSearch` with:

```go
// webSearch runs the provider cascade and renders numbered title/url/snippet
// entries. Reliability comes from the cascade, not from this function: a single
// engine returning a JS-challenge page (200 OK, zero parseable results) is
// indistinguishable from "no results", so websearch treats it as a reason to try
// the next engine. Exhausting every engine still yields a NON-error empty notice
// so the model can fall back to web_fetch without tripping the oscillation guard.
func (h *hostToolSet) webSearch(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	client := h.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base := h.webRetryBase
	if base <= 0 {
		base = 500 * time.Millisecond
	}

	results, err := (&websearch.Client{
		HTTP:      client,
		RetryBase: base,
		Providers: h.searchProviders(),
	}).Search(ctx, query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "(no search results)", nil
	}
	if len(results) > maxWebSearchResults {
		results = results[:maxWebSearchResults]
	}
	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return strings.TrimSpace(sb.String()), nil
}

// searchProviders builds the provider list for this toolset. A workspace that
// has stored a search API key (as an ordinary encrypted secret, injected into
// subprocessEnv alongside the agent's other secrets) gets that provider FIRST
// and skips scraping entirely; otherwise the keyless cascade applies. When
// ddgBaseURL is set (tests only) the cascade collapses to that single endpoint.
func (h *hostToolSet) searchProviders() []websearch.Provider {
	if h.ddgBaseURL != "" {
		return websearch.DefaultProviders(map[string]string{"ddg-html": h.ddgBaseURL})[:1]
	}
	var out []websearch.Provider
	if p := websearch.KeyedProvider("brave", h.subprocessEnv["SEARCH_KEY_BRAVE"], ""); p != nil {
		out = append(out, p)
	}
	if p := websearch.KeyedProvider("tavily", h.subprocessEnv["SEARCH_KEY_TAVILY"], ""); p != nil {
		out = append(out, p)
	}
	return append(out, websearch.DefaultProviders(nil)...)
}
```

Add the import `"github.com/ilijad1/simple-agents/internal/websearch"`.

- [ ] **Step 7: Run the whole coder suite**

Run: `go test ./internal/coder/ -count=1 -v 2>&1 | tail -40`
Expected: PASS. Existing `TestWebSearchReturnsResults`, `TestWebSearchRetriesTransient`, and `TestWebSearchNoResultsNonError` pass unchanged because `ddgBaseURL` still collapses the cascade to one test endpoint. `TestWebFetchStripsHTML` may need its assertion updated from stripped-text to markdown — if it asserts a plain-text run, change the expectation to the markdown form; do not weaken it to a substring that both would satisfy.

- [ ] **Step 8: Commit**

```bash
git add internal/coder/hosttools.go internal/coder/hosttools_web_test.go
git commit -m "feat(coder): web_fetch parses HTML properly and is SSRF-guarded; web_search uses the cascade"
```

---

### Task 7: Make web tools available in chat

**Files:**
- Modify: `internal/coder/hosttools.go` (`tools()` ~133-186; `execute()` ~453-550)
- Modify: `internal/coder/hosttools_web_test.go` (replace the two "disabled when not exec" tests)
- Modify: `cmd/simple-agents/main.go:313` (CLI chat allowed tools)
- Modify: `internal/prompts/prompts.go` (`BuildChatSystemPrompt` capability text)
- Test: `internal/coder/cli_chat_test.go`

**Interfaces:**
- Consumes: everything from Task 6
- Produces: `web_fetch` and `web_search` are offered whenever tools are offered at all (chat included); `run_script` and `bash` remain exec-gated

- [ ] **Step 1: Write the failing test**

Replace `TestWebFetchDisabledWhenNotExec` and `TestWebSearchDisabledWhenNotExec` in `internal/coder/hosttools_web_test.go` with:

```go
// Web tools are read-only and cannot carry secrets, so they are offered in chat
// too. The exec gate exists for tools that run code (run_script, bash) — it
// never applied to fetch/search for the right reason.
func TestWebToolsOfferedInChat(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	var names []string
	for _, tool := range h.tools() {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"web_fetch", "web_search"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s must be offered without exec tools, got %v", want, names)
		}
	}
	for _, unwanted := range []string{"run_script", "bash"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("%s must stay exec-gated, got %v", unwanted, names)
		}
	}
}

func TestWebFetchExecutesWithoutExecTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("chat can read this"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: false, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out := h.execute(context.Background(), llm.ToolCall{Name: "web_fetch",
		Args: json.RawMessage(`{"url":"` + srv.URL + `"}`)})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("web_fetch should work in chat, got %q", out)
	}
	if !strings.Contains(out, "chat can read this") {
		t.Errorf("unexpected body: %q", out)
	}
}

func TestExecToolsStillGated(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	for _, name := range []string{"run_script", "bash"} {
		out := h.execute(context.Background(), llm.ToolCall{Name: name, Args: json.RawMessage(`{}`)})
		if !strings.Contains(out, "not available") {
			t.Errorf("%s should be refused in chat, got %q", name, out)
		}
	}
}
```

Add `"slices"` and `"encoding/json"` to that file's imports if not present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/coder/ -run 'TestWebToolsOffered|TestWebFetchExecutesWithout' -v`
Expected: FAIL — `web_fetch must be offered without exec tools`.

- [ ] **Step 3: Move the web tools out of the exec block**

In `internal/coder/hosttools.go`'s `tools()`, cut the two `llm.Tool` literals for `web_fetch` and `web_search` out of the `if h.includeExecTools {` block and append them to the base `tools` slice instead — immediately after the `glob` entry, before the `if h.includeExecTools` block. Update the `search_files`/`glob` grouping comment to read:

```go
	// Read-only tools, always offered (chat included): file discovery plus the
	// two web tools. None of them execute code or carry secrets, so the exec
	// gate below — which exists for run_script/bash — does not apply to them.
```

- [ ] **Step 4: Drop the execute() gates for the web tools**

In `execute()`, delete these two guard blocks:

```go
	case "web_fetch":
		if !h.includeExecTools {
			return "error: web_fetch is not available"
		}
```

becomes:

```go
	case "web_fetch":
```

and likewise for `web_search`. Leave the `run_script` and `bash` guards exactly as they are.

- [ ] **Step 5: Grant the CLI chat coder its native web tools**

In `cmd/simple-agents/main.go:313`, change:

```go
								cd = cd.WithAllowedTools("Read,Write,Edit,Glob,Grep,Bash(" + connBin + " connector exec:*)")
```

to:

```go
								cd = cd.WithAllowedTools("Read,Write,Edit,Glob,Grep,WebFetch,WebSearch,Bash(" + connBin + " connector exec:*)")
```

Find the sibling call a few lines above (the no-connector branch, around line 292-300) that sets `"Read,Write,Edit,Glob,Grep"` and add `,WebFetch,WebSearch` there too, so both branches match.

- [ ] **Step 6: Tell the chat prompt the tools exist**

In `internal/prompts/prompts.go`, find `BuildChatSystemPrompt`'s backend-aware capability text and add this sentence to the tool-calling branch's tool list (after the file tools, before any connector block):

```
You can also look things up on the public web: use web_search to FIND a URL when you do not have one, then web_fetch to READ it. Both are read-only and cannot carry secrets or reach private addresses — they are for public pages only.
```

- [ ] **Step 7: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s 2>&1 | grep -v "^ok" | head -20`
Expected: no failures printed.

- [ ] **Step 8: Verify the build and smoke-test the server**

```bash
make deploy && sleep 3 && curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/
```
Expected: `200`.

- [ ] **Step 9: Commit — Phase 1 complete and mergeable**

```bash
git add internal/coder/ internal/prompts/prompts.go cmd/simple-agents/main.go
git commit -m "feat: make web_fetch and web_search available in chat"
```

---

# PHASE 2 — Conversion

Ends mergeable: every common document format converts to markdown, and a file can enter the knowledge base from an agent, the CLI, the web UI, or a chat message.

---

### Task 8: CSV and TSV → markdown tables

**Files:**
- Create: `internal/convert/tabular.go`
- Test: `internal/convert/tabular_test.go`
- Modify: `internal/convert/convert.go` (`ToMarkdown` dispatch)

**Interfaces:**
- Consumes: `Result`, `Options`, `Kind`, `normalizeText`, `titleFromFilename` (Task 1)
- Produces: `tabularToMarkdown(data []byte, kind Kind, opt Options) (Result, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/convert/tabular_test.go`:

```go
package convert

import (
	"fmt"
	"strings"
	"testing"
)

func TestCSVToMarkdown(t *testing.T) {
	csv := "Region,Sales,Notes\nEMEA,120,\"grew, fast\"\nAPAC,98,flat\n"
	got, err := ToMarkdown([]byte(csv), Options{Filename: "q3-sales.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindCSV {
		t.Errorf("Kind = %q", got.Kind)
	}
	if got.Title != "q3 sales" {
		t.Errorf("Title = %q, want %q", got.Title, "q3 sales")
	}
	for _, want := range []string{
		"| Region | Sales | Notes |",
		"| --- | --- | --- |",
		"| EMEA | 120 | grew, fast |",
		"| APAC | 98 | flat |",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestTSVToMarkdown(t *testing.T) {
	got, err := ToMarkdown([]byte("a\tb\n1\t2\n"), Options{Filename: "x.tsv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "| a | b |") || !strings.Contains(got.Markdown, "| 1 | 2 |") {
		t.Errorf("unexpected tsv output:\n%s", got.Markdown)
	}
}

func TestCSVEscapesPipes(t *testing.T) {
	got, err := ToMarkdown([]byte("a,b\nx|y,z\n"), Options{Filename: "p.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, `x\|y`) {
		t.Errorf("a pipe in a cell must be escaped or it breaks the table, got:\n%s", got.Markdown)
	}
}

func TestCSVRaggedRows(t *testing.T) {
	// Real exports have ragged rows; they must not abort the conversion.
	got, err := ToMarkdown([]byte("a,b,c\n1,2\n3,4,5,6\n"), Options{Filename: "r.csv"})
	if err != nil {
		t.Fatalf("ragged rows must not fail: %v", err)
	}
	if !strings.Contains(got.Markdown, "| 1 | 2 |") {
		t.Errorf("short row should be padded, got:\n%s", got.Markdown)
	}
	if len(got.Warnings) == 0 {
		t.Error("ragged rows should be recorded as a warning")
	}
}

// A realistically large export must survive INTACT. Silently dropping rows makes
// them unsearchable and invisible to agents, so the row cap is a safety valve
// far above real-world sizes, not a routine truncation.
func TestCSVLargeSurvivesIntact(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("id,value\n")
	const rows = 5000
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&sb, "%d,v%d\n", i, i)
	}
	got, err := ToMarkdown([]byte(sb.String()), Options{Filename: "big.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.Contains(got.Markdown, "rows omitted") {
		t.Errorf("a %d-row csv must not be truncated", rows)
	}
	if !strings.Contains(got.Markdown, "| 4999 | v4999 |") {
		t.Error("the last row must be present and searchable")
	}
	if len(got.Warnings) != 0 {
		t.Errorf("no warning expected for a normal-sized file, got %v", got.Warnings)
	}
}

// Beyond the safety valve, truncation must announce itself in BOTH the body and
// Result.Warnings — the importer writes warnings into the note's frontmatter, so
// a truncated note declares itself rather than looking complete.
func TestCSVBeyondCapWarnsInBodyAndWarnings(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("id,value\n")
	for i := 0; i < maxTableRows+50; i++ {
		fmt.Fprintf(&sb, "%d,v%d\n", i, i)
	}
	got, err := ToMarkdown([]byte(sb.String()), Options{Filename: "huge.csv"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "rows omitted") {
		t.Error("truncation must be stated in the body")
	}
	if len(got.Warnings) == 0 {
		t.Error("truncation must also surface as a Result warning, or the note's frontmatter will not declare it")
	}
}

func TestCSVEmptyIsError(t *testing.T) {
	if _, err := ToMarkdown([]byte("\n"), Options{Filename: "empty.csv"}); err == nil {
		t.Error("an empty csv must error rather than produce a blank note")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/convert/ -run TestCSV -v`
Expected: FAIL — `convert: csv is not supported yet`.

- [ ] **Step 3: Write the converter**

Create `internal/convert/tabular.go`:

```go
package convert

import (
	"encoding/csv"
	"fmt"
	"strings"
)

// maxTableRows is a SAFETY VALVE against a pathological file, not a routine
// truncation. It is deliberately high: a converted note is stored on disk and
// read through paging (read_file takes offset/limit) and chunked retrieval, so
// a long table costs nothing at rest — whereas dropping rows makes them
// unsearchable and invisible to every agent, which is silent data loss on the
// file type users are most likely to compute over. Anything actually omitted is
// recorded as a Result warning, which the importer writes into the note's
// frontmatter, so a truncated note always declares itself.
const maxTableRows = 50000

// tabularToMarkdown renders delimited data as a markdown table. The first
// record becomes the header, which is what a delimited export virtually always
// means.
func tabularToMarkdown(data []byte, kind Kind, opt Options) (Result, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1 // tolerate ragged rows rather than aborting
	r.LazyQuotes = true    // tolerate stray quotes in real-world exports
	if kind == KindTSV {
		r.Comma = '\t'
	}
	records, err := r.ReadAll()
	if err != nil {
		return Result{}, fmt.Errorf("convert: parse %s: %w", kind, err)
	}
	records = dropEmptyRecords(records)
	if len(records) == 0 {
		return Result{}, fmt.Errorf("convert: %s contained no rows", kind)
	}

	res := Result{Kind: kind, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	header := records[0]
	width := len(header)
	for _, rec := range records {
		if len(rec) > width {
			width = len(rec)
		}
	}
	if width > len(header) {
		res.Warnings = append(res.Warnings, "some rows had more columns than the header row")
	}

	var sb strings.Builder
	writeRow(&sb, pad(header, width))
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(&sb, sep)

	body := records[1:]
	ragged := false
	for i, rec := range body {
		if i >= maxTableRows {
			omitted := len(body) - maxTableRows
			fmt.Fprintf(&sb, "\n_%d further rows omitted (%d total)._\n", omitted, len(body))
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"row limit reached: %d of %d rows are not in this note — read the preserved original for the full data",
				omitted, len(body)))
			break
		}
		if len(rec) != width {
			ragged = true
		}
		writeRow(&sb, pad(rec, width))
	}
	if ragged {
		res.Warnings = append(res.Warnings, "some rows had a different column count than the header row")
	}
	res.Markdown = normalizeText(sb.String())
	return res, nil
}

func writeRow(sb *strings.Builder, cells []string) {
	escaped := make([]string, len(cells))
	for i, c := range cells {
		escaped[i] = escapeCell(c)
	}
	sb.WriteString("| " + strings.Join(escaped, " | ") + " |\n")
}

// escapeCell makes a value safe inside a markdown table: a literal pipe would
// otherwise split the cell, and an embedded newline would break the row.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func pad(cells []string, width int) []string {
	if len(cells) >= width {
		return cells[:width]
	}
	out := make([]string, width)
	copy(out, cells)
	return out
}

func dropEmptyRecords(records [][]string) [][]string {
	out := records[:0]
	for _, rec := range records {
		for _, c := range rec {
			if strings.TrimSpace(c) != "" {
				out = append(out, rec)
				break
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Wire the dispatch**

In `internal/convert/convert.go`, add to `ToMarkdown`'s switch, above the `default` case:

```go
	case KindCSV, KindTSV:
		return tabularToMarkdown(data, kind, opt)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/convert/ -v`
Expected: PASS — all six tabular tests plus the earlier ones.

- [ ] **Step 6: Commit**

```bash
git add internal/convert/
git commit -m "feat(convert): csv and tsv to markdown tables"
```

---

### Task 9: DOCX → markdown

**Files:**
- Create: `internal/convert/ooxml.go`
- Test: `internal/convert/ooxml_test.go`
- Modify: `internal/convert/convert.go` (dispatch), `internal/convert/detect.go` (resolve zip by inspecting parts)

**Interfaces:**
- Consumes: `Result`, `Options`, `Kind` (Task 1)
- Produces: `docxToMarkdown(data []byte, opt Options) (Result, error)`, `openOOXML(data []byte) (*zip.Reader, error)`, `readZipPart(zr *zip.Reader, name string) ([]byte, error)`, `detectOOXMLKind(data []byte) Kind`

- [ ] **Step 1: Write the failing test**

Create `internal/convert/ooxml_test.go`:

```go
package convert

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildZip assembles an in-memory zip from name→content pairs. Building the
// fixture in code rather than committing a binary keeps the test readable and
// lets each case state exactly the XML shape it is exercising.
func buildZip(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

const docxBody = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Quarterly Review</w:t></w:r></w:p>
  <w:p><w:r><w:t>Revenue was </w:t></w:r><w:r><w:t>up 12%.</w:t></w:r></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>EMEA up</w:t></w:r></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>APAC flat</w:t></w:r></w:p>
  <w:p/>
  <w:tbl>
   <w:tr><w:tc><w:p><w:r><w:t>Region</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Sales</w:t></w:r></w:p></w:tc></w:tr>
   <w:tr><w:tc><w:p><w:r><w:t>EMEA</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>120</w:t></w:r></w:p></w:tc></w:tr>
  </w:tbl>
 </w:body>
</w:document>`

func TestDOCXToMarkdown(t *testing.T) {
	data := buildZip(t, map[string]string{"word/document.xml": docxBody})
	got, err := ToMarkdown(data, Options{Filename: "review.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindDOCX {
		t.Errorf("Kind = %q", got.Kind)
	}
	for _, want := range []string{
		"# Quarterly Review",
		"Revenue was up 12%.", // runs inside one paragraph must join
		"- EMEA up",
		"- APAC flat",
		"| Region | Sales |",
		"| EMEA | 120 |",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestDOCXTitleFromFirstHeading(t *testing.T) {
	data := buildZip(t, map[string]string{"word/document.xml": docxBody})
	got, _ := ToMarkdown(data, Options{Filename: "review.docx"})
	if got.Title != "Quarterly Review" {
		t.Errorf("Title = %q, want the first heading", got.Title)
	}
}

func TestDetectOOXMLFromArchiveParts(t *testing.T) {
	// The extension is missing/wrong; the archive's parts identify the format.
	data := buildZip(t, map[string]string{"word/document.xml": docxBody})
	if got := Detect(data, "mystery.bin", ""); got != KindDOCX {
		t.Errorf("Detect = %q, want docx from the archive parts", got)
	}
}

func TestDOCXMissingPartIsError(t *testing.T) {
	data := buildZip(t, map[string]string{"docProps/app.xml": "<x/>"})
	if _, err := ToMarkdown(data, Options{Filename: "broken.docx"}); err == nil {
		t.Error("a docx with no document.xml must error, not return a blank note")
	}
}

func TestZipBombRefused(t *testing.T) {
	// A part that inflates beyond the cap must be refused rather than read.
	huge := strings.Repeat("A", maxPartBytes+1)
	data := buildZip(t, map[string]string{"word/document.xml": huge})
	if _, err := ToMarkdown(data, Options{Filename: "bomb.docx"}); err == nil {
		t.Error("an oversized part must be refused")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/convert/ -run TestDOCX -v`
Expected: FAIL — `convert: docx is not supported yet`.

- [ ] **Step 3: Write the OOXML reader and the DOCX converter**

Create `internal/convert/ooxml.go`:

```go
package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// maxPartBytes bounds how much a single archive part may inflate to. An OOXML
// file is a zip, so an untrusted upload can be a decompression bomb; refusing
// an oversized part is cheaper and safer than discovering it after allocation.
const maxPartBytes = 32 << 20 // 32 MiB

func openOOXML(data []byte) (*zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("convert: not a readable archive: %w", err)
	}
	return zr, nil
}

// readZipPart reads one named part, refusing anything that inflates past the cap.
func readZipPart(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if f.UncompressedSize64 > maxPartBytes {
			return nil, fmt.Errorf("convert: archive part %s is too large (%d bytes)", name, f.UncompressedSize64)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("convert: open %s: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
		if err != nil {
			return nil, fmt.Errorf("convert: read %s: %w", name, err)
		}
		if len(data) > maxPartBytes {
			return nil, fmt.Errorf("convert: archive part %s exceeded the size cap", name)
		}
		return data, nil
	}
	return nil, fmt.Errorf("convert: archive is missing %s", name)
}

// detectOOXMLKind identifies which OOXML format an archive holds by looking for
// the part each format defines. This is what lets detection survive a wrong or
// missing extension — all three formats share the same zip magic bytes.
func detectOOXMLKind(data []byte) Kind {
	zr, err := openOOXML(data)
	if err != nil {
		return KindUnknown
	}
	for _, f := range zr.File {
		switch {
		case f.Name == "word/document.xml":
			return KindDOCX
		case f.Name == "xl/workbook.xml":
			return KindXLSX
		case strings.HasPrefix(f.Name, "ppt/slides/slide"):
			return KindPPTX
		}
	}
	return KindUnknown
}

// docxParagraph is one <w:p>, decoded far enough to know its style and text.
type docxParagraph struct {
	Style   string
	IsList  bool
	Text    string
	IsTable bool
	Rows    [][]string
}

// docxToMarkdown converts word/document.xml. It walks the XML as a token stream
// rather than binding a struct: WordprocessingML nests runs, bookmarks, and
// revision marks unpredictably, and a token walk collects text correctly
// regardless of what wraps it.
func docxToMarkdown(data []byte, opt Options) (Result, error) {
	zr, err := openOOXML(data)
	if err != nil {
		return Result{}, err
	}
	part, err := readZipPart(zr, "word/document.xml")
	if err != nil {
		return Result{}, err
	}

	paras, err := parseDocxParagraphs(part)
	if err != nil {
		return Result{}, err
	}

	res := Result{Kind: KindDOCX, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	var sb strings.Builder
	for _, p := range paras {
		if p.IsTable {
			writeTable(&sb, p.Rows)
			sb.WriteString("\n")
			continue
		}
		text := strings.TrimSpace(p.Text)
		if text == "" {
			continue
		}
		switch {
		case p.IsList:
			sb.WriteString("- " + text + "\n")
		case strings.HasPrefix(p.Style, "Heading"):
			level := headingLevel(p.Style)
			if res.Title == titleFromFilename(opt.Filename) || res.Title == "" {
				res.Title = text
			}
			sb.WriteString("\n" + strings.Repeat("#", level) + " " + text + "\n\n")
		default:
			sb.WriteString(text + "\n\n")
		}
	}
	body := collapseBlankLines(sb.String())
	if strings.TrimSpace(body) == "" {
		return Result{}, fmt.Errorf("convert: docx contained no readable text")
	}
	res.Markdown = normalizeText(body)
	return res, nil
}

// parseDocxParagraphs walks the document body, emitting one entry per paragraph
// and one per table.
func parseDocxParagraphs(part []byte) ([]docxParagraph, error) {
	dec := xml.NewDecoder(bytes.NewReader(part))
	var out []docxParagraph
	var cur *docxParagraph
	var table *docxParagraph
	var row []string
	var cell strings.Builder
	inCell := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("convert: parse docx xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl":
				table = &docxParagraph{IsTable: true}
			case "tr":
				row = nil
			case "tc":
				inCell = true
				cell.Reset()
			case "p":
				if !inCell {
					cur = &docxParagraph{}
				}
			case "pStyle":
				if cur != nil {
					cur.Style = attrValue(t, "val")
				}
			case "numPr":
				if cur != nil {
					cur.IsList = true
				}
			case "t":
				var text string
				if err := dec.DecodeElement(&text, &t); err == nil {
					if inCell {
						cell.WriteString(text)
					} else if cur != nil {
						cur.Text += text
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "tc":
				inCell = false
				row = append(row, strings.TrimSpace(cell.String()))
			case "tr":
				if table != nil && len(row) > 0 {
					table.Rows = append(table.Rows, row)
				}
			case "tbl":
				if table != nil && len(table.Rows) > 0 {
					out = append(out, *table)
				}
				table = nil
			case "p":
				if !inCell && cur != nil {
					out = append(out, *cur)
					cur = nil
				}
			}
		}
	}
	return out, nil
}

// writeTable renders collected rows as a markdown table with the first row as
// the header — shared by the docx, xlsx and pptx converters.
func writeTable(sb *strings.Builder, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	writeRow(sb, pad(rows[0], width))
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(sb, sep)
	for _, r := range rows[1:] {
		writeRow(sb, pad(r, width))
	}
}

// headingLevel maps a Word style name ("Heading2") to a markdown level,
// clamped to 1-6.
func headingLevel(style string) int {
	digits := strings.TrimPrefix(style, "Heading")
	if digits == "" {
		return 1
	}
	n := int(digits[0] - '0')
	if n < 1 {
		return 1
	}
	if n > 6 {
		return 6
	}
	return n
}

func attrValue(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
```

- [ ] **Step 4: Resolve zip archives during detection**

In `internal/convert/detect.go`'s `Detect`, insert the archive check between the magic check and the extension check:

```go
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
```

- [ ] **Step 5: Wire the dispatch**

In `internal/convert/convert.go`, add to the switch:

```go
	case KindDOCX:
		return docxToMarkdown(data, opt)
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/convert/ -v`
Expected: PASS. Note `TestDetectOOXMLByExtension` (Task 1) still passes: its fixture is not a real zip, so archive inspection fails and detection falls back to the extension.

- [ ] **Step 7: Commit**

```bash
git add internal/convert/
git commit -m "feat(convert): docx to markdown via stdlib zip and xml"
```

---

### Task 10: XLSX and PPTX → markdown

**Files:**
- Modify: `internal/convert/ooxml.go` (add both converters)
- Modify: `internal/convert/convert.go` (dispatch)
- Test: `internal/convert/ooxml_test.go` (extend)

**Interfaces:**
- Consumes: `openOOXML`, `readZipPart`, `writeTable`, `maxTableRows` (Tasks 8-9)
- Produces: `xlsxToMarkdown(data []byte, opt Options) (Result, error)`, `pptxToMarkdown(data []byte, opt Options) (Result, error)`

- [ ] **Step 1: Write the failing test**

Append to `internal/convert/ooxml_test.go`:

```go
const xlsxWorkbook = `<?xml version="1.0"?>
<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <sheets><sheet name="Q3" sheetId="1" r:id="rId1"/></sheets>
</workbook>`

// Cells with t="s" reference sharedStrings by index; inline numbers do not.
const xlsxSheet = `<?xml version="1.0"?>
<worksheet><sheetData>
 <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
 <row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>120</v></c></row>
</sheetData></worksheet>`

const xlsxShared = `<?xml version="1.0"?>
<sst><si><t>Region</t></si><si><t>Sales</t></si><si><t>EMEA</t></si></sst>`

func TestXLSXToMarkdown(t *testing.T) {
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":           xlsxWorkbook,
		"xl/worksheets/sheet1.xml":  xlsxSheet,
		"xl/sharedStrings.xml":      xlsxShared,
	})
	got, err := ToMarkdown(data, Options{Filename: "sales.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindXLSX {
		t.Errorf("Kind = %q", got.Kind)
	}
	for _, want := range []string{"## Q3", "| Region | Sales |", "| EMEA | 120 |"} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestXLSXSparseRowsAlign(t *testing.T) {
	// A row that skips column A must not shift its values left — the cell
	// reference (r="B2") is what places a value, not its position in the XML.
	sheet := `<worksheet><sheetData>
	 <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
	 <row r="2"><c r="B2"><v>7</v></c></row>
	</sheetData></worksheet>`
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":          xlsxWorkbook,
		"xl/worksheets/sheet1.xml": sheet,
		"xl/sharedStrings.xml":     xlsxShared,
	})
	got, err := ToMarkdown(data, Options{Filename: "sparse.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "|  | 7 |") {
		t.Errorf("sparse row should keep its column position, got:\n%s", got.Markdown)
	}
}

const pptxSlide = `<?xml version="1.0"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
 <p:cSld><p:spTree>
  <p:sp><p:txBody><a:p><a:r><a:t>Roadmap</a:t></a:r></a:p></p:txBody></p:sp>
  <p:sp><p:txBody><a:p><a:r><a:t>Ship phase one</a:t></a:r></a:p></p:txBody></p:sp>
 </p:spTree></p:cSld>
</p:sld>`

func TestPPTXToMarkdown(t *testing.T) {
	data := buildZip(t, map[string]string{
		"ppt/slides/slide1.xml": pptxSlide,
		"ppt/slides/slide2.xml": strings.Replace(pptxSlide, "Roadmap", "Risks", 1),
	})
	got, err := ToMarkdown(data, Options{Filename: "deck.pptx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindPPTX {
		t.Errorf("Kind = %q", got.Kind)
	}
	for _, want := range []string{"## Slide 1", "Roadmap", "Ship phase one", "## Slide 2", "Risks"} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestPPTXSlidesInNumericOrder(t *testing.T) {
	// Zip entry order is arbitrary and slide10 sorts before slide2 lexically;
	// slides must come out in presentation order.
	parts := map[string]string{}
	for _, n := range []string{"1", "2", "10"} {
		parts["ppt/slides/slide"+n+".xml"] = strings.Replace(pptxSlide, "Roadmap", "S"+n, 1)
	}
	data := buildZip(t, parts)
	got, err := ToMarkdown(data, Options{Filename: "d.pptx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	i1, i2, i10 := strings.Index(got.Markdown, "S1"), strings.Index(got.Markdown, "S2"), strings.Index(got.Markdown, "S10")
	if !(i1 < i2 && i2 < i10) {
		t.Errorf("slides out of order: S1=%d S2=%d S10=%d\n%s", i1, i2, i10, got.Markdown)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/convert/ -run 'TestXLSX|TestPPTX' -v`
Expected: FAIL — `convert: xlsx is not supported yet`.

- [ ] **Step 3: Write both converters**

Append to `internal/convert/ooxml.go`:

```go
// xlsxToMarkdown renders each worksheet as a markdown table under a heading.
// Values live in xl/worksheets/sheetN.xml, but text values are stored once in
// xl/sharedStrings.xml and referenced by index (t="s"), so the shared table
// must be resolved or every text cell reads as a bare number.
func xlsxToMarkdown(data []byte, opt Options) (Result, error) {
	zr, err := openOOXML(data)
	if err != nil {
		return Result{}, err
	}
	shared := readSharedStrings(zr)
	names := sheetNames(zr)

	res := Result{Kind: KindXLSX, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	var sb strings.Builder
	sheets := 0
	for i := 1; ; i++ {
		part, err := readZipPart(zr, fmt.Sprintf("xl/worksheets/sheet%d.xml", i))
		if err != nil {
			break
		}
		rows, err := parseSheetRows(part, shared)
		if err != nil {
			return Result{}, err
		}
		if len(rows) == 0 {
			continue
		}
		sheets++
		name := fmt.Sprintf("Sheet%d", i)
		if i-1 < len(names) && names[i-1] != "" {
			name = names[i-1]
		}
		fmt.Fprintf(&sb, "## %s\n\n", name)
		if len(rows) > maxTableRows+1 {
			rows = rows[:maxTableRows+1]
			res.Warnings = append(res.Warnings, fmt.Sprintf("sheet %s truncated to %d rows", name, maxTableRows))
		}
		writeTable(&sb, rows)
		sb.WriteString("\n")
	}
	if sheets == 0 {
		return Result{}, fmt.Errorf("convert: xlsx contained no readable sheets")
	}
	res.Markdown = normalizeText(collapseBlankLines(sb.String()))
	return res, nil
}

// readSharedStrings returns the shared string table, or nil when absent (a
// workbook of pure numbers legitimately has none).
func readSharedStrings(zr *zip.Reader) []string {
	part, err := readZipPart(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil
	}
	var sst struct {
		SI []struct {
			T string   `xml:"t"`
			R []string `xml:"r>t"` // rich text splits a value across runs
		} `xml:"si"`
	}
	if err := xml.Unmarshal(part, &sst); err != nil {
		return nil
	}
	out := make([]string, 0, len(sst.SI))
	for _, si := range sst.SI {
		if si.T != "" {
			out = append(out, si.T)
			continue
		}
		out = append(out, strings.Join(si.R, ""))
	}
	return out
}

// sheetNames returns worksheet display names in workbook order.
func sheetNames(zr *zip.Reader) []string {
	part, err := readZipPart(zr, "xl/workbook.xml")
	if err != nil {
		return nil
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(part, &wb); err != nil {
		return nil
	}
	out := make([]string, 0, len(wb.Sheets))
	for _, s := range wb.Sheets {
		out = append(out, s.Name)
	}
	return out
}

// parseSheetRows decodes one worksheet into a dense grid. Cells carry an A1
// reference and sparse rows omit empty cells entirely, so column position comes
// from the reference — not from the cell's index in the XML.
func parseSheetRows(part []byte, shared []string) ([][]string, error) {
	var ws struct {
		Rows []struct {
			Cells []struct {
				Ref   string `xml:"r,attr"`
				Type  string `xml:"t,attr"`
				Value string `xml:"v"`
				Inline string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(part, &ws); err != nil {
		return nil, fmt.Errorf("convert: parse worksheet: %w", err)
	}
	var grid [][]string
	for _, r := range ws.Rows {
		row := []string{}
		for _, c := range r.Cells {
			col := columnIndex(c.Ref)
			for len(row) <= col {
				row = append(row, "")
			}
			row[col] = cellValue(c.Type, c.Value, c.Inline, shared)
		}
		grid = append(grid, row)
	}
	return grid, nil
}

func cellValue(typ, value, inline string, shared []string) string {
	switch typ {
	case "s":
		idx := 0
		fmt.Sscanf(value, "%d", &idx)
		if idx >= 0 && idx < len(shared) {
			return shared[idx]
		}
		return ""
	case "inlineStr":
		return inline
	default:
		return value
	}
}

// columnIndex converts the letter part of an A1 reference to a 0-based index
// ("A"→0, "B"→1, "AA"→26). An unparseable reference yields 0.
func columnIndex(ref string) int {
	idx := 0
	for _, ch := range ref {
		if ch < 'A' || ch > 'Z' {
			break
		}
		idx = idx*26 + int(ch-'A') + 1
	}
	if idx == 0 {
		return 0
	}
	return idx - 1
}

// pptxToMarkdown renders each slide as a heading followed by its text. Slides
// are numbered in the part name; zip entry order is arbitrary and lexical
// sorting puts slide10 before slide2, so numeric order is resolved explicitly.
func pptxToMarkdown(data []byte, opt Options) (Result, error) {
	zr, err := openOOXML(data)
	if err != nil {
		return Result{}, err
	}
	res := Result{Kind: KindPPTX, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	var sb strings.Builder
	slides := 0
	for i := 1; ; i++ {
		part, err := readZipPart(zr, fmt.Sprintf("ppt/slides/slide%d.xml", i))
		if err != nil {
			if slides == 0 && i < 3 {
				continue // tolerate a gap at the start
			}
			break
		}
		texts := extractDrawingText(part)
		if len(texts) == 0 {
			continue
		}
		slides++
		fmt.Fprintf(&sb, "## Slide %d\n\n", i)
		for j, t := range texts {
			if j == 0 {
				sb.WriteString("**" + t + "**\n\n")
				continue
			}
			sb.WriteString("- " + t + "\n")
		}
		sb.WriteString("\n")
	}
	if slides == 0 {
		return Result{}, fmt.Errorf("convert: pptx contained no readable slide text")
	}
	res.Markdown = normalizeText(collapseBlankLines(sb.String()))
	return res, nil
}

// extractDrawingText collects <a:t> values, one entry per <a:p> paragraph, so a
// shape's runs join into a single line instead of fragmenting.
func extractDrawingText(part []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(part))
	var out []string
	var cur strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				var text string
				if err := dec.DecodeElement(&text, &t); err == nil {
					cur.WriteString(text)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				if s := strings.TrimSpace(cur.String()); s != "" {
					out = append(out, s)
				}
				cur.Reset()
			}
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 4: Wire the dispatch**

In `internal/convert/convert.go`:

```go
	case KindXLSX:
		return xlsxToMarkdown(data, opt)
	case KindPPTX:
		return pptxToMarkdown(data, opt)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/convert/ -v`
Expected: PASS — all OOXML tests.

- [ ] **Step 6: Commit**

```bash
git add internal/convert/
git commit -m "feat(convert): xlsx and pptx to markdown"
```

---

### Task 11: PDF → markdown

**Files:**
- Create: `internal/convert/pdf.go`
- Test: `internal/convert/pdf_test.go`
- Test fixture: `internal/convert/testdata/simple.pdf`
- Modify: `internal/convert/convert.go` (dispatch), `go.mod`

**Interfaces:**
- Consumes: `Result`, `Options`, `Kind` (Task 1)
- Produces: `pdfToMarkdown(data []byte, opt Options) (Result, error)`; package var `pdftotextPath func() string` (overridable in tests to force the absent branch)

**Reliability note for the implementer:** this is the weakest converter in the package, and the plan is honest about it rather than papering over it. Pure-Go PDF extraction handles text-based PDFs and produces little or nothing on scanned pages, CID-encoded fonts, and heavily-laid-out documents. Two mitigations are mandatory: prefer `pdftotext` when installed, and **record a warning whenever extraction looks thin** so a bad extraction is visible in the note's frontmatter instead of passing as a good one.

- [ ] **Step 1: Create a real PDF fixture**

```bash
mkdir -p internal/convert/testdata
printf 'Quarterly Revenue Report\n\nRevenue grew twelve percent this quarter across EMEA and APAC.\n' \
  > /tmp/fixture.txt
# Any of these produces a small text PDF; use whichever is available.
command -v libreoffice >/dev/null && libreoffice --headless --convert-to pdf --outdir /tmp /tmp/fixture.txt
command -v enscript >/dev/null && enscript -B -o - /tmp/fixture.txt 2>/dev/null | ps2pdf - /tmp/fixture.pdf
cp /tmp/fixture.pdf internal/convert/testdata/simple.pdf
ls -l internal/convert/testdata/simple.pdf
```

Expected: a file of a few KB. If no converter is available on the host, generate one on any machine with `libreoffice` and commit it — the test needs a genuine PDF, not a synthetic byte string.

- [ ] **Step 2: Write the failing test**

Create `internal/convert/pdf_test.go`:

```go
package convert

import (
	"os"
	"strings"
	"testing"
)

func loadPDFFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/simple.pdf")
	if err != nil {
		t.Skipf("pdf fixture missing: %v", err)
	}
	return data
}

func TestPDFPureGo(t *testing.T) {
	data := loadPDFFixture(t)
	// Force the pure-Go path so this branch is covered even on a host that has
	// pdftotext installed.
	orig := pdftotextPath
	pdftotextPath = func() string { return "" }
	defer func() { pdftotextPath = orig }()

	got, err := ToMarkdown(data, Options{Filename: "report.pdf"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindPDF {
		t.Errorf("Kind = %q", got.Kind)
	}
	if got.Extractor != "pure-go" {
		t.Errorf("Extractor = %q, want pure-go", got.Extractor)
	}
	if !strings.Contains(strings.ToLower(got.Markdown), "revenue") {
		t.Errorf("expected extracted text, got:\n%s", got.Markdown)
	}
}

func TestPDFPrefersPdftotextWhenPresent(t *testing.T) {
	if pdftotextPath() == "" {
		t.Skip("pdftotext not installed on this host")
	}
	data := loadPDFFixture(t)
	got, err := ToMarkdown(data, Options{Filename: "report.pdf"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Extractor != "pdftotext" {
		t.Errorf("Extractor = %q, want pdftotext when it is installed", got.Extractor)
	}
}

// A PDF whose text layer yields almost nothing (scanned pages, CID fonts) must
// say so. Silently returning a near-empty body would let a failed extraction
// pass as a successful one — the single most likely way this converter misleads.
func TestPDFThinExtractionWarns(t *testing.T) {
	orig := pdftotextPath
	pdftotextPath = func() string { return "" }
	defer func() { pdftotextPath = orig }()

	// A structurally valid but text-free PDF.
	minimal := []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
	got, err := pdfToMarkdown(minimal, Options{Filename: "scan.pdf"})
	if err != nil {
		// Erroring is an acceptable outcome; a blank success is not.
		return
	}
	if strings.TrimSpace(got.Markdown) == "" {
		t.Fatal("Markdown must never be empty on a nil error")
	}
	if len(got.Warnings) == 0 {
		t.Error("a thin extraction must be recorded as a warning, not passed off as clean text")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/convert/ -run TestPDF -v`
Expected: FAIL — `undefined: pdftotextPath`.

- [ ] **Step 4: Add the PDF dependency**

```bash
go get github.com/ledongthuc/pdf@latest
CGO_ENABLED=0 go build ./...
```

Expected: module added; build succeeds with CGo disabled. Let the toolchain
resolve the version — do not hand-write a pseudo-version. Confirm it is pure Go:

```bash
go list -deps github.com/ledongthuc/pdf | grep -c "^C$" || echo "no cgo"
```

- [ ] **Step 5: Write the converter**

Create `internal/convert/pdf.go`:

```go
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
```

- [ ] **Step 6: Wire the dispatch and the image stub**

In `internal/convert/convert.go`, add:

```go
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
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/convert/ -v`
Expected: PASS. `TestPDFPrefersPdftotextWhenPresent` runs (this host has `/usr/bin/pdftotext`); `TestPDFPureGo` forces and covers the fallback.

- [ ] **Step 8: Verify against a messy real PDF**

Download one genuinely awkward PDF (a scanned document or a multi-column report) and check the output is honest:

```bash
cat > /tmp/pdfcheck.go <<'EOF'
package main

import (
	"fmt"
	"os"

	"github.com/ilijad1/simple-agents/internal/convert"
)

func main() {
	data, _ := os.ReadFile(os.Args[1])
	res, err := convert.ToMarkdown(data, convert.Options{Filename: os.Args[1]})
	fmt.Printf("err=%v extractor=%s warnings=%v\nbytes=%d\n---\n%.600s\n",
		err, res.Extractor, res.Warnings, len(res.Markdown), res.Markdown)
}
EOF
go run /tmp/pdfcheck.go <path-to-messy.pdf>
```

Expected: either usable text, or a `warnings=[...]` entry saying the extraction is incomplete. A near-empty body with no warning is a **bug** — fix the threshold before continuing.

- [ ] **Step 9: Commit**

```bash
git add internal/convert/ go.mod go.sum
git commit -m "feat(convert): pdf to markdown with pdftotext preference and honest warnings"
```

---

### Task 12: Import a converted file into the vault

**Files:**
- Create: `internal/vault/import.go`
- Test: `internal/vault/import_test.go`

**Interfaces:**
- Consumes: `convert.ToMarkdown`, `convert.Options`, `convert.Result` (Tasks 1-11); `Vault.WriteNote`, `Vault.Resolve`, `Vault.Root` (existing)
- Produces:
  - `vault.ImportInput{Data []byte; Filename, SourceURL, DestDir, Title string}`
  - `vault.ImportResult{NotePath, OriginalPath string; Kind, Extractor string; Warnings []string}`
  - `(*Vault).ImportFile(workspaceID string, in ImportInput) (ImportResult, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/vault/import_test.go`:

```go
package vault

import (
	"strings"
	"testing"
)

func TestImportFileWritesNoteWithFrontmatter(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	res, err := v.ImportFile(ws, ImportInput{
		Data:      []byte("Region,Sales\nEMEA,120\n"),
		Filename:  "q3 sales.csv",
		SourceURL: "https://example.com/q3.csv",
	})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.HasPrefix(res.NotePath, "notes/") || !strings.HasSuffix(res.NotePath, ".md") {
		t.Errorf("NotePath = %q, want a markdown note under notes/", res.NotePath)
	}

	data, err := v.ReadNote(ws, res.NotePath)
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"---\n",
		`source: "https://example.com/q3.csv"`,
		"kind: csv",
		"extractor: pure-go",
		"converted_at:",
		"| Region | Sales |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("note missing %q, got:\n%s", want, body)
		}
	}
}

func TestImportFileKeepsOriginal(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "data.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if res.OriginalPath == "" {
		t.Fatal("the original bytes must be preserved: conversion is lossy")
	}
	orig, err := v.ReadNote(ws, res.OriginalPath)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(orig) != "a,b\n1,2\n" {
		t.Errorf("original bytes altered: %q", orig)
	}
	note, _ := v.ReadNote(ws, res.NotePath)
	if !strings.Contains(string(note), res.OriginalPath) {
		t.Error("the note must link to the preserved original")
	}
}

func TestImportFileSanitizesName(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("x,y\n1,2\n"), Filename: "../../etc/passwd.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if strings.Contains(res.NotePath, "..") {
		t.Errorf("path traversal survived sanitization: %q", res.NotePath)
	}
}

func TestImportFileUniqueOnCollision(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	first, _ := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "dup.csv"})
	second, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n3,4\n"), Filename: "dup.csv"})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if first.NotePath == second.NotePath {
		t.Error("a second import of the same name must not overwrite the first")
	}
	data, _ := v.ReadNote(ws, first.NotePath)
	if !strings.Contains(string(data), "| 1 | 2 |") {
		t.Error("the first note was overwritten")
	}
}

func TestImportFileRespectsDestDir(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "x.csv", DestDir: "notes/finance"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.HasPrefix(res.NotePath, "notes/finance/") {
		t.Errorf("NotePath = %q, want it under the requested folder", res.NotePath)
	}
}

func TestImportFileUnsupportedIsError(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	if _, err := v.ImportFile(ws, ImportInput{Data: []byte{0x00, 0x01, 0x02}, Filename: "x.bin"}); err == nil {
		t.Error("an unconvertible file must error rather than create a blank note")
	}
}
```

Note: if `vault.New` has a different constructor signature in this codebase, match the one used by `internal/vault/vault_test.go` rather than inventing one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vault/ -run TestImportFile -v`
Expected: FAIL — `v.ImportFile undefined`.

- [ ] **Step 3: Write the importer**

Create `internal/vault/import.go`:

```go
package vault

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/convert"
)

// FilesDir is the vault folder holding preserved original uploads.
const FilesDir = "files"

// ImportInput describes a file entering the knowledge base.
type ImportInput struct {
	Data      []byte
	Filename  string // original name; used for the note name, title, and format detection
	SourceURL string // where it came from, when it came from the web
	DestDir   string // vault-relative folder for the note; defaults to notes/
	Title     string // overrides the derived title
}

// ImportResult reports where the import landed.
type ImportResult struct {
	NotePath     string
	OriginalPath string
	Kind         string
	Extractor    string
	Warnings     []string
}

// ImportFile converts a file to markdown and files it in the knowledge base.
// It is the single save path shared by the save_to_kb tool, the CLI bridge, the
// web upload endpoint, and chat attachments — so a document enters the vault
// the same way regardless of which door it came through.
//
// The original bytes are preserved alongside the note. Conversion is lossy by
// nature (a PDF's tables, a spreadsheet's formulas), and keeping the source
// means a later agent can re-extract instead of hitting a dead end.
func (v *Vault) ImportFile(workspaceID string, in ImportInput) (ImportResult, error) {
	if len(in.Data) == 0 {
		return ImportResult{}, fmt.Errorf("import: no file content")
	}
	res, err := convert.ToMarkdown(in.Data, convert.Options{
		Filename:  in.Filename,
		SourceURL: in.SourceURL,
	})
	if err != nil {
		return ImportResult{}, err
	}

	base := sanitizeBaseName(in.Filename)
	if base == "" {
		base = "imported-" + time.Now().Format("20060102-150405")
	}

	// Preserve the original first: if this fails, nothing has been written.
	originalRel := uniquePath(v, workspaceID, path.Join(FilesDir, base+originalExt(in.Filename)))
	if err := v.WriteNote(workspaceID, originalRel, in.Data); err != nil {
		return ImportResult{}, fmt.Errorf("import: preserve original: %w", err)
	}

	destDir := strings.Trim(strings.TrimSpace(in.DestDir), "/")
	if destDir == "" {
		destDir = "notes"
	}
	noteRel := uniquePath(v, workspaceID, path.Join(destDir, base+".md"))

	title := in.Title
	if title == "" {
		title = res.Title
	}
	if title == "" {
		title = base
	}

	note := renderImportedNote(title, in, res, originalRel)
	if err := v.WriteNote(workspaceID, noteRel, []byte(note)); err != nil {
		return ImportResult{}, fmt.Errorf("import: write note: %w", err)
	}

	return ImportResult{
		NotePath:     noteRel,
		OriginalPath: originalRel,
		Kind:         string(res.Kind),
		Extractor:    res.Extractor,
		Warnings:     res.Warnings,
	}, nil
}

// renderImportedNote builds the note body. The frontmatter records how the note
// was produced — source, extractor, and any quality warnings — so a lossy
// conversion is never mistaken for a faithful one.
func renderImportedNote(title string, in ImportInput, res convert.Result, originalRel string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "title: %q\n", title)
	source := in.SourceURL
	if source == "" {
		source = in.Filename
	}
	fmt.Fprintf(&sb, "source: %q\n", source)
	fmt.Fprintf(&sb, "kind: %s\n", res.Kind)
	fmt.Fprintf(&sb, "extractor: %s\n", res.Extractor)
	fmt.Fprintf(&sb, "original_bytes: %d\n", len(in.Data))
	fmt.Fprintf(&sb, "original_file: %q\n", originalRel)
	fmt.Fprintf(&sb, "converted_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	if len(res.Warnings) > 0 {
		sb.WriteString("warnings:\n")
		for _, w := range res.Warnings {
			fmt.Fprintf(&sb, "  - %q\n", w)
		}
	}
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# %s\n\n", title)
	if len(res.Warnings) > 0 {
		sb.WriteString("> **Conversion notes:** " + strings.Join(res.Warnings, "; ") + "\n\n")
	}
	fmt.Fprintf(&sb, "_Converted from [%s](%s)._\n\n", path.Base(originalRel), originalRel)
	sb.WriteString(res.Markdown)
	if !strings.HasSuffix(res.Markdown, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

var unsafeNameChars = regexp.MustCompile(`[^a-zA-Z0-9._ -]+`)

// sanitizeBaseName reduces an arbitrary (possibly hostile) filename to a safe
// note name: no directory components, no traversal, no control characters.
func sanitizeBaseName(filename string) string {
	base := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	base = strings.TrimSuffix(base, path.Ext(base))
	base = unsafeNameChars.ReplaceAllString(base, "-")
	base = strings.Trim(strings.Join(strings.Fields(base), " "), "-. ")
	if len(base) > 80 {
		base = base[:80]
	}
	if base == "" || base == "." || base == ".." {
		return ""
	}
	return base
}

// originalExt returns the original file's extension, or "" when it has none.
func originalExt(filename string) string {
	ext := path.Ext(path.Base(strings.ReplaceAll(filename, `\`, "/")))
	if len(ext) > 10 || unsafeNameChars.MatchString(strings.TrimPrefix(ext, ".")) {
		return ""
	}
	return ext
}

// uniquePath appends -2, -3, … until the path is free, so an import never
// silently overwrites an existing note.
func uniquePath(v *Vault, workspaceID, rel string) string {
	if _, err := v.ReadNote(workspaceID, rel); err != nil {
		return rel
	}
	ext := path.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := v.ReadNote(workspaceID, candidate); err != nil {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d%s", stem, time.Now().UnixNano(), ext)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/vault/ -run TestImportFile -v`
Expected: PASS — all six tests.

- [ ] **Step 5: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s 2>&1 | grep -v "^ok" | head`
Expected: no failures.

- [ ] **Step 6: Commit**

```bash
git add internal/vault/import.go internal/vault/import_test.go
git commit -m "feat(vault): import a converted file as a note, preserving the original"
```

---

### Task 13: KB bridge + `save_to_kb` tool + CLI subcommands

**Files:**
- Create: `internal/vault/bridge.go`
- Test: `internal/vault/bridge_test.go`
- Modify: `internal/coder/hosttools.go` (`tools()`, `execute()`, new `saveToKB`)
- Modify: `cmd/simple-agents/main.go` (start the bridge in `serve`; add `kbCmd()`)

**Interfaces:**
- Consumes: `(*Vault).ImportFile`, `ImportInput`, `ImportResult` (Task 12); `(*Vault).NewSearcher` (existing)
- Produces:
  - `vault.NewBridge(v *Vault) *Bridge`, `(*Bridge).Start() error`, `(*Bridge).URL() string`, `(*Bridge).Register(workspaceID string) string` (returns a scoped token), `(*Bridge).Unregister(token string)`
  - Host tool `save_to_kb` with args `{source, dest_dir?, title?}`
  - CLI: `simple-agents kb convert <path-or-url> [--dest <dir>] [--title <t>]`, `simple-agents kb search <query>`

**Design note:** the bridge mirrors `connectors.Bridge` exactly — a loopback listener in the host process, a per-run bearer token scoped to one workspace. That pattern is already proven here, and Landlock restricts the filesystem rather than loopback TCP, so a sandboxed CLI coder can reach it.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/bridge_test.go`:

```go
package vault

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func startTestBridge(t *testing.T) (*Bridge, *Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b := NewBridge(v)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Close)
	return b, v, b.Register("ws1")
}

func post(t *testing.T, url, token string, payload any) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestBridgeConvertWritesNote(t *testing.T) {
	b, v, token := startTestBridge(t)
	resp, out := post(t, b.URL()+"/convert", token, map[string]any{
		"filename": "data.csv",
		"content":  "YSxiCjEsMgo=", // base64 of "a,b\n1,2\n"
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	notePath, _ := out["note_path"].(string)
	if notePath == "" {
		t.Fatalf("no note_path in %v", out)
	}
	if _, err := v.ReadNote("ws1", notePath); err != nil {
		t.Errorf("note not written: %v", err)
	}
}

func TestBridgeRejectsBadToken(t *testing.T) {
	b, _, _ := startTestBridge(t)
	resp, _ := post(t, b.URL()+"/convert", "not-a-real-token", map[string]any{"filename": "x.csv", "content": "YSxiCg=="})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBridgeSearchScopedToWorkspace(t *testing.T) {
	b, v, token := startTestBridge(t)
	v.WriteNote("ws1", "notes/a.md", []byte("the dentist appointment is tuesday"))
	v.EnsureScaffold("ws2")
	v.WriteNote("ws2", "notes/b.md", []byte("another workspace dentist note"))

	resp, out := post(t, b.URL()+"/search", token, map[string]any{"query": "dentist"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	results, _ := out["results"].(string)
	if !bytes.Contains([]byte(results), []byte("notes/a.md")) {
		t.Errorf("own workspace note missing: %q", results)
	}
	if bytes.Contains([]byte(results), []byte("notes/b.md")) {
		t.Error("a token scoped to ws1 must never surface another workspace's notes")
	}
}

func TestBridgeUnregister(t *testing.T) {
	b, _, token := startTestBridge(t)
	b.Unregister(token)
	resp, _ := post(t, b.URL()+"/search", token, map[string]any{"query": "x"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked token must stop working, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vault/ -run TestBridge -v`
Expected: FAIL — `undefined: NewBridge`.

- [ ] **Step 3: Write the bridge**

Create `internal/vault/bridge.go`:

```go
package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Bridge lets a CLI coder subprocess reach the knowledge base's conversion and
// search paths in the host process. It mirrors connectors.Bridge: a loopback
// listener plus a per-run bearer token scoped to exactly one workspace, so a
// coder can never read or write another tenant's vault. Landlock confines the
// filesystem, not loopback TCP, so a sandboxed child can still reach it.
type Bridge struct {
	v   *Vault
	mu  sync.RWMutex
	tok map[string]string // token → workspaceID
	srv *http.Server
	ln  net.Listener
}

func NewBridge(v *Vault) *Bridge {
	return &Bridge{v: v, tok: map[string]string{}}
}

// Start binds a loopback listener on an ephemeral port and serves the bridge.
func (b *Bridge) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("kb bridge listen: %w", err)
	}
	b.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/convert", b.handleConvert)
	mux.HandleFunc("/search", b.handleSearch)
	b.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = b.srv.Serve(ln) }()
	return nil
}

func (b *Bridge) Close() {
	if b.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.srv.Shutdown(ctx)
	}
}

// URL is the base a subprocess should POST to, or "" when not started.
func (b *Bridge) URL() string {
	if b.ln == nil {
		return ""
	}
	return "http://" + b.ln.Addr().String()
}

// Register issues a token scoped to one workspace and returns it.
func (b *Bridge) Register(workspaceID string) string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)
	b.mu.Lock()
	b.tok[token] = workspaceID
	b.mu.Unlock()
	return token
}

// Unregister revokes a token when its run ends.
func (b *Bridge) Unregister(token string) {
	b.mu.Lock()
	delete(b.tok, token)
	b.mu.Unlock()
}

// authorize maps a request's bearer token to its workspace.
func (b *Bridge) authorize(r *http.Request) (string, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		return "", false
	}
	b.mu.RLock()
	ws, ok := b.tok[token]
	b.mu.RUnlock()
	return ws, ok
}

func (b *Bridge) handleConvert(w http.ResponseWriter, r *http.Request) {
	ws, ok := b.authorize(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Filename  string `json:"filename"`
		Content   string `json:"content"` // base64
		SourceURL string `json:"source_url"`
		DestDir   string `json:"dest_dir"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content must be base64"})
		return
	}
	res, err := b.v.ImportFile(ws, ImportInput{
		Data: data, Filename: req.Filename, SourceURL: req.SourceURL,
		DestDir: req.DestDir, Title: req.Title,
	})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"note_path": res.NotePath, "original_path": res.OriginalPath,
		"kind": res.Kind, "extractor": res.Extractor, "warnings": res.Warnings,
	})
}

func (b *Bridge) handleSearch(w http.ResponseWriter, r *http.Request) {
	ws, ok := b.authorize(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	hits, err := b.v.NewSearcher().Search(ctx, ws, req.Query)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var sb strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&sb, "%s:%d: %s\n", h.Path, h.Line, h.Snippet)
	}
	if sb.Len() == 0 {
		sb.WriteString(fmt.Sprintf("(no matches for %q)\n", req.Query))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": sb.String()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
```

- [ ] **Step 4: Run bridge tests to verify they pass**

Run: `go test ./internal/vault/ -run TestBridge -v`
Expected: PASS — all four tests.

- [ ] **Step 5: Write the failing test for the host tool**

Create `internal/coder/hosttools_savekb_test.go`:

```go
package coder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/llm"
	"github.com/ilijad1/simple-agents/internal/vault"
)

func newSaveKBToolset(t *testing.T) (*hostToolSet, *vault.Vault) {
	t.Helper()
	base := t.TempDir()
	v := vault.New(base)
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	return &hostToolSet{
		workspaceID:      "ws1",
		vlt:              v,
		workDir:          v.Root("ws1"),
		includeExecTools: false,
	}, v
}

func TestSaveToKBFromVaultPath(t *testing.T) {
	h, v := newSaveKBToolset(t)
	if err := os.WriteFile(filepath.Join(v.Root("ws1"), "raw.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	out := h.execute(context.Background(), llm.ToolCall{
		Name: "save_to_kb",
		Args: json.RawMessage(`{"source":"raw.csv"}`),
	})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("save_to_kb failed: %s", out)
	}
	if !strings.Contains(out, "notes/") {
		t.Errorf("result should name the created note, got %q", out)
	}
}

func TestSaveToKBOfferedInChat(t *testing.T) {
	h, _ := newSaveKBToolset(t)
	var found bool
	for _, tool := range h.tools() {
		if tool.Name == "save_to_kb" {
			found = true
		}
	}
	if !found {
		t.Error("save_to_kb must be available without exec tools: it converts and files, it does not execute")
	}
}

func TestSaveToKBMissingSourceIsError(t *testing.T) {
	h, _ := newSaveKBToolset(t)
	out := h.execute(context.Background(), llm.ToolCall{Name: "save_to_kb", Args: json.RawMessage(`{"source":"nope.csv"}`)})
	if !strings.HasPrefix(out, "error:") {
		t.Errorf("a missing source must error, got %q", out)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/coder/ -run TestSaveToKB -v`
Expected: FAIL — `error: unknown tool save_to_kb`.

- [ ] **Step 7: Add the tool**

In `internal/coder/hosttools.go`, add to the always-on tool list in `tools()` (after `glob`):

```go
		{Name: "save_to_kb", Description: "Convert a document to markdown and file it in the user's knowledge base. " +
			"source is either a vault path (e.g. \"downloads/report.pdf\") or a public http(s) URL. " +
			"Handles pdf, docx, pptx, xlsx, csv, html and plain text; the original file is preserved alongside the note. " +
			"Returns the created note's path. Use this instead of writing your own extraction script.",
			Parameters: rawSchema(`{"type":"object","properties":{"source":{"type":"string","description":"vault path or public http(s) URL"},"dest_dir":{"type":"string","description":"vault folder for the note (default notes/)"},"title":{"type":"string","description":"override the derived title"}},"required":["source"]}`)},
```

Add to `execute()`'s switch:

```go
	case "save_to_kb":
		out, err := h.saveToKB(ctx, args.Source, args.DestDir, args.Title)
		if err != nil {
			return "error: " + err.Error()
		}
		return out
```

Add the three fields to `execute()`'s args struct:

```go
		Source  string `json:"source"`
		DestDir string `json:"dest_dir"`
		Title   string `json:"title"`
```

And add the implementation near `searchFiles`:

```go
// saveToKB converts a document and files it in the knowledge base. The source is
// either a vault-relative path or a public URL; a URL is fetched through the
// SAME guarded client web_fetch uses, so importing cannot become a way around
// the private-address block.
func (h *hostToolSet) saveToKB(ctx context.Context, source, destDir, title string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("source is required")
	}
	if h.vlt == nil {
		return "", fmt.Errorf("save_to_kb unavailable: no vault")
	}

	var (
		data     []byte
		filename string
		srcURL   string
	)
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		raw, name, err := h.fetchRaw(ctx, source)
		if err != nil {
			return "", err
		}
		data, filename, srcURL = raw, name, source
	} else {
		abs, err := h.resolveVault(source)
		if err != nil {
			return "", err
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("read %s: %v", source, err)
		}
		data, filename = raw, filepath.Base(abs)
	}

	res, err := h.vlt.ImportFile(h.workspaceID, vault.ImportInput{
		Data: data, Filename: filename, SourceURL: srcURL, DestDir: destDir, Title: title,
	})
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("ok: saved %s (%s, via %s); original kept at %s",
		res.NotePath, res.Kind, res.Extractor, res.OriginalPath)
	if len(res.Warnings) > 0 {
		msg += "\nwarnings: " + strings.Join(res.Warnings, "; ")
	}
	return msg, nil
}

// fetchRaw downloads a URL's bytes (not its rendered text) for conversion.
func (h *hostToolSet) fetchRaw(ctx context.Context, rawURL string) ([]byte, string, error) {
	client := h.httpClient
	if client == nil {
		if h.allowPrivateHosts {
			client = &http.Client{Timeout: 60 * time.Second}
		} else {
			client = guardedHTTPClient(60 * time.Second)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch %s: %v", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWebBody))
	if err != nil {
		return nil, "", err
	}
	name := path.Base(rawURL)
	if i := strings.IndexByte(name, '?'); i >= 0 {
		name = name[:i]
	}
	if name == "" || name == "/" || name == "." {
		name = "download"
	}
	return data, name, nil
}
```

Add imports as needed: `"os"`, `"path"`, `"path/filepath"`, `"github.com/ilijad1/simple-agents/internal/vault"`.

- [ ] **Step 8: Add the CLI subcommands**

In `cmd/simple-agents/main.go`, register `kbCmd()` in the `Commands` slice next to `connectorCmd()`, and add:

```go
// kbCmd is how a CLI coder reaches the knowledge base's conversion and search
// paths: it POSTs to the loopback KB bridge in the host process, which runs the
// SAME vault.ImportFile / Searcher code the API engine calls in-process. The
// bridge URL and a run-scoped token come from SA_KB_URL / SA_KB_TOKEN.
func kbCmd() *cli.Command {
	post := func(ctx context.Context, endpoint string, payload any) error {
		base, token := os.Getenv("SA_KB_URL"), os.Getenv("SA_KB_TOKEN")
		if base == "" || token == "" {
			return fmt.Errorf("no knowledge-base bridge available in this run")
		}
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(ctx, "POST", base+endpoint, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("kb bridge unreachable: %w", err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		fmt.Print(string(out))
		return nil
	}
	return &cli.Command{
		Name:  "kb",
		Usage: "Knowledge-base actions (used by CLI coders)",
		Commands: []*cli.Command{
			{
				Name:      "convert",
				Usage:     "Convert a file to markdown and save it: kb convert <path> [--dest notes/x] [--title T]",
				ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dest", Usage: "vault folder for the note"},
					&cli.StringFlag{Name: "title", Usage: "override the derived title"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					src := cmd.Args().First()
					if src == "" {
						return fmt.Errorf("usage: kb convert <path> [--dest <dir>]")
					}
					data, err := os.ReadFile(src)
					if err != nil {
						return fmt.Errorf("read %s: %w", src, err)
					}
					return post(ctx, "/convert", map[string]any{
						"filename": filepath.Base(src),
						"content":  base64.StdEncoding.EncodeToString(data),
						"dest_dir": cmd.String("dest"),
						"title":    cmd.String("title"),
					})
				},
			},
			{
				Name:      "search",
				Usage:     "Search the knowledge base: kb search <query>",
				ArgsUsage: "<query>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					q := strings.Join(cmd.Args().Slice(), " ")
					if strings.TrimSpace(q) == "" {
						return fmt.Errorf("usage: kb search <query>")
					}
					return post(ctx, "/search", map[string]any{"query": q})
				},
			},
		},
	}
}
```

Add imports `"encoding/base64"`, `"path/filepath"`, `"strings"` if missing.

- [ ] **Step 9: Start the bridge in `serve` and inject its env**

In `cmd/simple-agents/main.go`'s `serve` action, immediately after the connector bridge is started (~line 205), add:

```go
			// Loopback KB bridge so CLI coders reach conversion + search in-process.
			kbBridge := vault.NewBridge(vlt)
			if err := kbBridge.Start(); err != nil {
				return fmt.Errorf("start kb bridge: %w", err)
			}
```

Then, wherever the CLI chat coder's allowed tools are set (both branches touched in Task 7), extend the scoped Bash grant and inject the env:

```go
								kbToken := kbBridge.Register(workspaceID)
								cd = cd.WithExtraEnv(map[string]string{
									"SA_KB_URL":   kbBridge.URL(),
									"SA_KB_TOKEN": kbToken,
								}).WithAllowedTools("Read,Write,Edit,Glob,Grep,WebFetch,WebSearch," +
									"Bash(" + connBin + " connector exec:*),Bash(" + connBin + " kb:*)")
```

Do the same in `internal/agentrunner/runner.go` where the connector bridge token is registered for a run, so agent runs on a CLI coder get `SA_KB_URL`/`SA_KB_TOKEN` too. Unregister the token when the run ends, alongside the existing connector-token cleanup.

- [ ] **Step 10: Run the full suite and smoke-test**

```bash
go test ./... -count=1 -timeout 120s 2>&1 | grep -v "^ok" | head
make deploy && sleep 3 && curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/
```
Expected: no test failures; `200`.

- [ ] **Step 11: Commit**

```bash
git add internal/vault/bridge.go internal/vault/bridge_test.go internal/coder/ cmd/simple-agents/main.go internal/agentrunner/runner.go
git commit -m "feat: save_to_kb tool and kb bridge for CLI coders"
```

---

### Task 14: KB file upload in the web UI

**Files:**
- Modify: `web/api_kb.go` (register `POST /kb/upload`, add `apiUploadKBFile`)
- Test: `web/api_kb_upload_test.go`
- Modify: `web/ui/src/pages/kb/KBPage.tsx` (drop target + upload button)

**Interfaces:**
- Consumes: `(*Vault).ImportFile` (Task 12); existing `requireActiveWorkspaceAPI` middleware
- Produces: `POST /api/v1/kb/upload` (multipart, field `file`, optional field `dir`) → `{note_path, original_path, kind, extractor, warnings}`

- [ ] **Step 1: Write the failing test**

Create `web/api_kb_upload_test.go`:

```go
package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func uploadRequest(t *testing.T, filename, content, dir string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if dir != "" {
		mw.WriteField("dir", dir)
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write([]byte(content))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestAPIKBUploadConvertsAndFiles(t *testing.T) {
	s, ws := newTestServerWithWorkspace(t) // existing helper used by the other api_kb tests
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, withSession(t, uploadRequest(t, "sales.csv", "a,b\n1,2\n", ""), ws))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		NotePath  string `json:"note_path"`
		Kind      string `json:"kind"`
		Extractor string `json:"extractor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasSuffix(out.NotePath, ".md") || out.Kind != "csv" {
		t.Errorf("unexpected response: %+v", out)
	}
}

func TestAPIKBUploadRejectsOversized(t *testing.T) {
	s, ws := newTestServerWithWorkspace(t)
	big := strings.Repeat("a,b\n", maxUploadBytes/4+10)
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, withSession(t, uploadRequest(t, "big.csv", big, ""), ws))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

func TestAPIKBUploadRequiresWorkspace(t *testing.T) {
	s, _ := newTestServerWithWorkspace(t)
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, uploadRequest(t, "x.csv", "a,b\n", "")) // no session
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want an auth failure", rec.Code)
	}
}

func TestAPIKBUploadUnsupportedIsUnprocessable(t *testing.T) {
	s, ws := newTestServerWithWorkspace(t)
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, withSession(t, uploadRequest(t, "x.bin", "\x00\x01\x02", ""), ws))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}
```

Match `newTestServerWithWorkspace` / `withSession` to whatever helpers the existing `web/api_kb*_test.go` files use; do not invent new ones.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run TestAPIKBUpload -v`
Expected: FAIL — 404, route not registered.

- [ ] **Step 3: Add the handler**

In `web/api_kb.go`, register the route in `registerKBAPI`:

```go
	g.POST("/kb/upload", s.apiUploadKBFile)
```

and add:

```go
// maxUploadBytes caps an uploaded file. Conversion allocates, and an unbounded
// upload is a trivial memory-exhaustion vector on a home server.
const maxUploadBytes = 25 << 20 // 25 MiB

// apiUploadKBFile accepts a document, converts it to markdown, and files it in
// the workspace's knowledge base. It shares vault.ImportFile with the save_to_kb
// tool and the CLI bridge, so a file lands identically no matter which door it
// came through.
func (s *Server) apiUploadKBFile(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "no file uploaded")
	}
	if fh.Size > maxUploadBytes {
		return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("file is %d bytes; the limit is %d", fh.Size, maxUploadBytes))
	}
	src, err := fh.Open()
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not read the upload")
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, maxUploadBytes+1))
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not read the upload")
	}
	if len(data) > maxUploadBytes {
		return jsonErr(c, http.StatusRequestEntityTooLarge, "too_large", "file exceeds the upload limit")
	}

	res, err := s.vault.ImportFile(w.ID, vault.ImportInput{
		Data:     data,
		Filename: fh.Filename,
		DestDir:  strings.TrimSpace(c.FormValue("dir")),
	})
	if err != nil {
		// A format we cannot convert is a property of the request, not a server
		// fault — 422 so the UI can say "we can't read this kind of file".
		return jsonErr(c, http.StatusUnprocessableEntity, "unsupported_format", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"note_path":     res.NotePath,
		"original_path": res.OriginalPath,
		"kind":          res.Kind,
		"extractor":     res.Extractor,
		"warnings":      res.Warnings,
	})
}
```

Add imports `"io"`, `"fmt"`, `"strings"`, and `"github.com/ilijad1/simple-agents/internal/vault"` if missing.

- [ ] **Step 4: Register the route in the parity inventory**

In `web/api_parity_test.go`, add `POST /api/v1/kb/upload` to the `want` table. That table is the merge gate; a route missing from it fails the build.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./web/ -run 'TestAPIKBUpload|TestAPIParityInventory' -v`
Expected: PASS.

- [ ] **Step 6: Add the SPA drop target**

In `web/ui/src/pages/kb/KBPage.tsx`, add an upload affordance to the tree pane header and a drop handler on the tree container:

```tsx
async function uploadFile(file: File, dir: string): Promise<void> {
  const body = new FormData();
  body.append("file", file);
  if (dir) body.append("dir", dir);
  const res = await fetch("/api/v1/kb/upload", { method: "POST", body });
  const data = await res.json();
  if (!res.ok) throw new Error(data?.message ?? "Upload failed");
  return data;
}

// Inside the component:
const [dragging, setDragging] = useState(false);

const onDrop = useCallback(
  async (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(false);
    const files = Array.from(e.dataTransfer.files);
    for (const file of files) {
      try {
        const res = await uploadFile(file, currentDir);
        toast({
          title: `Imported ${file.name}`,
          description: res.warnings?.length
            ? res.warnings.join("; ")
            : `Saved as ${res.note_path}`,
        });
      } catch (err) {
        toast({ title: `Could not import ${file.name}`, description: String(err) });
      }
    }
    await refreshTree();
  },
  [currentDir, refreshTree, toast],
);
```

Wire `onDragOver={(e) => { e.preventDefault(); setDragging(true); }}`, `onDragLeave={() => setDragging(false)}`, and `onDrop={onDrop}` on the tree container, with a visible ring while `dragging` is true. Add a matching `<input type="file" multiple>` behind an "Import" button for keyboard and mobile users — drop-only would make the feature unreachable without a pointer.

- [ ] **Step 7: Build the UI and verify**

```bash
make ui && make deploy && sleep 3
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/
```
Expected: `200`. Then in a browser: drop a PDF onto the KB tree and confirm a note appears with frontmatter naming the extractor.

- [ ] **Step 8: Commit**

```bash
git add web/api_kb.go web/api_kb_upload_test.go web/api_parity_test.go web/ui/src/pages/kb/
git commit -m "feat(kb): upload and convert files from the web UI"
```

---

### Task 15: Chat file attachments

**Files:**
- Modify: `internal/gateway/telegram.go` (handle document/photo messages)
- Modify: `internal/gateway/gateway.go` (extend the inbound message type with an optional attachment)
- Modify: `internal/gateway/router.go` (route an attachment to `ImportFile`)
- Test: `internal/gateway/attachment_test.go`

**Interfaces:**
- Consumes: `(*Vault).ImportFile` (Task 12)
- Produces: `gateway.Attachment{Filename string; Data []byte}` on the inbound message; `Router` imports it and replies with the note path

- [ ] **Step 1: Write the failing test**

Create `internal/gateway/attachment_test.go`:

```go
package gateway

import (
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/vault"
)

func TestRouterImportsAttachment(t *testing.T) {
	v := vault.New(t.TempDir())
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	r := &Router{vault: v} // match the Router's actual construction in router.go

	reply, err := r.handleAttachment("ws1", Attachment{
		Filename: "budget.csv",
		Data:     []byte("item,cost\nrent,900\n"),
	})
	if err != nil {
		t.Fatalf("handleAttachment: %v", err)
	}
	if !strings.Contains(reply, "notes/") {
		t.Errorf("reply should name the created note, got %q", reply)
	}
}

func TestRouterAttachmentUnsupportedRepliesClearly(t *testing.T) {
	v := vault.New(t.TempDir())
	v.EnsureScaffold("ws1")
	r := &Router{vault: v}

	reply, err := r.handleAttachment("ws1", Attachment{Filename: "x.bin", Data: []byte{0, 1, 2}})
	if err == nil && !strings.Contains(strings.ToLower(reply), "couldn") {
		t.Errorf("an unconvertible attachment must say so plainly, got reply=%q err=%v", reply, err)
	}
}

func TestRouterAttachmentTooLarge(t *testing.T) {
	v := vault.New(t.TempDir())
	v.EnsureScaffold("ws1")
	r := &Router{vault: v}

	big := make([]byte, maxAttachmentBytes+1)
	if _, err := r.handleAttachment("ws1", Attachment{Filename: "big.csv", Data: big}); err == nil {
		t.Error("an oversized attachment must be refused")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestRouterAttachment -v`
Expected: FAIL — `undefined: Attachment`.

- [ ] **Step 3: Add the attachment type and router handling**

In `internal/gateway/gateway.go`, add:

```go
// Attachment is a file a user sent through a chat platform. Adapters download
// the bytes and hand them to the router; conversion and storage happen once,
// in the shared vault.ImportFile path, so a file sent in Telegram lands exactly
// as one uploaded in the web UI does.
type Attachment struct {
	Filename string
	Data     []byte
}
```

Add an `Attachment *Attachment` field to the inbound message struct the adapters construct.

In `internal/gateway/router.go`, add:

```go
// maxAttachmentBytes caps a chat attachment. Chat platforms already cap uploads
// well below this; the limit exists so a hostile or misbehaving adapter cannot
// hand the router an unbounded buffer.
const maxAttachmentBytes = 25 << 20

// handleAttachment converts a chat attachment and files it in the knowledge
// base, returning the message to send back.
func (r *Router) handleAttachment(workspaceID string, att Attachment) (string, error) {
	if len(att.Data) == 0 {
		return "", fmt.Errorf("attachment was empty")
	}
	if len(att.Data) > maxAttachmentBytes {
		return "", fmt.Errorf("attachment is too large (%d bytes; limit %d)", len(att.Data), maxAttachmentBytes)
	}
	if r.vault == nil {
		return "", fmt.Errorf("knowledge base is unavailable")
	}
	res, err := r.vault.ImportFile(workspaceID, vault.ImportInput{
		Data: att.Data, Filename: att.Filename,
	})
	if err != nil {
		return fmt.Sprintf("I couldn't read **%s** — %s", att.Filename, err.Error()), nil
	}
	msg := fmt.Sprintf("Saved **%s** to your knowledge base as `%s`.", att.Filename, res.NotePath)
	if len(res.Warnings) > 0 {
		msg += "\n\n_Note: " + strings.Join(res.Warnings, "; ") + "_"
	}
	return msg, nil
}
```

In `Router.Handle()`, before the plain-text branch, add:

```go
	if msg.Attachment != nil {
		reply, err := r.handleAttachment(msg.WorkspaceID, *msg.Attachment)
		if err != nil {
			return "⚠️ " + err.Error(), nil
		}
		return reply, nil
	}
```

- [ ] **Step 4: Download attachments in the Telegram adapter**

In `internal/gateway/telegram.go`, in the handler that builds an inbound message, add a document branch:

```go
	// Telegram delivers a file as a Document (or a Photo, which arrives as a
	// sized list — the last entry is the largest). Either way the bytes come
	// from a two-step getFile → download, which telebot wraps in File/Download.
	if doc := m.Document; doc != nil {
		if data, name, err := g.downloadTelegramFile(doc.File, doc.FileName); err == nil {
			inbound.Attachment = &Attachment{Filename: name, Data: data}
		} else {
			slog.Warn("telegram: attachment download failed", "err", err)
		}
	}
```

and add the helper:

```go
// downloadTelegramFile fetches an attachment's bytes via the bot API.
func (g *TelegramGateway) downloadTelegramFile(f telebot.File, name string) ([]byte, string, error) {
	rc, err := g.bot.File(&f)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxAttachmentBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxAttachmentBytes {
		return nil, "", fmt.Errorf("attachment exceeds the size limit")
	}
	if strings.TrimSpace(name) == "" {
		name = "attachment"
	}
	return data, name, nil
}
```

- [ ] **Step 5: Check whether Discord and Slack are cheap**

Both deliver an attachment as a URL on the message rather than a file id, so the adapter work is a plain authenticated GET into the same `Attachment` struct. Add them **only if each is genuinely a few lines**; if either needs real plumbing, stop and leave it — the spec defers them explicitly rather than expanding this task.

```bash
grep -n "MessageCreate\|Attachments" internal/gateway/discord.go | head
grep -n "Files\|url_private" internal/gateway/slack.go | head
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/gateway/ -count=1 -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 7: Verify end to end**

```bash
make deploy && sleep 3 && make logs
```
Send a small PDF or CSV to the Telegram bot as a document. Expected: a reply naming the created note; the note is visible in the KB browser with frontmatter recording source and extractor.

- [ ] **Step 8: Commit — Phase 2 complete and mergeable**

```bash
git add internal/gateway/
git commit -m "feat(gateway): import chat file attachments into the knowledge base"
```

---

# PHASE 3 — Knowledge-base retrieval

Ends mergeable: `search_files` ranks results and returns usable chunks over the whole vault and every file type, and the agent designer sees the notes that matter instead of 30 arbitrary filenames.

---

### Task 16: Heading-aware chunking

**Files:**
- Create: `internal/vault/chunk.go`
- Test: `internal/vault/chunk_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `vault.Chunk{Path, Heading, Text string; Line int}`, `vault.ChunkMarkdown(path, content string) []Chunk`, `vault.ChunkPlain(path, content string) []Chunk`, const `targetChunkChars = 1500`

- [ ] **Step 1: Write the failing test**

Create `internal/vault/chunk_test.go`:

```go
package vault

import (
	"strings"
	"testing"
)

func TestChunkMarkdownSplitsOnHeadings(t *testing.T) {
	doc := `# Trip plan

Intro paragraph.

## Flights

Booked with Wizz on the 3rd.

## Hotels

Staying near the centre.
`
	chunks := ChunkMarkdown("notes/trip.md", doc)
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3 (one per heading section): %+v", len(chunks), chunks)
	}
	if chunks[1].Heading != "Trip plan > Flights" {
		t.Errorf("Heading = %q, want the full trail", chunks[1].Heading)
	}
	if !strings.Contains(chunks[1].Text, "Wizz") {
		t.Errorf("chunk text wrong: %q", chunks[1].Text)
	}
	if chunks[1].Line < 5 {
		t.Errorf("Line = %d, want the heading's line in the file", chunks[1].Line)
	}
	for _, c := range chunks {
		if c.Path != "notes/trip.md" {
			t.Errorf("Path = %q", c.Path)
		}
	}
}

func TestChunkMarkdownSplitsOversizedSection(t *testing.T) {
	// A long section with no subheadings must still be split, or one huge note
	// would monopolise every result.
	body := strings.Repeat("Sentence about budget planning. ", 200) // ~6400 chars
	chunks := ChunkMarkdown("notes/long.md", "# Budget\n\n"+body)
	if len(chunks) < 3 {
		t.Fatalf("a %d-char section should split into several chunks, got %d", len(body), len(chunks))
	}
	for _, c := range chunks {
		if len(c.Text) > targetChunkChars*2 {
			t.Errorf("chunk of %d chars exceeds the bound", len(c.Text))
		}
		if c.Heading != "Budget" {
			t.Errorf("split chunks must keep the section heading, got %q", c.Heading)
		}
	}
}

func TestChunkMarkdownNoHeadings(t *testing.T) {
	chunks := ChunkMarkdown("notes/flat.md", "just a line\nand another\n")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("Heading = %q, want empty for a note with no headings", chunks[0].Heading)
	}
}

func TestChunkSkipsEmpty(t *testing.T) {
	if got := ChunkMarkdown("notes/empty.md", "\n\n   \n"); len(got) != 0 {
		t.Errorf("an empty note should yield no chunks, got %+v", got)
	}
}

func TestChunkPlain(t *testing.T) {
	chunks := ChunkPlain("files/data.txt", strings.Repeat("row of data. ", 400))
	if len(chunks) < 2 {
		t.Fatalf("plain text should split by size, got %d", len(chunks))
	}
	if chunks[0].Path != "files/data.txt" {
		t.Errorf("Path = %q", chunks[0].Path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vault/ -run TestChunk -v`
Expected: FAIL — `undefined: ChunkMarkdown`.

- [ ] **Step 3: Write the chunker**

Create `internal/vault/chunk.go`:

```go
package vault

import (
	"strings"
)

// targetChunkChars is the size a chunk aims for. Big enough that a retrieved
// chunk answers the question on its own (the whole point of returning chunks
// rather than matching lines), small enough that several fit inside a tool
// result's byte cap.
const targetChunkChars = 1500

// Chunk is one retrievable passage of a note.
type Chunk struct {
	Path    string // vault-relative path of the source file
	Heading string // heading trail, e.g. "Trip plan > Flights" ("" if none)
	Text    string // the passage
	Line    int    // 1-based line in the file where this passage starts
}

// ChunkMarkdown splits a markdown document at heading boundaries. Headings are
// the author's own structure, so a section is the natural unit of meaning — and
// carrying the heading trail means a retrieved chunk states where it came from
// without the reader needing the rest of the file.
//
// A section longer than the target is split further, on paragraph boundaries, so
// one long note cannot monopolise a result set. Splitting never cuts mid-line.
func ChunkMarkdown(path, content string) []Chunk {
	lines := strings.Split(content, "\n")
	var (
		out      []Chunk
		trail    []string
		curLines []string
		curStart = 1
		curHead  string
	)

	flush := func() {
		text := strings.TrimSpace(strings.Join(curLines, "\n"))
		curLines = nil
		if text == "" {
			return
		}
		for _, part := range splitOversized(text) {
			out = append(out, Chunk{Path: path, Heading: curHead, Text: part, Line: curStart})
		}
	}

	for i, line := range lines {
		if level, title, ok := parseHeading(line); ok {
			flush()
			trail = updateTrail(trail, level, title)
			curHead = strings.Join(trail, " > ")
			curStart = i + 1
			continue
		}
		curLines = append(curLines, line)
	}
	flush()
	return out
}

// ChunkPlain splits non-markdown text purely by size. Used for converted
// documents and other text files that carry no heading structure.
func ChunkPlain(path, content string) []Chunk {
	text := strings.TrimSpace(content)
	if text == "" {
		return nil
	}
	var out []Chunk
	for _, part := range splitOversized(text) {
		out = append(out, Chunk{Path: path, Text: part, Line: 1})
	}
	return out
}

// parseHeading recognizes an ATX heading line ("## Title") and returns its
// level and text.
func parseHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

// updateTrail replaces the trail from the given level down, so "# A / ## B /
// ## C" yields the trail A > C rather than A > B > C.
func updateTrail(trail []string, level int, title string) []string {
	if level-1 < len(trail) {
		trail = trail[:level-1]
	}
	for len(trail) < level-1 {
		trail = append(trail, "")
	}
	return append(trail, title)
}

// splitOversized breaks text longer than the target into paragraph-aligned
// parts, falling back to line boundaries when a single paragraph is itself
// oversized. It never splits mid-line.
func splitOversized(text string) []string {
	if len(text) <= targetChunkChars {
		return []string{text}
	}
	var out []string
	var cur strings.Builder
	appendPart := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, para := range strings.Split(text, "\n\n") {
		if cur.Len() > 0 && cur.Len()+len(para) > targetChunkChars {
			appendPart()
		}
		if len(para) > targetChunkChars {
			for _, line := range strings.Split(para, "\n") {
				if cur.Len() > 0 && cur.Len()+len(line) > targetChunkChars {
					appendPart()
				}
				cur.WriteString(line)
				cur.WriteString("\n")
			}
			continue
		}
		cur.WriteString(para)
		cur.WriteString("\n\n")
	}
	appendPart()
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/vault/ -run TestChunk -v`
Expected: PASS — all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/vault/chunk.go internal/vault/chunk_test.go
git commit -m "feat(vault): heading-aware chunking for retrieval"
```

---

### Task 17: BM25 index with a cached lifecycle

**Files:**
- Create: `internal/vault/index.go`
- Test: `internal/vault/index_test.go`

**Interfaces:**
- Consumes: `Chunk`, `ChunkMarkdown`, `ChunkPlain` (Task 16); `convert.ToMarkdown` (Phase 2)
- Produces: `vault.Scored{Chunk; Score float64}`, `(*Vault).Indexer() *Indexer`, `(*Indexer).Search(workspaceID, query string, limit int) []Scored`, `(*Indexer).Invalidate(workspaceID string)`

**Lifecycle contract (get this right — it is the difference between "negligible" and a stalled designer):**
- One `Indexer` per `Vault`, created lazily and cached for the process's lifetime.
- A search revalidates by walking the vault and comparing each file's `(mtime, size)` against the cached entry. Unchanged files reuse their cached chunks; only changed or new files are re-read.
- **Body extraction for a non-markdown file runs once per file-version**, never per query and never per design turn. A PDF is converted the first time it is seen and then only again if it changes on disk.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/index_test.go`:

```go
package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedVault(t *testing.T) (*Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := v.WriteNote(ws, rel, []byte(body)); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("notes/health.md", "# Health\n\n## Appointments\n\nBooked an orthodontist visit for Tuesday morning.\n")
	write("notes/travel.md", "# Travel\n\nFlights to Lisbon in September. Booked with Wizz.\n")
	write("memory/USER.md", "# User\n\nIlija lives in Skopje and works on self-hosted infrastructure.\n")
	write("notes/ids.md", "# Ids\n\nrun id 7f3a91e2-4c8b-4d2e-9a11-6b0f5c2d8e41 failed\n")
	write("files/expenses.csv", "item,cost\nrent,900\ngroceries,240\n")
	return v, ws
}

// The headline case: literal search cannot find "dentist" in a note that says
// "orthodontist". Ranked retrieval must.
func TestIndexFindsWhatLiteralSearchMisses(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "dentist appointment", 5)
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if !strings.Contains(got[0].Path, "health.md") {
		t.Errorf("top result = %q, want notes/health.md", got[0].Path)
	}
}

func TestIndexSearchesWholeVaultNotJustNotes(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "Skopje infrastructure", 5)
	var found bool
	for _, s := range got {
		if strings.Contains(s.Path, "memory/USER.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("memory/ must be searchable, got %+v", paths(got))
	}
}

func TestIndexSearchesNonMarkdownContent(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "groceries", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "expenses.csv") {
		t.Errorf("csv body must be searchable, got %+v", paths(got))
	}
}

func TestIndexMatchesFilename(t *testing.T) {
	v, ws := seedVault(t)
	// "expenses" appears in the FILENAME only, never in the body.
	got := v.Indexer().Search(ws, "expenses", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "expenses") {
		t.Errorf("a filename match must rank, got %+v", paths(got))
	}
}

func TestIndexCarriesHeadingTrail(t *testing.T) {
	v, ws := seedVault(t)
	got := v.Indexer().Search(ws, "orthodontist", 3)
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if !strings.Contains(got[0].Heading, "Appointments") {
		t.Errorf("Heading = %q, want the section trail", got[0].Heading)
	}
}

func TestIndexDeterministicOrder(t *testing.T) {
	v, ws := seedVault(t)
	first := paths(v.Indexer().Search(ws, "booked", 5))
	for i := 0; i < 5; i++ {
		if got := paths(v.Indexer().Search(ws, "booked", 5)); strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("order changed between runs: %v vs %v", first, got)
		}
	}
}

func TestIndexPicksUpChanges(t *testing.T) {
	v, ws := seedVault(t)
	idx := v.Indexer()
	if got := idx.Search(ws, "kayaking", 5); len(got) != 0 {
		t.Fatalf("unexpected pre-existing match: %+v", paths(got))
	}
	// mtime resolution can be coarse; make the change unambiguous.
	time.Sleep(10 * time.Millisecond)
	if err := v.WriteNote(ws, "notes/new.md", []byte("# New\n\nWent kayaking on the Vardar.\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := idx.Search(ws, "kayaking", 5)
	if len(got) == 0 || !strings.Contains(got[0].Path, "new.md") {
		t.Errorf("a new note must be found without a restart, got %+v", paths(got))
	}
}

func TestIndexSkipsInternalDir(t *testing.T) {
	v, ws := seedVault(t)
	sidecar := filepath.Join(v.Root(ws), InternalDir, "db-export", "x.json")
	os.MkdirAll(filepath.Dir(sidecar), 0o755)
	os.WriteFile(sidecar, []byte(`{"secret":"orthodontist"}`), 0o600)

	for _, s := range v.Indexer().Search(ws, "orthodontist", 10) {
		if strings.Contains(s.Path, InternalDir) {
			t.Errorf("internal sidecars must never be retrievable, got %q", s.Path)
		}
	}
}

func TestIndexEmptyQuery(t *testing.T) {
	v, ws := seedVault(t)
	if got := v.Indexer().Search(ws, "   ", 5); len(got) != 0 {
		t.Errorf("an empty query should return nothing, got %+v", paths(got))
	}
}

func paths(in []Scored) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Path)
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vault/ -run TestIndex -v`
Expected: FAIL — `v.Indexer undefined`.

- [ ] **Step 3: Write the index**

Create `internal/vault/index.go`:

```go
package vault

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/ilijad1/simple-agents/internal/convert"
)

// Scored is a chunk with its relevance score.
type Scored struct {
	Chunk
	Score float64
}

// BM25 parameters. These are the standard defaults and are not worth tuning
// without a relevance benchmark to tune against.
const (
	bm25K1 = 1.2
	bm25B  = 0.75

	// Weights for where a term matched. A term in the filename or a heading is
	// stronger evidence of aboutness than one occurrence in a body paragraph:
	// "the ATC report" should find atc-report.md even if the body never repeats
	// those words.
	pathMatchBoost    = 2.5
	headingMatchBoost = 1.5

	// maxIndexFileBytes bounds what is read into the index.
	maxIndexFileBytes = 4 << 20
)

// Indexer holds per-workspace retrieval state for the process's lifetime.
//
// The index is NOT persisted. At this vault size (hundreds of files, a few
// hundred KB) a rebuild is trivial, and keeping it in memory removes a schema,
// a migration, a corruption mode, and a staleness bug — the reliability
// argument for the whole feature.
//
// Cost control is what makes this safe to call on every design turn: a search
// revalidates by stat-ing files and comparing (mtime, size). Unchanged files
// reuse their cached chunks, so extracting text from a PDF or spreadsheet
// happens ONCE PER FILE VERSION, never per query.
type Indexer struct {
	v  *Vault
	mu sync.Mutex
	ws map[string]*wsIndex
}

type wsIndex struct {
	files map[string]*fileEntry // vault-relative path → cached chunks
	// Corpus statistics, recomputed when the file set changes.
	chunks    []Chunk
	df        map[string]int // term → number of chunks containing it
	avgLen    float64
	corpusGen int64
}

type fileEntry struct {
	modTime int64
	size    int64
	chunks  []Chunk
	terms   []map[string]int // per-chunk term frequencies, aligned with chunks
}

// Indexer returns the vault's retrieval index, creating it on first use.
func (v *Vault) Indexer() *Indexer {
	v.indexOnce.Do(func() {
		v.indexer = &Indexer{v: v, ws: map[string]*wsIndex{}}
	})
	return v.indexer
}

// Invalidate drops a workspace's cached index, forcing a full rebuild on the
// next search. Callers do not normally need this — revalidation is automatic —
// but a bulk import can use it to skip a stat walk.
func (i *Indexer) Invalidate(workspaceID string) {
	i.mu.Lock()
	delete(i.ws, workspaceID)
	i.mu.Unlock()
}

// Search returns the top-scoring chunks for a query, best first.
func (i *Indexer) Search(workspaceID, query string, limit int) []Scored {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}

	i.mu.Lock()
	idx := i.refresh(workspaceID)
	// Snapshot under the lock. `chunks := idx.chunks` would NOT be safe: a slice
	// header shares its backing array, and recompute() re-appends into that same
	// array — so a concurrent refresh (a scheduled run calling search_files while
	// a design turn retrieves) would tear reads out from under the scorer.
	// recompute() also allocates a fresh slice for the same reason.
	n := len(idx.chunks)
	chunks := make([]Chunk, n)
	copy(chunks, idx.chunks)
	tf := make([]map[string]int, 0, n)
	for _, path := range sortedKeys(idx.files) {
		tf = append(tf, idx.files[path].terms...)
	}
	df, avgLen := idx.df, idx.avgLen
	i.mu.Unlock()

	if n == 0 || len(tf) != n {
		return nil
	}

	scored := make([]Scored, 0, n)
	for ci, c := range chunks {
		score := bm25Score(terms, tf[ci], df, avgLen, n)
		score += fieldBoost(terms, c)
		if score > 0 {
			scored = append(scored, Scored{Chunk: c, Score: score})
		}
	}
	// Stable ordering: score desc, then path, then line — so an identical query
	// always returns an identical list, which the tests pin and users rely on.
	sort.SliceStable(scored, func(a, b int) bool {
		if scored[a].Score != scored[b].Score {
			return scored[a].Score > scored[b].Score
		}
		if scored[a].Path != scored[b].Path {
			return scored[a].Path < scored[b].Path
		}
		return scored[a].Line < scored[b].Line
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// refresh revalidates the workspace index against the filesystem. Must be
// called with the mutex held.
func (i *Indexer) refresh(workspaceID string) *wsIndex {
	idx := i.ws[workspaceID]
	if idx == nil {
		idx = &wsIndex{files: map[string]*fileEntry{}}
		i.ws[workspaceID] = idx
	}

	root := i.v.Root(workspaceID)
	if root == "" {
		return idx
	}

	seen := map[string]bool{}
	changed := false

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// .kb holds internal sidecars — never user knowledge, and it would
			// leak DB exports into retrieval results.
			if d.Name() == InternalDir {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := i.v.Rel(workspaceID, path)
		if relErr != nil {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxIndexFileBytes {
			return nil
		}
		seen[rel] = true

		if prev, ok := idx.files[rel]; ok &&
			prev.modTime == info.ModTime().UnixNano() && prev.size == info.Size() {
			return nil // unchanged: reuse cached chunks, no re-read, no re-extract
		}

		entry := i.buildEntry(path, rel, info.ModTime().UnixNano(), info.Size())
		if entry == nil {
			delete(idx.files, rel)
			changed = true
			return nil
		}
		idx.files[rel] = entry
		changed = true
		return nil
	})

	for rel := range idx.files {
		if !seen[rel] {
			delete(idx.files, rel)
			changed = true
		}
	}
	if changed || idx.df == nil {
		idx.recompute()
	}
	return idx
}

// buildEntry reads and chunks one file. Non-markdown files go through convert,
// which is why this runs once per file version and never per query.
func (i *Indexer) buildEntry(abs, rel string, modTime, size int64) *fileEntry {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	var chunks []Chunk
	if strings.EqualFold(filepath.Ext(rel), ".md") {
		chunks = ChunkMarkdown(rel, string(data))
	} else {
		res, convErr := convert.ToMarkdown(data, convert.Options{Filename: rel})
		if convErr != nil {
			// Not convertible (a binary blob, an image): it is still findable by
			// name via the path boost, so index it with an empty body rather
			// than dropping it entirely.
			chunks = []Chunk{{Path: rel, Text: "", Line: 1}}
		} else {
			chunks = ChunkPlain(rel, res.Markdown)
		}
	}
	if len(chunks) == 0 {
		chunks = []Chunk{{Path: rel, Text: "", Line: 1}}
	}
	terms := make([]map[string]int, len(chunks))
	for ci, c := range chunks {
		terms[ci] = termFreq(tokenize(c.Text))
	}
	return &fileEntry{modTime: modTime, size: size, chunks: chunks, terms: terms}
}

// recompute rebuilds corpus-wide statistics from the per-file caches.
func (idx *wsIndex) recompute() {
	// A FRESH slice, never idx.chunks[:0] — a reader may still be scoring the
	// old backing array (see the snapshot comment in Search).
	idx.chunks = make([]Chunk, 0, len(idx.chunks))
	idx.df = map[string]int{}
	total := 0
	for _, path := range sortedKeys(idx.files) {
		entry := idx.files[path]
		for ci, c := range entry.chunks {
			idx.chunks = append(idx.chunks, c)
			total += len(c.Text)
			for term := range entry.terms[ci] {
				idx.df[term]++
			}
		}
	}
	if n := len(idx.chunks); n > 0 {
		idx.avgLen = float64(total) / float64(n)
	}
	idx.corpusGen++
}

// bm25Score is the standard Okapi BM25 score of one chunk for a query.
func bm25Score(queryTerms []string, tf map[string]int, df map[string]int, avgLen float64, n int) float64 {
	if avgLen <= 0 {
		avgLen = 1
	}
	docLen := 0.0
	for _, c := range tf {
		docLen += float64(c)
	}
	var score float64
	for _, term := range queryTerms {
		f := float64(tf[term])
		if f == 0 {
			continue
		}
		idf := math.Log(1 + (float64(n)-float64(df[term])+0.5)/(float64(df[term])+0.5))
		score += idf * (f * (bm25K1 + 1)) / (f + bm25K1*(1-bm25B+bm25B*docLen/avgLen))
	}
	return score
}

// fieldBoost rewards matches in the file path and the heading trail. Without
// it, a query naming a file ("expenses") would score zero against a document
// whose body never repeats its own name.
func fieldBoost(queryTerms []string, c Chunk) float64 {
	pathTerms := termFreq(tokenize(strings.ReplaceAll(c.Path, "/", " ")))
	headTerms := termFreq(tokenize(c.Heading))
	var boost float64
	for _, term := range queryTerms {
		if pathTerms[term] > 0 {
			boost += pathMatchBoost
		}
		if headTerms[term] > 0 {
			boost += headingMatchBoost
		}
	}
	return boost
}

// tokenize lowercases and splits on non-alphanumerics, dropping stopwords and
// single characters. Deliberately NOT a stemmer: a deterministic, explainable
// tokenizer beats a clever one we cannot debug when a search "should" have
// matched.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 || stopwords[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"was": true, "are": true, "you": true, "your": true, "from": true, "has": true,
	"have": true, "not": true, "but": true, "all": true, "can": true, "its": true,
	"about": true, "into": true, "out": true, "our": true, "their": true,
}

func termFreq(terms []string) map[string]int {
	out := make(map[string]int, len(terms))
	for _, t := range terms {
		out[t]++
	}
	return out
}

func sortedKeys(m map[string]*fileEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Add the Indexer fields to Vault**

In `internal/vault/vault.go`, add to the `Vault` struct:

```go
	// indexer is the process-lifetime retrieval index, created on first use.
	indexOnce sync.Once
	indexer   *Indexer
```

Add `"sync"` to that file's imports.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/vault/ -run TestIndex -v`
Expected: PASS — all nine tests.

- [ ] **Step 6: Verify there is no data race**

Add to `internal/vault/index_test.go`:

```go
// The designer retrieves on every turn while a scheduled run can call
// search_files concurrently — same workspace, same Vault, same Indexer.
func TestIndexConcurrentSearchAndWrite(t *testing.T) {
	v, ws := seedVault(t)
	idx := v.Indexer()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); idx.Search(ws, "booked flights", 5) }()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v.WriteNote(ws, fmt.Sprintf("notes/conc%d.md", n), []byte("# C\n\nbooked something\n"))
		}(i)
	}
	wg.Wait()
}
```

Run: `go test ./internal/vault/ -run TestIndexConcurrent -race -count=3 -v`
Expected: PASS with no `DATA RACE` report. Add `"sync"` and `"fmt"` to the test imports.

- [ ] **Step 7: Verify the cost claim**

Add a benchmark to `internal/vault/index_test.go`:

```go
func BenchmarkIndexSearchWarm(b *testing.B) {
	v := New(b.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)
	for i := 0; i < 200; i++ {
		v.WriteNote(ws, filepath.Join("notes", strings.Repeat("n", 3)+string(rune('a'+i%26))+".md"),
			[]byte("# Note\n\nsome content about budgets and travel and health\n"))
	}
	idx := v.Indexer()
	idx.Search(ws, "budgets", 5) // warm
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Search(ws, "budgets", 5)
	}
}
```

Run: `go test ./internal/vault/ -run XXX -bench BenchmarkIndexSearchWarm -benchtime 20x`
Expected: a warm search over 200 notes completes in single-digit milliseconds. If it is materially slower, the revalidation walk is doing work it should be caching — fix that before wiring the designer, which calls this every turn.

- [ ] **Step 8: Commit**

```bash
git add internal/vault/index.go internal/vault/index_test.go internal/vault/vault.go
git commit -m "feat(vault): BM25 chunk index with per-file-version caching"
```

---

### Task 18: Upgrade `search_files` to merged literal + ranked results

**Files:**
- Modify: `internal/coder/hosttools.go` (`searchFiles`, tool description)
- Test: `internal/coder/hosttools_search_test.go` (extend)

**Interfaces:**
- Consumes: `(*Vault).Indexer`, `Scored` (Task 17); existing `(*Vault).NewSearcher`
- Produces: `search_files` returns exact matches first, then ranked chunks; name and signature unchanged

- [ ] **Step 1: Write the failing test**

Append to `internal/coder/hosttools_search_test.go`:

```go
func TestSearchFilesReturnsRankedChunks(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	v.WriteNote(ws, "notes/health.md", []byte("# Health\n\n## Appointments\n\nBooked an orthodontist visit for Tuesday.\n"))
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), "dentist appointment")
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if !strings.Contains(out, "notes/health.md") {
		t.Errorf("ranked retrieval should find the note, got:\n%s", out)
	}
	if !strings.Contains(out, "orthodontist") {
		t.Errorf("the result should carry the passage text, not just a path, got:\n%s", out)
	}
	if !strings.Contains(out, "Appointments") {
		t.Errorf("the heading trail should be shown, got:\n%s", out)
	}
}

// Exact matching must not regress: BM25 is worse than literal search for a UUID
// or an error string, so both run and exact hits come first.
func TestSearchFilesKeepsExactMatching(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)
	const id = "7f3a91e2-4c8b-4d2e-9a11-6b0f5c2d8e41"
	v.WriteNote(ws, "notes/ids.md", []byte("# Ids\n\nrun id "+id+" failed\n"))
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), id)
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if !strings.Contains(out, "notes/ids.md") {
		t.Errorf("an exact identifier must still match, got:\n%s", out)
	}
	if !strings.Contains(out, "Exact matches") {
		t.Errorf("exact hits should be labelled and listed first, got:\n%s", out)
	}
}

func TestSearchFilesNoMatchesIsNonError(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out, err := h.searchFiles(context.Background(), "zzz-nothing-matches-this")
	if err != nil {
		t.Fatalf("no matches must not be an error: %v", err)
	}
	if !strings.Contains(out, "no matches") {
		t.Errorf("unexpected output: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coder/ -run TestSearchFiles -v`
Expected: FAIL — the result carries no passage text or heading.

- [ ] **Step 3: Rewrite searchFiles**

In `internal/coder/hosttools.go`, replace `searchFiles` with:

```go
// maxRankedChunks bounds how many ranked passages search_files returns.
const maxRankedChunks = 10

// searchFiles answers "where in my knowledge base is this?" with two passes,
// deliberately kept both:
//
//   - Exact (ripgrep, fixed-string): unbeatable for a UUID, an error string, or
//     a code identifier, where ranked retrieval would dilute the one right hit.
//   - Ranked (BM25 over heading-aware chunks): finds a note about "dentist" that
//     says "orthodontist", and returns whole passages with their heading trail
//     so one call yields usable context instead of a read_file walk.
//
// Exact hits come first because a caller who typed an exact token wants it.
// No matches remains a NON-error so it never trips the oscillation guard.
func (h *hostToolSet) searchFiles(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if h.vlt == nil {
		return "", fmt.Errorf("search_files unavailable: no vault")
	}

	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var sb strings.Builder
	exactPaths := map[string]bool{}

	if hits, err := h.vlt.NewSearcher().Search(sctx, h.workspaceID, query); err == nil && len(hits) > 0 {
		if len(hits) > maxSearchHits {
			hits = hits[:maxSearchHits]
		}
		sb.WriteString("Exact matches:\n")
		for _, hit := range hits {
			fmt.Fprintf(&sb, "%s:%d: %s\n", hit.Path, hit.Line, hit.Snippet)
			exactPaths[hit.Path] = true
		}
	}

	ranked := h.vlt.Indexer().Search(h.workspaceID, query, maxRankedChunks)
	var wrote int
	for _, r := range ranked {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		if wrote == 0 {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("Related passages:\n")
		}
		wrote++
		location := r.Path
		if r.Heading != "" {
			location += " — " + r.Heading
		}
		fmt.Fprintf(&sb, "\n[%s]\n%s\n", location, strings.TrimSpace(r.Text))
	}

	if sb.Len() == 0 {
		return fmt.Sprintf("(no matches for %q)", query), nil
	}
	return truncate(sb.String()), nil
}
```

Update the `search_files` tool description in `tools()`:

```go
		{Name: "search_files", Description: "Search the user's whole knowledge base (vault) and get back the passages that matter. " +
			"Returns exact text matches as `path:line: snippet`, followed by the most relevant passages with their note path and section heading — " +
			`e.g. search_files with query "dentist appointment" finds a note that says "orthodontist visit". ` +
			"Covers every file type, including converted csv/pdf/docx content, and matches on file names as well as content. " +
			"Use this INSTEAD of read_file-ing your way through folders.",
			Parameters: rawSchema(`{"type":"object","properties":{"query":{"type":"string","description":"what to look for; plain words work better than exact phrases"}},"required":["query"]}`)},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/coder/ -run TestSearchFiles -count=1 -v`
Expected: PASS. Existing `hosttools_search_test.go` tests must also still pass; if one asserted the old `path:line:`-only shape, update it to assert the exact-match section rather than deleting the assertion.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/hosttools.go internal/coder/hosttools_search_test.go
git commit -m "feat(coder): search_files returns ranked passages alongside exact matches"
```

---

### Task 19: Give the designers real KB context

**Files:**
- Modify: `internal/agentdesigner/flow.go` (`loadKBManifest` ~205-224, `kbLister` interface ~199)
- Modify: `internal/skilldesigner/flow.go` (`loadKBManifest` ~971)
- Modify: `internal/vault/index.go` (add `FolderSummary`)
- Create: `internal/vault/kbcontext.go`
- Modify: `internal/prompts/prompts.go` (`<knowledge_base>` block wording)
- Test: `internal/vault/kbcontext_test.go`

**Interfaces:**
- Consumes: `(*Indexer).Search`, `Scored` (Task 17)
- Produces: `(*Vault).FolderSummary(workspaceID string) string`; `vault.BuildKBContext(v *Vault, workspaceID, query string) string`; `agentdesigner.Flow.WithVault(v *vault.Vault) *Flow` and the same on `skilldesigner.Flow`; `DesignSystemParams.KBManifest` now carries the folder summary + retrieved passages

**Placement note:** `BuildKBContext` lives in `internal/vault`, not in `agentdesigner`. Both designers need it, and having `skilldesigner` import `agentdesigner` just to reach a helper would be the wrong dependency edge.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/kbcontext_test.go`:

```go
package vault

import (
	"strings"
	"testing"
)

func seedDesignerVault(t *testing.T) (*Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	v.WriteNote(ws, "notes/health.md", []byte("# Health\n\n## Appointments\n\nOrthodontist visit booked for Tuesday.\n"))
	v.WriteNote(ws, "files/expenses.csv", []byte("item,cost\nrent,900\n"))
	for i := 0; i < 80; i++ {
		v.WriteNote(ws, "notes/bulk/n"+string(rune('a'+i%26))+string(rune('a'+i/26))+".md", []byte("# Filler\n\nnothing relevant\n"))
	}
	return v, ws
}

func TestBuildKBContextRetrievesRelevantPassages(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "remind me about my dentist appointments")

	if !strings.Contains(got, "notes/health.md") {
		t.Errorf("expected the relevant note, got:\n%s", got)
	}
	if !strings.Contains(got, "Orthodontist") {
		t.Errorf("expected passage TEXT, not just a path, got:\n%s", got)
	}
}

func TestBuildKBContextFindsNonMarkdownByName(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "summarize my expenses spreadsheet each month")
	if !strings.Contains(got, "expenses.csv") {
		t.Errorf("a non-markdown file must be reachable, got:\n%s", got)
	}
}

func TestBuildKBContextIsBounded(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "filler")
	if len(got) > maxKBContextBytes {
		t.Errorf("context is %d bytes, over the %d cap", len(got), maxKBContextBytes)
	}
	// 80+ notes must NOT appear as 80 individual paths.
	if strings.Count(got, "notes/bulk/") > 8 {
		t.Errorf("the folder summary should replace an exhaustive path list, got:\n%s", got)
	}
}

func TestBuildKBContextStatesWhenNothingMatched(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "quantum chromodynamics lattice simulation")
	if !strings.Contains(strings.ToLower(got), "no existing notes matched") {
		t.Errorf("an empty retrieval must be stated explicitly so the designer asks instead of inventing a path, got:\n%s", got)
	}
}

func TestBuildKBContextEmptyVault(t *testing.T) {
	v := New(t.TempDir())
	v.EnsureScaffold("empty")
	if got := BuildKBContext(v, "empty", "anything"); got == "" {
		t.Error("an empty vault should still describe itself rather than returning nothing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vault/ -run TestBuildKBContext -v`
Expected: FAIL — `undefined: BuildKBContext`.

- [ ] **Step 3: Add the folder summary to the vault**

Append to `internal/vault/index.go`:

```go
// FolderSummary describes the shape of a workspace's knowledge base: which
// folders exist, how many files each holds, and which kinds. It replaces the
// old exhaustive path list, which capped at 60 files in walk order and rendered
// the first 30 — so in a 153-note vault, 123 notes were invisible and the
// visible 30 were arbitrary. A summary is bounded by folder count rather than
// file count, so it stays honest as the vault grows.
func (v *Vault) FolderSummary(workspaceID string) string {
	root := v.Root(workspaceID)
	if root == "" {
		return ""
	}
	type folderStat struct {
		count int
		exts  map[string]int
	}
	folders := map[string]*folderStat{}

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == InternalDir {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := v.Rel(workspaceID, path)
		if relErr != nil {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			dir = "(root)"
		}
		stat, ok := folders[dir]
		if !ok {
			// NOTE: not named `fs` — that would shadow the io/fs package this
			// same function uses for fs.DirEntry.
			stat = &folderStat{exts: map[string]int{}}
			folders[dir] = stat
		}
		stat.count++
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
		if ext == "" {
			ext = "no extension"
		}
		stat.exts[ext]++
		return nil
	})

	if len(folders) == 0 {
		return "The knowledge base is empty."
	}
	var sb strings.Builder
	for _, dir := range sortedFolderNames(folders) {
		f := folders[dir]
		kinds := make([]string, 0, len(f.exts))
		for _, ext := range sortedExtNames(f.exts) {
			kinds = append(kinds, fmt.Sprintf("%s×%d", ext, f.exts[ext]))
		}
		fmt.Fprintf(&sb, "- %s/ — %d files (%s)\n", dir, f.count, strings.Join(kinds, ", "))
	}
	return sb.String()
}

func sortedFolderNames[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedExtNames(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Add `"fmt"` to that file's imports.

- [ ] **Step 4: Write BuildKBContext**

Create `internal/vault/kbcontext.go`:

```go
package vault

import (
	"fmt"
	"strings"
)

// maxKBContextBytes caps the knowledge-base block injected into a design turn.
const maxKBContextBytes = 6 * 1024

// maxKBContextChunks is how many retrieved passages are shown.
const maxKBContextChunks = 5

// BuildKBContext assembles the <knowledge_base> block for a design turn: a
// folder summary describing the vault's shape, plus the passages most relevant
// to what the user is asking for.
//
// This replaces an exhaustive path list that capped at 60 files in walk order
// and rendered 30 of them — arbitrary, truncated, and content-free. Retrieval
// runs over the WHOLE vault and every file type, matched on filename and path
// as well as body text, so a request naming "expenses.csv" resolves even though
// the designer is text-only and cannot call a search tool itself.
//
// When nothing matches, the block says so in as many words. A designer that
// invents a plausible note path is worse than one that asks.
func BuildKBContext(v *Vault, workspaceID, query string) string {
	if v == nil || workspaceID == "" {
		return ""
	}
	var sb strings.Builder

	summary := v.FolderSummary(workspaceID)
	if summary == "" {
		summary = "The knowledge base is empty."
	}
	sb.WriteString("Knowledge base structure:\n")
	sb.WriteString(summary)

	hits := v.Indexer().Search(workspaceID, query, maxKBContextChunks)
	var shown int
	for _, h := range hits {
		if strings.TrimSpace(h.Text) == "" {
			continue
		}
		if shown == 0 {
			sb.WriteString("\nExisting notes relevant to this request:\n")
		}
		location := h.Path
		if h.Heading != "" {
			location += " — " + h.Heading
		}
		entry := fmt.Sprintf("\n[%s]\n%s\n", location, strings.TrimSpace(h.Text))
		if sb.Len()+len(entry) > maxKBContextBytes {
			break
		}
		sb.WriteString(entry)
		shown++
	}
	if shown == 0 {
		sb.WriteString("\nNo existing notes matched this request. Do not guess a file path — if the user refers to a document you cannot see here, ask them where it is or whether they still need to add it.\n")
	}

	out := sb.String()
	if len(out) > maxKBContextBytes {
		out = out[:maxKBContextBytes] + "\n…(truncated)\n"
	}
	return out
}
```

- [ ] **Step 5: Wire it into both designers**

In `internal/agentdesigner/flow.go`, replace `loadKBManifest`'s body:

```go
// loadKBManifest returns the knowledge-base block for a design turn: the
// vault's folder structure plus the passages relevant to the conversation so
// far. It is recomputed every turn because the relevant passages depend on what
// the user has said, not just on what exists.
func (f *Flow) loadKBManifest(workspaceID string) string {
	if f.vlt == nil {
		return ""
	}
	return vault.BuildKBContext(f.vlt, workspaceID, f.retrievalQuery(workspaceID))
}

// retrievalQuery is what the designer's KB retrieval scores against: the user's
// own words from this session, which is a far better query than any summary we
// could synthesize.
func (f *Flow) retrievalQuery(workspaceID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess := f.sessions[workspaceID]
	if sess == nil {
		return ""
	}
	var parts []string
	for _, m := range sess.Messages {
		if m.Role == "user" {
			parts = append(parts, m.Content)
		}
	}
	// Recent turns describe the current need most precisely.
	if len(parts) > 4 {
		parts = parts[len(parts)-4:]
	}
	return strings.Join(parts, " ")
}
```

Add a `vlt *vault.Vault` field to `Flow` and a `WithVault(v *vault.Vault) *Flow` setter, wired in `cmd/simple-agents/main.go` where the flow is constructed. Keep the existing `WithKBLister`/`kb` field only if another caller uses it; otherwise delete it and its `NotePaths` interface, since `NotePaths` no longer has a consumer.

Match the field names to the session struct's actual message field (`Messages`, `History`, or similar) — read `flow.go` before editing rather than assuming.

Apply the identical change in `internal/skilldesigner/flow.go:971`.

- [ ] **Step 6: Update the prompt wording**

In `internal/prompts/prompts.go`, in the `<knowledge_base>` block (around line 928 and again at 2131), change the framing from "here are the user's note paths" to:

```
The block below describes the user's knowledge base: its folder structure, and any existing notes relevant to this request (with their content). Use these real notes when designing — reference the actual paths shown.

If the block says no notes matched, the user's knowledge base has nothing on this topic. Do NOT invent a file path. Ask the user where the information lives, or design the agent to create the note itself.
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/agentdesigner/ ./internal/skilldesigner/ ./internal/vault/ -count=1 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 8: Run the full suite and verify end to end**

```bash
go test ./... -count=1 -timeout 120s 2>&1 | grep -v "^ok" | head
CGO_ENABLED=0 go build ./... && make ui && make deploy && sleep 3
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/
```
Expected: no test failures; `200`.

Then in the browser: start an agent design conversation mentioning a topic you have notes on, and confirm the designer references your real notes by path.

- [ ] **Step 9: Commit — Phase 3 complete and mergeable**

```bash
git add internal/agentdesigner/ internal/skilldesigner/ internal/vault/ internal/prompts/prompts.go cmd/simple-agents/main.go
git commit -m "feat(designer): retrieve relevant KB passages instead of an arbitrary path list"
```

---

## Documentation

- [ ] **Update CLAUDE.md**

After all three phases merge, update these sections:

- **Key packages table**: add `internal/convert` and `internal/websearch` rows; extend `internal/vault` to mention `Indexer`, `ImportFile`, and `Bridge`.
- **API coder engine**: `web_fetch`/`web_search` are no longer exec-gated; `save_to_kb` is a new always-on tool; `search_files` returns ranked passages.
- **Per-user knowledge base (vault)**: add `files/` to the layout diagram.
- **Web UI routes**: add `POST /api/v1/kb/upload`.
- **Known gaps**: remove the line about the API/CLI network capability split if it is now closed; add that OCR is unavailable and that pure-Go PDF extraction is weaker than `pdftotext`.

```bash
git add CLAUDE.md
git commit -m "docs: record the convert, websearch, and retrieval subsystems"
```
