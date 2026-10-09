# CodeQL log-injection alerts: make `logsafe.Value` a recognised sanitiser

## Problem

Seven `go/log-injection` (medium) code-scanning alerts were open on `main` at
0.17.0 (#66, #67, #69–#73). The SARIF data-flow paths split them in two:

- **Five (#69–#73) are already sanitised at the call site.** Each path enters
  `internal/logsafe.Value`, runs through its rune loop and `strings.Builder`, and
  leaves at "call to Value" — CodeQL does not model that loop as a sanitiser, so
  it tracks the taint straight through. The August rollout of `logsafe`
  (#245/#249) therefore never satisfied CodeQL: alerts #57, #59, #60 and #64
  show as "fixed" only because their lines moved, and the same sinks reopened as
  #69–#73.
- **Two (#66, #67) are genuinely unwrapped**: the browser proxy logs the
  request's `addr` (`internal/browser/proxy.go`) and the designer feasibility
  probe logs a user-typed `rawURL` (`internal/browser/feasibility.go`).

There are no GitHub issues tracking these; the alerts themselves are the record.

## What CodeQL accepts

`semmle/go/security/LogInjectionCustomizations.qll` recognises exactly two
sanitisers: a `StringOps::ReplaceAll` whose replaced string is `"\n"` or `"\r"`
(that is `strings.ReplaceAll`, or `strings.Replace` with negative `n`), and an
argument formatted with `%q`. Nothing else.

## Design

1. **`logsafe.Value` opens with `strings.ReplaceAll(s, "\r", " ")` and
   `strings.ReplaceAll(s, "\n", " ")`** before its existing loop. Output is
   byte-identical — the loop already maps both to a space — so no log line
   changes. Fixing the function rather than its callers clears #69–#73 and every
   future call site in one place.
2. **Pin it with a source-reading test.** The two calls look redundant beside a
   loop that does the same thing, which is exactly the shape of line a tidy-up
   deletes; doing so changes no behaviour and fails no unit test, while silently
   reopening every alert. The test fails if either call is removed, and a comment
   says why.
3. **Wrap the two browser sites** — and their `err`, because a dial or
   Playwright error embeds the very address/URL beside it (the lesson already
   recorded at `internal/vault/kbsearch.go`). The CONNECT branch of the proxy has
   the identical shape and is wrapped too, for consistency, though CodeQL does
   not flag it.

## Out of scope

- The previously **dismissed** log-injection alerts (#41–#45) stay dismissed;
  their reasoning (structured slog attributes are quoted by every handler) is
  unchanged and this work does not touch those sites.
- No alert is dismissed by this change. Alerts close on their own when CodeQL
  re-analyses `main` after merge.

## Verification

- `go test ./internal/logsafe/ ./internal/browser/ ./internal/vault/ ./web/`.
- The PR's own CodeQL run (`codeql.yml` triggers on `pull_request`) must report
  zero open `go/log-injection` alerts for `refs/pull/<N>/merge`.
