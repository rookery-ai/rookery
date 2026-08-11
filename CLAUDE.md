# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Tenancy model: single-owner, multi-workspace

The platform has **one owner** (the installer; a single row in the `owner` table) who logs in and
manages **workspaces**. A **workspace** is a fully isolated tenant — its own vault, claude-home,
secrets, agents, connector, and inlined coder config — and replaces the old per-user account.
Workspaces have no login of their own: the owner **enters** a workspace by typing that workspace's
**master password** (re-entered on every switch). The web session is two-level: `owner_id`
(logged in) + `active_workspace_id` (entered). All tenant-scoped tables key off `workspace_id`.
Bootstrap the owner with `rookery owner bootstrap -u <name> -p <pw>`.

Terminology map (fully renamed throughout): user → **workspace**, admin → **owner**,
`user_id` → `workspace_id`, `db.User` → `db.Workspace` (+ new `db.Owner`).

## Commands

```bash
# Build
go build -o bin/rookery ./cmd/rookery

# Run all tests
go test ./... -count=1 -timeout 120s

# Run a specific package's tests
go test -v ./internal/agentdesigner/... -run TestFlow

# Run the server (after build)
./bin/rookery serve

# Bootstrap the owner account (first run only)
./bin/rookery owner bootstrap -u <username> -p <password>

# Reset the owner password (single-owner model, no login required)
./bin/rookery owner reset-password -p <new-password>

# Migrations are applied automatically when the database is opened —
# there is no separate migration command.

# Deploy / restart the server (build + run in background, logs to logs/server.log)
make deploy    # stop existing server, rebuild, start in background
make restart   # stop + start (no rebuild)
make stop      # stop the running server
make logs      # tail -f logs/server.log
make status    # show running server process
make test      # run the unit tests
make ci        # run the full PR gate locally (fmt, vet, -race, cross-compile, UI, docs-sync)
make docker-build / docker-run   # slim container image (podman or docker)

# Frontend (web/ui): build the SPA into the binary
make ui        # npm ci + vite build → web/ui/dist (embedded on next go build)
make build     # ui + go build (full artifact); make build-go for Go-only
# Dev loop: cd web/ui && npm run dev  (Vite on :5173, proxies /api to :8080)
```

AST guardrail tests shell out to `python3`. If Python is not available, those tests self-skip.

> **Deploy workflow:** When the user says "restart the server", "rebuild", or
> "deploy", run `make deploy` — it stops the running server, rebuilds, and
> starts it in the background with logs captured to `logs/server.log`. The
> server listens on `0.0.0.0:8080` by default (override host with `ROOKERY_HOST=…`, port
> with `ROOKERY_PORT=…`). Set `ROOKERY_PUBLIC_URL` to the externally-reachable base URL so OAuth
> callbacks are correct. `rookery connector exec <tool> --args '<json>'` is the
> subcommand CLI coders use to reach the connector bridge (not for manual use).
> The UI is the embedded React SPA served at `http://host:8080/` (build it into the
> binary with `make ui` before `go build`; `/app` + `/app/*` 301-redirect to `/`).
> Verify with `make status` / `make logs`; smoke-test the SPA + API with
> `curl -sS http://127.0.0.1:8080/` (200 HTML) and
> `curl -sS http://127.0.0.1:8080/api/v1/auth/session` (200 JSON).

## Git workflow

- **Always branch, never commit directly to `main`.** All work happens on a
  feature branch off `main`. When the work is finished, open a **pull request**
  back into `main` — `main` only ever advances through merged PRs.
- **Conventional Commits.** Structure every commit message as
  `type(scope): summary` (e.g. `feat(gateway): …`, `fix(web/chat): …`,
  `refactor(vault): …`, `docs: …`). Types: `feat`, `fix`, `refactor`, `docs`,
  `test`, `chore`, `perf`, `build`, `ci`. Scope is optional but preferred.
- **Deploy from `main` for production** — only after the work is finished and
  merged. `make deploy` on `main` is the production path.
- **Local branch deploys are fine for testing.** When a development phase or a
  group of tasks is complete and needs to be exercised on the running server,
  it's OK to `make deploy` from the feature branch locally before the PR merges —
  that's for testing, not production.

## Documentation sync

Four surfaces describe this project and each can be wrong without anything
failing: `README.md`, `CLAUDE.md`, the documentation site and the landing page
(both in `ilijad1/rookery-web`, checked out at `~/rookery-web`).

**Before opening a pull request, use the `docs-sync` skill.** It holds the
change-to-page trigger map and the cross-repository procedure. A change that
alters a connector provider, a `ROOKERY_*` variable, a CLI subcommand, a core
skill, a chat adapter, a backup destination, an `/api/v1` route or a packaging
target has a documentation obligation in both repositories.

`make docs-sync-check` mechanises the checkable half — counts, variable
names, command names, provider names, logo coverage — against the source
rather than against other prose, and runs inside `make ci` (`ci-docs`); it
resolves the website checkout via `ROOKERY_WEB_DIR`, then a sibling of the
main checkout derived from git's common dir, then `~/rookery-web`, and skips
website assertions when none of those exist — set `ROOKERY_WEB_DIR` to point
it at an unmerged website branch from inside a worktree. It does not check
whether a paragraph describes a feature correctly.

Verify every claim against source, never against another document. The
provider count in `README.md` once drifted for months because it was copied
forward instead of measured.

## CI/CD and release process

**Every change ships through this path. There are no manual tags and no manual
image pushes.**

1. **Branch** off `main`. Never commit to `main` directly.
2. **Commit** using Conventional Commits (`type(scope): summary`).
3. **Open a PR.** Its **title** must itself be a valid Conventional Commit —
   merges are squashes, so the title becomes the commit that lands on `main` and
   is what release-please reads to compute the next version.
4. **PR checks must pass** (`.github/workflows/pr.yml`, seven jobs):
   - `Conventional commit title`
   - `Go build and test` — gofmt, `go vet`, `go test -race` (**900s timeout**,
     not 600s: the `web` package alone measures ~343s under `-race`, 13× its
     non-race time)
   - `Cross-compile` — all six GOOS/GOARCH pairs. **This is the guard that keeps
     `GOOS=windows` compiling**; it was broken for the repo's entire history
     precisely because nothing ever built it.
   - `Frontend` — `npm ci`, `tsc -b`, `oxlint`, `vitest`, `vite build`
   - `Security scan` — govulncheck, Trivy (fs), gitleaks. CodeQL runs in its own
     workflow because it needs `security-events: write`.
   - `Container smoke test` — builds the image, Trivy-scans it, runs it, and
     asserts `/healthz`, the SPA root and the session endpoint all answer. **One
     of the project's two end-to-end gates** — this one covers the container
     image.
   - `Package smoke test` — builds a goreleaser snapshot, then installs the
     **rpm** (in a Fedora container), the **deb** (in a Debian container) and
     extracts the **tar.gz**, running `owner bootstrap` + `serve` + `healthcheck`
     from a working directory unrelated to the source tree. **The project's other
     end-to-end gate** — this one covers the native deb/rpm/tar.gz artifacts;
     nothing had ever installed one before it existed, which is exactly how they
     shipped unable to open their own database. Run it locally with
     `make ci-package` — it is deliberately excluded from `make ci` because a
     snapshot build takes minutes.
5. **Run the same checks locally first** with `make ci` — it covers `Go build
   and test` and `Cross-compile` in full, and `Frontend` through typecheck/lint/
   vitest but not the `vite build` step the CI job also runs. It does **not**
   run four of the seven gates at all: `Conventional commit title` (needs the
   PR title, not anything runnable locally), `Security scan`, `Container smoke
   test`, and `Package smoke test` — the last is available separately as
   `make ci-package`, kept out of `make ci` because a snapshot build takes
   minutes. `make ci-fmt` / `ci-vet` / `ci-test` / `ci-cross` / `ci-ui` /
   `ci-docs` run the covered pieces individually. The documentation check
   (`ci-docs`) runs as a step inside the `Go build and test` job, not as a job
   of its own, so the gate count stays at seven.
6. **Squash-merge.** release-please then maintains a release PR on `main`.
7. **Merging the release PR** tags the repo, which fires
   `.github/workflows/release.yml`: goreleaser publishes binaries, `.deb`/`.rpm`,
   checksums, cosign signatures and SBOMs, and buildx pushes the multi-arch
   image to GHCR.

Versioning starts at **v0.1.0** with `bump-minor-pre-major`, so a breaking
change bumps the minor while the project is pre-1.0. Reaching 1.0.0 is a
deliberate act at public release.

**Secrets:** the pipeline needs exactly one, `RELEASE_PLEASE_TOKEN` — see
`docs/ci-setup.md` for that plus the required branch-protection settings. GHCR
authenticates with the built-in `GITHUB_TOKEN`, cosign signs keylessly via OIDC,
and the scanners need no credentials. **Do not add secrets that have no
consumer.**

## Distribution

The project is **Rookery** (`github.com/ilijad1/rookery`); the binary, module and
package are all lowercase `rookery`, and every environment variable is prefixed
`ROOKERY_`. The project domain is **rookery.cloud** — it is the documented
`ROOKERY_PUBLIC_URL` example because OAuth providers reject redirect URIs on
non-public hostnames, so a `.lan` address fails Google's validation outright.

**Native binaries are the primary artifact**; the container image is secondary.

| Target | Sandbox | Service | Tier |
|---|---|---|---|
| linux amd64/arm64 | Landlock | systemd **user** unit + `enable-linger` | 1 |
| container (linux) | Landlock (verified ABI 8 under rootless Podman) | runtime-managed | 1 |
| darwin amd64/arm64 | **none** | launchd (not yet shipped) | 2 |
| windows amd64/arm64 | **none** | SCM (not yet shipped) | 2 |

**Off Linux there is no filesystem sandbox at all** — `sandbox.Supported()`
returns false and callers do not wrap, so coder subprocesses run unconfined.
`/healthz` and the startup log both report this.

One-command installers (`install.sh`/`install.ps1`), a Homebrew tap and Windows
service registration are **deferred until the repository is public**: release
assets on a private repo require an authenticated request, so `curl | sh` cannot
work yet. Everything those installers will need is already built.

Release artifacts (`.goreleaser.yaml`): six binary archives, `.deb`/`.rpm`
carrying the systemd user unit, `checksums.txt` + cosign keyless signature, and
an SBOM per archive.

### Container

```bash
make docker-build           # honours podman or docker, whichever is installed
make docker-run             # port 8080, data in the rookery-data volume

podman run -d --name rookery -p 8080:8080 \
  -v rookery-data:/data ghcr.io/ilijad1/rookery:latest
```

The image is **slim**: it contains no CLI coder binary and sets
`ROOKERY_CODER_MODE=slim`, so workspaces must use the `api` coder kind. It does ship
python3, ripgrep, poppler-utils and tesseract, so `/healthz` reports no
capability warnings inside it. ~270 MB.

Two container notes worth knowing: **Podman ignores `HEALTHCHECK`** unless built
with `--format docker` (Docker/buildx honours it), and the image no longer
copies `migrations/` beside the binary — the SQL is embedded (root `migrations`
package, `//go:embed *.sql`), so the container and the native binaries run the
identical code path. That copy existed to satisfy an exe-relative lookup which
made the deb, rpm and every archive fail on first use with `read migrations
dir`; embedding removed the lookup and the whole class of bug. `//go:embed`
fails the build when it matches nothing, so a missing migration set can no
longer reach a user.

### Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `ROOKERY_HOST` | `0.0.0.0` | bind address; `127.0.0.1` for loopback-only |
| `ROOKERY_PORT` | `8080` | listen port |
| `ROOKERY_DATA_DIR` | `~/.rookery` | data root; also relocates the DB |
| `ROOKERY_SESSION_KEY` | generated, then pinned to `<data_dir>/session.key` | hex 32-byte session key |
| `ROOKERY_PUBLIC_URL` | — | externally reachable base URL for OAuth callbacks; validated at use (`internal/publicurl.Normalize`) and overridden by the instance URL in owner settings |
| `ROOKERY_SANDBOX` | `1` | `0`/`false`/`off` disables Landlock confinement |
| `ROOKERY_CODER_MODE` | `full` | `slim` removes the local CLI coder kind entirely |

`ROOKERY_CODER_MODE` is **policy** ("this build has no CLI coder"), deliberately
distinct from **detection** (`coder.DetectInstalled` — "none is on PATH right
now"). Slim is enforced at four layers: config parsing (an unknown value is a
startup error), the settings API (skips the host probe), the SPA (hides the
local engine), and both write paths + `coder.ForWorkspace`, which returns
`ErrLocalCoderDisabled` naming the fix rather than spawning a missing binary.

### Health

`GET /healthz` is unauthenticated (outside `/api/v1`) and reports version,
commit, sandbox status including Landlock ABI, coder mode, and host-tool
presence — booleans only, never paths. It backs the container `HEALTHCHECK`
(via the `rookery healthcheck` subcommand), the CI smoke test, and
operator triage.

**A `python3` warning is not cosmetic**: without it the agent-tool AST guardrail
in `internal/agentdesigner/guardrails.go` self-skips, so generated tool scripts
run unchecked. `rg`, `pdftotext` and `tesseract` degrade KB search, PDF
extraction and OCR respectively.

## Architecture

### Entry point & wiring

`cmd/rookery/main.go` loads `config.yaml` via `internal/config`, wires all services, and
delegates subcommands via `github.com/urfave/cli/v3`. The `serve` subcommand:
1. Opens/migrates SQLite DB
2. Creates secrets service, coder, agent designer, agent runner, skill designer (`skilldesigner.Flow`)
3. Starts `GatewayManager` (loads all `platform_connections` from DB, starts per-workspace adapters)
4. Starts scheduler and reminder background goroutines (nightly GC also sweeps expired `skill_drafts` + orphaned staging dirs)
5. Starts Echo web server

### Inbound message pipeline

```
Per-workspace chat adapter (Telegram, Discord)
  → GatewayManager.route()
    → IdentityResolver  (platform_user_id → internal workspace_id via platform_identities table)
    → Router.Handle()
      → /agent  → agentdesigner.Flow (conversational FSM)
      → /skill  → skilldesigner.Flow (conversational FSM; list/create/cancel)
      → /run    → agentrunner.Runner
      → /secret → SecretStore
      → /remind → reminder.Service (create/list/delete)
      → /chat → db.Chat (start/list/stop/resume/delete)
      → /memory → memory.Store (add/list/delete bullets in GENERAL.md)
      → plain text → one-off chat (coder.Coder with read+write KB tools + the workspace's connector tools; see "Chat knowledge-base access" + "Chat connector access")
```

### Key packages

| Package | Responsibility |
|---|---|
| `internal/config` | YAML config + env overrides |
| `internal/db` | SQLite via `modernc.org/sqlite`; `DB`, models, per-table query helpers |
| `internal/auth` | `BootstrapOwner`, `Authenticate` (owner login), `ChangePassword` (owner), `CreateWorkspace(name, about)`, `GenerateSecretsSalt`, bcrypt |
| `internal/rbac` | `CanPerform(db, workspaceID, permission)` — reads `workspace_permissions` table |
| `internal/secrets` | AES-256-GCM store; Argon2id key derivation; `GetAll()` decrypts all for env injection; `Proxy()` resolves `${NAME}` in-memory only |
| `internal/gateway` | `Gateway` interface, `GatewayManager`, `Router`, `IdentityResolver`; adapters `TelegramGateway` + `DiscordGateway` (DM-only, discordgo, user-id identity + DM-channel resolution, mandatory delete; opaque **string** message IDs throughout) + `SlackGateway` (DM-only, Socket Mode, two-token credentials — bot token + app-level token routed via `encrypted_config` — mrkdwn renderer, mandatory delete). An **adapter registry** (`RegisterAdapter`/`AdapterFactory`/`DispatchFunc`) replaced the hard-coded platform `switch` in `GatewayManager.start()` — a new platform registers its factory from an `init()`. A **render subsystem** (`internal/gateway/render`: `Renderer` interface + registry + `render.For(platform)`) decouples formatting from the router: `Router.Handle()` emits neutral CommonMark and each adapter renders on send — Telegram via a goldmark-AST MarkdownV2 renderer, Discord via CommonMark passthrough (native support). A declarative **`CredSpec`** framework (`credspec.go`: fields + `Label`/`Blurb`/`SetupSteps` + `SplitCreds` token/`encrypted_config` split) drives both the connect flow and the SPA connectors page (backed by the `/api/v1/connectors` JSON endpoints; one card per registered platform). |
| `internal/convert` | Bytes + filename/MIME → markdown. Pure function: no vault, no network, no LLM — which is what makes it testable against golden fixtures and identical across hosts. `ToMarkdown(data, Options) (Result, error)` + `Detect` + `IsTextual`. Handles html (real `x/net/html` parse, prefers `<main>`/`<article>`, drops nav/footer/script), csv/tsv, docx/pptx/xlsx (stdlib `archive/zip`+`encoding/xml`, no vendor SDK), pdf (prefers `pdftotext -layout` when on PATH, pure-Go fallback, **warns whenever extraction looks thin** so a scanned PDF cannot pass as a clean one), json, and images (stub — no OCR). `Result.Warnings` is load-bearing: it flows into the note's frontmatter so a lossy conversion declares itself. Typed sentinel `ErrUnsupportedFormat`. Conversion is ONE-DIRECTIONAL (into markdown); exporting markdown to other formats is a planned future KB action, not an agent capability. |
| `internal/websearch` | Query → `[]Result` via a provider cascade. Optional keyed provider first (`SEARCH_KEY_BRAVE`/`SEARCH_KEY_TAVILY`, resolved as ordinary encrypted secrets), then a keyless cascade (DDG html → DDG lite → Mojeek → Bing). A provider returning ZERO results means "try the next engine", not "the answer is nothing" — a 200-OK JS-challenge page is indistinguishable from genuine no-results, which is the whole reason the cascade exists. Transient failures (429/5xx/network) retry INSIDE one provider; exhausting every provider is a NON-error empty slice, because the coder's tool loop treats any `error:` as a failing call worth blocking. |
| `internal/nethttp` | The single private-address dial guard (`GuardedClient`, `DenyPrivateAddr`, `IsBlockedIP`). Enforced at DIAL time via `net.Dialer.Control`, not by URL inspection — the only approach that catches a hostname RESOLVING into private space and every redirect hop. Blocks loopback/RFC1918/link-local/unique-local/CGNAT-tailscale/cloud-metadata, plus the NAT64/6to4/Teredo transition ranges that embed an IPv4 address (partial by nature — a network-specific NAT64 prefix cannot be enumerated). Load-bearing because chat can now reach the web and the loopback interface hosts the connector + KB bridges and their per-run bearer tokens. `internal/coder/netguard.go` delegates here; do not fork a second copy. |
| `internal/fonts` | The single copy of the UI font (`InterVariable.woff2`, latin subset, ~48 KB). Its own package because `go:embed` cannot reach outside its own directory and TWO consumers need these exact bytes: `internal/export` (which base64-inlines it into exported HTML/PDF) and the SPA (via the `@fonts` Vite alias). A second checked-in copy would drift silently, so there is deliberately only one. A test asserts the embedded bytes are a real woff2 (`wOF2` magic) and not a truncated or LFS-pointer checkout. |
| `internal/iolimit` | `ReadCapped` + `ErrTooLarge` — the shared capped read every ingest door uses (KB upload, web-chat attachment, Telegram/Discord/Slack attachment, KB bridge, `save_to_kb` URL fetch), all enforcing one 25 MiB cap. Reads `cap+1` and REJECTS rather than truncating: a silently truncated import writes a note whose frontmatter states a byte count that is not the source's. `CappingWriter` is the write-side analogue — bounds a stream written into an `io.Writer` (Slack's `slack.Client.GetFile` insists on an `io.Writer` and has no size bound; there is no stdlib `io.LimitWriter`), rejecting at the same `cap+1` boundary. |
| `internal/coder` | `Coder`: two engines behind one API. **CLI engine** — runs a coder CLI subprocess with full per-workspace isolation (`CoderBackend` interface: one struct per coder — Claude/OpenCode/Codex/Gemini/Cursor, plus a generic fallback). **API engine** (`api_engine.go`+`hosttools.go`, `coder_kind=="api"`) — an in-process LLM tool-calling loop (via `internal/llm`) that offers the model host tools (`read_file`/`write_file`/`edit_file`/`list_dir` + read-only discovery `search_files`/`glob` + exec tools `run_script`/`bash`/`web_fetch`/`web_search`) scoped+sandboxed to the vault, no subprocess. `WithNoTools()` text-only; `WithExtraEnv()` secret injection; `WithAPIConfig`/`WithSecretsLookup`/`WithVault`/`WithProgress`/`IsAPI()` for the API engine; `ForWorkspace(w, …)` builds a coder (local or api) from the workspace's inlined config |
| `internal/llm` | Thin, reusable transport over provider chat-completion/messages APIs with native function-calling (tool use). `Provider` interface + registry (`openai`, `openrouter`, `anthropic`, `generic` OpenAI-compatible, plus ~27 further providers registered against the OpenAI schema — see `coder.APIProviders()`); `Request`/`Response`/`Message`/`Tool`/`ToolCall`/`Usage`; shared HTTP plumbing with rate-limit-aware backoff (`ErrRateLimit` transient 429 → retry across a per-minute window; `ErrQuotaExhausted` 402 → no retry; `ErrAuth`, `ErrToolsUnsupported`). Knows nothing about vaults/sandboxes/protocol — the agentic loop lives in `internal/coder`. |
| `internal/connectors` | Self-managed-OAuth + API-key connector layer (replaces Composio). Embedded `providers/*.yaml` (auth config) + `connectors/*.yaml` (curated action manifests) for **91 providers** (Google-family incl. Calendar/Tasks/AdSense/GA4/Search Console, YouTube, GitHub, Slack, OpenAI, Notion, Outlook/Teams, Jira, HubSpot, Dropbox, Calendly, Asana, ClickUp, Airtable, Intercom, SendGrid, Monday, Salesforce, Shopify, Mailchimp, Zendesk, Stripe, Twilio, Trello); `Registry` (+ `OAuthProvider` for `auth_parent` aliasing, `ProviderNames()` backing the connections page), `Execute` (typed choke point), `applyAuth` (Bearer/api-key header/query/Basic + templated Basic username), `renderBody`/`renderForm`/`body_arg` body kinds, `ActiveBoundConns`/`ConnectInput`/`token_extra`/`key_extra` per-connection value sources, `OAuthClient`, `DBTokenStore` (+ headless `RunRefreshLoop`), `Bridge` (loopback HTTP so CLI coders reach `Execute` — used by runs AND chat), `ToolDefs`/`ResolveTool` (single-source tool naming for both coder kinds). All tokens `secrets.EncryptWithSystemKey`-encrypted. |
| `internal/buildphase` | Tiny package holding `ROOKERY_BUILD_PHASE`/`generation` marker (set during agent/skill builds; the connector `Execute` build-guard refuses mutating actions when present). Its own package so it outlives any one integration. |
| `internal/agentdesigner` | `Flow` FSM (Describing→Designing→Verifying→Done); conversational design shared between web and Telegram; auto-schedule; `RunFullGuardrails`/`RunToolGuardrails` (ethics + AST only); `toolstree.go` recursive path-safe `WriteToolsTree`/`ReadToolsTree` for multi-file projects; `isTestArtifact` classifier + `cleanupTestArtifacts` (post-save junk removal); `statefile.go` (`StateFilePath`/`ReadState`/`WriteState`/`RenderStateTemplate`) owns an agent's `state.md` format (see "Agent state" below); `migrate_files.go` (`MigrateAgentFilesToMarkdown`) is the idempotent startup migration off the old `state.json`/`agent.json` pair; `ParseRequiredSecrets` (`flow.go`) parses AGENT.md's `# Required secrets:` header — the only source of an agent's declared secrets now that `agent.json` is gone |
| `internal/skilldesigner` | Conversational skill-creator wizard mirroring `agentdesigner.Flow` (FSM Idle→AwaitingResume→Describing→Designing→Verifying→Done, SSE progress, 7-day drafts, approval triggers); `SkillSaver` writes SKILL.md+scripts/ to vault + DB upsert; generation runs with the `skill-creator` core skill, vetting runs the `skill-vetter` core skill as a text-only audit; `vettingBlocksSave()` parses the verdict line. Wired to BOTH surfaces: the SPA (`/api/v1/skills/design`) and chat platforms (`/skill`). `Start` is the chat entry point (opens in `StateDescribing`, asks for a description, no coder call); `StartDesign` is the web one (its form collects the description up front). |
| `internal/skilllibrary` | Embedded core skill catalog (`go:embed skills/*/SKILL.md`) — always-on for every user, no DB rows, no admin gate. `LoadBundled()`, `CoreSkillContent(slug)`, `IsCoreSkill()`, `ParseMeta()` (Anthropic+openclaw YAML frontmatter: requires.bins/anyBins/env, install specs). Supersedes the admin-catalog approach dropped in migration 009. |
| `internal/agentrunner` | Load agent → decrypt secrets into env via `WithExtraEnv` → coder subprocess → capture `[CHAT]` lines → send via GatewayManager; timestamped run logs; `RunInput.OnProgress` per-turn hook for live SSE streaming. Skills pool = core skills (embedded) + user skills; the agent's DECLARED skills come from the `agent_skills` DB table (`db.ListAgentSkillNames`, the source of truth), never from AGENT.md; `resolveSkillBins` resolves declared tools' paths for the runtime `<skill_environment>` block; `loadDeclaredSkillContent` reads core skills from the embed. **Reliable delivery**: `parseCoderOutput` (blank lines don't end `[CHAT]`; empty `[CHAT]` dropped; a stray `[/CHAT]` close tag weak models sometimes emit is stripped and never delivered; `[SILENT]` detected; `[STATE]` merged and saved via `agentdesigner.WriteState` into `state.md`'s json fence) + `extractProseMessage` fallback when no `[CHAT]` emitted and not silent → visible warning when nothing deliverable. Covered by `runner_test.go`. |
| `internal/sandbox` | Self-contained Landlock filesystem confinement for coder subprocesses (Linux). `Spec`, `Supported()`, `Wrap()` (re-exec via the hidden `__sandbox-exec` helper), `Exec()` (applies Landlock + rlimits, then `execve`). No external dependency. |
| `internal/scheduler` | Cron scheduler: polls `agent_schedules`, fires runner, decrypts stored master password for secret injection; `WithSender()` delivers output to users |
| `internal/reminder` | Creates/lists/fires reminders; background polling goroutine. Reminders live only in the DB and the reminders UI tab — they are NOT reflected to the vault. |
| `internal/chat` | `Chat` create/list/stop/resume/delete; 30-min idle auto-stop; `BuildUserContext` (shared **identity-only** context builder for one-off chat — profile/memory/agents/MCP; the broader KB is retrieved on demand via tools, not injected here) |
| `internal/prompts` | Central home for all LLM prompt construction: `BuildDesignSystemPrompt` (+ `<knowledge_base>` block + `KBManifest`), `BuildImplementationPrompt`, `BuildEditImplementationPrompt` (diagnose-before-fix), `BuildCoderPrompt` (+ `<skill_instructions>` + `<skill_environment>` blocks), `BuildChatSystemPrompt` (chat read+write KB instruction), `BuildChildAgentFollowUpPrompt`, `BuildSkillMetaPrompt`, `BuildReminderParsePrompt`, skill-creator prompts (`BuildSkillDesignSystemPrompt`, `BuildSkillImplementationPrompt`, `BuildSkillVettingPrompt`, `SkillEnvBlock`). `SkillRef`/`SkillBin` types. No inline prompt text exists outside this package. Shared single-source blocks: `agentPhilosophyBlock` (three-tier), `platformContextBlock`, `coderCapabilitiesBlock` (backend-aware), `agentArchitectureGateBlock`, `testingRulesBlock` (one bounded smoke test + dry run; real secrets at build time, no outbound sends), `shellSafetyBlock`, `scriptRobustnessBlock`, `connectedToolsBlock` (backend-aware native-tools vs `connector exec` guidance). `ChatAppsForPlatforms` + `MapCoderBackend` bridge callers to prompt params. |
| `internal/memory` | Per-user structured context store. Memory lives as named `.md` files in `memory/` (`USER.md`, `SOUL.md`, `GENERAL.md`, etc.) — editable via the KB browser. `ContextString()` reads all files, skips placeholder-only ones, and returns sectioned markdown for LLM injection. `Append/List/Delete` target GENERAL.md bullet lines (used by Telegram `/memory` command). `MigrateToStructuredFiles()` consolidates legacy UUID-keyed entries at startup. |
| `internal/vault` | Per-user Obsidian-style knowledge base: `Vault` (paths + `Resolve` safety + file IO), `Reflector` (chats→markdown+sidecar), `LinkIndex` ([[wikilinks]]), `Searcher` (ripgrep), `Guard` (post-run write-scope enforcement), `MigrateLegacyLayout`, `MigrateSessionsToChats`. |
| `internal/audit` | Structured audit event writer → `audit_logs` table |
| `internal/backup` | Owner-level snapshot/restore of the WHOLE install (database + every workspace vault) into one passphrase-encrypted `.rkb` file. `Snapshot` (`VACUUM INTO` → tar+gzip → chunked AES-256-GCM, staged to a temp file then uploaded), `StageRestore`/`ApplyPendingRestore`/`CancelRestore`/`Verify`, `Destination` interface + `LocalDestination`/`S3Destination` (hand-rolled `signV4`, no AWS SDK), `Config` in `system_settings` (`backup.config`; passphrase + S3 secret encrypted under the system key), `Scheduler` (own ticker; daily/weekly, missed runs collapse), `Prune` (keep-last-N), `AcquireLock` (flock). See "Backup and restore" below. |
| `internal/profile` | Per-user personalization (name, email, location, timezone, tone, language, notes); stored in the generic `settings` table; `Load()`/`Save()`/`ContextString()` for LLM injection; `LoadLocation()` for timezone-aware reminder parsing |
| `internal/skillstore` | `SkillStore`: install/load/delete SKILL.md based skills per workspace. `SkillDir(base, workspaceID, name)` is the path helper shared with the skill designer (staging dirs use the `.staging-<name>` convention). |
| `web/` | Echo v4 web server: the `/api/v1` JSON API + the embedded React SPA (`web/ui`, served at `/`). The old server-rendered template UI was deleted — the SPA is the only front end. Handler files now hold API handlers + shared cores (e.g. `saveConnector`, `loadAgentDetail`, `saveWorkspaceCoderCore`, `handleOAuthCallback`) reused by the JSON layer. |

### Per-user knowledge base (vault)

Every user has one Obsidian-style vault — a single directory of interlinked markdown notes.
`internal/vault` owns all vault path/IO/safety logic. The SQLite DB remains the system-of-record;
chats are *reflected* into the vault as markdown + JSON sidecars.

```
<data_dir>/vaults/<workspaceID>/
├── README.md                       # vault home note (scaffolded)
├── notes/                          # user-authored notes/journals/plans/todos
├── memory/
│   ├── USER.md                     # workspace profile — name, location, role, background
│   ├── SOUL.md                     # communication style and preferences
│   ├── GENERAL.md                  # quick notes added via /memory Telegram command
│   └── <any>.md                    # additional context files the user creates
├── skills/<name>/SKILL.md          # per-workspace skills
├── agents/<agentID>/               # an agent's OWN writable area
│   ├── AGENT.md  state.md
│   ├── tools/*.py  notes/*.md
│   └── logs/run_<ts>.md
├── chats/<id>.md                   # reflected chat transcripts
└── .kb/                            # internal: db-export/ JSON sidecars, links.json (hidden)
```

`claude-homes/<workspaceID>/` stays OUTSIDE the vault (holds `.claude` credentials — never backed up).

**Memory injection.** All `.md` files in `memory/` are automatically injected into every LLM
context (design sessions, agent runs, one-off chat) via `memory.ContextString()`. Files whose
body is only headings and HTML placeholder comments are silently skipped until the user fills
them in. `EnsureScaffold` creates `USER.md` and `SOUL.md` with placeholder content on first visit.

Key types in `internal/vault`:
- **`Vault`** — `Root/AgentsDir/AgentDir/MemoryDir/SkillsDir`; **`Resolve(workspaceID, rel)`** is the security primitive every read/write path uses (rejects `..`/absolute escapes); `WriteNote` (atomic), `Read/Delete/Rename/List/EnsureScaffold`. `List` hides dotfiles.
- **`Reflector`** — `ReflectChat/ReflectAgentRun`: markdown note + `.kb/db-export/<table>/<id>.json` sidecar. **Reminders and inbox notifications are NOT reflected.** An inbox message is a delivery record, not knowledge: the row lives in `inbox_messages`, the Home inbox renders it, and an agent run's delivered text is already archived in `agents/<id>/logs/run_<ts>.md` under "Output sent to user". The old `inbox/<uuid>.md` projection was a third copy that gave every note a non-distinguishing heading ("⏰ Reminder", "🤖 weather (cron)"), grew one file per notification forever, and — because `inbox` was never added to `kbExcludedDirs` — fed a stream of "🌤 25°C, clear sky" into the agent-/skill-designer retrieval meant to quote the user's own knowledge. `vault.RemoveLegacyInboxNotes` (startup, idempotent) sweeps `inbox/` + `.kb/db-export/inbox_messages/` from installs that had it; deleting rather than archiving is safe because every note's source row is still in the DB. Consequently `inbox`/`reminders` are no longer in `protectedTopDirs`, `kbSystemFolderLabels`, `kbDisplayTitle` or `links.go`'s priority/exclusion lists — the platform does not own those names, so a user folder called `inbox` is an ordinary user folder.
- **`LinkIndex`** — `[[wikilink]]` parsing/resolution + `RenderHTMLLinks`; `Backlinks`.
- **`Searcher`** — `ripgrepSearcher` (rg `--json`, pure-Go fallback).
- **`Guard`** — detective post-run write-scope enforcement (snapshot/revert). No longer wired into agent runs (the policy changed to let agents edit the KB directly — see "Agent access model"); the type + tests remain as a reusable utility.
- **`MigrateLegacyLayout()`** — idempotent startup migration of pre-vault `agents/`, `memory/` (jsonl→md), `skills/` into vaults.

**Agent access model.** An agent's run CWD is its own vault dir; the coder prompt (`BuildCoderPrompt`, `<knowledge_base>` block) tells it to READ the whole vault and WRITE to both its own dir and the user's knowledge base (notes, memory, user files) — durable knowledge is persisted into the KB across runs. The Landlock sandbox grants RW over the whole vault root (confined to that user's vault + HOME; the DB, config, and other users' vaults stay out of reach). System-managed dirs (`.kb/`, `chats/`, other agents' `agents/<id>/`) are off-limits by prompt, not hard-enforced. The chat uses the same model (see `prompts.BuildChatSystemPrompt`).

**Agent state (`state.md`).** An agent's memory between runs is a markdown document, not a bare JSON file: `agents/<agentID>/state.md` — a `# State — <name>` heading, an italic intro paragraph, a ```` ```json ```` fence holding the machine state, and an optional `## Notes` section the agent may add human-facing context to (`internal/agentdesigner/statefile.go`: `StateFilePath`/`ReadState`/`WriteState`/`RenderStateTemplate`). The `[STATE]` output marker is unchanged (see "Agent output protocol" below) — the runner still merges JSON on every `[STATE]` block — but the merge now targets the fence inside this file. `WriteState` splices only the fence, preserving the heading/intro/`## Notes` byte-for-byte; the fence is located with a line-based scanner (`findStateFence`), deliberately not a regex, which could not disambiguate a corrupted fence from a legitimate later one in `## Notes`; a damaged or absent fence degrades to empty state rather than erroring. All three decode sites use `json.Number` (not `float64`), preserving integer fidelity above 2^53 — e.g. a 64-bit Discord snowflake ID, which silently truncated under the old `state.json` decode. The KB refuses to save a running agent's `state.md` (`PUT /api/v1/kb/note` → 409 `agent_running`), checked on the finalized save path server-side since the frontend can't be trusted to send the well-behaved form; the guard is check-then-write (a run can still start in the gap) and covers only `PUT` — a delete/rename of `state.md` mid-run is unguarded.

**Startup migration.** `agentdesigner.MigrateAgentFilesToMarkdown` (`migrate_files.go`, run in `serve` before the scheduler starts — scheduled runs read `state.md` via the new runner path) walks every workspace's `agents/*/` dirs, including `draft_<slug>` dirs, and for each: (1) converts `state.json` → `state.md` with a verify-then-delete gate — write, read back with `ReadState`, deep-compare against the original (also decoded with `json.Number`, so the comparison itself can't paper over a rounding loss), and only then remove `state.json`; any failure at any step leaves both files in place and logs loudly, never silently dropping state; (2) reconciles `agent.json`'s legacy `Skills` field into `agent_skills` (the old `ReconcileSkillAttachmentsToDB` job, absorbed here because it must run before `agent.json` is deleted); (3) deletes `agent.json`. Idempotent — an agent dir with `state.md` present and no `agent.json` is a no-op on every subsequent boot.

**KB file kinds.** The note endpoint (`GET /api/v1/kb/note`) sniffs content rather than trusting the extension — `kind: "markdown"` for `.md` files (the existing WYSIWYG/raw editor, unchanged), `"code"` for any other file that decodes as valid UTF-8 under the 1 MiB inline cap (a read-only monospace view, no save affordance), or `"binary"` otherwise (a download-only panel; content omitted). A file exactly at the 1 MiB boundary is classified `"code"`. Navigation carries an explicit `dir` hint instead of guessing from the filename, so extensionless files still open correctly.

**KB selection assist (`POST /api/v1/kb/assist`).** One blocking, text-only coder call over a
passage the user selected in the editor — the backend half of the editor's Improve/Proofread/
Explain/Reformat panel (`AIActions.tsx`, surfaced from `BubbleToolbar.tsx`'s bubble menu; see
"KB rich-text editor: five formatting/AI constructs" below for the panel itself). The action set is closed
(`prompts.KBAssistActions()`: `improve`, `proofread`, `explain`, `reformat`) and the prompt text
lives entirely in `prompts.BuildKBAssistPrompt` (`internal/prompts/kbassist.go`), per the
project's standing rule that no prompt text lives outside `internal/prompts`. Three actions ask
for a straight replacement passage; `explain` deliberately does not — its prompt tells the model
NOT to rewrite, because the result is shown read-only and must never be pasted over the user's
prose. The selected passage is capped at `maxAssistSelectionBytes` (16 KiB) and an over-cap
selection is **rejected, not truncated** — the same reject-not-truncate contract as
`internal/iolimit`, but intentionally a separate, much smaller constant: iolimit's 25 MiB governs
ingest doors (uploads, attachments, the KB bridge), not a single LLM call. `path` is only prompt
context (the call runs `WithNoTools`, so the model cannot open the file itself) but still passes
through `vault.Resolve` — an endpoint that echoes an unvalidated path into a model prompt is
exactly the kind of thing that quietly becomes a real read later. Quota/rate-limit/auth coder
failures reuse `agentrunner.FriendlyRunError` (exported for this reason) so a workspace out of
quota gets the identical sentence here as it does from a scheduled agent run, returned as a 503
`coder_unavailable` rather than a generic 500.

**KB rich-text editor: five formatting/AI constructs.** The WYSIWYG editor (`web/ui/src/pages/kb/`)
adds underline, two colour marks, callouts, toggle lists, resizable images, and the AI actions panel
above, all as TipTap/ProseMirror extensions layered on `buildExtensions()` (`editor.ts`). Three
constraints turned out to be load-bearing enough that they're worth knowing before touching any of
this — each currently documented only inline, in the file it governs:

- **Mark registration order sets DOM nesting, which sets colour precedence.** `buildExtensions()`
  registers `KBBgColor` before `KBTextColor` — TipTap ranks marks by registration order, and the
  lower-rank (earlier) mark renders as the OUTER span on serialize, the higher-rank (later) mark as
  the INNER one. Since an element's own `color` is applied directly rather than inherited, whichever
  span sits closest to the text wins — so `KBTextColor` must be innermost for a text colour applied
  inside a highlight to override the highlight's pinned foreground, while a highlight with no text
  colour still shows its own pinned foreground (nothing nested inside it to override it). Reordering
  those two registrations silently flips which colour wins wherever both are applied to the same
  text. See the comment above `KBBgColor` in `editor.ts`.
- **ProseMirror's `renderSpec` hazard forces the colour marks to build real DOM nodes instead of spec
  arrays.** Returning the usual `["span", attrs, 0]` tuple from `renderHTML` breaks colour fidelity:
  `prosemirror-model`'s `renderSpec` special-cases an attrs key literally named `style` by assigning
  it via `dom.style.cssText = …` rather than `dom.setAttribute("style", …)`, and `cssText` assignment
  round-trips through the CSSOM, which canonicalizes any recognized colour into `rgb(...)` — so
  `"#ef4444"` comes back as `"rgb(239, 68, 68)"` the moment the mark serializes, independent of
  parsing, and `checkFidelity`'s byte-for-byte comparison then fails and the note opens read-only.
  `KBTextColor.renderHTML`/`KBBgColor.renderHTML` in `marks/colors.ts` sidestep this by constructing
  the `<span>` element themselves and calling `setAttribute` directly, preserving the literal hex.
  `kbImage.ts`'s width attribute hits the identical hazard for the same reason (see its
  `renderHTML` comment) — its NodeView applies the pixel width as inline style directly on the DOM
  element rather than through an attrs-keyed `style`.
- **The toggle's canonical serialized form is `<details>`/`<summary>` on SEPARATE lines, and the
  glued-together spelling (`<details><summary>...`) is not a fixed point alongside it.** Both forms
  parse to the identical ProseMirror doc (markdown-it treats each as CommonMark "type 6" raw HTML
  blocks), but a serializer can only ever reproduce ONE canonical spelling — parsing throws away
  whether the source had them glued or on separate lines — so the two are mutually exclusive
  canonical choices, not a matter of preference. Separate lines won because it's GitHub's own
  documented convention and the form real-world markdown (a pasted README snippet, a vault-writing
  agent) actually produces. `nodes/toggle.ts`'s top comment has the full reasoning, including the
  prior reverted attempt that glued them — do not "fix" this back to gluing, it would only move the
  read-only-until-first-save gap onto the more common input.

Also worth carrying over: `AIActions.tsx`'s `selectionMarkdown`/`accept()` are what make the AI
actions panel selection-aware rather than document-wide (captured range remapped through every
editor transaction while the bubble menu is unmounted, verified live before writing); `lib/copyText`
is the ONE clipboard write in the whole app for the reason given at its top (`navigator.clipboard`
is undefined over plain HTTP on a LAN, the normal way to reach a self-hosted install) — a KB or chat
surface reaching for `navigator.clipboard` directly instead is a bug, not a style choice.

**Export fidelity is NOT uniform across the five constructs** — `internal/export`'s HTML/PDF/DOCX
path (goldmark built without `html.WithUnsafe()`, so raw HTML is replaced with the literal comment
`<!-- raw HTML omitted -->` rather than rendered, precisely so a note can never inject a `<script>`)
degrades each one differently depending on whether it's raw HTML on the wire or plain markdown:
- **Toggle** — worst case: `<details>`/`<summary>` are both raw HTML on the wire, so the wrapper
  AND the summary TEXT are dropped together (the summary's words live inside the omitted block, not
  beside it). The body survives, but as an ordinary paragraph with no indication it was ever inside
  a collapsible.
- **Underline, both colour marks** — the `<span style>`/`<u>` wrapper is raw HTML and is dropped,
  but the enclosed TEXT is an ordinary child node the renderer still walks, so the words survive with
  formatting stripped.
- **Callouts, resized images** — markdown, not raw HTML, so both survive structurally, just
  degraded: a callout serializes as a plain `> [!kind] title` blockquote (`nodes/callout.ts`), which
  goldmark renders as an ordinary `<blockquote>` with the literal `[!kind]` marker text visible,
  since it has no notion of Obsidian's callout syntax; a resized image's width lives in the alt
  slot (`![alt|420](src)`, `kbImage.ts`), so the exported `<img>`'s alt text carries the literal
  `|420` as visible noise rather than an actual size.

See `marks/colors.ts`'s top comment for the toggle/colour-mark case specifically.

**KB table editing is a control surface, not a capability.** TipTap already implements
`addRowAfter`/`deleteColumn`/etc.; nothing reached them, so a table was inserted at a fixed 3x3 and
never changed again. The slash item now dispatches `kb:insertTable` (the same window-event pattern
Image and File attachment use, since a React dialog cannot open from an editor command) and
`TableSizePicker` offers a hover grid up to 8x8; `TableControls` renders hover handles carrying
insert-before/insert-after/delete. Four things are load-bearing:
- **Every action goes through an editor command, never a DOM edit** — the commands produce the
  canonical document, and a subtly different one makes `checkFidelity` open the note READ-ONLY on
  the next load while the table still looks correct on screen. `tableEditing.test.ts` round-trips
  every operation, every picker size 1x1–8x8, and a pipe-bearing cell (the `pipeSafeTable` case).
- **Hovering sets the caret** into the cell, because TipTap's commands are selection-relative.
  Without it the buttons operate on whichever cell was clicked LAST — the worst failure available,
  since it looks like it worked and edits the wrong row.
- **`tableGeometry.ts` is pure** for the same reason `placeMenu` is: jsdom reports zeroes for every
  rect, so a test driving the real editor proves a handle MOUNTS but never where it lands.
  `clampToViewport` pushes a handle back inside the edge rather than hiding it — an overlapping
  handle is usable, an invisible one is not. `cellCoords` honours `colSpan`, or a merged cell
  inserts the column in the wrong place on exactly the tables hardest to repair by hand.
- **The header-row checkbox states its own caveat**: markdown has no way to express a table WITHOUT
  a header row (the delimiter line is mandatory), so a headerless table has its first row promoted
  on the next save.
Merged cells are deliberately not offered — `pipeSafeTable` already drops a `colspan`/`rowspan` note
to the HTML/placeholder path, so a merge button would be a button that makes the note read-only.

**Chat knowledge-base access (on-demand retrieval + editing).** The one-off chat coder runs with `WithDir(vaultRoot).WithAllowedTools("Read,Write,Edit,Glob,Grep")` and a system instruction (`prompts.BuildChatSystemPrompt`) naming the vault root. The LLM retrieves and edits the user's notes **on demand** — only on turns that touch the KB — instead of having the vault injected every prompt. `chat.BuildUserContext` now returns identity-only context (profile/memory/agents/MCP); the old always-on `[Related knowledge base]` keyword-snippet block was removed. The tool set is file-only (no `Bash`/`WebFetch`): the chat can create/edit/read notes but cannot delete, rename, or run shell commands. The same applies to agents (RW over the vault via the sandbox). The detective `Guard` is no longer wired into agent runs — it would revert the KB edits that are now intentional — so agent/chat KB edits persist.

**Chat connector access.** One-off chat (both web `handleChatMessage` and Telegram) also exposes the workspace's **ACTIVE** service connections to the chat coder (`connectors.ActiveBoundConns` — all of them; chat isn't an agent so there's no per-agent binding), wired identically to how the API/CLI split works elsewhere: the **API engine** gets them as native function tools (`coder.WithConnectors`), a **CLI coder** reaches them via the loopback bridge (`bridge.Register` → `ROOKERY_CONNECTOR_URL`/`ROOKERY_CONNECTOR_TOKEN` env → `rookery connector exec`, plus a scoped `Bash(<bin> connector exec:*)` grant since chat is otherwise file-only). Both paths hit the same `connectors.Execute` (mutating allowed — chat is like a run, `buildPhase=false`). `BuildChatSystemPrompt(vaultRoot, backendType, conns, connToolNames, connectorBin)` appends `connectedToolsBlock` so the model knows the tools exist; with no active connections / no bridge, chat behaves exactly as the file-only default.

**Agent designer KB awareness.** The designer is text-only (`WithNoTools`) but its system prompt (`BuildDesignSystemPrompt`, `<knowledge_base>` block) now knows the app has a built-in vault that agents read/write, and is told to prefer it over Notion/external note apps for the user's own knowledge. Each design turn injects a fresh retrieval-backed block via `Flow.WithVault(v)` → `vault.BuildKBContext(v, workspaceID, query)` → `DesignSystemParams.KBManifest` — a folder-shape summary (`Vault.FolderSummary`, one line per folder regardless of how many files it holds — note this bounds bytes PER FOLDER, not in total as folder COUNT grows, which is why `BuildKBContext` gives the summary its own 2 KiB budget with a `…and N more folders` marker; unlike the old exhaustive path list that capped at 60 files/rendered 30) plus the passages most relevant to the conversation so far (via `Indexer().Search`, scored against the session's own recent user turns + the current message — the designer has no search tool of its own, so this is done for it on every turn). When nothing matches, the block says so explicitly and the prompt tells the designer to ask the user rather than invent a path. `skilldesigner.Flow` mirrors this identically (`WithVault`, its own `loadKBManifest`/`retrievalQuery`) — `BuildKBContext` lives in `internal/vault`, not `agentdesigner`, precisely so both designers can reach it without an awkward cross-designer import. `vault.NotePaths`/`Flow.WithKBLister`/the `kbLister` interface are gone — `BuildKBContext` was their only consumer.

### Unified conversational agent creation

Agent creation uses a single `agentdesigner.Flow` FSM shared between Telegram and web. No agent types — every agent is the same structure.

**FSM states:** `StateDescribing` (Telegram only) → `StateDesigning` → `StateVerifying` → `StateDone`

**Approval triggers** — two tests, deliberately different:
- **`isApproval`** (used in `StateDesigning`, strict) — exact match on `"approve"`, `"go ahead"`, `"build it"`, `"create it"`, `"/approve"` (trailing punctuation trimmed). Casual `"ok"`/`"yes"` while answering design questions does NOT launch a full generation run.
- **`isVerifyApproval`** (used in `StateVerifying`, forgiving) — also accepts `"yes"`, `"save"`, `"ok"`, `"looks good"`, `"confirm"`, `"go"`, `"do it"`, `"ship it"`, `"lgtm"`, `"perfect"`, `"great"`, …, and excludes negative cues (`"don't"`, `"not yet"`, `"change"`, `"wait"`, `"instead"`). A natural confirmation saves the build instead of being read as a change request.

**Change requests no longer discard the build.** When the user replies in `StateVerifying` with something that isn't approval, the session returns to `StateDesigning` but **keeps** `PendingAgentMD`/`PendingTools` in memory — a misfire (e.g. `"yes"`, `"save"`, `"ok"`) no longer silently drops the generated agent. The next approve re-generates with the change context and overwrites.

**On approval:** `runGeneration()` calls the coder with the same tool set as an agent run (`WithDir(agentDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit").WithProgress(notify)`) — so the coder runs REAL end-to-end tests against live services during the build, not mock-only. `WithProgress(notify)` streams the API engine's per-tool-call milestones (`🔧 run_script(...)` / `🔧 write_file(...)` / `🔧 web_search(...)`) to the build SSE + Telegram — the same live visibility a run has (agentrunner wires `WithProgress(OnProgress)`), so a weak-model build no longer looks frozen at the static `🤖 Coder is building your agent…` string. No-op for the CLI engine (it never calls the progress sink). Secrets are injected via `WithSecretsLoader`/`WithExtraEnv` so the real API calls the agent will make at run time are actually exercised here. The one hard exception: never send real OUTBOUND messages on the user's behalf at build time (enforced by the testing-rules prompt, not by withholding credentials). Coder writes `AGENT.md` + `tools/*.py` to disk, runs scripts via Bash, fixes errors, outputs `[TEST_OUTPUT]...[/TEST_OUTPUT]`. Flow reads AGENT.md, runs guardrails, stores in `PendingAgentMD`/`PendingTools`, moves to `StateVerifying`. A missing `[TEST_OUTPUT]` no longer automatically keeps the user in `StateDesigning`: on the API backend, if the engine CONFIRMED the authored script ran (`Result.ScriptVerified`) the build advances to `StateVerifying` with the captured real output shown (see "Script-verification bridge"); it only stays in `StateDesigning` when there's no confirmed run and no clean marker.

**Create-mode draft working dir (`draft_<slug>`).** A create build runs in a readable `agentdesigner.DraftAgentDir(vaultsBase, workspaceID, agentName)` = `agents/draft_<slugifyAgentName(name)>` — named from the agent's NAME, not the opaque UUID, so a work-in-progress agent is recognizable in the KB browser. The dir is KEPT across blocked/designing/verifying builds (a failed build never removes it) so a resumed draft's next generation iterates in the same place and `recoverBuiltAgentFromDisk` can recover an interrupted build. `finalizeAgent` (on save) promotes it to the canonical `AgentDir(<uuid>)` and removes the draft dir; the nightly GC sweeps `DraftAgentDir` on draft expiry (create only — edit drafts point `AgentID` at the LIVE agent and are never swept). `DesignSession.HasSaveableBuild` drives whether "keep it as-is" (`isKeepAsIs`) is offered.

**Test-artifact cleanup.** A real end-to-end test leaves junk in the agent dir (downloaded files, run outputs, scratch probes like `_probe.py`). `cleanupTestArtifacts(agentDir)` (post-approval, in `saveAndFinish`/`updateAndFinish`) removes that junk so only shipping source remains; artifacts persist through `StateVerifying` so the user can see real test output as proof. `isTestArtifact(path, name, toolsDir)` in `toolstree.go` is the shared classifier (binary-download extensions, run-output file names/suffixes, `_`-prefixed scratch probes at the `tools/` top level, root-level scratch `.json`). `ReadToolsTree` also skips test artifacts so they never corrupt the pending-tools map or trip guardrails.

**Generation failure handling:** `[BLOCKED]` marker, `ErrUsageLimit`, and timeout all return soft user-facing strings (not Go errors) — user stays in `StateDesigning`.

**SSE progress:** `DesignSession` carries `progressCh chan string` + `cancelGenerate`. `GetProgressChan(workspaceID)` lets the web SSE handler stream milestone events to the browser. The build streams the API engine's per-tool-call `🔧 …` milestones (via `WithProgress(notify)` in `runGeneration`) alongside the fixed `⚙️ Preparing workspace…` / `🤖 Coder is building your agent…` / `🔍 Validating agent safety checks…` strings — so generation is observable tool-call by tool-call, not a frozen spinner. The skill designer (`skilldesigner.runGeneration`) wires `WithProgress(notify)` the same way for parity.

**Auto-schedule:** If AGENT.md starts with `# Suggested schedule: */10 * * * *`, `parseSuggestedSchedule()` calls `db.UpsertAgentSchedule()` immediately.

**Skills selection via `# Skills:` header.** `DesignSession.Skills` is `[]prompts.SkillRef` (name+description), loaded once on start as **core skills (embedded) + the user's own skills**. The designer's `<available_skills>` block lists each skill with its description and **requires** the coder to emit a `# Skills: skill-one, skill-two` header line in AGENT.md (alongside the schedule line) declaring EXACTLY the skills this agent needs — never all of them, and never omitting the line (`# Skills: none` if it needs none). `parseSkillsLine(agentMD, installed)` reads that header and is deliberately tolerant of LLM formatting drift: case-insensitive heading matching (any `#` level, optional `required/needed/uses` qualifier, `:`/`-`/`=` separator), splits on `,`/`;`/`|`/`+`/`&`/`/`/` and `/` or `/newline, strips backticks/quotes/trailing prose (`pdf (for …)` → `pdf`), and also reads a bullet/numbered list when the heading has no inline names. Names are matched case-insensitively against the installed pool (so multi-word names like "Google Workspace" survive — bare spaces are NOT separators); unknown names are dropped with a warning rather than failing. Contract: returns `nil` ONLY when no skills header is found at all (caller treats as "declared none"); a present-but-empty/`none` header returns a non-nil empty slice. On approval `saveAndFinish`/`updateAndFinish` persist the declared names to the `agent_skills` DB table (the source of truth — see "Skill attachments source of truth" below); if no header is found, `agentdesigner.SelectSkills` runs as a fallback — one text-only call that asks the model directly which skills the agent needs, parsed with the same tolerant matcher and failing CLOSED (empty + a warning) on any error, so a weak model omitting the header no longer means zero skills. An explicit `# Skills: none` is respected and does NOT trigger the fallback (`parseSkillsLine` returns nil only when the header is absent entirely — that nil-vs-empty distinction is load-bearing). On an EDIT, an explicit header still wins; hand-curation is protected upstream instead, by `loadAgentForEdit` rewriting the header from `agent_skills` before the coder sees AGENT.md (mirroring the schedule line), so the UI and the file cannot drift. The header requirement is now injected by `availableSkillsBlock` into the design prompt AND both implementation prompts — previously only the design conversation asked for it, which is why `agent_skills` was empty across the whole install. Covered by `parse_skills_test.go` + `skills_db_test.go`.

**Skill attachments source of truth (DB, not AGENT.md).** The `agent_skills` DB table — keyed by **skill name**, not `skill_id` — is the single source of truth for an agent's skills (core + user, by name). Core (embedded) skills have no `skills`-table row, so they could never be represented by the old `skill_id` FK; migration 010 rebuilt `agent_skills(agent_id, skill_name)` and backfilled existing user-skill rows by resolving `skill_id → name`. The designer (`SaveAgent`/`UpdateAgent`) writes the parsed `# Skills:` names here — AGENT.md is for the LLM only, the DB is the skill record; there is no `agent.json` cache to keep in sync, because `agent.json` is gone (see "Agent state" and "Startup migration" above). The runner (`runCoderAgent`) and the agent page (`loadAgentDetail`) read declared skills from `db.ListAgentSkillNames(agentID)` exclusively. The web Skills card renders core + user skill checkboxes by name (`AttachedSet`/`AttachedSkills` + `CoreSkills`/`AllSkills`); `handleSaveAgentSkills` accepts `skill_names` (not IDs), validates against core ∪ user names, and writes the DB only. Deleting a user skill calls `db.DeleteAgentSkillsByName` to drop dangling attachments. **One-time cutover (absorbed into the state migration):** the standalone `ReconcileSkillAttachmentsToDB` startup step is gone — its job now runs as one phase of `agentdesigner.MigrateAgentFilesToMarkdown` (see "Startup migration" above), which must reconcile `agent.json`'s legacy `Skills` field into the DB *before* deleting that file, since `agent.json` was the only place it lived. Same semantics as before: seeds the DB only when the agent has no `agent_skills` rows yet, skipping the legacy "all core skills" fallback-bloat signature.

### Prompt architecture (coder-agnostic, three-tier)

All prompts live in `internal/prompts` (single source). The designer produces **coder-agnostic** AGENT.md — it says WHAT to do, never runtime-specific tool names (so it works on a full coder like claude-code/codex OR a basic model call like OpenRouter GLM). HOW the coder acts on files is injected separately based on `BackendType`:

- **`platformContextBlock(chatApps, vaultRoot)`** — full Rookery primer (flexible ever-growing KB with USER-REORGANIZABLE vs SYSTEM-WRITTEN fixed locations, secrets store, chats, reminders, connected chat apps + commands, output protocol, schedule). Injected into design, implementation, and runtime prompts.
- **`coderCapabilitiesBlock(backendType)`** — three-way: `BackendFullCoder` (CLI) → direct tool access; `BackendToolCalling` (the `api` engine) → native function calls (`read_file`/`write_file`/`edit_file`/`list_dir`/`search_files`/`glob`/`web_search`/`web_fetch`/`run_script`) the host executes, final answer as protocol markers; `BackendBasicModel` → `[READ_FILE]`/`[WRITE_FILE]`/`[RUN_SCRIPT]` output markers. `MapCoderBackend()` maps the coder's backend type (`"api"` → tool-calling) to these. `BuildChatSystemPrompt(vaultRoot, backendType, conns, connToolNames, connectorBin)` is likewise backend-aware (tool-calling chat offers the file tools incl. `search_files`/`glob` but NOT the exec/network tools) and appends `connectedToolsBlock` when the workspace has active connections.
- **`agentPhilosophyBlock()`** — three-tier taxonomy (TIER 1 reasoning-only / TIER 2 one script / TIER 3 multi-file) with NOT-TO-DO lists; forces the coder to pick the simplest tier that solves the task (prevents writing a script for trivial reasoning work).
- **`agentArchitectureGateBlock()`** — mandatory TASK ANALYSIS → TIER DECISION → NOTIFICATION DECISION → SCHEDULE DECISION before any file is created. Supports no-notification (`[SILENT]`) and no-schedule (`none`) agents.
- **`ChatAppsForPlatforms()`** — central platform→`ChatAppInfo` (name + commands) mapping; callers load via `db.ListUserPlatformConnections` (no GatewayManager method needed).
- Design UX is non-technical: a jargon blocklist (FORBIDDEN: AGENT.md, Python, script, vault, cron, JSON, shell, Bash, webhook, endpoint); asks notification preference + schedule; emits a `[TECHNICAL SPEC]` for the code generator.

### Connector service layer (self-managed OAuth; replaces Composio — which is fully removed)

`internal/connectors` is the platform's own external-service integration: **self-managed OAuth** +
**native typed tools** per connected account. It is **coder-agnostic** (knows nothing about coders) —
both coder kinds converge on `connectors.Execute`. **There is no Composio anywhere in the codebase.**

- **Data files, not code.** Adding a service = a `providers/<p>.yaml` (auth config) + a
  `connectors/<p>.yaml` (curated action manifest), both `go:embed`ed. `LoadBundled()` parses them.
  **91 providers (~471 actions):** the Google family (Gmail/Drive/Sheets/Docs **+ AdSense/GA4/
  Search Console**), **YouTube**, GitHub, Slack, OpenAI, Notion, Outlook, Teams, Jira, HubSpot,
  Dropbox, Calendly, Asana, ClickUp, Airtable, Intercom, SendGrid, Monday, Salesforce,
  Shopify, Mailchimp, Zendesk, Stripe, Twilio, Trello.
  (AWS SigV4 + PostgreSQL were scoped but dropped.) Each action = name + JSON-schema params +
  `mutating` flag + a request template (method/URL/query + one body kind) + `response_extract`.
  Every provider declares a `category:` grouping it on the connections page (one of Google,
  Publishing & Media, Advertising, Productivity, Communication, Commerce, Developer, Support,
  Other; empty renders under Other). The UI list is **derived from the registry**
  (`Registry.ProviderNames()`, sorted) — the old hardcoded `availableServiceProviders` slice is
  gone, so adding a service really is two YAML files and no Go change.
- **The publisher-side Google providers discover their own identifiers.** AdSense, GA4, Search
  Console, and YouTube are read-only and alias the `google` OAuth app, and each ships a list
  action (`adsense_list_accounts`, `ga4_list_properties`, `gsc_list_sites`,
  `youtube_my_channel`) that uses the SAME scope as its reporting action. That is why none of
  them needs `connect_inputs`: the agent enumerates accounts/properties/sites and picks one,
  rather than the identifier being pinned at connect time. Two renderer features exist for
  them: `{{arg|escape}}` opt-in path escaping (a Search Console site URL sits inside a path
  segment, while AdSense's `accounts/pub-…` and GA4's `properties/…` carry REAL separators that
  a blanket escape would corrupt), and the `ga4_report`/`ga4_realtime` body builders (GA4 wants
  `metrics` as `[{"name":"…"}]`; `renderBody` can substitute an array but not restructure one).
- **Auth is declarative + reusable.** A provider is OAuth2 (default) or `auth.kind: api_key`
  (`placement: header`/`query`/`basic`, `value_prefix`, `basic_user_template` for a two-part Basic
  username like Twilio's SID). Cross-provider reuse via `auth_parent`: a child (e.g. `google_sheets`)
  reuses the parent (`google`) OAuth app/token — one consent, per-service connection rows + binding;
  `Registry.OAuthProvider(name)` resolves the parent for endpoints/creds/refresh, `ProviderByName`
  keeps the child's scopes/actions. Per-connection values feed `{{conn.<key>}}` in URL/body templates
  from four sources: `connect_inputs` (fields the paste-key form collects — Shopify shop, Zendesk
  subdomain/email), `token_extra` (fields captured from the OAuth token response — Salesforce
  `instance_url`), `key_extra` (parsed from the API key — Mailchimp datacenter), and the `post_connect`
  hook (Jira cloud id).
- **Body kinds** (mutually exclusive per action, `render.go`): `body:` nested JSON (`renderBody` —
  type-preserving, optional-key-omitting, array passthrough), `body_arg:` (the whole body is one
  object arg — Salesforce sObjects), `form:` (`application/x-www-form-urlencoded` via `renderForm`,
  bracket-notation keys + array→repeated-key — Stripe/Twilio), or a Go `body_builder` for non-JSON
  encodings (`gmail_rfc822`/`gmail_reply`/`gmail_draft`, `notion_page`, `msgraph_*`, `jira_*`).
- **`Execute(ctx, reg, store, client, conn, action, args, buildPhase)`** — the single typed choke
  point: validate args → refuse `mutating` actions when `buildPhase` (build-time guard, keyed off
  `internal/buildphase.EnvVar`) → `store.AccessToken` (refresh if near expiry) → render request →
  `applyAuth` (`auth.go`: Bearer / api-key header/query/HTTP-Basic / templated-Basic username, per
  the provider's `auth` block) + provider `static_headers` (resolved via `OAuthProvider` so aliased
  children inherit the parent's) → call (1 transient retry) → normalize into a `ConnectorError`
  taxonomy (auth/ratelimit/server/needs-reauth/bad-args/build-blocked).
- **OAuth** (`oauth.go`): `ConsentURL`/`ExchangeCode`/`Refresh`/`ExchangeLongLived`/`FetchIdentity`.
  `token_expiry` has a third mode beyond `expiring`/`never`: **`exchange`** (Meta) means there is
  NO refresh token — a short-lived token is swapped for a ~60-day one via the `fb_exchange_token`
  grant and renewed by exchanging the CURRENT access token again. `Refresh` routes there so
  `DBTokenStore`/`RunRefreshLoop` need no Meta branch, and `ExchangeLongLived` returns the new
  access token as the RefreshToken too — the store hands RefreshToken back on the next renewal,
  and for this provider that IS what you exchange, so omitting it would break the *second*
  renewal ~60 days in. `post_connect` may now also **replace the connection's access token**
  (`PostConnectResult.AccessToken`): the `meta_page_token` hook swaps the user token for the first
  managed Page's own token, because publishing to a Page requires the PAGE token. That keeps the
  credential in `encrypted_access_token` instead of plaintext `extra` — which is why the design's
  "encrypt `extra`" change was dropped as unnecessary. A connection therefore means "this Page";
  several Pages means connecting several times. Per-provider config
  covers the real quirks: `token_expiry: never` (GitHub/Notion — empty `expires_at`, never refreshed),
  `token_auth: basic` + `token_content_type: json` (Notion), `static_headers` (Notion-Version, GitHub
  Accept), `authorize_extra` (Atlassian audience/prompt, Google access_type/prompt, Notion owner),
  `post_connect: atlassian_cloudid` (resolves Jira cloud id into `service_connections.extra`, exposed
  to URL templates as `{{conn.cloudid}}`). Refresh-token **rotation** is persisted (Atlassian).
- **Tokens** are `secrets.EncryptWithSystemKey`-encrypted (headless — the background `RunRefreshLoop`
  and cron runs decrypt without a master password). `DBTokenStore` reads/refreshes/persists them.
- **Tool exposure** (`tools.go` — single source): `ToolDefs(bound)` builds the tool set (single
  account → bare `gmail_send_email`; multiple of one provider → `gmail_send_email__<slug(label)>`,
  slugged to the provider's `^[a-zA-Z0-9_-]{1,64}$`); `ResolveTool(bound, name)` reverses it.
  - **API engine** exposes them as native function tools in `hostToolSet` (`coder/connectortools.go`).
  - **CLI coders** reach the SAME `Execute` via a **loopback bridge** (`bridge.go`): a `127.0.0.1`
    HTTP listener started in `serve`; the runner registers a per-run bearer token scoped to the run's
    bound connections; the coder runs `rookery connector exec <tool> --args '<json>'` (a thin
    client subcommand) which POSTs to it. Tokens never leave the host; Landlock restricts filesystem,
    not loopback TCP, so a sandboxed coder can reach it (the `rookery` binary dir is granted
    RO+exec in the sandbox spec so the child can exec it). The bridge response is **byte-capped**
    at `maxBridgeResult` (8 KiB, mirroring `coder.maxToolResult`) via `capBridgeData` — the API
    engine always truncated and the bridge did not, and an analytics or ad-insights report is
    exactly the payload that exploited the gap. Under the cap the envelope is unchanged
    (`{"data": …}`); over it, `data` becomes a truncated STRING plus `truncated: true` and a note
    telling the model to narrow its query, because a JSON value cut in place still parses and
    reads as complete data.
- **`connect_inputs` work on the OAuth path too**, not just the paste-key form. A value that
  cannot be discovered from any API (a Google Ads developer token) is collected BEFORE consent
  and rides the **signed OAuth state** — already HMAC-signed and TTL'd, so no server-side pending
  row exists to garbage-collect when a user abandons the consent screen. Base64 keeps the JSON
  clear of the `~` field separator; the callback accepts both the 4- and 5-field state shapes,
  because a state issued before the change can still be in flight across a deploy. Required
  inputs are validated at CONNECT, not at callback — otherwise a user completes consent and is
  then told a field was missing. Two further one-field provider generalisations live alongside:
  `token_exchange_grant` (Threads uses `th_exchange_token`, not Meta's `fb_exchange_token`) and
  `client_param` (TikTok names the client id `client_key` in both the consent URL and the token
  request). Both default to today's behaviour.
- **Approval gate for public writes** (`internal/approval`, opt-in, default OFF). Three layers:
  an action-level `public_write: true` in the connector YAML marks irreversible PUBLIC
  publishing (`mutating` is too blunt — pausing an ad campaign is mutating but private and
  reversible); a binding-level `agent_connections.approval_mode` (`auto` default | `approve`)
  chooses per agent+account, so one agent can post autonomously to a personal account while
  requiring approval on a company one; and `Execute`'s `Policy{BuildPhase, Parker}` (which
  replaced the bare `buildPhase bool`) enforces it. Semantics are **park, plain**: a gated call
  is written to `pending_actions` and the coder gets a queue ticket as a SUCCESS (never an
  `error:` string — the tool loop would retry it), the run finishes, and the owner resolves it
  via `/pending` `/approve <id>` `/reject <id>` in chat or the web inbox. Park sits AFTER arg
  validation (never ask a human to approve a broken call) and BEFORE the token fetch (approval
  arrives hours later, so ARGS are stored and re-rendered against a fresh token at send time).
  `Parker.Park` returning `("", nil)` means "not gated — send now", which is how a mixed set of
  bindings is honoured. `ClaimPendingAction` is a conditional UPDATE making `status` the lock,
  so chat and the web inbox racing cannot double-publish. `ParkerFor` returns nil when an agent
  has no gated binding (ungated installs pay nothing) and fails OPEN on a DB error — failing
  closed would silently halt an autonomous agent the user never gated. Both coder kinds get the
  same parker (`Coder.WithParker`, `Bridge.RegisterGated`) so changing coder kind cannot disable
  the setting. Accepted costs of park, recorded: no chaining, no error reaction, and state drift
  if the owner rejects — mitigated only by the parked result's wording. Stale rows expire after
  7 days in the nightly GC. **Control surface:**
  `PUT /api/v1/agents/:id/connections/:connID/approval` toggles a binding;
  `GET /api/v1/approvals` + `POST /api/v1/approvals/:id/{approve,reject}` resolve from the web,
  so a workspace with no chat platform connected is not stuck until the expiry.
  **One-off chat is deliberately NOT gated** — `ParkerFor` returns nil when `agentID == ""` and
  the chat bridge registration passes no parker. Chat is a human typing a request in real time,
  so gating would hold the user against themselves; the residual gap is that they have not
  reviewed the wording the model produced. Revisit if a workspace-level default is added.
- **Agent binding** (`agent_connections` table, keyed by connection id) is the source of truth for
  run-time tool exposure — NOT the AGENT.md `# Connections:` header. THREE ways to bind: the designer
  parses a `# Connections:` header (`agentdesigner.parseConnectionsLine`, tolerant of inline OR
  bullet/comment-list form) into the table; OR **auto-bind** (`agentdesigner.AutoBindTargets`) — when
  a weak model OMITS the header, the designer binds exactly the connections the build's connector-tool
  calls actually used (the API engine tracks used connection ids on `coder.Result.UsedConnectionIDs`,
  persisted across restart/keep-as-is via `agent_drafts.pending_used_connections`) — never all, never
  clobbering an existing binding, explicit header always wins; OR the **Attach-connections card** on the agent page
  (checkboxes → `handleSaveAgentConnections` → `SetAgentConnections`), which is the reliable path when
  a weak model forgets the header. Builds expose ALL workspace connections; runs expose only bound
  ones. The build/impl AND runtime prompts inject `connectedToolsBlock` (backend-aware: native tools
  vs the `connector exec` command) so the coder knows the tools exist and is told there is **no
  Composio/SDK/service keys** in the env.

**The everyday tier** (waves 1–4, 2026-08) opened a second axis alongside the business/SaaS
providers: services people use in their own lives. Three shapes, all data-only —
**personal cloud** (Todoist, YNAB, Raindrop.io) paste a token; **self-hosted**
(Home Assistant, Immich, Paperless-ngx) pair a token with the user's own `base_url`,
collected via `connect_inputs` with `normalize: base_url` (`NormalizeBaseURL` requires a
scheme and strips trailing slashes but **preserves a path prefix** — `/nextcloud` and a
reverse-proxied `/paperless` are mainstream) and reached because connectors deliberately
do not use the private-address dial guard; and **keyless** (Open-Meteo) needs no
credential at all via `auth.kind: none`. Google Calendar and Google Tasks ride the
existing Google OAuth app through `auth_parent` — `buildConsentURL` passes the CHILD's
own scopes to the PARENT's endpoint, so each child consents separately and adding them
did not disturb existing Gmail connections.

`auth.kind: "none"` touches five places: `applyAuth` returns early (the default branch
would send `Authorization: Bearer ` with an empty value), `DBTokenStore.AccessToken`
returns `("", nil)` before the expiry check (an unset expiry reads as *expired* and
would route the row into a refresh it cannot survive), the connect endpoint relaxes the
key requirement and rejects a duplicate, `connectAPIKeyCore` stores no ciphertext and
names the row after the provider, and the SPA renders `kind: "keyless"` as a bare
Connect button. `RunRefreshLoop` needs no change — `ConnectionsNearExpiry` already
filters on `expires_at <> '' AND encrypted_refresh_token <> ''`.

**Wave 2** added Readwise, Toggl Track, ntfy, Jellyfin, AdGuard Home, Miniflux, Firefly
III, TMDB, Wikipedia, Hacker News, Frankfurter and Strava — Strava filling the
`Health & Fitness` category that shipped empty. It brought one framework field:
`auth.basic_pass_literal` makes the CREDENTIAL the HTTP Basic username and a fixed
string the password, the inverse of `basic_user_template` (Toggl wants the token as the
user and the literal `api_token` as the password). `basic_user_template` still wins when
both are set, so Zendesk and Twilio are untouched. **Wave 3** added the homelab stack —
Sonarr, Radarr, Grafana, n8n, Gitea, Karakeep, Audiobookshelf, Changedetection.io and
Syncthing — plus Steam, Last.fm, Clockify and WakaTime.

**Fitbit was replaced by `google_health`, and Zoom removed** (2026-08). Fitbit's Web
API is decommissioned in September 2026 along with its OAuth server; Google Health
supersedes it and authenticates through the SHARED Google OAuth app, so it is an
`auth_parent: google` child rather than a provider of its own. Existing Fitbit tokens do
not carry over — every user re-consents. Zoom was pulled after its connect flow could
not be completed against a real account. `TestRemovedProvidersStayRemoved` keeps both
out, because the obvious fix for "Fitbit is missing" is to re-add the YAML, which would
ship a connector against an API that stops answering.

**The paste form now names the credential from the provider YAML.** `key_label`/
`key_hint` were written into every `api_key` provider and reached nothing: the wizard
hardcoded `"<Provider> API key"`, which was simply wrong for the providers that take no
API key — AdGuard Home reuses the web-UI login, Nextcloud wants an app password. Both
fields are now on the services DTO and rendered by the form.

**Wave 4** added Open Library, OpenStreetMap (Nominatim), Open Food Facts, Nextcloud,
Mealie, Vikunja, Gotify, Linkwarden, Portainer, Fitbit, Oura, Spotify and Trakt —
taking `Health & Fitness` from one provider to three (Fitbit was later removed; see
above). **Withings was deliberately dropped**: its token exchange posts
`action=requesttoken` rather than a standard grant, which the OAuth client cannot
express, and shipping a provider that cannot authenticate is worse than omitting it.

**Three more providers block or throttle anonymous clients**, so `static_headers` carries
an identifying `User-Agent` for Nominatim, Open Library and Open Food Facts as well as
Wikipedia. Nextcloud needs two mandatory headers of its own — `OCS-APIRequest: true`
(the OCS API rejects requests without it) and `Accept: application/json` (it returns XML
otherwise, which `extract` cannot read). Fitbit (before its later removal) and Spotify
both require HTTP Basic client auth on the token endpoint (`token_auth: basic`); body
credentials fail with `invalid_client`.

**Wikimedia blocks the default Go user-agent** with a 403 citing its robot policy, so
`wikipedia.yaml` sets a descriptive `User-Agent` in `static_headers`. Every Wikipedia
action failed until it did; found by live verification and pinned by a test, because
nothing else would catch its removal.

Providers not confirmed against their live API carry `unverified: true` in their YAML;
`TestWave1ProvidersDeclareVerificationStatus` fails if a wave-1 provider is neither
verified nor marked. Open-Meteo is verified by a `//go:build livecheck` test that calls
the real API — excluded from the normal run so CI never depends on a third party.

**`response_extract` walks DOTTED paths, and `response_filter` narrows arrays.**
`extract` originally resolved a single top-level key, so any nested path silently
returned the whole body — `$.data.children` (Reddit) and `$.data.user`/`$.data.videos`
(TikTok) had never once narrowed. The failure is invisible in the YAML and surfaces only
as a truncated blob against the bridge's 8 KiB cap, which is why the fix matters more
than it looks. `ResponseFilter{Field, PrefixArg}` is the client-side complement, for APIs
with no server-side filter: Home Assistant's `/api/states` returns every entity in the
house, so `ha_list_states`'s `entity_prefix` is honoured after extraction. A missing
filter argument yields an empty prefix and no-ops — matching nothing would return `[]`,
which reads to the model as "you have no sensors".

**Connectors deliberately do NOT use the private-address dial guard.**
`connectors.Execute` falls back to a plain `&http.Client{Timeout: 30s}`, and every
call site passes nil or an unguarded client — unlike `internal/websearch`, the coder's
`web_fetch`, and the Discord attachment fetcher, which all use
`nethttp.GuardedClient`. This is the property the **self-hosted tier** (Home Assistant,
Immich, Paperless-ngx) is built on: those services live at RFC1918 or Tailscale
addresses that the guard blocks at dial time. The guard's threat model is untrusted
content steering a fetch; a connector's host comes from vendored YAML or from a value
the single owner typed into their own install, so it does not apply here.
`connectors.TestExecuteReachesPrivateAddresses` pins this, and its failure message
says what breaks. Revisit if Rookery ever becomes multi-tenant — that test is where
the conversation should start.

**UI:** the SPA connections page (backed by the `/api/v1/services` JSON endpoints) — per-workspace
OAuth-app creds + connect per provider, with per-provider setup guidance
(`label`/`setup_url`/`setup_steps` in the provider YAML). The OAuth **callback** is the one
server-rendered redirect route that survives the SPA cutover: `GET /dashboard/connectors/services/callback/:provider`
(HMAC-signed, TTL'd `state`; path FROZEN because it's the registered external redirect URI; it finishes
with an HTTP redirect back to the SPA). `ROOKERY_PUBLIC_URL` sets the callback base (Google rejects
non-public-TLD/`http` redirect URIs — use `https://` or `http://localhost`).

**Redirect-URI reliability.** `internal/publicurl` owns the instance base URL
(`Resolve`: the `system_settings.public_url` row → `ROOKERY_PUBLIC_URL` → detection
from the request) and judges it against a provider's `redirect_policy` YAML block
(`Check`, a pure function). Only a policy marked `verified: true` may hard-block
the Connect button; an absent block is the zero `Policy`, which is fully
permissive, so rolling policies out provider by provider can never lock a user
out. Host classification hard-blocks only RFC-reserved suffixes — an ICANN
public suffix passes, and a PSL *private* entry such as `github.io` degrades to a
soft warning, because `publicsuffix.PublicSuffix` reports `icann=false` for both
it and `.lan`. The consent-time redirect URI is pinned into the signed OAuth
state (a 6th `~` field; 4- and 5-field states are still accepted for the 10-minute
TTL) so the token exchange cannot use a different string than consent did — and
a divergence is logged and proceeds, never rejected, because the user has already
granted consent by then. Provider `setup_steps` carry a `{{redirect_uri}}`
placeholder that the SPA substitutes (browser-side, rendered as copyable code
rather than a link — following our own callback with no `state` only errors);
`connectors.TestSetupStepsUsePlaceholderNotProse` bans the old "shown above"
wording. The hard block is **UI-only by design**: the policy predicts a third
party's rules rather than expressing an invariant we own, so a server-side gate
would turn a stale YAML entry into a lockout with no override.

### Skill system (core + user skills)

Two pools of skills, both surfaced to the agent designer and the runner as `[]prompts.SkillRef`:

- **Core skills** — embedded in the binary (`internal/skilllibrary/skills/*/SKILL.md`, `go:embed`). Always-on for every user: no DB rows, no disk seeding, no admin gate. `LoadBundled()` enumerates metadata; `CoreSkillContent(slug)` returns the full SKILL.md (frontmatter+body) for agent-context injection when an agent declares the skill; `IsCoreSkill(slug)` is the reserved-name guard. `ParseMeta()` reads Anthropic+openclaw YAML frontmatter (name, description, version, license, category, `metadata.openclaw.requires.{bins,anyBins,env}`, `metadata.openclaw.install[]`). 22 bundled skills. File Processing: csv, pdf, docx, pptx, xlsx, markdown, image-ocr. Agent Behaviour: kb-curation, change-detection, notification-writing, agent-collaboration, resilient-runs, time-and-timezones. Web & Research: web-research, playwright-browser. Development: git-and-github, cli-tool-installer. Productivity: email-triage, calendar-scheduling. Integrations: api-integration. Meta: skill-creator, skill-vetter. (web-search + web-scraper merged into web-research, and github-integration became git-and-github, because all three duplicated native tools or the GitHub connector.) **A core skill ships SKILL.md only — never a `scripts/` directory**: CoreSkillContent returns the embedded markdown and nothing else, and nothing materializes the embed to disk, so a shipped script would reference a file that never reaches the agent's working dir. Core skills teach through inline snippets; USER skills, which live on disk in the vault, may ship scripts. Pinned by `skilllibrary.TestCoreSkillsShipNoScripts` and the rest of `catalog_test.go` (frontmatter parses, name == directory, description carries triggers, referenced `scripts/` paths exist). (The Composio-based composio-toolkit + google-workspace skills were removed; connected services are reached via native connector tools.)
- **User skills** — created via the skill creator (below) or imported (ZIP/pasted SKILL.md), per-workspace, written to `<vault>/skills/<name>/SKILL.md` (+ `scripts/`), tracked in the `skills` table. Loaded from disk by `skillstore`.

At run time (`agentrunner.runCoderAgent`), the agent's declared skills' content is injected into the coder prompt's `<skill_instructions>` block. Core skill content comes from the embed (`skilllibrary.CoreSkillContent`); user skill content is read from disk. `resolveSkillBins` resolves the absolute path of every CLI tool a declared skill requires (`requires.bins` / `anyBins`: `$HOME/.local/bin/<bin>` then `PATH`) and `prompts.SkillEnvBlock` builds a `<skill_environment>` block telling the agent where each tool lives (or to install it via the cli-tool-installer skill) plus sandbox conventions (invoke by absolute path, use `$TMPDIR` not `/tmp`, secrets are env vars, vault root).

**Skill format.** `skills/<name>/SKILL.md` (required: YAML frontmatter + markdown body) + optional `scripts/` (deterministic code) + `references/` (on-demand docs). Only `name`+`description` are strictly required; `description` is the trigger — it must say what the skill does AND the contexts that activate it. Tool names are written BARE in the body (the runtime env block supplies the real path).

**Conversational skill creator** (`internal/skilldesigner`, driven by the SPA via the `/api/v1/skills/design` FSM endpoints and by chat platforms via `/skill`): mirrors `agentdesigner.Flow`'s shape — FSM (`StateIdle → StateAwaitingResume → StateDescribing → StateDesigning → StateVerifying → StateDone`), SSE progress, 7-day drafts (`skill_drafts` table, one per user), strict/forgiving approval triggers (same split as the agent designer). Flow:
1. Design conversation (text-only coder, `BuildSkillDesignSystemPrompt`) — focused Q&A, proposes a plan, asks for `approve`. Drafts are persisted on every turn so the conversation survives reloads/restarts (even on usage-limit).
2. Generation (`runGeneration`, `BuildSkillImplementationPrompt` with the `skill-creator` core skill body) — coder writes SKILL.md (+ `scripts/`) into a staging dir (`<vault>/skills/.staging-<name>/`, live folder touched only on approval), tests scripts, emits `[TEST_OUTPUT]`. Guardrails (`CheckEthics` + `RunToolGuardrails`) run on the actual generated content.
3. Vetting (`vetSkill`, `BuildSkillVettingPrompt` with the `skill-vetter` core skill body as the system prompt) — a second text-only coder call audits the skill for malicious behaviour (exfil of vault notes/USER.md/SOUL.md/secrets, raw-IP network calls, obfuscated payloads, sudo, destructive ops, …) and emits a structured report. `vettingBlocksSave()` parses the authoritative `Verdict:` line (a pure `❌ do not save` blocks save; an echoed `✅ safe to save | ⚠️ … | ❌ do not save` alternation does NOT — guards against a literal model echoing the option list). A blocking verdict keeps the user in `StateDesigning` and the skill is NOT saved. Covered by `flow_test.go`.
4. Approval → `SkillSaver.SaveSkill` writes SKILL.md+scripts/ to the vault, upserts the `skills` row (in-place overwrite if a skill of the same name exists; core-skill names are reserved and rejected), drops the draft + session, cleans up staging.

Nightly GC (in `serve`) sweeps expired skill drafts and their orphaned `.staging-<name>/` dirs alongside agent drafts.

### Conversational agent editing

Editing reuses the same `Flow` FSM via `DesignSession.IsEdit`. `loadAgentForEdit` reads live `AGENT.md` and reconciles its `# Suggested schedule:` line against the real `agent_schedules` row before the coder sees it.

**One chat surface.** `AgentEditPage` mounts the SAME `DesignerSurface` as creation from the first paint — there is no pre-screen. It used to render its own full-width chrome until the first reply landed and then swap in the surface, which jumped the layout to the designer's 10% gutter and showed no bubble (and no typing indicator) for the whole first coder round-trip. The only thing that pre-screen did is now a prop: **`startEndpoint`** routes the VERY FIRST message of a fresh session to `/api/v1/agents/:id/edit/start` (body `{message}` only — `startPayload` is the alternative way to open a session and is never merged in); every later message goes to `endpoints.design`, since a created edit session is indistinguishable from a create session. Two things make that work: `handleStartEditDesign` returns the FULL design-turn body (`web.designTurnResponse`, shared with `handleDesignChat`) — without `state` the stepper never leaves "Describe" and the Build button never appears, because nothing remounts into `GET /design/state` any more — and **`acceptRecoveredSession`** vetoes a recovered session that isn't this agent's edit (the design session is a per-workspace SINGLETON, so mount recovery would otherwise adopt an unrelated create conversation and offer to save the wrong agent). A vetoed session is treated as absent, SSE attach included, so another entity's build log can't stream into this page. `handleCancel` is likewise gated on a `sessionTouchedRef` — an untouched surface navigates without POSTing cancel, which would otherwise kill a stranger's in-flight build.

**Diagnose-before-fix flow** (`BuildEditImplementationPrompt` + the edit variant of `BuildDesignSystemPrompt`): the designer must DIAGNOSE the root cause in plain English to the user → CONFIRM the proposed fix → AWAIT APPROVAL, then the editor states the root cause + fix in code, applies only the targeted change, and fully re-tests, proving the original bug no longer occurs. Prevents superficial edits that don't fix the actual problem.

**Edit generation** runs in a sibling staging dir (`<agentID>-edit-staging`) — the live agent dir is never touched before approval. On approval, `updateAndFinish()` calls `AgentDesigner.UpdateAgent` → `db.UpdateAgentDescription` (UPDATE, not INSERT). `reconcileScheduleOnSave()` reuses the existing schedule row's ID to avoid duplicate rows and double-firing.

### Agent output protocol (AGENT.md)

- **`[CHAT]`** — sends a message to the user. A `[CHAT]` block runs until the next protocol marker (`[STATE]`, `[CALL]`, `[SILENT]`, a new `[CHAT]`) or end of output; **blank lines are part of the message, not a terminator** (an earlier rule ended the block at a blank line and silently dropped real content — fixed). Empty/whitespace-only `[CHAT]` blocks are dropped (never deliver a blank message).
- **`[STATE]...[/STATE]`** — JSON merged into `state.md`'s ```` ```json ```` fence (null = delete key); the heading, intro, and any `## Notes` prose are left untouched. Agents must not hand-edit the json fence directly, and should extend `## Notes` with a targeted edit rather than a full overwrite. Inline and multi-line forms accepted.
- **`[CALL: <agent-name>]`** — invoke another agent synchronously (max depth 3, cycle detection).
- **`[SILENT]`** — emitted alone as the last line by note-only/state-only agents that intentionally produce no user-facing message. Suppresses the prose-delivery fallback (see "Reliable delivery" below).

### Reliable delivery

Delivery does **not** depend solely on the coder emitting `[CHAT]` — models (especially basic ones) sometimes forget the marker and write the message as plain prose, or emit only reasoning. The runner (`runCoderAgent`, `parseCoderOutput`, `extractProseMessage` in `internal/agentrunner/runner.go`) guarantees:

1. **Empty `[CHAT]` filtered** — a blank marker never delivers an empty message.
2. **Prose fallback** — if no `[CHAT]` was parsed and `[SILENT]` was NOT emitted, the coder's prose output (protocol markers stripped) is delivered as the message, with a `no [CHAT] marker emitted; delivered prose as fallback` warning recorded.
3. **`[SILENT]`** — when present, the prose fallback is suppressed so silent agents aren't noisified by stray prose.
4. **Visible failure** — if a run succeeds but produces nothing deliverable and didn't signal `[SILENT]`, the user receives `⚠️ <agent> ran but produced no notification — see the run log.` instead of a silent success.

Delivery reaches both paths: `SendOutput` (durable — web → `gateway.SendToUser`, scheduler → chat platform) and `OnProgress` (live SSE). Parser behavior is covered by `runner_test.go`.

### Secret injection

Secrets stored encrypted in `secrets` table. Three sources of `MasterPw` at runtime:
- **Scheduled runs** — `scheduler.go` decrypts stored `EncryptedMasterPassword` (encrypted with `systemKey`).
- **Manual runs** — `handleRunAgent()` does the same. No password field on the run form.
- **Agent generation** — `Flow.WithSecretsLoader()` decrypts and injects via `WithExtraEnv(secrets)` so agents can make real API calls during validation. Same loader is wired on the skill creator (`skilldesigner.Flow.WithSecretsLoader`).

### Coder tool isolation

`internal/coder/coder.go` modifiers (all return a shallow copy):

- **`WithNoTools()`** — `--allowedTools ""`. Used for design conversation turns.
- **`WithAllowedTools(tools)`** — Required whenever `--setting-sources ""` is active or subprocess blocks on permission prompts. Agent generation: `"Bash,WebFetch,Read,Write,Edit"` (matches the run set, so builds do real end-to-end tests against live services); skill generation: `"Bash,Write,Edit,Read"`; runs: `"Bash,WebFetch,Read,Write,Edit"`.
- **`WithDir(dir)`** — overrides subprocess CWD. Used by generation AND every agent run — without it the agent writes to the shared per-workspace home and contaminates other agents.
- **`WithExtraEnv(env)`** — merges additional env vars. System overrides always take precedence.
- **`WithBackendType(t)`** — forces `"claude"`, `"generic"`, `"api"`, or `""` (auto-detect by binary name).
- **`WithAPIConfig(provider, model, baseURL, apiKeySecretName)`** — switches the coder to the in-process API engine (`coder_kind=="api"`). Once set, `Generate`/`Chat`/`Ping` dispatch to the tool-calling loop instead of a subprocess. **`WithSecretsLookup(f)`** attaches the lazy provider-key resolver (the API engine fetches its own key by secret name at run time, so every call site authenticates regardless of env injection); **`WithVault(v)`** attaches the vault for the host file tools; **`WithProgress(f)`** streams per-tool-call milestones to the run AND build SSE (agent + skill designer builds wire it too); **`IsAPI()`** reports the kind. `Ping(ctx, workspaceID)` now takes a workspace id (needed to resolve the API key).

**`CoderBackend`** (`internal/coder/backend.go`): one struct per coder — `claudeBackend` (JSON, `--setting-sources ""`), `opencodeBackend` (`run <prompt> --format json`, NDJSON events, XDG isolation — VERIFIED end-to-end on a real host incl. the success path: `ollama-cloud/glm-5.2` → reply; the reply text is nested in `part.text` for `part.type=="text"` events, not top-level `text` — parsed accordingly), and authored-unverified `codexBackend` (`exec --json`, `CODEX_HOME`), `geminiBackend` (`-p --output-format json --yolo`, `~/.gemini`), `cursorBackend` (`-p --output-format json --trust`); `genericCLIBackend` is the last-resort fallback. Each backend declares its own `configEnv` (per-workspace config-dir env vars) + `seedFiles` (operator auth seeded in; sessions/history are not). `coder.BackendForBin` maps a chosen binary to its backend type; `Coder.Smoke` is the fail-loud end-to-end check surfaced in coder settings. The API engine is not a `CoderBackend` — it bypasses the subprocess path entirely.

**Critical:** `--setting-sources ""` + no `--allowedTools` = subprocess hangs indefinitely (CLI engine only).

**Coder detection off Linux.** `DetectInstalled` takes a `detectHost` (GOOS, home,
`LookPath`, `Stat`, `Getenv`) rather than calling the OS directly, because there is
no macOS or Windows runner here and every bug it had was platform-specific.
`exec.LookPath` already honours `PATHEXT` on Windows, so a coder **on PATH** always
resolved; the fallback search is what was broken, in three separate ways. It looked
only in `~/.local/bin` — missing Homebrew's `/opt/homebrew/bin` (Apple silicon) and
`/usr/local/bin` (Intel), which matters because **a launchd-started process inherits
a minimal PATH containing neither**, so detection could fail for someone whose
terminal finds the binary without any trouble. It missed `%APPDATA%\npm` and
`%LOCALAPPDATA%\Programs` on Windows. And it gated every candidate on
`fi.Mode()&0o111 != 0`, a bit **Go never sets on Windows** (mode is synthesized from
file attributes), so the fallback there could not match anything at all — compounded
by statting the bare name when npm installs these coders as `claude.cmd` shims.
`coderSearchDirs` supplies the per-platform list and `binCandidates` expands
PATHEXT-shaped names on Windows only. The executable-bit test still applies on POSIX,
where it is real. `detect_platform_test.go` describes all three platforms against a
fake filesystem; the claim is that the logic is right and pinned, **not** that it was
run on a Mac.

**OpenCode requires an explicit model (multi-provider, no built-in default).** Unlike Claude (whose default model is tied to its login), OpenCode talks to many providers and has NO default model of its own. When none is specified it targets a hardcoded default provider (**OpenRouter**) and returns `coder error: User not found. (status 401)` if that provider isn't authed — the failure looks like broken auth but is really a missing model. The model comes from the workspace's `CoderModel` field, passed as `opencode run … -m <provider/model>` (e.g. `ollama-cloud/glm-5.2`); `opencodeBackend.buildArgs` adds `-m` only when `CoderModel` is set. **The per-workspace sandbox redirects `XDG_CONFIG_HOME` to an empty dir, so OpenCode does NOT inherit the operator's `~/.config/opencode/opencode.json`** (its default model, `plugin` list, and `mcp` servers such as `oh-my-openagent` / `codebase-memory-mcp`) — only the seeded `~/.local/share/opencode/auth.json`. Consequences: (a) setting a default model in the host `opencode.json` does NOT reach workspaces — the model must come from `CoderModel`; (b) host plugins/MCP intentionally do not run inside the sandbox. Re-authing a provider (`opencode auth login`) does not change the default-model selection, so it alone never fixes the 401.

### API coder engine (`coder_kind == "api"`)

A workspace can run its coder as a **direct LLM provider API** instead of a host CLI binary. `Coder.runAPI`/`runToolLoop` (`internal/coder/api_engine.go`) drive an in-process loop: `Complete → execute host tools against the vault → feed results back → Complete`, until the model emits a final answer (no tool calls), the turn budget is spent, or the deadline passes. The model's final text carries the same `[CHAT]`/`[STATE]`/`[SILENT]` protocol markers, so the runner's parser is unchanged.

- **Host tools** (`hosttools.go`, `hostToolSet`): `read_file`/`write_file`/`edit_file`/`list_dir` are vault-path-safe (relative to workDir/vault root, escapes rejected). Two **always-on read-only discovery tools** (NOT exec-gated — safe in chat, closing the API-chat gap with the CLI chat's `Grep`/`Glob`): `search_files(query)` exposes the existing `vault.Searcher` (ripgrep + pure-Go fallback, case-insensitive fixed-string, 5 matches/file, skips `.kb`) so "find the note where I mentioned the dentist" is a TIER-1 lookup instead of a `read_file` walk; `glob(pattern)` finds files by name/pattern (`*`/`?`/`**`) across the vault via `compileGlob`→anchored regexp, skipping dotfiles + `.kb`; an **absolute-within-vault** path passed as the pattern is relativized first (mirror `resolveVault`) so a weak model that types the full vault path still matches, and an absolute path outside the vault is rejected. Both search the **whole vault root** (not workDir) and return a non-`error:` empty-result notice on no matches (so they never trip the oscillation guard). Three **exec tools** are gated behind `includeExecTools` (agent builds+runs only — workDir ≠ vault root; excluded from chat for CLI-parity): `run_script` (`python3`) and `bash` both run sandboxed via Landlock (`buildScriptCommand`) with the agent's secrets in env (provider key stripped), reporting stdout+stderr on failure; `web_fetch(url)` is an HTTP(S) client in the **host process** (no sandbox — it adds no capability agents lack via run_script/bash) that returns text (HTML reduced to readable text via a stdlib stripper), **retries transient 429/5xx/network internally** so a blip never trips the loop-guard, and **cannot carry secrets** (authenticated calls use run_script/bash); `web_search(query)` is the discovery complement — a keyless DuckDuckGo HTML scrape (`ddgHTMLEndpoint`, browser `User-Agent`) returning numbered title/url/snippet entries (real URL decoded from the `uddg` redirect param via `parseDDGResults`/`decodeDDGRedirect`, HTML stripped), with the same transient-retry contract as `web_fetch` and a 200-but-no-results page yielding `"(no search results)"` (non-error) so the model falls back to `web_fetch` without tripping the guard. `ddgBaseURL` (empty→production) lets tests point at an httptest server. All results are byte-capped and never empty (an empty tool result breaks strict serializers). This closes the CLI-vs-API capability gap: a simple public fetch/find is now TIER 1 via `web_fetch`/`web_search`/`search_files`/`glob` (see the network-split + file-discovery tier guidance in `prompts.agentArchitectureGateBlock`), matching a CLI coder. **Caveat:** an arbitrary `bash` string is sandboxed but NOT AST-scanned the way an authored `tools/*.py` is at build.
- **Turn budgets**: `maxAPITurns` (25) for runs/chat; `maxBuildAPITurns` (40) + `buildMaxTokens` (8192) for builds. A budget-exhausted loop gets one grace turn to wrap up: `[BLOCKED]` for a build (parsed by the designer), plain language for a run/chat.
- **Build-time script verification** (weak-model hardening, build only): the engine refuses to "finish" a build while the model authored a helper script that never once returned real output — `verifyFinishNudge` drives it to run/inspect/fix (bounded by `maxVerifyNudges`), or report the failure in plain language. Plus a loop-guard (`recentFails` ring + `consecutiveFails`) that short-circuits repeated/oscillating failing calls.
- **Script-verification bridge → `coder.Result`.** The engine tracks per authored `tools/*.py` whether it RAN with real stdout (`hostToolSet.producedOutput`) and captures that stdout (`lastVerifiedOutput`, secret-redacted via `redactSecrets`). `runToolLoop` surfaces this ground truth on `Result.ScriptVerified` / `Result.ScriptOutput` (+ `Result.ScriptRan` = an authored script was executed at least once, for observability). The agent designer's `decideBuildOutcome(workDir, resultText, backendType, scriptVerified, scriptOutput)` **trusts the engine** instead of re-deriving verification from a `[TEST_OUTPUT]` marker the weak model often forgets: an engine-confirmed run advances to review showing the real captured output as the sample, and the weak-backend gate (`BackendToolCalling && hasAuthoredScript && thinProof && !scriptVerified`) only fires when the engine did NOT confirm a run — fixing the false "I couldn't confirm the helper it wrote actually runs." When that gate DOES fire, the `agentdesigner: build not presentable` slog carries `script_ran` to discriminate "ran but produced nothing" (broken/outbound-blocked) from "never ran". Fields are zero for CLI coders and runs/chat. Covered by `build_outcome_test.go` + `api_engine_test.go`.
- **Design conversations vs one-off chat** (`Chat` split by `noTools`): `chatAPI` (text-only single completion, real alternating user/assistant turns so the model doesn't re-ask its opening question) vs `chatToolsAPI` (adds the host file tools, minus the exec tools `run_script`/`bash`/`web_fetch`, for on-demand KB read/write — parity with the CLI chat's file-only set).
- **Providers** (`internal/llm`): `openai`, `openrouter`, `anthropic`, `generic` (any OpenAI-compatible endpoint; base URL required). Not probed — always available in the settings picker via `coder.APIProviders()`.

### Usage-limit / rate-limit detection

`coder.ErrUsageLimit` — CLI: non-zero exit with empty stdout+stderr; API: provider 402 (credits/quota exhausted, `ErrQuotaExhausted`). `coder.ErrRateLimited` — API transient 429 that didn't clear within the retry budget (distinct so the message says "try again in a moment", not "out of quota"). `coder.ErrAPIAuth` (bad/missing key) and `coder.ErrMaxTurns` (budget exhausted) are config/run errors, not usage limits. `agentrunner.FriendlyRunError` converts each to a user-facing message sent via `input.SendOutput` on every run failure. Also handled softly during generation and design conversation turns. API token usage is accumulated across the loop (`coder.Usage`) and persisted per run.

### Guardrails

`internal/agentdesigner/guardrails.go`:
- `CheckEthics(code, "")` — blocklist (rm -rf, drop table, bitcoin wallet, etc.). Used on AGENT.md.
- `RunFullGuardrails(code, profile)` / `RunToolGuardrails(filename, code, profile)` — ethics + AST, where
  `profile` is `ProfileAgentTool` or `ProfileSkillScript`. Both ban `eval`, `exec`, `compile`,
  `__import__`, `os.system`, `os.popen`, the `os.exec*`/`os.spawn*` family, `socket.socket`, and any
  `shell=` keyword that is not provably `False`. They differ on one axis: `ProfileAgentTool` (an agent's
  `tools/*.py`) bans `subprocess.*` outright, while `ProfileSkillScript` (a user skill's `scripts/`)
  ALLOWS list-form `subprocess` so a skill can drive an installed CLI tool — and additionally rejects any
  `**` spread into a `subprocess.*` call, since the checker cannot prove `shell` is absent from it.
  The AST check is a best-effort filter, NOT a security boundary (an aliased `import subprocess as sp`
  defeats the receiver-keyed rules); Landlock and the skill-vetter audit are the actual enforcement.
  `skilldesigner.guardrailsForGeneratedFile` routes `.md` files to the doc-ethics profile so a reference
  doc describing a destructive command isn't blocked as though it executed one.

### Per-workspace coder isolation

- `CLAUDE_CONFIG_DIR` → `<data_dir>/claude-homes/<workspaceID>/.claude/`
- `HOME` → `<data_dir>/claude-homes/<workspaceID>/`
- `--setting-sources ""` — suppresses `settings.json` and all `CLAUDE.md` traversal.
- `.credentials.json` copied from operator's `~/.claude/` on every invocation.

**`ANTHROPIC_CONFIG_DIR` does NOT work** — only `CLAUDE_CONFIG_DIR` redirects config.

### Coder filesystem confinement (Landlock)

`internal/sandbox` adds preventive filesystem confinement via Linux Landlock LSM. No external deps, no setuid, no namespaces.

**Mechanism:** `coder.buildCommand()` wraps the real command as `rookery __sandbox-exec <base64-spec>`. The helper applies `landlock.V5.BestEffort().RestrictPaths(...)` then `syscall.Exec`s the real command. Inherited by all children (`claude`→`bash`→`python`).

**Allowed:** RW: per-workspace HOME + agent workdir. RO: system paths, coder binary dir, the `rookery` binary dir (so a confined CLI coder can exec `rookery connector exec`), the workspace's vault root. Denied: SQLite DB, config.yaml, other workspaces' vaults.

`config.SandboxConfig.Enabled` (default true; `ROOKERY_SANDBOX=0` disables). With Landlock unavailable, the sandbox is not applied and nothing physically prevents writes outside the vault — agents/chat run trusted within the user's own vault.

### Database

SQLite via `modernc.org/sqlite` (CGo-free). WAL mode + foreign keys set on open. Migrations in alphabetical order from `migrations/`.

The base schema was consolidated into `migrations/001_initial_schema.up.sql` during the workspace
refactor (the old incremental migrations were collapsed; data was wiped and re-created fresh);
incremental migrations resume from there — `002_coder_api` adds `workspaces.coder_base_url`, and
`003_agent_runs_usage` adds `agent_runs.{prompt,completion,total}_tokens` for the API coder; `005_connectors` adds the self-managed-OAuth tables; `006_connection_extra` adds `service_connections.extra` (JSON); `007_draft_used_connections` adds `agent_drafts.pending_used_connections` (persists build-used connections for auto-bind).
Tables: `owner` (single row), `workspaces` (replaces `users`; carries `about` + inlined coder
config: `coder_kind`/`coder_bin`/`coder_timeout_s`/`coder_backend_type` + the now-active API-coder
fields `coder_provider`/`coder_model`/`coder_api_key_secret`/`coder_base_url`), `platform_connections`,
`platform_identities`, `agents`, `agent_schedules`, `agent_runs`, `secrets`, `chats`, `reminders`,
`workspace_permissions` (was `user_permissions`), `mcp_servers`, `workspace_settings` (was
`user_settings`), `system_settings` (owner/system-level, not tenant-scoped, no FK),
`audit_logs` (records active `workspace_id`; owner is the implicit actor), `schema_migrations`,
`chat_messages` (FK `chat_id`→`chats`), `skills`, `agent_skills` (keyed by `(agent_id, skill_name)`),
`service_provider_configs`/`service_connections`/`agent_connections` (self-managed-OAuth connectors — all secret columns encrypted under the system key),
`agent_drafts`/`skill_drafts` (one row per workspace; 7-day TTL). Every tenant table keys off
`workspace_id`. There is **no** `coders` table — coder config is inlined on `workspaces`.

### Web UI routes

**The server-rendered template UI has been deleted (big-bang cutover).** There are now exactly
**two** HTTP surfaces: the embedded **React SPA** at `/` and the **`/api/v1` JSON API**. All the old
`/dashboard/*` and `/admin/*` HTML routes, the `TemplateRenderer`/`setupTemplates`/`parseTemplates`
machinery, the `web/templates/` + `web/static/` directories, and their `templates_dir`/`static_dir`
config plus the two environment overrides that fed them are gone. The SPA talks to the JSON API for
everything.

#### Design system (tokens first, not per-page)

**The type scale, radii and colours are remapped in `index.css`'s `@theme inline`, never at call
sites.** Tailwind v4 resolves every `text-*` utility from a `--text-*` token, so raising the scale in
one file grew all ~405 existing `text-xs`/`text-sm` uses at once. Two traps: **each `--text-X` must
be set together with its `--text-X--line-height` partner** (size alone leaves line-height pinned to
the old metric, making text *cramped* rather than more readable), and **raising `body { font-size }`
does not do this** — `text-sm` is an absolute rem value, so the body rule only reaches elements
carrying no `text-*` class. `density.test.ts` fails the build on any `text-[<n>px]` literal, because
a hardcoded pixel size is immune to the token remap and stays small forever.

**`contrast.test.ts` computes WCAG ratios out of the stylesheet itself**, in both themes, for
`--foreground`/`--muted`/`--muted-2` and for `--ok`/`--warn`/`--danger` against `--background`,
`--chrome` **and their own `-soft` fill** (the tightest constraint). This exists because an earlier
review measured `--ok` at 3.68:1 on `--ok-soft` and darkened it — a manual finding that nothing
stopped the next palette edit from undoing. Changing a colour token means running this.

**`button { cursor: pointer }` is restored in an `@layer base` rule.** Tailwind v4's Preflight
dropped it to match the browser, and a `<button>`'s browser default is `cursor: default` — so 54 raw
buttons across the app hovered as if inert. It read worst in the KB pane, where `FileTree`'s rows opt
into `cursor-pointer` explicitly while search results did not, which is how "KB search finds the
pages but they are not clickable" was reported. The clicks always worked; the affordance never did.
Fixed once in base so new buttons inherit it.

**The UI font is vendored at `internal/fonts/InterVariable.woff2` — one copy, two consumers.** It is
its own Go package because `go:embed` cannot reach outside its own directory, and both the Go export
path and the SPA (via the `@fonts` Vite alias) need the same bytes; a second checked-in copy would
drift silently. A CDN `@import` is not an option — Rookery ships as a single binary for offline/LAN
installs. It is declared in **three** places: `index.css` (`--font-sans`), `pages/kb/editor.css`
(explicitly, so a future body-style change cannot silently drop the KB editor back to a system font),
and `internal/export/html.go`, which **base64-inlines it as a `data:` URI** rather than naming it —
`ToPDF` shells out to a headless renderer on the *server*, which will not have Inter installed, so a
named font would silently fall back while still reporting success. DOCX can only *name* the font
(embedding in OOXML is out of scope), which is recorded in `docx.go` as a stated limitation.

**`lib/entityIcons.tsx` is the single icon map**, read by the rail, `PageTitle`, the command
palette's kind labels and the settings nav. Rules: lucide only, `currentColor` always (never coloured
except `text-danger`/`-warn`/`-ok`), `size-4` inline / `size-5` in a page title. The one exception is
`components/brand/ProviderLogo.tsx`, which keeps brand colour — a monochrome Slack mark is harder to
recognise than a coloured one. Before this map, `SettingsPage` held **emoji strings** for its section
nav while every other surface used lucide, which is the whole reason settings looked "coloured and
everything else grey"; a test fails the build if emoji return.

**Brand logos are generated by `scripts/vendor-brand-logos.sh`; never hand-edit
`web/ui/src/assets/logos/`.** A hand-edit is silently lost on the next run, and the run rewrites the
whole manifest, so check `git status` after and revert incidental upstream churn (simple-icons
redraws marks — Threads has already changed once). Four properties worth knowing:

- **`inline_class_styles` runs before the `<style>` strip, and is not optional.** The strip itself is
  required (these files are inlined with `dangerouslySetInnerHTML`), but Illustrator and Inkscape
  export marks as `class="st2"` plus a stylesheet — so stripping it left every classed element at the
  SVG default `fill: black`. That silently shipped **six** broken marks: llama.cpp was a solid black
  square (its background `<rect>` is `.st2`), frankfurter a black blob, Google Ads and Google
  Analytics black silhouettes, Gotify a black blob, and Open Library had lost three stroke paths.
  Every one passed the existing tests. The inliner handles selector LISTS (`.p, .s {…}` — reading
  only the last class leaves the other black) and skips `@media` blocks (frankfurter ships a
  `prefers-color-scheme: dark` rule that would invert it, and the tile is always white).
  `TestBrandLogoAssetsCarryNoDanglingClasses` fails on any surviving `class=`.
- **A mark can pass every structural test and render invisibly.** `ProviderLogo` draws on a WHITE
  tile, so lobehub's `-color` variant for Kimi — a white mark on a transparent field, meant for
  Moonshot's own blue container — showed an empty square with a speck. Prefer the monochrome variant
  when a brand offers one; the tile pins `#18181b` for `currentColor`.
  `TestBrandLogoMarksAreVisibleOnTheTile`
  catches only the TOTAL case, and says so: deciding the partial case needs rendered AREA, which the
  source cannot give you (Hacker News draws its full-canvas background as the 18-byte path
  `m4 4h188v188h-188z`, so any length heuristic ranks a correct mark below a broken one). Partial
  cases are pinned per brand instead.
- **Removing a provider means removing its manifest line too.** Fitbit and Zoom were deleted on
  purpose, but their lines survived and a re-vendor silently recreated both logos.
- **Size is paid on render**, since every logo is inlined. LocalAI's genuine square vector is a
  41-path illustration that alone grew the `ProviderLogo` chunk from 286 KB to 376 KB, so it takes
  the `UPSTREAM_PNG_LARGE` path — the publisher's own 1024px raster downscaled to 128px (~17 KB),
  still 3-4× the rendered tile.

**Buttons**: `default` = primary, `outline` = secondary, `ghost` = tertiary/inline, `destructive` =
removes data. Every *action* button carries a leading icon, with two deliberate carve-outs because
the blanket rule reads worse: dialog footer **pairs** (Cancel/Save) and the `link` variant stay
text-only. A destructive *confirm* keeps its icon — there the icon is the warning.

**`PageContainer`** (`mx-auto w-full max-w-[1600px] px-8 py-6`) is the one page wrapper; it replaced
four independent hardcoded widths that centred content and left ~900px empty on a 1920px display.
`mx-auto` only bites past the cap, so a 1440px viewport is genuinely fluid. It keeps `px`/`py`
separate rather than a `p-*` shorthand **on purpose**: tailwind-merge treats `p` and `px` as
different groups, so `cn("p-8","px-[7%]")` keeps BOTH and lets stylesheet order pick the winner —
the same trap CLAUDE.md records for `ChatScroll`. The KB editor relies on overriding it (`px-[7%]`,
applied to **both** the WYSIWYG container and the raw textarea; changing only one makes switching
modes jump sideways). Forms still cap their own field column (~640px) — a 1500px text input is worse,
not better.

**`PageTitle`** owns only the heading *group* (icon + `<h1>` + optional subtitle), not the whole
header row: pages already have their own search boxes and actions, so scoping it to the part that was
inconsistent made it adoptable at all 16 sites without restructuring them.

**`DialogContent`'s width cap must stay unprefixed, and its grid must stay
`grid-cols-1`** (`dialog.test.tsx` pins both). The base used to end
`sm:max-w-lg`, a different tailwind-merge conflict group from a caller's
`max-w-2xl` — so both survived the merge and the responsive one won at ≥640px,
pinning **every dialog in the app** to 512px regardless of what it asked for.
The same merge ate `max-w-[calc(100%-2rem)]`, so the small-viewport inset now
lives on `w-`, not `max-w-`. Separately, with the implicit `auto` grid track a
grid item's automatic minimum size is content-based (CSS Grid §6.6), so one wide
non-wrapping child — the KB icon picker's category tab strip, ~880px of
max-content, whose own `overflow-x: auto` does **not** zero its min-content
contribution — stretched the track and every sibling with it straight through
the side of the modal. `grid-cols-1` emits `repeat(1, minmax(0, 1fr))`, whose
`0` min sizing function contains it; no `min-w-0` is needed. This is the third
recorded instance of the tailwind-merge group trap (see `ChatScroll` and
`PageContainer` below).

**The side slide-over is `w-[clamp(400px,33vw,720px)]`, set in BOTH `sheet.tsx` and `AppShell`** —
the width used to live in two places and had already drifted (`sm:max-w-sm` vs `sm:max-w-md`). A test
asserts they agree. `AppShell`'s `p-0 gap-0` on the content well stays: panel content owns its inner
padding, and a shell-level `p-4` would double chrome for a full-height embed like the global chat.

**The shell root is `h-screen overflow-hidden` and the KB editor pane is `overscroll-contain`.**
The shell is a fixed-height frame in which every scrolling region is explicit, and both halves are
load-bearing. Without them, a long KB note propagated its scrollable overflow to the initial
containing block (`documentElement.scrollHeight` 2359 against a `clientHeight` of 900), so wheeling
once more with the editor pane already at its bottom chained out to the document and dragged the
icon rail and context pane off the top — measured `documentElement.scrollTop` 0 → 1459, rail `top`
0 → −1459. It read as "scrolling past the note into blank background", because that is the page
behind the shell. `overscroll-contain` stops the chaining; `overflow-hidden` is what makes the
document unscrollable at all (setting `scrollTop` directly still moved the rail otherwise).
**jsdom has no layout engine and no scrolling**, so the vitest suites can only assert the two
declarations are present — `scripts/verify-kb-layout.py` drives a real browser and asserts the
behaviour, and is the only thing that can. Two plausible-sounding theories were disproved by that
harness and are recorded in the spec: the blank band below a short note is NOT inert (clicking it
already places a caret, which is what `min-height: 60vh` is for), and `scrollIntoView` does NOT
chain to ancestors here.

**The KB slash menu measures itself before placing, and re-measures on a `ResizeObserver`.**
`placeMenu` (`pages/kb/SlashMenu.tsx`) is a pure function — caret rect, menu size and viewport in,
`{left, top, maxHeight}` out — precisely because jsdom returns zeros for every rect, so a test
driving the real popup can prove it OPENS but never WHERE it lands. That gap is how the original
shipped with `top = caret.bottom + 4` and no bounds check: with the caret on the last line of a long
note the 442px popup rendered 410px below the fold. The observer is equally load-bearing —
`ReactRenderer` has not laid the list out when `onStart` appends the element, so the first measure
reads height 0, zero fits below any caret, and the placement is wrong exactly when it matters. All
listeners read the LATEST suggestion props, since `@tiptap/suggestion` hands out a fresh
`clientRect` closure per update and they fire outside those calls.

**Owner settings is five separate sections, not one stacked page** (`owner-workspaces`,
`owner-instance-url`, `owner-system`, `owner-backup`, `owner-audit`), under an `OWNER` group in the
settings pane nav; `?section=owner` redirects to the first. Each mounts `OwnerGate` **independently**,
which costs nothing extra: the gate's probe is a react-query on the shared `["admin","overview"]`
key, so five mounts share one request, and one unlock covers all five because **the server owns the
verification stamp** — the component is the affordance, not the protection. A test asserts every
owner slug renders gated, so a missed wrap fails the build rather than quietly exposing an
install-level section.

**Emoji are generated, not curated.** `scripts/gen-emoji.mjs` turns a vendored Unicode data file into
`pages/kb/emojiData.generated.ts` (1906 emoji, 9 standard groups, keyword search) with **zero runtime
dependencies** — emoji-mart was the escape hatch the old curated file itself named, but a ~200 KB
runtime dep with its own styling to theme is a poor trade for data. The generated file is
**committed** so the release build never runs the generator, and `emojiData.test.ts` re-runs it and
compares, so a stale commit fails CI instead of shipping an old set.

**Workspace presets are 36 inline SVGs** (`lib/workspaceIcons.tsx`) — eight renderings of the
Rookery mark in the brand's hues, then 28 gradient-plus-motif tiles, all legible at the 20px the
rail actually renders. `web/api_settings.go`'s `workspaceIcons` validator must list the same slugs,
and `TestWorkspaceIconSlugsMatchTheSPA` parses the TSX to assert it: a preset added only to the SPA
400s on save, one added only to Go has no artwork, and neither failure is visible in either file
alone. **Custom upload is deliberately not built** — it is the one item needing a multipart
endpoint, an `iolimit` cap, MIME sniffing (SVG is an XSS vector), vault storage and a two-shape icon
field.

`DEFAULT_WORKSPACE_ICON` (`"rookery"`) is what an **unset** icon renders — it used to be the
workspace name's initial on a solid square. An **unknown** slug still falls back to that monogram,
and the distinction is deliberate: an unknown value means a workspace configured by a NEWER build,
where rendering the default would silently present it as the user's choice. The 28 motif slugs and
motifs are frozen (a rename orphans every workspace that stored it); the 2026-08 palette pass
re-derived only their gradients onto one ember-compatible recipe, and kept hue SEPARATION on purpose
— telling workspaces apart at 20px is the only job these have, so pulling them all toward orange
would defeat it.

**The Rookery mark lives in `components/brand/RookeryMark.tsx`, drawn inline.** `RookeryMark` strokes
in `currentColor`; `RookeryTile` is the tile form, whose glyph is painted the explicit brand cream
because a tile supplies its own background and an inherited foreground would vanish into the fill
(its gradient `id` is a prop for the same reason `WorkspaceAvatar` derives one — two on screen with
the same id make the second reference the first's gradient); `RookeryLogo` is the mark-plus-wordmark
lockup, whose tight gap is load-bearing, since mark and word read as one logo only while they sit
closer to each other than to anything else. It is a component and not an `<img>` because **an image
cannot inherit `currentColor`** — that is exactly how the documentation site's mark ended up painting
black and disappearing on the dark theme. `public/favicon.svg` stays a separate file (a browser tab
needs a real one) and is the one copy that cannot be generated from the component. Sign-in and
workspace selection carry the mark because they are the only two screens outside the app shell,
where the rail's branding is absent.

**Shell primitives** (`web/ui/src/components/shell/`): every page renders inside `AppShell` —
an icon rail + list panel + a `ContextPane` slot. The context pane is user-resizable —
`usePaneWidth`/`PaneResizeHandle` (`usePaneWidth.tsx`) persist a 200–560px width to `localStorage`
(`sa.paneWidth`; a corrupt or out-of-range stored value falls back to the 256px default rather than
being clamped), draggable via pointer events or fully keyboard-operable (`role="separator"`, arrow
keys step 16px, Home/End jump to the extremes, double-click resets). `ContextPaneHeader`/
`ContextSection` (`ContextPaneParts.tsx`) are the shared title/section primitives all five context
panes (Home, Chats, Connections, KB, Settings) are built from, so heading case/padding/the header's
bottom border don't drift per-page. `ToastProvider`/`ToastHost` (`Toast.tsx`) is the app's toast
system and its one `aria-live="polite"` region — mounted once regardless of whether a toast is
showing, so screen readers don't miss the first announcement; a toast carries an optional action
(e.g. "Undo") and auto-dismisses after 5s. `useDeferredDelete` (`lib/useDeferredDelete.ts`) builds
the inbox's and reminders' delete-with-undo on top of it: clicking delete hides the row immediately
and shows an Undo toast, but the real DELETE call is deferred 5s — it fires only on expiry (or on
`beforeunload`/route-away, which flush every pending delete so none is silently dropped), never on
click; Undo cancels the timer so the call is never made at all. No soft-delete schema needed — the
"delete" is a pending client-side timer, committed or cancelled.

Home's inbox (`pages/home/HomePage.tsx`) groups notifications under calendar-day headers
(Today/Yesterday/`Weekday, D Mon`, bucketed in local time), flags a failed agent run with a "Failed"
status badge, marks unread rows with a left accent bar, and deep-links each card to its source agent;
deleting a message or a reminder goes through the deferred-delete/undo flow above.

**Chat message chrome.** Every `ChatMessageBubble` (`components/chat/Bubbles.tsx`) renders a
`MessageMeta` footer — a `Day HH:MM` timestamp plus a copy-to-clipboard button. The footer is
**always mounted** and revealed purely by opacity (`opacity-0 group-hover:opacity-100
focus-within:opacity-100`); mounting it on hover would insert a node under the cursor and cancel an
in-progress drag-select, so `select-none` is scoped to the footer row and never applied to the
message body. **Designer turns are timestamped too**, so the two chat surfaces read identically: both
`Flow`s stamp `db.ChatMessage.CreatedAt` on every history append, and `web.designHistoryDTO` (the one
mapper behind the agent resume/state and skill resume handlers) emits it as a `created_at`
RFC3339Nano **string** — a `time.Time` DTO field would defeat `omitempty` (a no-op on structs) and
stamp pre-timestamp drafts year 1. Turns appended client-side (the optimistic user bubble, each
assistant reply, the resume message) are stamped in the browser via `nowStamp()`, because the design
endpoints return prose, not a transcript row. `createdAt` stays optional: an old draft's turn simply
omits the field and the footer degrades to the copy button alone. The **timezone**
reaches the footer as CONTEXT (`lib/timezone.tsx`: `TimeZoneProvider` at the app root,
`useTimeZone()` at the leaf) rather than a `useSession()` call inside the bubble — the bubble is
mounted in places with no `QueryClientProvider` above it, where `useQuery` would throw; an undefined
context degrades to browser-local. `formatMessageTime` (`lib/utils.ts`) wraps `Intl` in a try/catch
because `profile.Timezone` is free text (`""`/`"CEST"`/`"UTC+2"` all throw `RangeError`), and a throw
during render would blank the whole conversation. Opening a chat from `ChatsPage` **auto-resumes it
once per open** if it is stopped (the chip is presentational — `handleChatMessage` never checks
`chat.active`) and focuses the composer; the decision is latched in a ref on the FIRST detail load of
the mount, before the active check, so a later manual Stop sticks.

**Copying a message works without a secure context.** `navigator.clipboard` exists ONLY in a secure
context (https, or localhost) — and the normal way to reach a self-hosted install is plain HTTP on
the LAN (`http://<host>:8080`), where it is `undefined` and reading `.writeText` off it throws. So
`MessageMeta.copy()` guards with `navigator.clipboard?.writeText` and falls back to
`document.execCommand("copy")` via an off-screen (NOT `display:none` — a hidden node is unselectable
and copies nothing) textarea, restoring the user's own selection afterwards. When both paths fail the
button shows a "Copy failed" state: the earlier silent no-op is precisely why the broken button went
unnoticed.

**Chat gutters (the 10% column).** On the full-page chat surfaces — `ChatsPage` and both designers via
`DesignerSurface` — the messages and the composer share one column inset 10% on each side
(`ChatScroll className="px-[10%]"` + `<Composer gutter>`). The ~448px slide-over panel opts out
(`ChatWindow compact`). No rule is drawn above the composer: the design is deliberately unframed.
Two traps live here:
- `ChatScroll`'s base padding is `px-4 py-4`, **not** the `p-4` shorthand. tailwind-merge treats `p`
  and `px` as different groups, so `cn("p-4", "px-[10%]")` keeps BOTH classes and leaves the winner to
  the generated stylesheet's ordering — the composer would inset while the bubbles did not. Two `px-*`
  classes are one group, where the last provably wins.
- A page-level composer registers as the docked bottom bar (`components/shell/dockedComposer.tsx`)
  so `AppShell` lifts the floating action buttons above it; otherwise they sit on top of the Send
  button. The 10% gutter alone only clears them above ~1100px viewport width. That context lives in
  its own module purely to break an import cycle (`Composer → AppShell → GlobalChatButton →
  ChatWindow → Composer`). The registration is COUNTED, not a boolean: on a route change the incoming
  composer mounts before the outgoing one unmounts.

**Session `timezone`.** `GET /api/v1/auth/session` carries the active workspace's profile timezone
(`""` when unset or no workspace entered). It lives here rather than on `/api/v1/settings` because
the SPA already loads and caches the session once, while the settings endpoint re-probes the host
filesystem for installed coders on every call.

```
/                        # embedded React SPA (index.html); every unmatched deep path falls through
/*                       #   to the SPA catch-all (client-side routing). 503 if built without `make ui`.
/app, /app/*             # 301 → the same path with /app stripped (legacy; SPA moved from /app to /)
/dashboard/connectors/services/callback/:provider   # GET: OAuth callback — the ONE non-SPA, non-API
                         #   route. Registered standalone (guarded requireOwner → requireActiveWorkspace
                         #   → requireSetupComplete). FROZEN: this exact path is the redirect URI
                         #   registered in external OAuth apps, so it must never change. Finishes with an
                         #   HTTP redirect back to the SPA connections page (/connections?...), not JSON.
```

#### `/api/v1` (JSON API — the SPA's only backend)

The JSON API is the whole application surface (spec §12). The authoritative, exhaustive route
inventory is the `want` table in `web/api_parity_test.go` (`TestAPIParityInventory`) — a merge gate
asserting every planned route is registered via `s.echo.Routes()`; consult it directly rather than
duplicating the full list here. Route groups:

- **auth** — session, login, logout, change-password
- **workspaces + admin** — list/create/enter/leave/delete workspaces, permissions, admin overview/audit/settings
- **agents + design** — CRUD, run + run-progress SSE, schedule, agent-md, skills, connections, and the full conversational design FSM (design/cancel/resume/dismiss/progress/state, edit/start)
- **skills** — CRUD, core-skill read, and the conversational skill-design FSM (design/cancel/resume/dismiss/progress)
- **secrets** — list/create/delete
- **connectors** — chat-platform connections (Telegram/Discord/Slack): list/create/delete/test
- **services** — self-managed-OAuth service connections: list, per-provider creds/connect/apikey, delete
- **chats** — CRUD, messages, resume/stop
- **reminders + inbox** — reminders CRUD + poll; inbox list/poll/read/read-all/delete
- **kb** — tree, note read/write/new/delete/rename, search, raw, resolve, selection assist (AI actions)
- **settings + setup** — profile/workspace/coder/master-password settings, coder test, setup wizard
- **search** — global search

The embedded SPA is served at `/` (see above); `/app` + `/app/*` 301-redirect to their `/app`-stripped
equivalents. Serving/redirect wiring lives in `web/spa.go` (`setupSPARoutes`), not the JSON API group.

**A slice field on a DTO must never marshal to `null`.** A Go nil slice becomes JSON `null`, and a
TypeScript default parameter (`requires = []`) substitutes only for `undefined` — never for `null`.
`flattenRequires` (`web/api_skills.go`) returned `var out []string`, so every core skill declaring no
tooling served `"requires":null` and `requires.length` threw, unmounting the whole route with
"Unexpected Application Error". It reproduced on every built-in skill and on no user skill, because
those happened to declare requirements — which is exactly the shape that makes it look like a
frontend bug. Initialise with `[]T{}` on the server AND normalise with `?? []` at the consumer; a
test asserting on the RAW response bytes is the one that catches it, since decoding into `[]string`
erases the distinction.

### Owner vs. workspace separation

The two-level session (`owner_id` logged in + `active_workspace_id` entered) is unchanged; only the
guard mechanism moved to the JSON API now that the template routes are gone.

- **Owner-scoped** endpoints (`/api/v1/admin/*`, workspace management) are guarded by `requireOwnerAPI`
  (session `owner_id` → `c.Set("owner")`, 401 JSON if absent).
- **Workspace-scoped** endpoints (agents, secrets, connectors, chats, reminders, KB) add
  `requireActiveWorkspaceAPI` (session `active_workspace_id` → `c.Set("workspace")`, 403 `no_workspace` JSON if none)
  + `requireSetupCompleteAPI`. Handlers read `c.Get("workspace").(*db.Workspace)`.
- The template middlewares `requireOwner` / `requireActiveWorkspace` / `requireSetupComplete`
  (redirect variants, in `web/server.go`) still exist but now guard **only** the standalone OAuth
  callback route (the one browser-facing, non-API endpoint that needs workspace context).
- Entering/switching + leaving are JSON-API actions (`POST /api/v1/workspaces/:id/enter` /
  `.../leave`). `verifyWorkspaceMasterPassword` (shared core in `web/handlers_admin.go`) decrypts the
  workspace's `encrypted_master_password` with the system key and compares to the typed one (an access
  gate — the stored form must remain so the scheduler can decrypt for headless cron runs). Re-prompts
  on every switch.

### Per-workspace coder

Each workspace inlines its own coder config on the `workspaces` row (`coder_kind` `local`/`api`,
`coder_bin`, `coder_timeout_s`, `coder_backend_type`, and for `api`:
`coder_provider`/`coder_model`/`coder_api_key_secret`/`coder_base_url`). `coder.ForWorkspace(w, …)`
builds a `*coder.Coder` from it — a **local** CLI coder or the **api** engine — falling back to the
system defaults when unset; `coder.DetectInstalled()` probes PATH **and the
platform's usual install directories** for supported binaries (claude/claude-code,
opencode, codex, gemini, cursor) — see "Coder detection off Linux" below — and
`coder.APIProviders()` returns a curated catalog of ~31 named providers in two
tiers. **Hosted** covers the frontier labs (OpenAI, Anthropic, Gemini, xAI,
Mistral, DeepSeek, Moonshot, Z.AI), the routers (OpenRouter, OpenCode Zen/Go,
Perplexity), the enterprise clouds (**AWS Bedrock**, **Alibaba Cloud/Qwen**) and
the open-weight inference clouds (Groq, Ollama Cloud, Together, Fireworks,
Cerebras, SambaNova, Nebius, DeepInfra, plus the Hugging Face and GitHub Models
aggregators). **Local** covers self-hosted OpenAI-compatible servers — Ollama,
LM Studio, llama.cpp, vLLM, LocalAI and Jan — which need no API key
(`RequiresKey: false`, enforced as an **iff** against `Group == GroupLocal` by
`TestAPIProviders_KeylessIsLocalTier`, so a hosted provider cannot forget its
key requirement and a local one cannot demand a key it does not need).
`coder.PlanKeySecret` stores `placeholderLocalKey` for that tier, because
`llm.New` rejects an empty key. A "Custom (OpenAI-compatible)" escape hatch
remains **last** in the list (`TestAPIProviders_CustomIsGenericAndLast`).

Base URLs are single-sourced in `internal/llm.DefaultBaseURL(name)`, are not
duplicated in the catalog, and are **always resolved, never templated**:
`llm.New` assigns the value straight into the HTTP client with no validation,
so a `{region}` placeholder would satisfy every other test and then fail at
request time with an opaque DNS error. Bedrock therefore ships `us-east-1` (on
the `bedrock-mantle` endpoint AWS recommends — the one that takes a Bedrock API
key as a plain bearer token, with no SigV4 signing, which is the only reason
Bedrock is a drop-in) and region variation goes through the per-workspace
override. `TestAPIProviders_BaseURLsAreDialable` pins this.

The coder form accepts an inline API key pasted directly into a settings field,
which `coder.PlanKeySecret` transparently stores as an encrypted
`CODER_KEY_<PROVIDER>` secret. The **base-URL override is prefilled** with the
selected provider's default rather than left blank, auto-expands for the local
tier, and shows the effective URL on the collapsed Advanced toggle — the
capability always existed and always persisted, but was undiscoverable behind a
generic placeholder, so a non-default Ollama port could not be configured in
practice. An unmodified prefill still posts an empty `base_url`, so a workspace
keeps following the registry default rather than freezing on today's URL.

**Azure OpenAI and Google Vertex AI are deliberately absent** — see
`docs/superpowers/specs/2026-08-04-llm-provider-expansion-design.md`. Azure uses
an `api-key` header, a deployment name in the path and a mandatory
`api-version` query parameter; Vertex mints short-lived OAuth tokens from a
service account, which `llm.Config.APIKey` (a plain string `llm.New` rejects
when empty) cannot express. Each needs its own provider implementation rather
than a catalog row. The web `coderForWorkspace(id)` and the runner's injected coder
factory (`Runner.WithCoderFactory`, wired in `main.go`) both use `ForWorkspace` — as do the agent
designer, skill creator, and Telegram chat (via the `coderFor(workspaceID)` factory in `main.go`) —
so scheduled + manual runs, generation, and chat all honor the workspace's coder.

**Both kinds are fully implemented.** The `api` kind resolves its provider API key lazily via
`WithSecretsLookup` (a closure in `main.go`/`web.Server.secretsLookup` that decrypts the workspace
master password and reads the named secret, same path the scheduler uses) — so it authenticates on
every call site regardless of whether that path injects secrets via env. Settings/setup save
provider/model/base-url/api-key-secret through `db.UpdateWorkspaceCoder`.

### Natural language reminders

`internal/reminder/timeparser.go` — `ParseNaturalTime(text, now, loc)` parses expressions like `"in 10 minutes"`, `"tomorrow at 3pm"`, `"next Tuesday at noon"`. Both web UI and Telegram use `profile.LoadLocation(db, workspaceID)` so reminders fire in the workspace's timezone.

---

## Backup and restore

One **owner-level** snapshot covers the entire install — the database plus every
workspace's vault — in a single passphrase-encrypted `.rkb` file. Configured in
owner settings (`BackupSection`), scheduled daily/weekly, restorable via CLI or
from the UI.

**The system key is why this design looks the way it does.** `secrets.SystemKey`
encrypts `workspaces.encrypted_master_password`, every `service_connections`
OAuth token, and every `platform_connections` bot token. It used to be derived
from the **hostname** whenever `ROOKERY_SYSTEM_KEY` was unset, so a naive file-copy
backup restored on new hardware produced an install that booted, looked healthy,
and had silently lost every scheduled agent and every connector. Three
consequences, all load-bearing:

- **The key travels inside the encrypted snapshot** (`Manifest.SystemKey`), which
  is what makes cross-machine restore one step. It is also why the envelope needs
  a passphrase — and why the passphrase is the one thing an owner must not lose.
- **`secrets.SystemKey(dataDir, hasWorkspaces)` pins the key to
  `<data_dir>/system.key`.** Resolution order is `ROOKERY_SYSTEM_KEY` → the file →
  derive-and-persist (hostname-derived when the install already has workspaces,
  so an upgrade keeps its exact key; random for a fresh install). `SystemKeyFromEnv`
  survives only as the legacy path the migration test compares against — **every
  call site must use `SystemKey`**, or it will diverge from the restored key with
  no visible symptom.
- **`ApplyPendingRestore` moves the OLD `system.key` into `.pre-restore-<ts>/`
  together with the database and vaults**, and writes the new key only after that
  succeeds. Leaving it behind would make the rollback copy undecryptable the
  instant a restore landed. Only the newest `.pre-restore-*` is kept.

**`session.key` is the system key's sibling, and is deliberately NOT in the
snapshot.** `secrets.SessionKey(dataDir, configured)` resolves the cookie-signing
key by the same order — configured value → `<data_dir>/session.key` → generate and
persist at 0600 — because the fallback it replaced was the literal
`"change-me-in-production-32bytes!!"` compiled into a published binary, so every
install that never set `ROOKERY_SESSION_KEY` signed its sessions with a key anyone
could read out of the repository. Unlike the system key it encrypts nothing at
rest, so losing it costs one sign-in rather than the whole install; leaving it out
of the `.rkb` means a restore onto new hardware does not also transplant live
session cookies. An empty `data_dir` (tests) yields an ephemeral key rather than a
shared constant.

**Restore only ever runs against a dead install.** `serve` calls
`ApplyPendingRestore` at the very top — *before* the database is opened or
migrated — then holds an exclusive `flock` on `<data_dir>/rookery.pid` for
its whole lifetime. The offline CLI takes the same lock and refuses when the
server holds it. The settings button does not swap anything itself: it stages,
writes a `.restore-pending` marker, and shuts the server down, so the swap
happens on the next boot through the identical code path. `rookery backup
cancel-restore` abandons a staged restore that would otherwise fire weeks later.

**Snapshot contents.** `db/rookery.db` (via `VACUUM INTO` — copying the
live file is torn, the WAL is multi-megabyte) plus `vaults/**`. Excluded:
`claude-homes/` (regenerable; `.credentials.json` is re-copied per invocation),
`config.yaml`, staging/work dirs. The vault walker is a **raw `filepath.WalkDir`,
never `vault.List`** — those hide dotfiles, which would silently drop `.kb/`
(db-export sidecars, `links.json`) from every snapshot.

Details worth knowing before changing this code:

- `readArchive` **drains to the end of the gzip stream** before returning. tar
  stops at its own end-of-archive marker, which sits before the gzip trailer, so
  without the drain the CRC32 is never checked and tail damage goes undetected.
- Snapshot names have one-second granularity, so `freeSnapshotName` probes the
  destination and advances by whole seconds — two runs in the same second
  otherwise resolved to one name and the second silently overwrote the first.
- The envelope is **framed** (1 MiB AES-256-GCM frames, frame index + final flag
  authenticated as AAD) rather than one-shot: that bounds memory and makes
  reordering and truncation detectable. A first frame that fails to authenticate
  is reported as `ErrBadPassphrase` — a wrong passphrase and a corrupted frame 0
  are genuinely indistinguishable.
- `Prune` and both destinations filter on `IsSnapshotName`, so a bucket or folder
  shared with other data never has a foreign file listed, downloaded or deleted.
- `POST /api/v1/backup/restore` is **exempt from the shared 25 MiB `iolimit`
  cap** — a real snapshot exceeds it as soon as a workspace has attachments.
- The eight `/api/v1/backup/*` routes sit on the **owner** group with no
  `requireActiveWorkspace`: backup covers every workspace, so it must be
  configurable before one exists.
- No new dependencies: SigV4 is stdlib HMAC/SHA-256, and the CLI suppresses
  terminal echo with `stty` rather than pulling in `golang.org/x/term`.

**Not built** (deliberate): per-workspace restore, incremental/deduplicated
backup, and the Google Drive / Dropbox / GitHub destinations — adding one is a
new `Destination` implementation plus a settings form. GitHub was considered and
rejected: a daily encrypted blob committed to git grows history without bound and
cannot be pruned.

## Known gaps

- **Thin e2e coverage.** CI has two end-to-end gates: the container smoke test (`pr.yml` → `Container smoke test`) starts the real image and asserts `/healthz`, the SPA root and the session endpoint answer, and the package smoke test (`pr.yml` → `Package smoke test`) installs the built deb/rpm and extracts the tar.gz, running `owner bootstrap` + `serve` + `healthcheck` on each. Everything above that — coder subprocess round-trips (real edit → test → approve), agent runs, connector calls — is still exercised manually. Unit tests cover logic boundaries.
- **Local-coder Model field not in the settings UI** — the coder settings/setup form collects a model only for the `api` coder kind; the `#coder_local` section has just the binary picker. So `workspaces.CoderModel` cannot be set for a **local** CLI coder through the UI, even though the runner already passes it as `-m`/`--model` (opencode/cursor). This blocks OpenCode out of the box (see "OpenCode requires an explicit model" above — with no model it 401s on its OpenRouter default). Until a Model input is added to `#coder_local` (+ read in `handleSaveWorkspaceCoder`/`handleSetupCoder`), `CoderModel` for a local coder must be set another way (e.g. directly in the DB). Two clean fixes, not yet built: (1) add the local Model field; (2) have `opencodeBackend` fall back to a host-configured default model when `CoderModel` is empty. Codex/Gemini also don't yet receive `cliModel` (noted in `selectBackend`).
- **Discord adapter** — implemented (DM-only); live WS round-trip is operator-verified. **Slack adapter** — implemented (DM-only, Socket Mode); live loop operator-verified. Note: Slack's Socket Mode inbound loop does not auto-restart after a *fatal* reconnect failure (reconnect exhaustion) — outbound still works, but inbound DMs stop until the connector is re-saved or the server restarts; a per-adapter supervisor is a future framework enhancement. Mattermost/Matrix adapters — not yet implemented (framework ready: adapter registry + `CredSpec` + render subsystem all support a new platform via `init()` registration alone; Mattermost should be a hand-rolled thin REST+WS client, NOT the heavy official SDK; Matrix E2EE needs `-tags goolm` to stay CGo-free). The connectors UI (SPA `/connections` → Chat apps tab, backed by `/api/v1/connectors`) is `CredSpec`-driven — a new platform's connect card is data, not hand-written markup. **Design stance:** all adapters use an **outbound** connection (bot dials out; zero inbound port) — a deliberate security property for self-hosted/home installs (works behind NAT, home firewall can drop-by-default, no forgeable public endpoint). **Webhook-based platforms** (WhatsApp/Viber/LINE/Teams/Messenger/Google Chat) are deferred OUT of the home-install core; if built, they must be tunnel/relay-first (outbound), never a raw open port. Future outbound-only candidates: Zulip (event-queue long-poll), XMPP. See `docs/superpowers/specs/2026-07-15-multi-platform-chat-adapters-design.md`.
- **Skill editing + import via chat** — `/skill` covers list/create/cancel, but there is no `/skill edit` (the skill designer has no edit mode at all, unlike `agentdesigner.StartEdit`) and no skill import (ZIP / pasted SKILL.md) over chat, which needs per-adapter file-upload handling. The remaining half of the skill parity gap.
- **MCP servers** — `mcp_servers` table exists; MCP tool execution not implemented.
- **Custom workspace image upload** — the 36 presets are inline SVG on purpose (no endpoint, no
  storage, no MIME validation, crisp at any size). Uploading a custom image is the one requested UI
  item deliberately deferred: it needs a multipart endpoint, a 25 MiB `iolimit` cap, MIME sniffing
  (SVG is an XSS vector needing sanitising or rasterising), a vault storage location with backup
  implications, and relaxing `web/api_settings.go`'s slug validator into a two-shape field
  (`preset:<slug>` vs `upload:<id>`). Bundling it into a visual-polish change would have put a
  security review on that change's critical path.
- **Connector provider configs (non-Google) unverified against live APIs** — google/github/notion verified end-to-end against real accounts; outlook/jira were hand-authored (rendering unit-tested only). Verify each against live docs before relying on it. A dev harness for this lives at `cmd/livecheck` (uncommitted; runs `connectors.Execute` against real stored tokens).
- **Connector native tools for CLI coders** — CLI coders reach connector actions via the `rookery connector exec` command (loopback bridge), not as native function tools in their own loop; true native parity for MCP-capable coders (claude-code) would be an MCP transport over the same `connectors.Execute` (not built).
- **Build-time connector testing exposes ALL workspace connections** (the agent hasn't declared bindings yet); a real run exposes only the agent's bound connections (`agent_connections`).
- **CLI-chat connector permission is a scoped Bash grant** — a CLI chat coder is otherwise file-only; when connectors are wired it gets `Bash(<bin> connector exec:*)` (only that command). Relies on the coder CLI honoring command-scoped Bash permissions (claude-code does); a coder that doesn't would need a wider grant.
