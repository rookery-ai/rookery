# Connector write tools that small models can drive, and a Deel connector

Date: 2026-10-09
Status: approved (owner delegated all gates: "do this without my intervention")

## Problem

On the API engine, small models such as DeepSeek Flash fail at Google Docs edits.
They either report an edit that did not happen or change the wrong place. Reading the code
shows three causes, and none of them is the model's fault:

1. **The model never sees the document.** `docs_get_document` returns the raw
   `documents.get` JSON (`response_extract: "$"`). That JSON carries every run's
   `textStyle`, `paragraphStyle` and list metadata, at roughly 500–800 bytes per
   paragraph. Both coder kinds cap a tool result at 8 KiB (`coder.maxToolResult`, and
   `maxBridgeResult` for the bridge), so a realistic document is cut off after its first
   dozen paragraphs. Every index the model then supplies for a later position is a guess.
2. **Every write tool takes character indexes.** `docs_insert_text`, `docs_append_text`
   (`end_index` is required), `docs_delete_range`, `docs_set_heading_style`,
   `docs_set_text_style` (which also requires a hand-written `fields` mask) and
   `docs_create_bullets` all require offsets measured in **UTF-16 code units**. A model
   cannot compute those reliably even when it can see the document.
3. **Success is indistinguishable from failure.** A `batchUpdate` replies with `[{}]`
   for most requests. `replaceAllText` answers **HTTP 200 with zero `occurrencesChanged`**
   when nothing matched, and the zero is omitted entirely because proto3 JSON omits zeros.
   A small model reads both as "done". `sheets_find_replace` and
   `slides_replace_all_text` have the same silent-zero shape.

The same review found smaller defects in the rest of the Google and Microsoft write surface:

- `msgraphMessage` turns `to: "a@x.com, b@y.com"` into ONE recipient whose address is
  the whole string. Graph rejects that, or misdelivers it.
- Neither Gmail nor Outlook can CC or BCC anyone.
- Gmail writes a raw UTF-8 `Subject:` header. Macedonian subjects are the owner's common
  case, and RFC 5322 requires an RFC 2047 encoded-word.
- `excel_update_range` requires an address whose shape matches `values` exactly. The
  model has to do that arithmetic, and Excel rejects any mismatch.
- OneNote can create a page but cannot add to one.
- `sheets_update_values` and `sheets_append_values` do not echo what they wrote.

## Goals

- A small model edits a Google Doc by naming **text it can see**: a sentence, a heading
  or a phrase. It never names an index.
- Every write either **proves itself** (the result states what changed and where, with
  a snippet re-read from the document) or **fails with an actionable sentence** that
  names the next tool to call.
- An edit computed against a stale read is refused rather than landing somewhere else.
- No existing action is renamed or removed. Built agents reference these names in
  AGENT.md and in `agent_connections`.

## Non-goals

- **Live verification against Google.** Every Google connection on the development host
  is `NEEDS_REAUTH`. Verification is therefore fixture-based, using realistic
  `documents.get` bodies, and the owner tests live after deploy. This is recorded in the
  PR rather than hidden.
- Slides positional editing (`slides_insert_text` needs an object id, but ids are listable).
- Nested lists, tables written from markdown, and images through markdown.

## Design

### 1. A `handler:` hook on `Action` (`internal/connectors/handlers.go`)

`Execute` sends exactly one request, and every anchor-based edit needs at least
read → write → re-read. Add an optional `handler: <name>` field to `Action`.
When it is set, `Execute` runs every existing gate unchanged — arg validation, the build
guard, the parker, the scope pre-check and the token fetch — and then hands control to a
registered Go function instead of rendering the request template:

```go
type handlerFunc func(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error)
```

`handlerCall.do(method, url, body)` is the existing request loop, extracted into one
function so a handler's calls carry identical auth, static headers, retry and error
mapping (`sendOnce`). This brings three properties for free:

- Both coder kinds converge, because the bridge and the API engine already go through `Execute`.
- The build guard still blocks every mutating handler action at build time.
- `public_write` parking still happens before any network call.

The `request:` block stays in the YAML and documents the primary endpoint, so the
catalog-wide URL-shape tests keep checking it. A handler action whose name has no
registered function fails `LoadBundled` validation in a test
(`TestHandlerActionsAreRegistered`). It does not fail at runtime.

### 2. Google Docs: read as paragraphs, write by anchor

`internal/connectors/gdocs.go` parses `documents.get` into a flat paragraph list,
covering body paragraphs and paragraphs inside table cells. Each paragraph records:
its UTF-16 `[start,end)`, its named style (`HEADING_2`…), whether it is a list item,
whether it sits inside a table, and its text. Each character also maps to its UTF-16
index, so a match found in Go strings converts exactly to Docs offsets. Non-text
elements (inline images, page breaks) count as one U+FFFC unit, which is what Docs counts.

**Matching** (`findMatches`) tries exact substring matching first. If nothing matches,
it falls back to a normalised match: case-folded, whitespace runs collapsed, and smart
quotes and dashes folded to ASCII. The normalised match is mapped back to original
offsets. The results drive these behaviours:

- **No match:** the error names the three paragraphs that share the most words with the
  anchor ("did you mean …"), so the model's next call can succeed.
- **Several matches without `occurrence`:** the error lists each match with its
  paragraph number and surrounding text, and asks for `occurrence` or a longer anchor.
  This is the fix for "modified the wrong location". A model that picked an ambiguous
  anchor used to get the first hit silently.

Actions follow. The names and old parameters are kept, and new parameters are optional:

| Action | Change |
|---|---|
| `docs_get_document` | **handler** — returns `{title, revision_id, url, paragraphs:[{n,start,end,style,list,table,text}], next_from_paragraph}`. Optional `from_paragraph`, `find` (keep only paragraphs containing it) and `headings_only`. Paragraph text is clipped at 300 chars and the page fills a ~6 KiB budget, so the result never hits the cap blind. |
| `docs_insert_text` | **handler** — `anchor_text` + `position` (`after_paragraph` default, `before_paragraph`, `after_text`, `before_text`), `occurrence`, `format`. The legacy `index` still works when no anchor is given. |
| `docs_append_text` | **handler** — `end_index` is now optional and ignored; the end is computed. |
| `docs_add_to_section` *(new)* | **handler** — `heading` + `text`, `position` `end` (default) / `start`. |
| `docs_replace_section` *(new)* | **handler** — replaces everything under a heading up to the next heading of the same or higher level. |
| `docs_replace_text` | **handler** — a targeted replace of ONE match (exact first, then normalised). Several matches need `occurrence` or `replace_all: true`. Zero matches is an error. |
| `docs_delete_text` *(new)* | **handler** — deletes one match, or its whole paragraph with `whole_paragraph`. |
| `docs_format_text` *(new)* | **handler** — bold/italic/underline/link/heading style applied to matched text. The `fields` mask is computed, never asked for. |
| index-based actions | Unchanged. Descriptions now say "advanced — prefer the anchor tools; indexes come from docs_get_document". |

**Writing text.** With `format: "markdown"` (the default, because models write markdown),
lines that start with `#`–`######` become headings, `- `/`* ` lines become bullets,
`1. ` lines become a numbered list, and `**x**` becomes bold. Everything else is literal.
`format: "plain"` inserts text verbatim. Inserted paragraphs that markdown did not style
are set to `NORMAL_TEXT` and lose any inherited bullet. This fixes the second silent
failure: text inserted after a heading inherits the heading's style.

**Write safety.** Each write sends `writeControl.requiredRevisionId` set to the revision
it just read. If the document changed between our read and our write, Google refuses the
edit (400). The handler re-reads and retries once, and then errors. An edit is never
applied against stale offsets. A caller may also pass the `revision_id` it saw in
`docs_get_document` to pin the edit to that view.

**Verification.** After every write the handler re-reads the document and confirms the
written text is present at the expected position (± 2 code units). The result is
`{"status":"applied","verified":true,"action":…,"paragraph":n,"before":"…","after":"…"}`.
`before`/`after` are snippets of the neighbouring text, so the model and the owner can see
where the edit landed. If the re-read cannot find the text, the result is an error that
says the API accepted the edit but the text is not where expected. It is never a success.

### 3. Silent zero is failure, everywhere

The new `Action.expect_change` field holds a dotted path that may include numeric
segments (for example `$.replies.0.replaceAllText.occurrencesChanged`). If the value at
that path is absent or `0`, `Execute` returns a `KindOther` error: "nothing changed: no
text matched …". `extractOK` learns numeric segments. Applied to `slides_replace_all_text`
and `sheets_find_replace`.

### 4. Other write fixes

- **Sheets:** `sheets_update_values` and `sheets_append_values` add
  `includeValuesInResponse=true`, so the result echoes the written cells.
- **Excel:** `excel_write_cells` *(new, handler)* takes `start_cell` + `values` and
  computes the A1 range from the shape (`B3` + 2×3 → `B3:D4`). The column math is pure
  and tested past `Z` (`AA`, `AZ`, `BA`).
- **Gmail:** `rfc822` gains `cc` and `bcc`, plus an RFC 2047 encoded `Subject`
  (`mime.QEncoding`) when the subject is not ASCII.
- **Outlook:** recipients are split on `,`/`;` into separate `emailAddress` objects, and
  `cc`/`bcc` are accepted on draft and send.
- **OneNote:** `onenote_append_to_page` *(new)* sends
  `PATCH /me/onenote/pages/{id}/content` with `[{"target":"body","action":"append","content":…}]`.
  It reuses the page builder's escaping rule: text without markup is wrapped in `<p>`.

### 5. Deel connector (separate PR)

Deel (`api.letsdeel.com/rest`, header-versioned with `X-Version: 2026-01-01`).
Authentication is an **organization API token** sent as `Authorization: Bearer`, using
`auth.kind: api_key`. OAuth2 exists, but it needs an app registered in Deel's App Store,
which is overkill for a single owner. Category is **Finance**. `unverified: true`,
because no Deel credential was available.

Read actions extract `$.data`:

- `deel_list_contracts` (`search`, `statuses`, `limit`, `after_cursor` → `response_cursor: $.page.cursor`)
- `deel_get_contract`
- `deel_list_people` (`search`, `limit`, `offset`)
- `deel_get_person`
- `deel_list_timesheets` (`contract_id`, `statuses`, `date_from`, `date_to`, `limit`, `offset`)
- `deel_list_invoice_adjustments` (same filters)
- `deel_list_legal_entities`
- `deel_list_countries`
- `deel_list_currencies`

Write actions, whose bodies are wrapped in `{"data":{…}}`:

- `deel_create_timesheet` (`contract_id`, `quantity`, `description`, `date_submitted`) — mutating.
- `deel_create_invoice_adjustment` (`contract_id`, `type` enum, `amount`, `description`,
  `date_submitted`) — mutating **and `public_write`**. It changes what a real person is
  paid, so it belongs behind the approval gate, following the precedent recorded in `wise.yaml`.
- `deel_review_timesheet` (`timesheet_id`, `status` approved|declined, `reason`) —
  mutating and `public_write`, for the same reason.

The provider ships with the full new-provider checklist: logo vendoring,
`check-docs-sync.py` DISPLAY_NAMES, the provider counts in README.md, CLAUDE.md and the
website, and a rookery-web docs PR.

## Testing

- `gdocs_test.go`: parsing a realistic fixture that includes a heading, a list, a table
  and emoji, which proves UTF-16 offsets. Also exact, normalised, ambiguous and missing
  matches, section bounds, and the markdown → requests output.
- `handlers_test.go`: an `httptest` Docs fake that applies `insertText`,
  `deleteContentRange` and `replaceAllText` to an in-memory paragraph model and enforces
  `requiredRevisionId`. Each handler is checked end to end: read, write, verify. A
  revision mismatch must refuse, and a fake that "accepts but drops" the write must
  produce a verification error.
- `expect_change` tests (zero, absent, positive), plus Graph recipient splitting, Gmail
  CC and encoded subject, Excel range arithmetic, the OneNote append body, and
  `TestHandlerActionsAreRegistered`.
- Deel: the catalog-wide hygiene, URL-shape, category, public-write and connect-input
  tests cover the YAML. One `httptest` round trip checks the `{"data":…}` wrapping and the
  `X-Version` header.
