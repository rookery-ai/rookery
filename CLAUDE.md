# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Tenancy model: single-owner, multi-workspace

The platform has **one owner** (the installer; a single row in the `owner` table) who logs in and
manages **workspaces**. A **workspace** is a fully isolated tenant — its own vault, claude-home,
secrets, agents, connector, and inlined coder config — and replaces the old per-user account.
Workspaces have no login of their own: the owner **enters** a workspace by typing that workspace's
**master password** (re-entered on every switch). The web session is two-level: `owner_id`
(logged in) + `active_workspace_id` (entered). All tenant-scoped tables key off `workspace_id`.
Bootstrap the owner with `simple-agents owner bootstrap -u <name> -p <pw>`.

Terminology map (fully renamed throughout): user → **workspace**, admin → **owner**,
`user_id` → `workspace_id`, `db.User` → `db.Workspace` (+ new `db.Owner`).

## Commands

```bash
# Build
go build -o bin/simple-agents ./cmd/simple-agents

# Run all tests
go test ./... -count=1 -timeout 120s

# Run a specific package's tests
go test -v ./internal/agentdesigner/... -run TestFlow

# Run the server (after build)
./bin/simple-agents serve

# Bootstrap the owner account (first run only)
./bin/simple-agents owner bootstrap -u <username> -p <password>

# Reset the owner password (single-owner model, no login required)
./bin/simple-agents owner reset-password -p <new-password>

# Database migration
./bin/simple-agents db migrate

# Deploy / restart the server (build + run in background, logs to logs/server.log)
make deploy    # stop existing server, rebuild, start in background
make restart   # stop + start (no rebuild)
make stop      # stop the running server
make logs      # tail -f logs/server.log
make status    # show running server process
make test      # run the unit tests

# Frontend (web/ui): build the SPA into the binary
make ui        # npm ci + vite build → web/ui/dist (embedded on next go build)
make build     # ui + go build (full artifact); make build-go for Go-only
# Dev loop: cd web/ui && npm run dev  (Vite on :5173, proxies /api to :8080)
```

AST guardrail tests shell out to `python3`. If Python is not available, those tests self-skip.

> **Deploy workflow:** When the user says "restart the server", "rebuild", or
> "deploy", run `make deploy` — it stops the running server, rebuilds, and
> starts it in the background with logs captured to `logs/server.log`. The
> server listens on `0.0.0.0:8080` by default (override host with `SA_HOST=…`, port
> with `SA_PORT=…`). Set `SA_PUBLIC_URL` to the externally-reachable base URL so OAuth
> callbacks are correct. `simple-agents connector exec <tool> --args '<json>'` is the
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

## Architecture

### Entry point & wiring

`cmd/simple-agents/main.go` loads `config.yaml` via `internal/config`, wires all services, and
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
| `internal/iolimit` | `ReadCapped` + `ErrTooLarge` — the shared capped read every ingest door uses (KB upload, web-chat attachment, Telegram/Discord/Slack attachment, KB bridge, `save_to_kb` URL fetch), all enforcing one 25 MiB cap. Reads `cap+1` and REJECTS rather than truncating: a silently truncated import writes a note whose frontmatter states a byte count that is not the source's. `CappingWriter` is the write-side analogue — bounds a stream written into an `io.Writer` (Slack's `slack.Client.GetFile` insists on an `io.Writer` and has no size bound; there is no stdlib `io.LimitWriter`), rejecting at the same `cap+1` boundary. |
| `internal/coder` | `Coder`: two engines behind one API. **CLI engine** — runs a coder CLI subprocess with full per-workspace isolation (`CoderBackend` interface: one struct per coder — Claude/OpenCode/Codex/Gemini/Cursor, plus a generic fallback). **API engine** (`api_engine.go`+`hosttools.go`, `coder_kind=="api"`) — an in-process LLM tool-calling loop (via `internal/llm`) that offers the model host tools (`read_file`/`write_file`/`edit_file`/`list_dir` + read-only discovery `search_files`/`glob` + exec tools `run_script`/`bash`/`web_fetch`/`web_search`) scoped+sandboxed to the vault, no subprocess. `WithNoTools()` text-only; `WithExtraEnv()` secret injection; `WithAPIConfig`/`WithSecretsLookup`/`WithVault`/`WithProgress`/`IsAPI()` for the API engine; `ForWorkspace(w, …)` builds a coder (local or api) from the workspace's inlined config |
| `internal/llm` | Thin, reusable transport over provider chat-completion/messages APIs with native function-calling (tool use). `Provider` interface + registry (`openai`, `openrouter`, `anthropic`, `generic` OpenAI-compatible); `Request`/`Response`/`Message`/`Tool`/`ToolCall`/`Usage`; shared HTTP plumbing with rate-limit-aware backoff (`ErrRateLimit` transient 429 → retry across a per-minute window; `ErrQuotaExhausted` 402 → no retry; `ErrAuth`, `ErrToolsUnsupported`). Knows nothing about vaults/sandboxes/protocol — the agentic loop lives in `internal/coder`. |
| `internal/connectors` | Self-managed-OAuth + API-key connector layer (replaces Composio). Embedded `providers/*.yaml` (auth config) + `connectors/*.yaml` (curated action manifests) for **32 providers** (Google-family incl. AdSense/GA4/Search Console, YouTube, GitHub, Slack, OpenAI, Notion, Outlook/Teams, Jira, HubSpot, Dropbox, Zoom, Calendly, Asana, ClickUp, Airtable, Intercom, SendGrid, Monday, Salesforce, Shopify, Mailchimp, Zendesk, Stripe, Twilio, Trello); `Registry` (+ `OAuthProvider` for `auth_parent` aliasing, `ProviderNames()` backing the connections page), `Execute` (typed choke point), `applyAuth` (Bearer/api-key header/query/Basic + templated Basic username), `renderBody`/`renderForm`/`body_arg` body kinds, `ActiveBoundConns`/`ConnectInput`/`token_extra`/`key_extra` per-connection value sources, `OAuthClient`, `DBTokenStore` (+ headless `RunRefreshLoop`), `Bridge` (loopback HTTP so CLI coders reach `Execute` — used by runs AND chat), `ToolDefs`/`ResolveTool` (single-source tool naming for both coder kinds). All tokens `secrets.EncryptWithSystemKey`-encrypted. |
| `internal/buildphase` | Tiny package holding `SA_BUILD_PHASE`/`generation` marker (set during agent/skill builds; the connector `Execute` build-guard refuses mutating actions when present). Its own package so it outlives any one integration. |
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
- **`Reflector`** — `ReflectChat/ReflectAgentRun`: markdown note + `.kb/db-export/<table>/<id>.json` sidecar. Reminders are NOT reflected.
- **`LinkIndex`** — `[[wikilink]]` parsing/resolution + `RenderHTMLLinks`; `Backlinks`.
- **`Searcher`** — `ripgrepSearcher` (rg `--json`, pure-Go fallback).
- **`Guard`** — detective post-run write-scope enforcement (snapshot/revert). No longer wired into agent runs (the policy changed to let agents edit the KB directly — see "Agent access model"); the type + tests remain as a reusable utility.
- **`MigrateLegacyLayout()`** — idempotent startup migration of pre-vault `agents/`, `memory/` (jsonl→md), `skills/` into vaults.

**Agent access model.** An agent's run CWD is its own vault dir; the coder prompt (`BuildCoderPrompt`, `<knowledge_base>` block) tells it to READ the whole vault and WRITE to both its own dir and the user's knowledge base (notes, memory, user files) — durable knowledge is persisted into the KB across runs. The Landlock sandbox grants RW over the whole vault root (confined to that user's vault + HOME; the DB, config, and other users' vaults stay out of reach). System-managed dirs (`.kb/`, `chats/`, other agents' `agents/<id>/`) are off-limits by prompt, not hard-enforced. The chat uses the same model (see `prompts.BuildChatSystemPrompt`).

**Agent state (`state.md`).** An agent's memory between runs is a markdown document, not a bare JSON file: `agents/<agentID>/state.md` — a `# State — <name>` heading, an italic intro paragraph, a ```` ```json ```` fence holding the machine state, and an optional `## Notes` section the agent may add human-facing context to (`internal/agentdesigner/statefile.go`: `StateFilePath`/`ReadState`/`WriteState`/`RenderStateTemplate`). The `[STATE]` output marker is unchanged (see "Agent output protocol" below) — the runner still merges JSON on every `[STATE]` block — but the merge now targets the fence inside this file. `WriteState` splices only the fence, preserving the heading/intro/`## Notes` byte-for-byte; the fence is located with a line-based scanner (`findStateFence`), deliberately not a regex, which could not disambiguate a corrupted fence from a legitimate later one in `## Notes`; a damaged or absent fence degrades to empty state rather than erroring. All three decode sites use `json.Number` (not `float64`), preserving integer fidelity above 2^53 — e.g. a 64-bit Discord snowflake ID, which silently truncated under the old `state.json` decode. The KB refuses to save a running agent's `state.md` (`PUT /api/v1/kb/note` → 409 `agent_running`), checked on the finalized save path server-side since the frontend can't be trusted to send the well-behaved form; the guard is check-then-write (a run can still start in the gap) and covers only `PUT` — a delete/rename of `state.md` mid-run is unguarded.

**Startup migration.** `agentdesigner.MigrateAgentFilesToMarkdown` (`migrate_files.go`, run in `serve` before the scheduler starts — scheduled runs read `state.md` via the new runner path) walks every workspace's `agents/*/` dirs, including `draft_<slug>` dirs, and for each: (1) converts `state.json` → `state.md` with a verify-then-delete gate — write, read back with `ReadState`, deep-compare against the original (also decoded with `json.Number`, so the comparison itself can't paper over a rounding loss), and only then remove `state.json`; any failure at any step leaves both files in place and logs loudly, never silently dropping state; (2) reconciles `agent.json`'s legacy `Skills` field into `agent_skills` (the old `ReconcileSkillAttachmentsToDB` job, absorbed here because it must run before `agent.json` is deleted); (3) deletes `agent.json`. Idempotent — an agent dir with `state.md` present and no `agent.json` is a no-op on every subsequent boot.

**KB file kinds.** The note endpoint (`GET /api/v1/kb/note`) sniffs content rather than trusting the extension — `kind: "markdown"` for `.md` files (the existing WYSIWYG/raw editor, unchanged), `"code"` for any other file that decodes as valid UTF-8 under the 1 MiB inline cap (a read-only monospace view, no save affordance), or `"binary"` otherwise (a download-only panel; content omitted). A file exactly at the 1 MiB boundary is classified `"code"`. Navigation carries an explicit `dir` hint instead of guessing from the filename, so extensionless files still open correctly.

**Chat knowledge-base access (on-demand retrieval + editing).** The one-off chat coder runs with `WithDir(vaultRoot).WithAllowedTools("Read,Write,Edit,Glob,Grep")` and a system instruction (`prompts.BuildChatSystemPrompt`) naming the vault root. The LLM retrieves and edits the user's notes **on demand** — only on turns that touch the KB — instead of having the vault injected every prompt. `chat.BuildUserContext` now returns identity-only context (profile/memory/agents/MCP); the old always-on `[Related knowledge base]` keyword-snippet block was removed. The tool set is file-only (no `Bash`/`WebFetch`): the chat can create/edit/read notes but cannot delete, rename, or run shell commands. The same applies to agents (RW over the vault via the sandbox). The detective `Guard` is no longer wired into agent runs — it would revert the KB edits that are now intentional — so agent/chat KB edits persist.

**Chat connector access.** One-off chat (both web `handleChatMessage` and Telegram) also exposes the workspace's **ACTIVE** service connections to the chat coder (`connectors.ActiveBoundConns` — all of them; chat isn't an agent so there's no per-agent binding), wired identically to how the API/CLI split works elsewhere: the **API engine** gets them as native function tools (`coder.WithConnectors`), a **CLI coder** reaches them via the loopback bridge (`bridge.Register` → `SA_CONNECTOR_URL`/`SA_CONNECTOR_TOKEN` env → `simple-agents connector exec`, plus a scoped `Bash(<bin> connector exec:*)` grant since chat is otherwise file-only). Both paths hit the same `connectors.Execute` (mutating allowed — chat is like a run, `buildPhase=false`). `BuildChatSystemPrompt(vaultRoot, backendType, conns, connToolNames, connectorBin)` appends `connectedToolsBlock` so the model knows the tools exist; with no active connections / no bridge, chat behaves exactly as the file-only default.

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

- **`platformContextBlock(chatApps, vaultRoot)`** — full Simple Agents primer (flexible ever-growing KB with USER-REORGANIZABLE vs SYSTEM-WRITTEN fixed locations, secrets store, chats, reminders, connected chat apps + commands, output protocol, schedule). Injected into design, implementation, and runtime prompts.
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
  **32 providers (~229 actions):** the Google family (Gmail/Drive/Sheets/Docs **+ AdSense/GA4/
  Search Console**), **YouTube**, GitHub, Slack, OpenAI, Notion, Outlook, Teams, Jira, HubSpot,
  Dropbox, Zoom, Calendly, Asana, ClickUp, Airtable, Intercom, SendGrid, Monday, Salesforce,
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
- **OAuth** (`oauth.go`): `ConsentURL`/`ExchangeCode`/`Refresh`/`FetchIdentity`. Per-provider config
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
    bound connections; the coder runs `simple-agents connector exec <tool> --args '<json>'` (a thin
    client subcommand) which POSTs to it. Tokens never leave the host; Landlock restricts filesystem,
    not loopback TCP, so a sandboxed coder can reach it (the `simple-agents` binary dir is granted
    RO+exec in the sandbox spec so the child can exec it). The bridge response is **byte-capped**
    at `maxBridgeResult` (8 KiB, mirroring `coder.maxToolResult`) via `capBridgeData` — the API
    engine always truncated and the bridge did not, and an analytics or ad-insights report is
    exactly the payload that exploited the gap. Under the cap the envelope is unchanged
    (`{"data": …}`); over it, `data` becomes a truncated STRING plus `truncated: true` and a note
    telling the model to narrow its query, because a JSON value cut in place still parses and
    reads as complete data.
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

**UI:** the SPA connections page (backed by the `/api/v1/services` JSON endpoints) — per-workspace
OAuth-app creds + connect per provider, with per-provider setup guidance
(`label`/`setup_url`/`setup_steps` in the provider YAML). The OAuth **callback** is the one
server-rendered redirect route that survives the SPA cutover: `GET /dashboard/connectors/services/callback/:provider`
(HMAC-signed, TTL'd `state`; path FROZEN because it's the registered external redirect URI; it finishes
with an HTTP redirect back to the SPA). `SA_PUBLIC_URL` sets the callback base (Google rejects
non-public-TLD/`http` redirect URIs — use `https://` or `http://localhost`).

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

`coder.ErrUsageLimit` — CLI: non-zero exit with empty stdout+stderr; API: provider 402 (credits/quota exhausted, `ErrQuotaExhausted`). `coder.ErrRateLimited` — API transient 429 that didn't clear within the retry budget (distinct so the message says "try again in a moment", not "out of quota"). `coder.ErrAPIAuth` (bad/missing key) and `coder.ErrMaxTurns` (budget exhausted) are config/run errors, not usage limits. `agentrunner.friendlyRunError` converts each to a user-facing message sent via `input.SendOutput` on every run failure. Also handled softly during generation and design conversation turns. API token usage is accumulated across the loop (`coder.Usage`) and persisted per run.

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

**Mechanism:** `coder.buildCommand()` wraps the real command as `simple-agents __sandbox-exec <base64-spec>`. The helper applies `landlock.V5.BestEffort().RestrictPaths(...)` then `syscall.Exec`s the real command. Inherited by all children (`claude`→`bash`→`python`).

**Allowed:** RW: per-workspace HOME + agent workdir. RO: system paths, coder binary dir, the `simple-agents` binary dir (so a confined CLI coder can exec `simple-agents connector exec`), the workspace's vault root. Denied: SQLite DB, config.yaml, other workspaces' vaults.

`config.SandboxConfig.Enabled` (default true; `SA_SANDBOX=0` disables). With Landlock unavailable, the sandbox is not applied and nothing physically prevents writes outside the vault — agents/chat run trusted within the user's own vault.

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
machinery, the `web/templates/` + `web/static/` directories, and the `templates_dir`/`static_dir` +
`SA_TEMPLATES_DIR`/`SA_STATIC_DIR` config are gone. The SPA talks to the JSON API for everything.

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
message body. `createdAt` is optional because `DesignerSurface` renders design-conversation turns
through the same component with no timestamps (it gets the copy button only). The **timezone**
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
- **kb** — tree, note read/write/new/delete/rename, search, raw, resolve
- **settings + setup** — profile/workspace/coder/master-password settings, coder test, setup wizard
- **search** — global search

The embedded SPA is served at `/` (see above); `/app` + `/app/*` 301-redirect to their `/app`-stripped
equivalents. Serving/redirect wiring lives in `web/spa.go` (`setupSPARoutes`), not the JSON API group.

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
system defaults when unset; `coder.DetectInstalled()` probes PATH + `~/.local/bin` for supported
binaries (claude/claude-code, opencode, codex, cursor) and `coder.APIProviders()` returns a curated catalog of ~16 named providers (OpenAI, Anthropic, OpenRouter, Z.AI, Ollama Cloud/Local, DeepSeek, Groq, xAI, Mistral, Gemini, OpenCode Zen/Go, Perplexity, Moonshot) plus a "Custom (OpenAI-compatible)" escape hatch; base URLs are single-sourced in `internal/llm.DefaultBaseURL(name)` and are not duplicated in the catalog; the coder form accepts an inline API key pasted directly into a settings field, which `coder.PlanKeySecret` transparently stores as an encrypted `CODER_KEY_<PROVIDER>` secret, with an Advanced base-URL override available per provider (required only for Custom). The web `coderForWorkspace(id)` and the runner's injected coder
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

## Known gaps

- No integration or e2e test coverage — unit tests cover logic boundaries; coder subprocess round-trips (real edit → test → approve) are exercised manually.
- **Local-coder Model field not in the settings UI** — the coder settings/setup form collects a model only for the `api` coder kind; the `#coder_local` section has just the binary picker. So `workspaces.CoderModel` cannot be set for a **local** CLI coder through the UI, even though the runner already passes it as `-m`/`--model` (opencode/cursor). This blocks OpenCode out of the box (see "OpenCode requires an explicit model" above — with no model it 401s on its OpenRouter default). Until a Model input is added to `#coder_local` (+ read in `handleSaveWorkspaceCoder`/`handleSetupCoder`), `CoderModel` for a local coder must be set another way (e.g. directly in the DB). Two clean fixes, not yet built: (1) add the local Model field; (2) have `opencodeBackend` fall back to a host-configured default model when `CoderModel` is empty. Codex/Gemini also don't yet receive `cliModel` (noted in `selectBackend`).
- **Discord adapter** — implemented (DM-only); live WS round-trip is operator-verified. **Slack adapter** — implemented (DM-only, Socket Mode); live loop operator-verified. Note: Slack's Socket Mode inbound loop does not auto-restart after a *fatal* reconnect failure (reconnect exhaustion) — outbound still works, but inbound DMs stop until the connector is re-saved or the server restarts; a per-adapter supervisor is a future framework enhancement. Mattermost/Matrix adapters — not yet implemented (framework ready: adapter registry + `CredSpec` + render subsystem all support a new platform via `init()` registration alone; Mattermost should be a hand-rolled thin REST+WS client, NOT the heavy official SDK; Matrix E2EE needs `-tags goolm` to stay CGo-free). The connectors UI (SPA `/connections` → Chat apps tab, backed by `/api/v1/connectors`) is `CredSpec`-driven — a new platform's connect card is data, not hand-written markup. **Design stance:** all adapters use an **outbound** connection (bot dials out; zero inbound port) — a deliberate security property for self-hosted/home installs (works behind NAT, home firewall can drop-by-default, no forgeable public endpoint). **Webhook-based platforms** (WhatsApp/Viber/LINE/Teams/Messenger/Google Chat) are deferred OUT of the home-install core; if built, they must be tunnel/relay-first (outbound), never a raw open port. Future outbound-only candidates: Zulip (event-queue long-poll), XMPP. See `docs/superpowers/specs/2026-07-15-multi-platform-chat-adapters-design.md`.
- **Skill editing + import via chat** — `/skill` covers list/create/cancel, but there is no `/skill edit` (the skill designer has no edit mode at all, unlike `agentdesigner.StartEdit`) and no skill import (ZIP / pasted SKILL.md) over chat, which needs per-adapter file-upload handling. The remaining half of the skill parity gap.
- **MCP servers** — `mcp_servers` table exists; MCP tool execution not implemented.
- **Connector provider configs (non-Google) unverified against live APIs** — google/github/notion verified end-to-end against real accounts; outlook/jira were hand-authored (rendering unit-tested only). Verify each against live docs before relying on it. A dev harness for this lives at `cmd/livecheck` (uncommitted; runs `connectors.Execute` against real stored tokens).
- **Connector native tools for CLI coders** — CLI coders reach connector actions via the `simple-agents connector exec` command (loopback bridge), not as native function tools in their own loop; true native parity for MCP-capable coders (claude-code) would be an MCP transport over the same `connectors.Execute` (not built).
- **Build-time connector testing exposes ALL workspace connections** (the agent hasn't declared bindings yet); a real run exposes only the agent's bound connections (`agent_connections`).
- **CLI-chat connector permission is a scoped Bash grant** — a CLI chat coder is otherwise file-only; when connectors are wired it gets `Bash(<bin> connector exec:*)` (only that command). Relies on the coder CLI honoring command-scoped Bash permissions (claude-code does); a coder that doesn't would need a wider grant.
