# Tool reliability: web, conversion, and knowledge-base retrieval

**Date:** 2026-07-22
**Status:** Approved
**Scope:** One spec, three implementation phases

## Problem

Three of the platform's most-used tool surfaces are structurally unreliable, each for a
different reason.

**Web.** `web_search` is a single keyless DuckDuckGo HTML scrape — one layout change or
JS-challenge interstitial and it returns nothing, with no second engine to fall back on.
`web_fetch` reduces HTML with four regexes, so a page arrives as one undifferentiated
whitespace-run of nav, cookie banner, body, and footer, and a PDF or DOCX URL returns a
dead end (`[not text; use run_script or bash]`). Both tools are gated behind
`includeExecTools`, so **chat has no web access at all**.

**Conversion.** There is no conversion code in the platform. Six core skills (pdf, docx,
xlsx, pptx, csv, image-ocr) *teach* a model to shell out to `pandoc`, `pdftotext`,
`pdfplumber`, or `markitdown`. On this host only `pdftotext` exists — pandoc, pdfplumber,
and markitdown are all absent, so most of that guidance fails at run time. There is also
no file upload anywhere in the application: a user cannot put a PDF into their knowledge
base at all.

**Retrieval.** `search_files` is literal fixed-string ripgrep with no ranking: a query for
"dentist" cannot find a note that says "orthodontist", and results come back as
`path:line: snippet` fragments, so the model must then `read_file` its way through whole
notes anyway. The agent designer is text-only and cannot call any tool, so it receives
`vault.NotePaths` — which skips `memory/`, `agents/`, `skills/`, and `chats/`, keeps only
`.md` files, stops at the **first 60 encountered in walk order**, and renders the first 30.
In a 153-note workspace, 123 notes are invisible to the designer and the visible 30 are
arbitrary. Non-markdown files are never visible.

The premise that "the whole knowledge base is pasted as context" was checked and is false:
the largest workspace's `memory/` totals 3.5 KB, and the broader vault is already
retrieved on demand. The real defect is the opposite — the designer sees an arbitrary,
truncated *list of filenames* and no content.

## Goals

- `web_fetch` and `web_search` succeed on the pages and queries a browser would, and are
  available in chat.
- Any common document format converts to markdown deterministically, with no host
  toolchain required, callable both as an LLM tool and directly from application code.
- Knowledge-base search returns ranked, usable context in one call, over the whole vault
  and every file type, and never regresses on exact-match queries.

## Decisions taken during implementation (2026-07-22)

**Ingest policy: convert every format to markdown.** The alternative considered was
storing already-textual formats (csv/json/xml) as-is and extracting only the one-way
formats (pdf/docx/pptx) — the retrieval index extracts and caches text per file-version
anyway, so it does not need a persisted note. Convert-everything was chosen for a single
uniform rule, and so that every ingested file appears as readable content in the knowledge
base. The cost is accepted: a converted table is a *rendering* of the data, not the data —
which is why the original bytes are always preserved alongside the note, and why the row
cap is a high safety valve that announces itself rather than a routine truncation.

**Write-back: CSV and markdown only.** Agents read every supported format but generate
only CSV and markdown, which any spreadsheet or editor opens. Generating binary OOXML or
PDF is an explicit non-goal: pure-Go *writing* of those formats is substantially harder
than reading them, and nothing in this design requires it.

## Non-goals

- **OCR.** Needs `tesseract`; not pure-Go. Images convert to a stub note that records what
  the file is and states that no text was extracted.
- **Embeddings / semantic search.** A 300 KB vault does not need vector search, and making
  the most-used KB tool depend on an external API being reachable contradicts the goal.
  `vault.Searcher` remains an interface so this stays a later drop-in.
- **Headless browser.** JS-rendered pages are out of scope.
- **Unrelated refactoring** of the coder, designer, or gateway layers.

## Architecture

The organizing principle: **every tool is a thin adapter over a plain Go package that the
application can also call directly.** Conversion is needed by an LLM tool, a web fetch, an
HTTP upload handler, and a chat adapter — so conversion cannot live inside the tool layer.

```
internal/convert/          NEW — bytes + filename/MIME → markdown. No vault, no LLM, no HTTP.
  detect.go                magic-byte sniffing first, extension second
  ooxml.go                 docx/pptx/xlsx via stdlib archive/zip + encoding/xml
  pdf.go                   pure-Go extractor; prefers `pdftotext -layout` when on PATH
  html.go                  golang.org/x/net/html walker → markdown
  tabular.go               csv/tsv → markdown tables

internal/websearch/        NEW — query → []Result. Provider cascade + optional keyed provider.

internal/vault/index.go    NEW file in existing package — BM25 chunk retrieval behind the
                           existing vault.Searcher interface.
```

Consumers:

| Consumer | Reaches it via |
|---|---|
| API-engine coder | `hostToolSet`: new `save_to_kb`; upgraded `search_files`, `web_search`, `web_fetch` |
| CLI coder | Loopback bridge → `simple-agents kb convert` / `simple-agents kb search` |
| Web UI upload | `POST /api/v1/kb/upload` (multipart) → `convert` → `vault.WriteNote` |
| Chat attachments | Adapter downloads the file → same `convert` + save path |
| `web_fetch` | Calls `convert` on the response body instead of regex-stripping it |
| Agent/skill designer | Calls the BM25 index directly — stays text-only, no tool loop |

### Coder-kind reach

Host tools are API-engine-only; a CLI coder reaches host capability through the loopback
bridge (the proven `simple-agents connector exec` pattern). **Conversion and KB search are
bridged; web tools are not.** A CLI coder already has native `WebFetch`/`WebSearch` that
are as good as or better than ours, so bridging them would duplicate capability; it has no
reliable conversion or ranked KB search at all, so those are the real gaps.

### Dependencies

No new heavy modules. OOXML formats are zip archives of XML — `archive/zip` plus
`encoding/xml` handle them, consistent with how the connector layer hand-rolled REST
instead of pulling vendor SDKs. `golang.org/x/net/html` is already in the dependency graph
(indirect, via echo), so a real HTML parser costs a promotion to direct rather than a new
dependency. `goldmark` is already present and is used for heading-aware markdown chunking.
The only genuinely new module is one small pure-Go PDF text extractor.

---

## Phase 1 — Web tools

### `internal/websearch`

A `Provider` interface with providers tried in order:

1. **Keyed provider**, when the workspace has a search key stored — `SEARCH_KEY_BRAVE` or
   `SEARCH_KEY_TAVILY`, resolved through the same `secretsLookup` closure the API coder
   uses for its provider key. Structured JSON; no scraping.
2. **Keyless cascade**: DuckDuckGo html → DuckDuckGo lite → Mojeek → Bing html. Each
   engine parses independently. A transient error *or* zero parsed results falls through
   to the next engine. Results are deduplicated on normalized URL.

The existing reliability contract is preserved and extended:

- Transient failures (429, 5xx, network, timeout) are retried **inside** a provider with
  backoff and never surface as an `error:` result.
- All engines exhausted with zero results returns `"(no search results)"` as a
  **non-error**, so it cannot trip the tool loop's oscillation guard.
- Only a genuine hard failure returns an error.

### `web_fetch`

- Browser-like `User-Agent` (the one `web_search` already uses successfully), gzip, and
  redirect following.
- Content type determined by **magic bytes first, response header second** — servers
  mislabel bodies.
- HTML is routed through `convert` → markdown with boilerplate reduction: prefer `<main>`
  or `<article>` when present; drop `nav`, `header`, `footer`, `aside`, `script`, `style`.
  Links, headings, lists, and tables survive as markdown.
- A PDF, DOCX, or XLSX URL now returns readable text instead of the current dead end.
- A per-toolset memo so an identical URL fetched twice within one tool loop costs one
  request. Scoped to the toolset (one run), not a global cache — bounded, no invalidation
  problem.

### Availability

Both tools move out from behind `includeExecTools`, making them available in chat on the
API engine. They are read-only and cannot carry secrets, which is why the exec gate never
applied to them for the right reason. CLI chat adds `WebFetch`/`WebSearch` to its
allowed-tools list — its own natives, not ours.

### SSRF containment

Chat currently has no network access whatsoever. Granting it `web_fetch` means it could
reach `127.0.0.1`, where the **connector bridge listens holding per-run bearer tokens**,
and cloud metadata endpoints. Therefore `web_fetch` denies loopback, RFC1918, link-local,
unique-local, and cloud metadata addresses. The check is applied **after DNS resolution**
(so a hostname resolving to a private address is caught) and **re-applied on every
redirect hop**.

---

## Phase 2 — Conversion

### API

```go
convert.ToMarkdown(data []byte, opt Options) (Result, error)
// Result{ Markdown, Title, Kind, Extractor, Warnings }
```

A pure function: bytes in, markdown out. No vault, no network, no LLM. That purity is what
makes it testable against committed golden fixtures and identical on every host.

Supported: PDF, DOCX, PPTX, XLSX, CSV/TSV, HTML, plain text and markdown (pass-through
with normalization), JSON. Images produce a stub note (see non-goals). An unsupported
format returns an error naming the format — never a silent empty result.

`Extractor` records which path produced the output (`pdftotext` vs `pure-go`), so results
are explainable rather than mysterious.

### Saving

`save_to_kb`, the UI upload, and chat attachments all share one save path:

- The note carries YAML frontmatter recording `source` (URL or filename), `converted_at`,
  `extractor`, original size, and any warnings. Conversion is lossy; every note explains
  how it was produced.
- **Original bytes are preserved** under `files/` and linked from the note, up to a size
  cap. A conversion that drops a table is not a dead end — a later agent can re-extract
  from the original.
- An empty note is never written.

### Tool surface

`save_to_kb` accepts a source (a vault path or a URL), an optional destination folder, and
an optional title. It returns the created note's path.

### Application surfaces

- **Web UI**: a drop/upload target on the KB page. New multipart endpoint
  `POST /api/v1/kb/upload`, plus SPA work. Uploaded paths go through `vault.Resolve`;
  size caps and a decompression cap on OOXML zips apply.
- **Chat attachments**: Telegram (`getFile`) and web chat are in scope for this spec.
  Discord and Slack deliver attachments as URLs and use the same seam; they are added if
  they prove to be a few lines each, and deferred otherwise.

---

## Phase 3 — Knowledge-base retrieval

### The index

`internal/vault/index.go` — in memory, per workspace, built lazily and revalidated by an
mtime scan. At this vault size (a few hundred files) that scan is negligible, and keeping
the index unpersisted eliminates a schema, a migration, a corruption mode, and a staleness
bug. That is the reliability argument.

- **Chunking**: markdown split on heading boundaries via goldmark, each chunk carrying its
  heading trail. Non-markdown files are indexed by name and path always, and by body
  wherever `convert` can extract one — so a CSV or a PDF report is searchable by content.
  Phase 2 pays for itself here.
- **Scoring**: BM25 over chunk bodies, boosted by filename/path token matches and heading
  matches, with a stable tiebreak so an identical query always returns an identical order.
- **Returns whole chunks** — path, heading trail, and text — not line fragments. One call
  yields usable context instead of a follow-up `read_file` walk.

Concrete limits: chunks target roughly 1500 characters, split only at heading boundaries so
a chunk is never cut mid-sentence. `search_files` returns the top 10 chunks, capped by the
existing `maxToolResult` (8 KiB). The designer injects the top 5 chunks, capped at 6 KiB.
`web_search` keeps its existing 6-result cap.

### Literal search is kept, not replaced

BM25 is worse than exact matching for a UUID, an error string, or a code identifier. So
`search_files` runs both and merges: exact ripgrep hits first, BM25 chunks after. The tool
is therefore never worse than today. The tool **name** stays `search_files` — prompts and
models are already primed on it — while its description and behaviour are upgraded.

### The designer

Every design turn retrieves top-K chunks against the conversation so far and injects them
as the `<knowledge_base>` block. The 30-arbitrary-paths list is replaced by a **folder
summary**: folder → file count → kinds present. Retrieval covers the **whole vault and
every file type**, matched on filename and path as well as body text, so a request naming
`expenses.csv` resolves.

When retrieval finds nothing, the block says so explicitly and the prompt instructs the
designer to **ask the user rather than invent a path**. The identical change applies to
`skilldesigner`, which carries the same `KBManifest` field.

The designer remains text-only (`WithNoTools`): it calls the index directly rather than
becoming a tool loop, so its latency and turn behaviour are unchanged.

Chat is structurally unchanged — identity-only context, retrieval on demand — it simply
gets a better tool.

---

## Error handling

Uniform across all three phases:

- No tool ever returns an empty string; an empty result is an explicit, non-error notice.
- Transient failures are retried internally and never surface as `error:`, so they cannot
  trip the oscillation guard.
- A definitive failure returns an error naming what failed and what to do instead.
- Conversion errors name the format; they never yield a blank note.

## Testing

- **convert**: one small committed fixture per format, table-driven, hermetic — no network
  and no host tools. The `pdftotext` path is tested both with the binary stubbed and with
  it absent, so both branches are covered on any CI host.
- **websearch**: an `httptest` server per engine covering a challenge page, zero results,
  429-then-200, and all-engines-fail — the last asserting the non-error empty result.
- **index**: a fixture vault asserting that a query literal search *misses* now hits, that
  a UUID still matches exactly through the literal path, and that ranking is deterministic
  across runs.
- **SSRF**: table-driven denial of loopback, RFC1918, link-local, and metadata addresses,
  including a hostname that resolves to a private address and a redirect into one.

## Security

- `web_fetch` cannot carry secrets (unchanged) and cannot reach private address space (new).
- Uploads: size cap, OOXML decompression cap, magic-byte sniffing, paths through
  `vault.Resolve`, no execution of uploaded content.
- Search keys are stored as ordinary encrypted secrets, resolved via the existing lookup.

## Phasing

Each phase is independently shippable and independently valuable:

1. **Web** — cascade, better fetch, chat availability, SSRF containment.
2. **Conversion** — `internal/convert`, `save_to_kb`, bridge subcommand, UI upload, chat
   attachments; `web_fetch` switches to it.
3. **Retrieval** — BM25 index, upgraded `search_files`, bridge subcommand, designer
   context.

Ordering constraints:

- Phase 1's HTML→markdown step needs `internal/convert/html.go`. That one file (plus
  `detect.go`) lands **with phase 1**; phase 2 adds the remaining format converters to the
  same package. Phase 1 is therefore still shippable on its own.
- Phase 3's non-markdown body indexing needs phase 2's converters, so phase 2 lands first.
  Phase 3's markdown chunking and ranking do not depend on phase 2.
