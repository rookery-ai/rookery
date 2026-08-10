# Rookery Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename Simple Agents to Rookery across code, environment, on-disk layout, CI/CD, packaging and UI, then add the README and LICENSE the repository needs before anyone else can look at it.

**Architecture:** Two pull requests. PR1 (Tasks 1–8) is the rename proper; every task leaves the tree building and the test suite green, so the history stays bisectable. PR2 (Tasks 9–10) adds README, LICENSE, favicon and repository metadata, none of which can break a build. Task 7 introduces `TestNoLegacyBrandStrings`, a merge gate that mechanically proves no legacy brand string survived — it is added at the point where it can pass, not before, so no commit ships a knowingly-red test.

**Tech Stack:** Go 1.26, SQLite (`modernc.org/sqlite`), Echo v4, React + Vite + TypeScript, goreleaser, release-please, cosign, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-07-29-rookery-rename-design.md`

## Global Constraints

- **Product name is `Rookery`**; binary, module and package name are lowercase `rookery`.
- **Environment prefix is `ROOKERY_`** — never `RK_`, never `ROOK_`. Sixteen live variables.
- **`SA_TEMPLATES_DIR` and `SA_STATIC_DIR` are DELETED, not renamed.** The template UI was removed in the SPA cutover; they have no consumer and survive only in prose.
- **The OAuth callback path `/dashboard/connectors/services/callback/:provider` is FROZEN.** It is the redirect URI registered in external OAuth applications. It does not change in this or any rename.
- **No migration code of any kind.** No `mv` helper, no refuse-to-boot guard, no `system.key` preservation, no dual-prefix snapshot regex, no `state.md` intro rewrite. The install is being wiped and recreated greenfield.
- **No `SA_*` compatibility shim.** Hard cutover.
- **No metaphor anywhere in code, schema, UI copy or the tagline.** Domain nouns stay literal: workspace, agent, vault, skill.
- **Never renamed:** git history, `CHANGELOG.md`, `CHANGES.md`, and everything under `docs/superpowers/`. These are dated records; rewriting them would falsify the archive.
- **`CLAUDE.md` IS rewritten** — it is live instruction that agents read every session.
- **Conventional Commits.** PR1 title: `feat!: rename to rookery`. PR2 title: `docs: launch-ready repo basics`.
- **Go test timeout is 900s**, not 600s — the `web` package alone measures ~343s under `-race`.
- **Branch:** all work happens on `worktree-rookery-rename`. Never commit to `main`.

---

### Task 1: Go module path and command directory

Renames the module and the `cmd/` directory, plus every build path that references them. Build-path references are included here — not deferred to the CI task — because omitting them leaves `make build-go` broken at this commit.

**Files:**
- Modify: `go.mod` (module line)
- Modify: 150 `*.go` files (import path)
- Rename: `cmd/simple-agents/` → `cmd/rookery/`
- Modify: `Makefile` (`BIN`, `PKG`, `LDFLAGS`, `stop`/`status` pgrep patterns)
- Modify: `Dockerfile` (build path, ldflags, output path, `COPY`, `HEALTHCHECK`, `ENTRYPOINT`)
- Modify: `.goreleaser.yaml` (`builds.main`, ldflags only — identity strings come in Task 6)
- Modify: `.github/workflows/pr.yml` (cross-compile build path)
- Modify: `.dockerignore` (stray `simple-agents` entry)

**Interfaces:**
- Produces: the import prefix `github.com/ilijad1/rookery`, used by every later task that touches a Go file.

- [ ] **Step 1: Rename the module and rewrite every import**

```bash
go mod edit -module github.com/ilijad1/rookery
grep -rl 'github.com/ilijad1/simple-agents' --include='*.go' . \
  | xargs sed -i 's|github.com/ilijad1/simple-agents|github.com/ilijad1/rookery|g'
```

- [ ] **Step 2: Rename the command directory**

```bash
git mv cmd/simple-agents cmd/rookery
```

- [ ] **Step 3: Fix the build paths in Makefile**

In `Makefile`, change these four lines:

```makefile
BIN := bin/rookery
PKG := ./cmd/rookery
LDFLAGS := -X github.com/ilijad1/rookery/internal/buildinfo.Version=$(VERSION) \
           -X github.com/ilijad1/rookery/internal/buildinfo.Commit=$(COMMIT) \
           -X github.com/ilijad1/rookery/internal/buildinfo.Date=$(DATE)
```

And the two process-matching patterns, in the `stop` and `status` targets:

```makefile
		pkill -f '[b]in/rookery serve' 2>/dev/null && echo "stopped (pkill)" || echo "not running"; \
```

```makefile
	@pgrep -af '[b]in/rookery serve' || echo "not running"
```

- [ ] **Step 4: Fix the build paths in Dockerfile**

In the `build` stage, replace the `go build` invocation's ldflags module paths and output:

```dockerfile
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/ilijad1/rookery/internal/buildinfo.Version=${VERSION} \
        -X github.com/ilijad1/rookery/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/ilijad1/rookery/internal/buildinfo.Date=${BUILD_DATE}" \
      -o /out/rookery ./cmd/rookery
```

In the `runtime` stage:

```dockerfile
COPY --from=build /out/rookery /usr/bin/rookery
```

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/bin/rookery", "healthcheck"]

ENTRYPOINT ["/usr/bin/rookery"]
CMD ["serve"]
```

- [ ] **Step 5: Fix the build paths in .goreleaser.yaml and pr.yml**

In `.goreleaser.yaml`, under `builds`, change `main` and the three ldflags (leave `project_name`, `id`, `binary` and the identity strings alone — Task 6 owns those):

```yaml
    main: ./cmd/rookery
```

```yaml
    ldflags:
      - -s -w
      - -X github.com/ilijad1/rookery/internal/buildinfo.Version={{.Version}}
      - -X github.com/ilijad1/rookery/internal/buildinfo.Commit={{.ShortCommit}}
      - -X github.com/ilijad1/rookery/internal/buildinfo.Date={{.Date}}
```

In `.github/workflows/pr.yml`, the `cross-compile` job's build step:

```yaml
        run: go build -o /dev/null ./cmd/rookery
```

In `.dockerignore`, change the bare `simple-agents` line to:

```
rookery
```

- [ ] **Step 6: Verify it builds and tests pass**

```bash
go build ./... && go vet ./... && gofmt -l . | grep -v '^\.claude/'
go test ./... -count=1 -timeout 900s
```

Expected: build succeeds, `gofmt` prints nothing, all tests PASS. If any test fails here it is a genuine miss — the import rewrite is purely mechanical and should be behaviour-preserving.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: rename Go module and command dir to rookery"
```

---

### Task 2: Environment variable prefix

Renames the sixteen live `SA_*` variables to `ROOKERY_*` and deletes the two dead ones.

**Files:**
- Modify: `internal/config/config.go` (the `applyEnv` getenv sites)
- Modify: `internal/buildphase/*.go` (`EnvVar` constant)
- Modify: every `*.go` file referencing an `SA_*` name (coder, connectors, vault, secrets, sandbox, web)
- Modify: `Dockerfile` (`ENV` block and its comment)
- Modify: `.github/workflows/pr.yml` (smoke-test assertion on `coder_mode`)

**Interfaces:**
- Consumes: the `github.com/ilijad1/rookery` import prefix from Task 1.
- Produces: `buildphase.EnvVar == "ROOKERY_BUILD_PHASE"`, consumed by `connectors.Execute`'s build guard.

- [ ] **Step 1: Confirm the exact set of live variables**

```bash
grep -rhno 'SA_[A-Z_]\+' --include='*.go' . | sed 's/.*://' | sort -u
```

Expected — exactly these sixteen: `SA_BUILD_PHASE`, `SA_CLAUDE_BIN`, `SA_CODER_CATALOG`, `SA_CODER_HAS_KEY`, `SA_CODER_MODE`, `SA_CONNECTOR_TOKEN`, `SA_CONNECTOR_URL`, `SA_DATA_DIR`, `SA_HOST`, `SA_KB_TOKEN`, `SA_KB_URL`, `SA_PORT`, `SA_PUBLIC_URL`, `SA_SANDBOX`, `SA_SESSION_KEY`, `SA_SYSTEM_KEY`.

`SA_TEMPLATES_DIR` and `SA_STATIC_DIR` must NOT appear — they exist only in prose. If either appears in a `.go` file, stop and report it: the spec assumed they were dead.

- [ ] **Step 2: Rewrite the prefix across Go, Dockerfile and workflows**

```bash
grep -rl 'SA_[A-Z_]' --include='*.go' --include='Dockerfile' --include='*.yml' . \
  | grep -v '^\./docs/superpowers/' \
  | xargs sed -i 's/\bSA_\([A-Z][A-Z_]*\)/ROOKERY_\1/g'
```

- [ ] **Step 3: Fix the Dockerfile comment prose**

The `ENV` block's comment names the variable in prose. Replace the sentence beginning "SA_PUBLIC_URL is REQUIRED" so it reads `ROOKERY_PUBLIC_URL is REQUIRED`, and the example line so it reads:

```dockerfile
#   -e ROOKERY_PUBLIC_URL=https://agents.example.com
```

Confirm the `ENV` block itself is now:

```dockerfile
ENV ROOKERY_DATA_DIR=/data \
    ROOKERY_HOST=0.0.0.0 \
    ROOKERY_PORT=8080 \
    ROOKERY_CODER_MODE=slim \
    HOME=/data
```

- [ ] **Step 4: Fix the smoke-test error message in pr.yml**

The container job asserts the image ships slim mode. Its error string names the variable:

```yaml
          echo "$body" | grep -q '"coder_mode":"slim"' \
            || { echo "::error::image is missing ROOKERY_CODER_MODE=slim"; exit 1; }
```

- [ ] **Step 5: Verify**

```bash
grep -rn 'SA_[A-Z_]' --include='*.go' --include='Dockerfile' --include='*.yml' . | grep -v '^\./docs/superpowers/'
```

Expected: no output.

```bash
go build ./... && go test ./... -count=1 -timeout 900s
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename SA_ env prefix to ROOKERY_"
```

---

### Task 3: On-disk names

Renames the data directory, database file, lock file, snapshot format and spill directory.

**Files:**
- Modify: `internal/config/config.go:90,97,128`
- Modify: `internal/backup/destination.go:35,40`
- Modify: `internal/backup/lock.go`, `internal/backup/lock_windows.go`
- Modify: `internal/backup/backup.go:111`
- Modify: `web/api_backup.go:307`
- Modify: `internal/coder/hosttools.go:1390` (`spillDirName`)
- Modify: `cmd/livecheck/main.go:23`
- Modify: `packaging/systemd/simple-agents.service` (data dir paths; the file is renamed in Task 6)
- Modify: affected tests under `internal/backup/`, `internal/coder/`

**Interfaces:**
- Produces: `backup.SnapshotName(t)` returning `rookery-<ts>.rkb`; `IsSnapshotName` matching only that form. Consumed by `Prune`, `LocalDestination`, `S3Destination`.

- [ ] **Step 1: Rename the data directory, database and lock file**

In `internal/config/config.go`, function `defaults()`:

```go
	dataDir := filepath.Join(home, ".rookery")
```

```go
		Database: DatabaseConfig{
			Path: filepath.Join(dataDir, "rookery.db"),
		},
```

In `applyEnv`:

```go
	if v := os.Getenv("ROOKERY_DATA_DIR"); v != "" {
		cfg.Data.Dir = v
		cfg.Database.Path = filepath.Join(v, "rookery.db")
	}
```

In `internal/backup/lock.go` and `internal/backup/lock_windows.go`:

```go
	return filepath.Join(dataDir, "rookery.pid")
```

- [ ] **Step 2: Write the failing test for the new snapshot naming**

Add to `internal/backup/destination_test.go` (create the file if it does not exist; if it does, append the function):

```go
func TestSnapshotNameUsesRookeryPrefixAndRkbExtension(t *testing.T) {
	ts := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	got := SnapshotName(ts)
	want := "rookery-20260729-030000.rkb"
	if got != want {
		t.Fatalf("SnapshotName = %q, want %q", got, want)
	}
	if !IsSnapshotName(got) {
		t.Fatalf("IsSnapshotName(%q) = false, want true", got)
	}
	// The old form must no longer be recognised: retention and listing filter on
	// this predicate, and the install is greenfield so no legacy file exists.
	if IsSnapshotName("simple-agents-20260729-030000.sab") {
		t.Fatal("IsSnapshotName accepted a legacy simple-agents/.sab name")
	}
}
```

- [ ] **Step 3: Run it to confirm it fails**

```bash
go test ./internal/backup/ -run TestSnapshotNameUsesRookeryPrefixAndRkbExtension -v
```

Expected: FAIL — `SnapshotName = "simple-agents-20260729-030000.sab", want "rookery-20260729-030000.rkb"`.

- [ ] **Step 4: Rename the snapshot format**

In `internal/backup/destination.go`:

```go
var snapshotNameRe = regexp.MustCompile(`^rookery-\d{8}-\d{6}\.rkb$`)

// SnapshotName renders the canonical name for a snapshot taken at t. The layout
// sorts lexically by time, which is what retention relies on.
func SnapshotName(t time.Time) string {
	return "rookery-" + t.UTC().Format("20060102-150405") + ".rkb"
}
```

In `internal/backup/backup.go`:

```go
	staged := filepath.Join(work, "snapshot.rkb")
```

In `web/api_backup.go`:

```go
		tmp, err := os.CreateTemp("", "rookery-restore-*.rkb")
```

- [ ] **Step 5: Rename the spill directory**

In `internal/coder/hosttools.go`:

```go
const spillDirName = ".rookery_out"
```

- [ ] **Step 6: Fix the dev harness and the systemd unit's data paths**

In `cmd/livecheck/main.go`:

```go
	dataDir := os.ExpandEnv("$HOME/.rookery")
```

In `packaging/systemd/simple-agents.service` (filename changes in Task 6):

```ini
Environment=ROOKERY_DATA_DIR=%h/.rookery
```

```ini
ReadWritePaths=%h/.rookery
```

- [ ] **Step 7: Update the tests that assert old on-disk names**

```bash
grep -rn 'simple-agents-2\|\.sab\|\.sa_out\|simple-agents\.db\|simple-agents\.pid\|\.simple-agents-v2' \
  --include='*_test.go' --include='*.test.tsx' --include='*.test.ts' .
```

Update each hit to the new name. Known sites: `internal/backup/archive_test.go`, `internal/backup/dest_s3_test.go`, `internal/coder/hosttools_spill_test.go`, `internal/coder/api_engine_test.go` (hardcoded vault/home paths under `.simple-agents-v2`), `web/ui/src/pages/settings/BackupSection.test.tsx`.

- [ ] **Step 8: Run the tests**

```bash
go test ./internal/backup/ ./internal/coder/ ./web/... -count=1 -timeout 900s
```

Expected: PASS, including the new test from Step 2.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "refactor: rename on-disk data dir, db, lock, snapshot and spill names"
```

---

### Task 4: Model-facing prompt and skill strings

These are the highest-risk strings in the rename: they are not compiled, not type-checked, and a mistake here surfaces as an agent failing at runtime rather than as a build error.

**Files:**
- Modify: `internal/prompts/prompts.go` (`connectedToolsBlock` fallback, `BuildSkillImplementationPrompt` platform name, `ConnectorBin` doc comments)
- Modify: `internal/skilllibrary/skills/skill-creator/SKILL.md`
- Modify: `internal/prompts/connected_tools_test.go`
- Modify: `internal/agentdesigner/statefile.go` (`RenderStateTemplate` intro)

**Interfaces:**
- Consumes: nothing from earlier tasks beyond the module path.
- Produces: the literal `rookery connector exec`, asserted by `connected_tools_test.go`.

- [ ] **Step 1: Update the test that pins the connector command**

In `internal/prompts/connected_tools_test.go`, replace the three assertions naming the old binary:

```go
	block := connectedToolsBlock(
		[]ConnectionRef{{Provider: "gmail", Label: "work"}},
		[]string{"gmail_search", "gmail_send_email"}, BackendFullCoder, "/opt/rookery/rookery")
	if !strings.Contains(block, "/opt/rookery/rookery connector exec") {
		t.Fatalf("explicit connector bin not used in block:\n%s", block)
	}
```

And in the test covering the empty-`connectorBin` fallback:

```go
	if !strings.Contains(block, "rookery connector exec") {
		t.Fatalf("fallback connector bin not used in block:\n%s", block)
	}
```

- [ ] **Step 2: Run it to confirm it fails**

```bash
go test ./internal/prompts/ -run TestConnectedTools -v
```

Expected: FAIL — the block still contains `simple-agents connector exec`.

- [ ] **Step 3: Update the prompt strings**

In `internal/prompts/prompts.go`, in `connectedToolsBlock`:

```go
	bin := connectorBin
	if bin == "" {
		bin = "rookery"
	}
```

Its doc comment, one line above the function:

```go
// backend gets the `rookery connector exec <tool>` command (which reaches the exact
```

In `BuildSkillImplementationPrompt`:

```go
	sb.WriteString("\" for the Rookery platform.\n\n")
```

And the two `ConnectorBin` field comments in the params structs:

```go
	ConnectorBin    string   // absolute rookery path for the CLI connector-exec command
```

```go
	// ConnectorBin is the absolute path to the rookery binary a CLI coder invokes as
	// `<bin> connector exec …`. Empty falls back to bare "rookery" (relies on PATH).
```

- [ ] **Step 4: Update the skill-creator core skill**

In `internal/skilllibrary/skills/skill-creator/SKILL.md`:

```markdown
Author new skills for the Rookery platform. A skill is a folder
```

- [ ] **Step 5: Update the agent state template**

In `internal/agentdesigner/statefile.go`, in `RenderStateTemplate`:

```go
*Managed by Rookery. The block below is this agent's memory between runs — edit it if you need to fix something by hand.*
```

No migration accompanies this. `WriteState` splices only the JSON fence and preserves the intro byte-for-byte, so a surviving file would keep the old string — but the install is being recreated greenfield, so none survives.

- [ ] **Step 6: Run the tests**

```bash
go test ./internal/prompts/ ./internal/skilllibrary/ ./internal/agentdesigner/ -count=1 -timeout 900s
```

Expected: PASS. `internal/skilllibrary/catalog_test.go` validates every core skill's frontmatter and referenced script paths, so it will catch a malformed SKILL.md edit.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: rename platform in model-facing prompts and skills"
```

---

### Task 5: UI strings and browser storage keys

**Files:**
- Modify: `web/ui/index.html` (title, inline theme script)
- Modify: `web/ui/src/theme.tsx` (`KEY`)
- Modify: `web/ui/src/components/shell/usePaneWidth.tsx` (`STORAGE_KEY`)
- Modify: `web/ui/src/pages/kb/useRecentFiles.ts` (`RECENT_KEY_PREFIX`)
- Modify: `web/ui/src/pages/Login.tsx` (wordmark)
- Modify: `web/ui/src/pages/settings/BackupSection.tsx` (CLI command, S3 prefix placeholder)
- Modify: `web/ui/src/components/shell/panewidth.test.tsx`, `web/ui/src/pages/kb/corpus.test.ts`

**Interfaces:**
- Produces: storage keys `rookery-theme`, `rookery.paneWidth`, `rookery.kb.recent`.

- [ ] **Step 1: Rename the theme key in BOTH places**

This is the trap: the key is read twice, and changing only one produces a theme flash on every page load that no test catches.

In `web/ui/index.html`:

```html
    <title>Rookery</title>
    <script>
      (function () {
        var t = localStorage.getItem("rookery-theme");
        var dark = t === "dark" || ((t === null || t === "system") && matchMedia("(prefers-color-scheme: dark)").matches);
        if (dark) document.documentElement.classList.add("dark");
      })();
    </script>
```

In `web/ui/src/theme.tsx`:

```ts
const KEY = "rookery-theme";
```

- [ ] **Step 2: Rename the remaining storage keys**

In `web/ui/src/components/shell/usePaneWidth.tsx`:

```ts
export const STORAGE_KEY = "rookery.paneWidth";
```

In `web/ui/src/pages/kb/useRecentFiles.ts`:

```ts
const RECENT_KEY_PREFIX = "rookery.kb.recent";
```

- [ ] **Step 3: Rename the visible copy**

In `web/ui/src/pages/Login.tsx`:

```tsx
        <h1 className="text-xl font-bold mb-1">Rookery</h1>
```

In `web/ui/src/pages/settings/BackupSection.tsx`, the CLI command shown when a restore is staged:

```tsx
          <code>rookery backup cancel-restore</code> to abandon it.
```

and the S3 prefix input placeholder:

```tsx
                placeholder="rookery/"
```

- [ ] **Step 4: Update the tests asserting old strings**

In `web/ui/src/components/shell/panewidth.test.tsx`, replace every `"sa.paneWidth"` literal with `"rookery.paneWidth"`.

In `web/ui/src/pages/kb/corpus.test.ts`, the `state.md` fixture's intro must match Task 4's template:

```ts
    md: '# State — Gmail Digest\n\n*Managed by Rookery. The block below is this agent\'s memory between runs — edit it if you need to fix something by hand.*\n\n```json\n{\n  "a": 1\n}\n```\n',
```

In `web/ui/src/pages/settings/BackupSection.test.tsx`, the snapshot fixture name:

```ts
      { name: "rookery-20260729-030000.rkb", size: 12345, mod_time: "2026-07-29T03:00:00Z" },
```

- [ ] **Step 5: Run the frontend gate**

```bash
cd web/ui && npm ci && npx tsc -b && npm run lint && npx vitest run && npm run build
```

Expected: typecheck, lint, tests and build all PASS.

- [ ] **Step 6: Commit**

```bash
cd ../.. && git add -A
git commit -m "refactor(web/ui): rename wordmark, title and browser storage keys"
```

---

### Task 6: Release identity and packaging

Everything that determines what the released artifact is called and how it is verified. This is where the silent failure lives: the cosign identity regexp fails at *verification* time, so a wrong value produces a fully green pipeline.

**Files:**
- Modify: `.goreleaser.yaml` (`project_name`, `builds.id`, `builds.binary`, archive contents, nfpm block, release footer)
- Modify: `release-please-config.json` (`package-name`)
- Rename: `packaging/systemd/simple-agents.service` → `packaging/systemd/rookery.service`
- Modify: that unit file (Description, Documentation, ExecStart)
- Modify: `Makefile` (docker image tag, volume, header comment)
- Modify: `Dockerfile` (OCI labels)
- Modify: `.github/workflows/pr.yml` (image tags, container name)
- Modify: `docs/ci-setup.md`

**Interfaces:**
- Consumes: `./cmd/rookery` build path from Task 1; `ROOKERY_DATA_DIR` from Task 2; `~/.rookery` from Task 3.

- [ ] **Step 1: Rename the systemd unit**

```bash
git mv packaging/systemd/simple-agents.service packaging/systemd/rookery.service
```

Then in that file:

```ini
Description=Rookery control plane
Documentation=https://github.com/ilijad1/rookery
```

```ini
ExecStart=/usr/bin/rookery serve
```

- [ ] **Step 2: Update goreleaser identity**

```yaml
project_name: rookery
```

```yaml
builds:
  - id: rookery
    main: ./cmd/rookery
    binary: rookery
```

```yaml
archives:
  - id: default
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files:
      - packaging/README.md
      - packaging/systemd/rookery.service
```

```yaml
nfpms:
  - id: packages
    package_name: rookery
    vendor: Ilija Dimitrovski
    homepage: https://github.com/ilijad1/rookery
    maintainer: Ilija Dimitrovski <ilija.dimitrovski@kroute.ai>
    description: |
      Multi-workspace AI agents control plane with a built-in knowledge base,
      connector layer and scheduler.
    license: proprietary
    formats: [deb, rpm]
    bindir: /usr/bin
    contents:
      - src: packaging/systemd/rookery.service
        dst: /usr/share/rookery/rookery.service
      - src: packaging/README.md
        dst: /usr/share/doc/rookery/README.md
```

The `license:` field stays `proprietary` in this task and changes to `Apache-2.0` in Task 9, alongside the LICENSE file that makes the claim true.

- [ ] **Step 3: Update the cosign identity regexp and the release footer**

This is the step that fails silently if botched. In `.goreleaser.yaml`, under `release.footer`:

```yaml
    ```bash
    cosign verify-blob checksums.txt \
      --bundle checksums.txt.bundle \
      --certificate-identity-regexp 'https://github\.com/ilijad1/rookery/.*' \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com
    ```

    ## Installing

    See `packaging/README.md` in the archive, or install the `.deb`/`.rpm` and
    follow `/usr/share/doc/rookery/README.md`.
```

Note the regexp keeps the escaped dot `github\.com` and the trailing `/.*`.

- [ ] **Step 4: Update release-please**

In `release-please-config.json`:

```json
      "package-name": "rookery",
```

Leave `.release-please-manifest.json` at `0.1.0` — the `feat!:` commit is what opens v0.2.0.

- [ ] **Step 5: Update container tags, volume and labels**

In `Makefile`:

```makefile
docker-build:
	$(CONTAINER_ENGINE) build -t rookery:local \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

## docker-run: run the locally built image with a persistent data volume
docker-run:
	$(CONTAINER_ENGINE) run --rm -it -p 8080:8080 \
		-v rookery-data:/data rookery:local
```

And the Makefile header comment's first line and data-dir mention:

```makefile
# rookery — build & local deploy helpers.
```

```makefile
# Defaults used by `serve`: listen 0.0.0.0:8080, data dir ~/.rookery
# (DB auto-migrates on open). Override the port with ROOKERY_PORT, e.g.
#   ROOKERY_PORT=8081 make deploy
```

In `Dockerfile`, the OCI labels:

```dockerfile
LABEL org.opencontainers.image.title="rookery" \
      org.opencontainers.image.description="Multi-workspace AI agents control plane" \
      org.opencontainers.image.source="https://github.com/ilijad1/rookery" \
      org.opencontainers.image.licenses="proprietary" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
```

In `.github/workflows/pr.yml`, the container job — image tag, Trivy ref, container name, and the exec path:

```yaml
          tags: rookery:pr
```

```yaml
          image-ref: rookery:pr
```

```yaml
          docker run -d --name rookery-smoke -p 18080:8080 rookery:pr
```

Every subsequent `sa-smoke` reference in that script becomes `rookery-smoke` (there are three: the `docker logs`, the `docker exec` and the `docker rm -f`), and the exec path becomes:

```yaml
          docker exec rookery-smoke /usr/bin/rookery healthcheck
```

`release.yml` needs no change: it already derives the image from `ghcr.io/${{ github.repository }}`, which follows the repository rename automatically.

- [ ] **Step 6: Update the operator-facing docs**

In `docs/ci-setup.md` (4 occurrences), replace `ilijad1/simple-agents-v2` with `ilijad1/rookery` and `ghcr.io/ilijad1/simple-agents-v2` with `ghcr.io/ilijad1/rookery`.

`packaging/README.md` (7 occurrences) ships inside every release archive and `.deb`/`.rpm`, so it is operator-facing documentation rather than a dated record. Apply the full identity mapping: binary name, `ROOKERY_*` variables, `~/.rookery` data dir, the `rookery.service` unit filename and its `/usr/share/rookery/` install path.

`docs/agent-designer-flow.html` (1 occurrence) is a generated diagram page; update the single string.

Confirm all three are clean:

```bash
grep -rn 'simple-agents\|Simple Agents\|SA_' docs/ci-setup.md packaging/README.md docs/agent-designer-flow.html
```

Expected: no output.

- [ ] **Step 7: Verify the release config parses and the image builds**

```bash
go build ./... && go test ./... -count=1 -timeout 900s
make docker-build
```

Expected: build and tests PASS; the image builds and is tagged `rookery:local`.

If `goreleaser` is installed locally, additionally:

```bash
goreleaser check
```

Expected: `1 configuration file(s) validated`.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "ci: rename release identity, packaging and container tags to rookery"
```

---

### Task 7: CLAUDE.md and the brand merge gate

Adds the test that mechanically proves the rename is complete, and rewrites the one documentation file that is live instruction rather than a dated record.

**Files:**
- Create: `internal/brandcheck/brandcheck_test.go`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: the completed rename from Tasks 1–6. This test is added here, not earlier, because this is the first point at which it can pass — no commit ships a knowingly-red test.

- [ ] **Step 1: Write the gate test**

Create `internal/brandcheck/brandcheck_test.go`:

```go
// Package brandcheck holds a single repository-wide guard test. It has no
// non-test source: the check is about the tree, not about any type.
package brandcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyTokens are the brand strings the Rookery rename removed. Several are
// model-facing prompt text or CI identity strings that no compiler and no other
// test would catch, which is why this guard is mechanical rather than trusted to
// review.
var legacyTokens = []string{
	"simple-agents",
	"simple_agents",
	"SimpleAgents",
	"Simple Agents",
	"SA_",
	".sa_out",
	".sab",
}

// allowedPrefixes are repo-relative paths exempt from the scan. They are dated
// records and release-please-managed history: they describe what was true when
// they were written, and rewriting them would falsify the archive. brandcheck
// itself is exempt because it necessarily contains every token it bans.
var allowedPrefixes = []string{
	"CHANGELOG.md",
	"CHANGES.md",
	"docs/superpowers/",
	"internal/brandcheck/",
}

// skipDirs are never walked: build output, dependencies and VCS internals.
var skipDirs = map[string]bool{
	".git":         true,
	".claude":      true,
	"node_modules": true,
	"dist":         true,
	"bin":          true,
	"logs":         true,
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the working directory")
		}
		dir = parent
	}
}

func TestNoLegacyBrandStrings(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		for _, p := range allowedPrefixes {
			if strings.HasPrefix(rel, p) {
				return nil
			}
		}

		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		// Skip anything implausibly large for source; it is generated or binary.
		if info.Size() > 2<<20 {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// A NUL byte means binary; scanning it would produce noise, not signal.
		if strings.IndexByte(string(data), 0) >= 0 {
			return nil
		}

		content := string(data)
		for _, tok := range legacyTokens {
			if strings.Contains(content, tok) {
				offenders = append(offenders, rel+": "+tok)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("legacy brand strings survive in %d place(s):\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
```

- [ ] **Step 2: Run it and fix whatever it finds**

```bash
go test ./internal/brandcheck/ -run TestNoLegacyBrandStrings -v
```

Expected on the first run: FAIL, listing every remaining occurrence — `CLAUDE.md` (rewritten in the next step) plus any site the earlier tasks missed.

The scan covers files no earlier task explicitly enumerated, which is the point of having it: expect hits in test fixtures, connector YAML comments, and any doc outside `docs/superpowers/`. Fix each reported file. Re-run until it passes.

This is the moment the rename is proven complete. Do not add exemptions to `allowedPrefixes` to make it pass; fix the file instead.

- [ ] **Step 3: Rewrite CLAUDE.md**

`CLAUDE.md` is live instruction read every session, so accuracy matters more than diff size. Apply the identity mapping throughout:

- Every `simple-agents` command example → `rookery` (`rookery owner bootstrap`, `rookery serve`, `rookery db migrate`, `rookery connector exec`, `rookery healthcheck`, `rookery backup cancel-restore`).
- Every `SA_*` variable in the environment table and prose → `ROOKERY_*`.
- The environment table's `ROOKERY_DATA_DIR` default → `~/.rookery`.
- `ghcr.io/ilijad1/simple-agents-v2` → `ghcr.io/ilijad1/rookery`; the `podman run` example's volume → `rookery-data`.
- `bin/simple-agents` → `bin/rookery`; `<data_dir>/simple-agents.pid` → `rookery.pid`; `db/simple-agents.db` → `rookery.db`; `.sa_out` → `.rookery_out`; `.sab` → `.rkb`; `sa.paneWidth` → `rookery.paneWidth`.
- The sentence recording that `SA_TEMPLATES_DIR`/`SA_STATIC_DIR` config is gone: keep the fact, drop the dead variable names, so the file no longer contains a legacy token.
- The `internal/backup` row's `.sab` mention → `.rkb`.
- Add one line under **Distribution** recording the project name and that `rookery.cloud` is the documented `ROOKERY_PUBLIC_URL` example for installs whose LAN hostname fails OAuth redirect validation.

Leave the OAuth callback path exactly as written — it is frozen.

- [ ] **Step 4: Run the full Go suite plus the gate**

```bash
go test ./... -count=1 -timeout 900s
```

Expected: PASS, including `TestNoLegacyBrandStrings`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs: rewrite CLAUDE.md for rookery and add the brand merge gate"
```

---

### Task 8: Full verification, repository rename, and PR1

**Files:** none modified — this task verifies and ships.

- [ ] **Step 1: Run the complete PR gate locally**

```bash
make ci
```

This runs `ci-fmt`, `ci-vet`, `ci-test` (race, 900s), `ci-cross` (all six GOOS/GOARCH pairs) and `ci-ui`. Expected: `all PR checks passed`.

The cross-compile matrix matters most here: the `cmd/` directory rename is exactly the kind of change that breaks a build path, and `GOOS=windows` was historically broken because nothing compiled it.

- [ ] **Step 2: Confirm no legacy string survives anywhere outside the archive**

```bash
grep -rn 'simple-agents\|Simple Agents\|SimpleAgents\|SA_\|\.sa_out\|\.sab' . \
  --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist \
  --exclude-dir=.claude --exclude-dir=bin --exclude-dir=logs \
  | grep -v '^\./CHANGELOG.md' \
  | grep -v '^\./CHANGES.md' \
  | grep -v '^\./docs/superpowers/' \
  | grep -v '^\./internal/brandcheck/'
```

Expected: no output.

- [ ] **Step 3: Rename the GitHub repository**

In place, so tags, releases, issues and release-please state survive and GitHub installs a redirect from the old path:

```bash
gh repo rename rookery --repo ilijad1/simple-agents-v2 --yes
```

Then point the local remote at the new name (the redirect would keep working, but an explicit remote avoids confusion):

```bash
git remote set-url origin git@github.com:ilijad1/rookery.git
git remote -v
```

- [ ] **Step 4: Push the branch and open PR1 as a draft**

```bash
git push -u origin worktree-rookery-rename
gh pr create --draft \
  --title 'feat!: rename to rookery' \
  --body "$(cat <<'EOF'
Renames Simple Agents to Rookery across code, environment, on-disk layout,
CI/CD, packaging and UI.

Spec: `docs/superpowers/specs/2026-07-29-rookery-rename-design.md`
Plan: `docs/superpowers/plans/2026-07-29-rookery-rename.md`

## What changed

| Surface | From | To |
|---|---|---|
| Go module | `github.com/ilijad1/simple-agents` | `github.com/ilijad1/rookery` |
| Command dir | `cmd/simple-agents/` | `cmd/rookery/` |
| Binary | `simple-agents` | `rookery` |
| Env prefix | `SA_*` (16) | `ROOKERY_*` |
| Data dir | `~/.simple-agents-v2` | `~/.rookery` |
| Database | `simple-agents.db` | `rookery.db` |
| Lock file | `simple-agents.pid` | `rookery.pid` |
| Snapshot | `simple-agents-<ts>.sab` | `rookery-<ts>.rkb` |
| Spill dir | `.sa_out` | `.rookery_out` |
| Storage keys | `sa-theme`, `sa.paneWidth`, `sa.kb.recent` | `rookery-*` / `rookery.*` |
| Image | `ghcr.io/ilijad1/simple-agents-v2` | `ghcr.io/ilijad1/rookery` |
| systemd unit | `simple-agents.service` | `rookery.service` |

`SA_TEMPLATES_DIR` and `SA_STATIC_DIR` are deleted rather than renamed — the
template UI they configured was removed in the SPA cutover.

## No migration code

The install is being recreated greenfield, so there is no `mv` helper, no
refuse-to-boot guard, no `system.key` preservation step and no dual-prefix
snapshot regex. This is deliberate and recorded in the spec's Non-goals.

## Frozen

`/dashboard/connectors/services/callback/:provider` is unchanged. It is the
redirect URI registered inside external OAuth applications.

## Verification

- `make ci` passes: gofmt, vet, `-race` (900s), all six cross-compile targets, frontend.
- New `TestNoLegacyBrandStrings` proves no legacy brand string survives outside
  `CHANGELOG.md`, `CHANGES.md` and `docs/superpowers/` (dated records).
- `connected_tools_test.go` pins the `rookery connector exec` literal that a CLI
  coder is instructed to invoke.

## Not verifiable before merge

The cosign certificate-identity regexp now reads
`https://github\.com/ilijad1/rookery/.*`. It is checked at *verification* time,
not build time, so this must be confirmed with `cosign verify-blob` against the
first release artifact after v0.2.0 publishes.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Confirm CI is green**

```bash
gh pr checks --watch
```

Expected: all six PR jobs pass. The container smoke test is the meaningful one — it exercises the renamed binary, data directory and image path end to end.

---

### Task 9: README, LICENSE and favicon

PR2 begins. These changes cannot break a build, which is why they are separated from the rename.

**Branching:** PR1 is not merged yet, so PR2 is **stacked on it** — a new branch cut from `worktree-rookery-rename`, with the pull request's base set to that same branch. Committing PR2's work onto PR1's branch instead would fold the two pull requests into one.

**Files:**
- Create: `README.md`
- Create: `LICENSE`
- Modify: `web/ui/public/favicon.svg`
- Modify: `.goreleaser.yaml` (nfpm `license`)
- Modify: `Dockerfile` (OCI license label)

- [ ] **Step 1: Cut the PR2 branch**

```bash
git checkout -b rookery-launch-basics
git branch --show-current
```

Expected: `rookery-launch-basics`.

- [ ] **Step 2: Add the Apache-2.0 license text**

```bash
gh api /licenses/apache-2.0 --jq .body > LICENSE
sed -i 's/\[yyyy\]/2026/; s/\[name of copyright owner\]/Ilija Dimitrovski/' LICENSE
grep -n 'Copyright 2026 Ilija Dimitrovski' LICENSE
```

Expected: the grep matches, confirming the appendix boilerplate was filled in rather than left as placeholders.

- [ ] **Step 3: Update the license metadata to match**

In `.goreleaser.yaml`, under `nfpms`:

```yaml
    license: Apache-2.0
```

In `Dockerfile`:

```dockerfile
      org.opencontainers.image.licenses="Apache-2.0" \
```

- [ ] **Step 4: Replace the favicon**

The current file is a generic purple lightning-bolt glyph unrelated to either name. Replace `web/ui/public/favicon.svg` entirely with a minimal geometric mark that keeps the existing purple accent and stays legible at 16px:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32" role="img" aria-label="Rookery">
  <rect width="32" height="32" rx="7" fill="#7e14ff"/>
  <circle cx="16" cy="10" r="3.4" fill="#fff"/>
  <circle cx="10" cy="21" r="3.4" fill="#fff"/>
  <circle cx="22" cy="21" r="3.4" fill="#fff"/>
</svg>
```

Three marks sharing one field: many agents, one install. Abstract rather than literal — the metaphor stays out of the copy, and a bird glyph would not survive 16px anyway.

- [ ] **Step 5: Write the README**

Create `README.md`:

````markdown
# Rookery

**Self-hosted AI agents that live on your knowledge base and act through your connected services.**

Rookery is a single-binary control plane for AI agents you own. Agents read and
write an Obsidian-style markdown vault, reach 45 external services through a
self-managed OAuth connector layer, run on a schedule or on demand, and talk to
you over Telegram, Discord or Slack. Everything runs on your hardware: the
database is SQLite, secrets are encrypted at rest, and coder subprocesses are
confined with Landlock on Linux.

## Quickstart

```bash
# Build (requires Go 1.26 and Node 24)
make build

# Create the owner account — first run only
./bin/rookery owner bootstrap -u <username> -p <password>

# Start the server on 0.0.0.0:8080
./bin/rookery serve
```

Open `http://localhost:8080`, log in, and create your first workspace.

### Container

```bash
podman run -d --name rookery -p 8080:8080 \
  -v rookery-data:/data ghcr.io/ilijad1/rookery:latest
```

The image is slim: it ships no CLI coder binary and sets
`ROOKERY_CODER_MODE=slim`, so workspaces must use the `api` coder kind.

## What it does

- **Workspaces** — fully isolated tenants, each with its own vault, secrets,
  agents and connections. One owner enters a workspace with its master password.
- **Knowledge base** — a markdown vault per workspace. Agents read the whole
  vault and write durable knowledge back into it across runs.
- **Agents** — created by conversation, not configuration. Describe what you
  want; the designer proposes a plan, generates and really tests it, then saves.
- **Connectors** — 45 providers, ~272 curated actions, self-managed OAuth. No
  third-party integration broker.
- **Skills** — reusable capability documents, 22 bundled plus your own.
- **Scheduling, reminders and chat** — cron-driven runs, natural-language
  reminders, and one-off chat with read/write access to your notes.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ROOKERY_HOST` | `0.0.0.0` | bind address; `127.0.0.1` for loopback-only |
| `ROOKERY_PORT` | `8080` | listen port |
| `ROOKERY_DATA_DIR` | `~/.rookery` | data root; also relocates the database |
| `ROOKERY_SESSION_KEY` | generated | hex 32-byte session key |
| `ROOKERY_SYSTEM_KEY` | generated | hex key encrypting stored credentials |
| `ROOKERY_PUBLIC_URL` | — | externally reachable base URL for OAuth callbacks |
| `ROOKERY_SANDBOX` | `1` | `0`/`false`/`off` disables Landlock confinement |
| `ROOKERY_CODER_MODE` | `full` | `slim` removes the local CLI coder kind |

`ROOKERY_PUBLIC_URL` matters more than it looks: OAuth providers reject redirect
URIs on non-public hostnames, so a `.lan` address fails Google's validation. Use
a real hostname — `rookery.cloud` is the documented example — or `http://localhost`.

## Platform support

| Target | Sandbox | Service |
|---|---|---|
| linux amd64/arm64 | Landlock | systemd user unit |
| container (linux) | Landlock | runtime-managed |
| darwin amd64/arm64 | none | launchd (not yet shipped) |
| windows amd64/arm64 | none | SCM (not yet shipped) |

**Off Linux there is no filesystem sandbox**: coder subprocesses run unconfined.
`/healthz` and the startup log both report this.

## Health

`GET /healthz` is unauthenticated and reports version, commit, sandbox status
including the Landlock ABI, coder mode and host-tool presence. A `python3`
warning is not cosmetic — without it the agent-tool AST guardrail self-skips.

## License

Apache-2.0. See [LICENSE](LICENSE).
````

- [ ] **Step 6: Verify the frontend still builds and the gate still passes**

```bash
cd web/ui && npx vitest run && npm run build && cd ../..
go test ./internal/brandcheck/ -run TestNoLegacyBrandStrings -v
```

Expected: PASS. The README deliberately contains no legacy token, so the gate covers it too.

- [ ] **Step 7: Commit**

```bash
git add README.md LICENSE web/ui/public/favicon.svg .goreleaser.yaml Dockerfile
git commit -m "docs: add README, Apache-2.0 LICENSE and the Rookery favicon"
```

---

### Task 10: Repository metadata and PR2

- [ ] **Step 1: Update the GitHub repository description and topics**

The current description predates the workspace model, the connector layer and the SPA:

```bash
gh repo edit ilijad1/rookery \
  --description 'Self-hosted AI agents that live on your knowledge base and act through your connected services' \
  --homepage 'https://rookery.cloud' \
  --add-topic ai-agents \
  --add-topic self-hosted \
  --add-topic golang \
  --add-topic knowledge-base \
  --add-topic automation \
  --add-topic oauth
```

- [ ] **Step 2: Push and open PR2 as a draft, based on PR1's branch**

```bash
git push -u origin rookery-launch-basics
gh pr create --draft \
  --base worktree-rookery-rename \
  --title 'docs: launch-ready repo basics' \
  --body "$(cat <<'EOF'
Adds the assets the repository needs before anyone else can usefully look at it.
Follows `feat!: rename to rookery`.

Spec: `docs/superpowers/specs/2026-07-29-rookery-rename-design.md`

- **README.md** — the repository had none. Tagline, quickstart, feature summary,
  the `ROOKERY_*` configuration table, platform-support matrix and license.
- **LICENSE** — Apache-2.0. The repository previously had no license at all,
  which made it legally unusable by anyone who saw it. Apache-2.0 over MIT for
  the express patent grant; over AGPL because a hosted offering is not planned.
- **favicon.svg** — the old mark was a generic purple lightning bolt unrelated
  to either name. Replaced with a minimal geometric mark that keeps the existing
  purple accent and stays legible at 16px.
- **License metadata** — `nfpms.license` and the OCI `licenses` label now say
  `Apache-2.0`, matching the file that makes the claim true.
- **Repository description and topics** — the old description predated the
  workspace model, the connector layer and the SPA.

Deferred to a follow-on "go public" spec: the docs site on rookery.cloud,
`install.sh`/`install.ps1`, the Homebrew tap, making the GHCR package public,
and flipping repository visibility.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Confirm CI is green**

```bash
gh pr checks --watch
```

Expected: all six PR jobs pass.

---

## Post-merge acceptance

Not part of any task's commit, because it cannot run until PR1 merges and a release publishes. Perform once.

- [ ] Delete `~/.simple-agents-v2`.
- [ ] `make build`; confirm the artifact is `bin/rookery`.
- [ ] `./bin/rookery owner bootstrap -u <name> -p <pw>`; confirm `~/.rookery` exists and contains `system.key` and `rookery.db`.
- [ ] `make deploy`; check `/healthz` reports sandbox status and no capability warnings.
- [ ] Create a workspace, enter it, connect one service via OAuth — exercises the frozen callback path and `ROOKERY_PUBLIC_URL`.
- [ ] Create and run one agent; confirm `[CHAT]` delivery and that `state.md` carries the new intro string.
- [ ] Take a backup; confirm the snapshot is named `rookery-<ts>.rkb`, then restore it.
- [ ] **After v0.2.0 publishes**, verify the signature — the one check no green pipeline can substitute for:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/ilijad1/rookery/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

- [ ] Delete the stale `ghcr.io/ilijad1/simple-agents-v2` package — a repository rename does not move GHCR packages.
