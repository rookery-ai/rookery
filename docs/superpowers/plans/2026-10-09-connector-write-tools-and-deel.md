# Plan: connector write tools + Deel

Spec: `docs/superpowers/specs/2026-10-09-connector-write-tools-and-deel-design.md`.
Execution: inline, by the implementer, with TDD for each Go unit. The owner delegated
the choice of execution method.

## PR 1 — `feat(connectors): anchor-based Google Docs edits that verify themselves`

Branch `feat/connectors-anchored-writes` off `main`.

1. **`extractOK` numeric segments + `expect_change`.** Test first:
   `TestExtractNumericSegments` and `TestExpectChangeZeroIsFailure` (absent, `0`, `3`).
   Add the `ExpectChange string` field to `Action`, check it in `Execute` after
   extraction, and apply it to `slides_replace_all_text` and `sheets_find_replace`.
2. **Extract the request loop.** Refactor the attempt loop in `Execute` into
   `(*sender).do(method, url, body, contentType) ([]byte, int, error)` with no behaviour
   change. Run the existing suite to prove it.
3. **Handler hook.** Add `Action.Handler`, the `handlers` registry,
   `handlerCall{do, getJSON, postJSON}`, and the dispatch in `Execute` after the token
   fetch. Add `TestHandlerActionsAreRegistered` and a test proving the build guard and
   parker still precede a handler.
4. **`gdocs.go` model.** `parseDoc(raw) (*gdoc, error)` builds paragraphs with UTF-16
   offsets, then add `findMatches`, `sectionBounds` and `suggest`. Tests use a
   hand-built `documents.get` fixture in `testdata/gdocs/` containing a title, headings,
   a list, a table and an emoji.
5. **Markdown → requests.** `buildInsert(at, text, format, anchorStyle)` returns
   `[]request` plus the inserted plain text. Pure, and table-tested.
6. **Docs handlers.** Implement `docs_get_document`, `docs_insert_text`,
   `docs_append_text`, `docs_add_to_section`, `docs_replace_section`,
   `docs_replace_text`, `docs_delete_text` and `docs_format_text`. They share
   `editLoop`: read → plan → batchUpdate with `requiredRevisionId` → on a revision
   conflict re-read and retry once → verify → result. The test fake is an in-memory
   paragraph model behind `httptest` with host rewriting.
7. **YAML.** Wire the handlers into `google_docs.yaml`, add the new actions, and rewrite
   the descriptions of the index-based actions to point at the anchor tools.
8. **Other writes.** Sheets `includeValuesInResponse`; Excel `excel_write_cells`
   (handler + `a1Range` math); Gmail `cc`/`bcc` + RFC 2047 subject; Graph recipient
   split + `cc`/`bcc`; OneNote `onenote_append_to_page` (builder + PATCH). Test each.
9. **Docs sync.** Use the `docs-sync` skill, update the CLAUDE.md connector section
   (handler hook, expect_change), and update the website connector pages if they list
   Docs actions.
10. **Checks.** `gofmt`, `go vet ./internal/connectors/...`, `go test ./internal/connectors/ ./internal/coder/ ./web/ -count=1`, `make ci-docs`.
    Then push and open the PR.

## PR 2 — `feat(connectors): Deel`

Branch `feat/connectors-deel` off `main`. It is not stacked on PR 1, so it gets CI.

1. Add `providers/deel.yaml` and `connectors/deel.yaml` per the spec.
2. Add a round-trip test (`deel_test.go`) for the `{"data":…}` body and `X-Version`.
3. Vendor the logo (`scripts/vendor-brand-logos.sh` manifest line) and add it to
   `check-docs-sync.py` DISPLAY_NAMES.
4. Update the provider count in README.md, CLAUDE.md and rookery-web. Open the
   rookery-web PR (docs-sync).
5. Run the targeted checks, push, and open the PR.

## Ship

1. Wait for CI on both PRs and squash-merge them, updating the branch between merges.
2. Merge the release-please PR, which bumps the minor version, and confirm the tag and
   release workflow.
3. Check out `main` locally, pull, `make deploy`, and confirm `/healthz` reports the new
   commit and version on :8899.
