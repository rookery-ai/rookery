# Plan: CodeQL log-injection alerts

Spec: `docs/superpowers/specs/2026-10-09-codeql-log-injection-design.md`

1. **Test first** — `internal/logsafe/logsafe_test.go`: add
   `TestValueUsesASanitiserCodeQLRecognises`, reading `logsafe.go` and asserting
   it contains `strings.ReplaceAll(s, "\n", " ")` and
   `strings.ReplaceAll(s, "\r", " ")`. Run it; expect failure.
2. **Implement** — prepend the two `ReplaceAll` calls to `logsafe.Value`, with a
   comment saying they are load-bearing for CodeQL. Run the package tests; all
   pass, including the existing behaviour tests (output unchanged).
3. **Browser sites** — import `internal/logsafe` in `internal/browser`; wrap
   `rawURL`/`err` in `feasibility.go` and `addr`/`err` in both proxy log lines.
   `go test ./internal/browser/`.
4. **Checks** — `make ci-fmt ci-vet`, `go test` for logsafe/browser/vault/web.
5. **Ship** — commit (Conventional Commits), docs-sync check, push, open PR.
6. **Verify** — wait for the PR's CodeQL analysis; query open
   `go/log-injection` alerts on `refs/pull/<N>/merge`; expect 0.
