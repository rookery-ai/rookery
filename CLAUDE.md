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

# Database migration
./bin/simple-agents db migrate

# Deploy / restart the server (build + run in background, logs to logs/server.log)
make deploy    # stop existing server, rebuild, start in background
make restart   # stop + start (no rebuild)
make stop      # stop the running server
make logs      # tail -f logs/server.log
make status    # show running server process
make test      # run the unit tests
```

AST guardrail tests shell out to `python3`. If Python is not available, those tests self-skip.

> **Deploy workflow:** When the user says "restart the server", "rebuild", or
> "deploy", run `make deploy` — it stops the running server, rebuilds, and
> starts it in the background with logs captured to `logs/server.log`. The
> server listens on `0.0.0.0:8080` by default (override with `SA_PORT=…`).
> Verify with `make status` / `make logs`; smoke-test with
> `curl -sS http://127.0.0.1:8080/login`.

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
Telegram adapter (per-workspace bot instance)
  → GatewayManager.route()
    → IdentityResolver  (platform_user_id → internal workspace_id via platform_identities table)
    → Router.Handle()
      → /agent  → agentdesigner.Flow (conversational FSM)
      → /run    → agentrunner.Runner
      → /secret → SecretStore
      → /remind → reminder.Service
      → /chat → db.Chat (start/list/stop/resume/delete)
      → /memory → memory.Store (add/list/delete bullets in GENERAL.md)
      → plain text → one-off chat (coder.Coder with read+write KB tools; see "Chat knowledge-base access")
```

### Key packages

| Package | Responsibility |
|---|---|
| `internal/config` | YAML config + env overrides |
| `internal/db` | SQLite via `modernc.org/sqlite`; `DB`, models, per-table query helpers |
| `internal/auth` | `BootstrapOwner`, `Authenticate` (owner login), `ChangePassword` (owner), `CreateWorkspace(name, about)`, `GenerateSecretsSalt`, bcrypt |
| `internal/rbac` | `CanPerform(db, workspaceID, permission)` — reads `workspace_permissions` table |
| `internal/secrets` | AES-256-GCM store; Argon2id key derivation; `GetAll()` decrypts all for env injection; `Proxy()` resolves `${NAME}` in-memory only |
| `internal/gateway` | `Gateway` interface, `GatewayManager`, `Router`, `IdentityResolver`, `TelegramGateway` |
| `internal/coder` | `Coder`: two engines behind one API. **CLI engine** — runs a coder CLI subprocess with full per-workspace isolation (`CoderBackend` interface abstracts Claude vs. generic CLIs). **API engine** (`api_engine.go`+`hosttools.go`, `coder_kind=="api"`) — an in-process LLM tool-calling loop (via `internal/llm`) that offers the model host tools (`read_file`/`write_file`/`edit_file`/`list_dir` + read-only discovery `search_files`/`glob` + exec tools `run_script`/`bash`/`web_fetch`/`web_search`) scoped+sandboxed to the vault, no subprocess. `WithNoTools()` text-only; `WithExtraEnv()` secret injection; `WithAPIConfig`/`WithSecretsLookup`/`WithVault`/`WithProgress`/`IsAPI()` for the API engine; `ForWorkspace(w, …)` builds a coder (local or api) from the workspace's inlined config |
| `internal/llm` | Thin, reusable transport over provider chat-completion/messages APIs with native function-calling (tool use). `Provider` interface + registry (`openai`, `openrouter`, `anthropic`, `generic` OpenAI-compatible); `Request`/`Response`/`Message`/`Tool`/`ToolCall`/`Usage`; shared HTTP plumbing with rate-limit-aware backoff (`ErrRateLimit` transient 429 → retry across a per-minute window; `ErrQuotaExhausted` 402 → no retry; `ErrAuth`, `ErrToolsUnsupported`). Knows nothing about vaults/sandboxes/protocol — the agentic loop lives in `internal/coder`. |
| `internal/composioassets` | Single source of truth for the Composio v3 helper scripts (`composio_helper.py`, `composio_discover.py`), `go:embed`ed and **seeded deterministically** into an agent's/skill's working dir by `WriteHelperFiles(dir)` BEFORE the coder runs (build AND real run) — so every generation gets byte-identical, safety-checked code instead of the LLM retyping it. `IsSeededFilename()` skips guardrail vetting on them; `BuildPhaseEnvVar`/`BuildPhaseGeneration` (`SA_BUILD_PHASE=generation`) tells `composio_helper.py` to refuse real outbound/destructive actions at build time. |
| `internal/agentdesigner` | `Flow` FSM (Describing→Designing→Verifying→Done); conversational design shared between web and Telegram; auto-schedule; `RunFullGuardrails`/`RunToolGuardrails` (ethics + AST only); `toolstree.go` recursive path-safe `WriteToolsTree`/`ReadToolsTree` for multi-file projects; `isTestArtifact` classifier + `cleanupTestArtifacts` (post-save junk removal) |
| `internal/skilldesigner` | Conversational skill-creator wizard mirroring `agentdesigner.Flow` (FSM Idle→AwaitingResume→Describing→Designing→Verifying→Done, SSE progress, 7-day drafts, approval triggers); `SkillSaver` writes SKILL.md+scripts/ to vault + DB upsert; generation runs with the `skill-creator` core skill, vetting runs the `skill-vetter` core skill as a text-only audit; `vettingBlocksSave()` parses the verdict line. Web-wired only (Telegram route not yet added). |
| `internal/skilllibrary` | Embedded core skill catalog (`go:embed skills/*/SKILL.md`) — always-on for every user, no DB rows, no admin gate. `LoadBundled()`, `CoreSkillContent(slug)`, `IsCoreSkill()`, `ParseMeta()` (Anthropic+openclaw YAML frontmatter: requires.bins/anyBins/env, install specs). Supersedes the admin-catalog approach dropped in migration 009. |
| `internal/agentrunner` | Load agent → decrypt secrets into env via `WithExtraEnv` → coder subprocess → capture `[CHAT]` lines → send via GatewayManager; timestamped run logs; `RunInput.OnProgress` per-turn hook for live SSE streaming. Skills pool = core skills (embedded) + user skills; the agent's DECLARED skills come from the `agent_skills` DB table (`db.ListAgentSkillNames`, the source of truth) not the manifest; `resolveSkillBins` resolves declared tools' paths for the runtime `<skill_environment>` block; `loadDeclaredSkillContent` reads core skills from the embed. **Reliable delivery**: `parseCoderOutput` (blank lines don't end `[CHAT]`; empty `[CHAT]` dropped; `[SILENT]` detected) + `extractProseMessage` fallback when no `[CHAT]` emitted and not silent → visible warning when nothing deliverable. Covered by `runner_test.go`. |
| `internal/sandbox` | Self-contained Landlock filesystem confinement for coder subprocesses (Linux). `Spec`, `Supported()`, `Wrap()` (re-exec via the hidden `__sandbox-exec` helper), `Exec()` (applies Landlock + rlimits, then `execve`). No external dependency. |
| `internal/scheduler` | Cron scheduler: polls `agent_schedules`, fires runner, decrypts stored master password for secret injection; `WithSender()` delivers output to users |
| `internal/reminder` | Creates/lists/fires reminders; background polling goroutine. Reminders live only in the DB and the reminders UI tab — they are NOT reflected to the vault. |
| `internal/chat` | `Chat` create/list/stop/resume/delete; 30-min idle auto-stop; `BuildUserContext` (shared **identity-only** context builder for one-off chat — profile/memory/agents/MCP; the broader KB is retrieved on demand via tools, not injected here) |
| `internal/prompts` | Central home for all LLM prompt construction: `BuildDesignSystemPrompt` (+ `<knowledge_base>` block + `KBManifest`), `BuildImplementationPrompt`, `BuildEditImplementationPrompt` (diagnose-before-fix), `BuildCoderPrompt` (+ `<skill_instructions>` + `<skill_environment>` blocks), `BuildChatSystemPrompt` (chat read+write KB instruction), `BuildChildAgentFollowUpPrompt`, `BuildSkillMetaPrompt`, `BuildReminderParsePrompt`, skill-creator prompts (`BuildSkillDesignSystemPrompt`, `BuildSkillImplementationPrompt`, `BuildSkillVettingPrompt`, `SkillEnvBlock`). `SkillRef`/`SkillBin` types. No inline prompt text exists outside this package. Shared single-source blocks: `agentPhilosophyBlock` (three-tier), `platformContextBlock`, `coderCapabilitiesBlock` (backend-aware), `agentArchitectureGateBlock`, `testingRulesBlock` (one bounded smoke test + dry run; real secrets at build time, no outbound sends), `shellSafetyBlock`, `scriptRobustnessBlock`, `composioServicesBlock`. `ChatAppsForPlatforms` + `MapCoderBackend` bridge callers to prompt params. |
| `internal/memory` | Per-user structured context store. Memory lives as named `.md` files in `memory/` (`USER.md`, `SOUL.md`, `GENERAL.md`, etc.) — editable via the KB browser. `ContextString()` reads all files, skips placeholder-only ones, and returns sectioned markdown for LLM injection. `Append/List/Delete` target GENERAL.md bullet lines (used by Telegram `/memory` command). `MigrateToStructuredFiles()` consolidates legacy UUID-keyed entries at startup. |
| `internal/vault` | Per-user Obsidian-style knowledge base: `Vault` (paths + `Resolve` safety + file IO), `Reflector` (chats→markdown+sidecar), `LinkIndex` ([[wikilinks]]), `Searcher` (ripgrep), `Guard` (post-run write-scope enforcement), `MigrateLegacyLayout`, `MigrateSessionsToChats`. |
| `internal/audit` | Structured audit event writer → `audit_logs` table |
| `internal/profile` | Per-user personalization (name, email, location, timezone, tone, language, notes); stored in the generic `settings` table; `Load()`/`Save()`/`ContextString()` for LLM injection; `LoadLocation()` for timezone-aware reminder parsing |
| `internal/skillstore` | `SkillStore`: install/load/delete SKILL.md based skills per workspace. `SkillDir(base, workspaceID, name)` is the path helper shared with the skill designer (staging dirs use the `.staging-<name>` convention). |
| `web/` | Echo v4 web server; full user dashboard + admin UI |

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
│   ├── AGENT.md  agent.json  state.json
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

**Chat knowledge-base access (on-demand retrieval + editing).** The one-off chat coder runs with `WithDir(vaultRoot).WithAllowedTools("Read,Write,Edit,Glob,Grep")` and a system instruction (`prompts.BuildChatSystemPrompt`) naming the vault root. The LLM retrieves and edits the user's notes **on demand** — only on turns that touch the KB — instead of having the vault injected every prompt. `chat.BuildUserContext` now returns identity-only context (profile/memory/agents/MCP); the old always-on `[Related knowledge base]` keyword-snippet block was removed. The tool set is file-only (no `Bash`/`WebFetch`): the chat can create/edit/read notes but cannot delete, rename, or run shell commands. The same applies to agents (RW over the vault via the sandbox). The detective `Guard` is no longer wired into agent runs — it would revert the KB edits that are now intentional — so agent/chat KB edits persist.

**Agent designer KB awareness.** The designer is text-only (`WithNoTools`) but its system prompt (`BuildDesignSystemPrompt`, `<knowledge_base>` block) now knows the app has a built-in vault that agents read/write, and is told to prefer it over Notion/external note apps for the user's own knowledge. Each design turn injects a fresh manifest of the user's existing note paths via `Flow.WithKBLister` → `vault.NotePaths(workspaceID)` → `DesignSystemParams.KBManifest`, so the designer can reference the user's actual notes.

### Unified conversational agent creation

Agent creation uses a single `agentdesigner.Flow` FSM shared between Telegram and web. No agent types — every agent is the same structure.

**FSM states:** `StateDescribing` (Telegram only) → `StateDesigning` → `StateVerifying` → `StateDone`

**Approval triggers** — two tests, deliberately different:
- **`isApproval`** (used in `StateDesigning`, strict) — exact match on `"approve"`, `"go ahead"`, `"build it"`, `"create it"`, `"/approve"` (trailing punctuation trimmed). Casual `"ok"`/`"yes"` while answering design questions does NOT launch a full generation run.
- **`isVerifyApproval`** (used in `StateVerifying`, forgiving) — also accepts `"yes"`, `"save"`, `"ok"`, `"looks good"`, `"confirm"`, `"go"`, `"do it"`, `"ship it"`, `"lgtm"`, `"perfect"`, `"great"`, …, and excludes negative cues (`"don't"`, `"not yet"`, `"change"`, `"wait"`, `"instead"`). A natural confirmation saves the build instead of being read as a change request.

**Change requests no longer discard the build.** When the user replies in `StateVerifying` with something that isn't approval, the session returns to `StateDesigning` but **keeps** `PendingAgentMD`/`PendingTools` in memory — a misfire (e.g. `"yes"`, `"save"`, `"ok"`) no longer silently drops the generated agent. The next approve re-generates with the change context and overwrites.

**On approval:** `runGeneration()` calls the coder with the same tool set as an agent run (`WithDir(agentDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit")`) — so the coder runs REAL end-to-end tests against live services during the build, not mock-only. Secrets are injected via `WithSecretsLoader`/`WithExtraEnv` so the real API calls the agent will make at run time are actually exercised here. The one hard exception: never send real OUTBOUND messages on the user's behalf at build time (enforced by the testing-rules prompt, not by withholding credentials). Coder writes `AGENT.md` + `tools/*.py` to disk, runs scripts via Bash, fixes errors, outputs `[TEST_OUTPUT]...[/TEST_OUTPUT]`. Flow reads AGENT.md, runs guardrails, stores in `PendingAgentMD`/`PendingTools`, moves to `StateVerifying`. If `[TEST_OUTPUT]` is absent, user stays in `StateDesigning`.

**Test-artifact cleanup.** A real end-to-end test leaves junk in the agent dir (downloaded files, run outputs, scratch probes like `_probe.py`). `cleanupTestArtifacts(agentDir)` (post-approval, in `saveAndFinish`/`updateAndFinish`) removes that junk so only shipping source remains; artifacts persist through `StateVerifying` so the user can see real test output as proof. `isTestArtifact(path, name, toolsDir)` in `toolstree.go` is the shared classifier (binary-download extensions, run-output file names/suffixes, `_`-prefixed scratch probes at the `tools/` top level, root-level scratch `.json`). `ReadToolsTree` also skips test artifacts so they never corrupt the pending-tools map or trip guardrails.

**Generation failure handling:** `[BLOCKED]` marker, `ErrUsageLimit`, and timeout all return soft user-facing strings (not Go errors) — user stays in `StateDesigning`.

**SSE progress:** `DesignSession` carries `progressCh chan string` + `cancelGenerate`. `GetProgressChan(workspaceID)` lets the web SSE handler stream milestone events to the browser.

**Auto-schedule:** If AGENT.md starts with `# Suggested schedule: */10 * * * *`, `parseSuggestedSchedule()` calls `db.UpsertAgentSchedule()` immediately.

**Skills selection via `# Skills:` header.** `DesignSession.Skills` is `[]prompts.SkillRef` (name+description), loaded once on start as **core skills (embedded) + the user's own skills**. The designer's `<available_skills>` block lists each skill with its description and **requires** the coder to emit a `# Skills: skill-one, skill-two` header line in AGENT.md (alongside the schedule line) declaring EXACTLY the skills this agent needs — never all of them, and never omitting the line (`# Skills: none` if it needs none). `parseSkillsLine(agentMD, installed)` reads that header and is deliberately tolerant of LLM formatting drift: case-insensitive heading matching (any `#` level, optional `required/needed/uses` qualifier, `:`/`-`/`=` separator), splits on `,`/`;`/`|`/`+`/`&`/`/`/` and `/` or `/newline, strips backticks/quotes/trailing prose (`pdf (for …)` → `pdf`), and also reads a bullet/numbered list when the heading has no inline names. Names are matched case-insensitively against the installed pool (so multi-word names like "Google Workspace" survive — bare spaces are NOT separators); unknown names are dropped with a warning rather than failing. Contract: returns `nil` ONLY when no skills header is found at all (caller treats as "declared none"); a present-but-empty/`none` header returns a non-nil empty slice. On approval `saveAndFinish`/`updateAndFinish` persist the declared names to the `agent_skills` DB table (the source of truth — see "Skill attachments source of truth" below); if no header is found, **no skills are attached** (the old "fall back to all installed skills" behaviour was removed because it polluted the agent page with every skill). Covered by `parse_skills_test.go` + `skills_db_test.go`.

**Skill attachments source of truth (DB, not AGENT.md).** The `agent_skills` DB table — keyed by **skill name**, not `skill_id` — is the single source of truth for an agent's skills (core + user, by name). Core (embedded) skills have no `skills`-table row, so they could never be represented by the old `skill_id` FK; migration 010 rebuilt `agent_skills(agent_id, skill_name)` and backfilled existing user-skill rows by resolving `skill_id → name`. The designer (`SaveAgent`/`UpdateAgent`) writes the parsed `# Skills:` names here; `manifest.Skills` (`agent.json`) is now left empty — AGENT.md/`agent.json` are for the LLM only, the DB is the skill record. The runner (`runCoderAgent`) and the agent page (`renderAgentDetail`) read declared skills from `db.ListAgentSkillNames(agentID)`, never from the manifest. The web Skills card renders core + user skill checkboxes by name (`AttachedSet`/`AttachedSkills` + `CoreSkills`/`AllSkills`); `handleSaveAgentSkills` accepts `skill_names` (not IDs), validates against core ∪ user names, and writes the DB only. Deleting a user skill calls `db.DeleteAgentSkillsByName` to drop dangling attachments. **One-time cutover:** `agentdesigner.ReconcileSkillAttachmentsToDB` (run at `serve` startup, idempotent) seeds the DB from each legacy `manifest.Skills` — but only when the DB has no rows for that agent yet, and skipping the legacy "all core skills" fallback-bloat signature — then clears `manifest.Skills` on disk.

### Prompt architecture (coder-agnostic, three-tier)

All prompts live in `internal/prompts` (single source). The designer produces **coder-agnostic** AGENT.md — it says WHAT to do, never runtime-specific tool names (so it works on a full coder like claude-code/codex OR a basic model call like OpenRouter GLM). HOW the coder acts on files is injected separately based on `BackendType`:

- **`platformContextBlock(chatApps, vaultRoot)`** — full Simple Agents primer (flexible ever-growing KB with USER-REORGANIZABLE vs SYSTEM-WRITTEN fixed locations, secrets store, chats, reminders, connected chat apps + commands, output protocol, schedule). Injected into design, implementation, and runtime prompts.
- **`coderCapabilitiesBlock(backendType)`** — three-way: `BackendFullCoder` (CLI) → direct tool access; `BackendToolCalling` (the `api` engine) → native function calls (`read_file`/`write_file`/`edit_file`/`list_dir`/`search_files`/`glob`/`web_search`/`web_fetch`/`run_script`) the host executes, final answer as protocol markers; `BackendBasicModel` → `[READ_FILE]`/`[WRITE_FILE]`/`[RUN_SCRIPT]` output markers. `MapCoderBackend()` maps the coder's backend type (`"api"` → tool-calling) to these. `BuildChatSystemPrompt(vaultRoot, backendType)` is likewise backend-aware (tool-calling chat offers the file tools incl. `search_files`/`glob` but NOT the exec/network tools).
- **`agentPhilosophyBlock()`** — three-tier taxonomy (TIER 1 reasoning-only / TIER 2 one script / TIER 3 multi-file) with NOT-TO-DO lists; forces the coder to pick the simplest tier that solves the task (prevents writing a script for trivial reasoning work).
- **`agentArchitectureGateBlock()`** — mandatory TASK ANALYSIS → TIER DECISION → NOTIFICATION DECISION → SCHEDULE DECISION before any file is created. Supports no-notification (`[SILENT]`) and no-schedule (`none`) agents.
- **`ChatAppsForPlatforms()`** — central platform→`ChatAppInfo` (name + commands) mapping; callers load via `db.ListUserPlatformConnections` (no GatewayManager method needed).
- Design UX is non-technical: a jargon blocklist (FORBIDDEN: AGENT.md, Python, script, vault, cron, JSON, shell, Bash, webhook, endpoint); asks notification preference + schedule; emits a `[TECHNICAL SPEC]` for the code generator.

### Composio integration

Composio connects agents to 250+ external services via the **v3 REST API** (not the deprecated SDK).

- `composioServicesBlock()` in `internal/prompts/prompts.go` is the **single source** of the v3 spec — injected into design, create, and edit prompts. `composioRuntimeNote()` is the leaner RUN-time variant (agents already know their tool slugs; re-injecting the full discovery spec made them re-run discovery).
- Base: `https://backend.composio.dev/api/v3` · Auth: `x-api-key` header
- Connected accounts: `GET /connected_accounts?limit=100` · Execute: `POST /tools/execute/{TOOL_SLUG}`
- **Helper scripts are seeded, not retyped.** `internal/composioassets.WriteHelperFiles` writes the verified `composio_helper.py`+`composio_discover.py` into `tools/` (agents) / `scripts/` (skills) before the coder runs, so a weaker model can't garble the safety logic. At build time `SA_BUILD_PHASE=generation` makes the helper refuse real outbound/destructive Composio actions (see `internal/composioassets`).
- Guardrails block: SDK imports (`from composio import`), hardcoded keys (`ak_...`), wrong host (`api.composio.dev`), old versions (`/v1/`, `/v2/`) — the version/host checks are gated per-line on a `composio` reference so an unrelated API's real `/v2/` endpoint isn't flagged; seeded helper files are skipped (`IsSeededFilename`).

### Skill system (core + user skills)

Two pools of skills, both surfaced to the agent designer and the runner as `[]prompts.SkillRef`:

- **Core skills** — embedded in the binary (`internal/skilllibrary/skills/*/SKILL.md`, `go:embed`). Always-on for every user: no DB rows, no disk seeding, no admin gate. `LoadBundled()` enumerates metadata; `CoreSkillContent(slug)` returns the full SKILL.md (frontmatter+body) for agent-context injection when an agent declares the skill; `IsCoreSkill(slug)` is the reserved-name guard. `ParseMeta()` reads Anthropic+openclaw YAML frontmatter (name, description, version, license, category, `metadata.openclaw.requires.{bins,anyBins,env}`, `metadata.openclaw.install[]`). 12 bundled skills: csv, pdf, docx, pptx, xlsx, markdown, web-search, web-scraper, playwright-browser, google-workspace, github-integration, composio-toolkit, cli-tool-installer, skill-creator, skill-vetter.
- **User skills** — created via the skill creator (below) or imported (ZIP/pasted SKILL.md), per-workspace, written to `<vault>/skills/<name>/SKILL.md` (+ `scripts/`), tracked in the `skills` table. Loaded from disk by `skillstore`.

At run time (`agentrunner.runCoderAgent`), the agent's declared skills' content is injected into the coder prompt's `<skill_instructions>` block. Core skill content comes from the embed (`skilllibrary.CoreSkillContent`); user skill content is read from disk. `resolveSkillBins` resolves the absolute path of every CLI tool a declared skill requires (`requires.bins` / `anyBins`: `$HOME/.local/bin/<bin>` then `PATH`) and `prompts.SkillEnvBlock` builds a `<skill_environment>` block telling the agent where each tool lives (or to install it via the cli-tool-installer skill) plus sandbox conventions (invoke by absolute path, use `$TMPDIR` not `/tmp`, secrets are env vars, vault root).

**Skill format.** `skills/<name>/SKILL.md` (required: YAML frontmatter + markdown body) + optional `scripts/` (deterministic code) + `references/` (on-demand docs). Only `name`+`description` are strictly required; `description` is the trigger — it must say what the skill does AND the contexts that activate it. Tool names are written BARE in the body (the runtime env block supplies the real path).

**Conversational skill creator** (`internal/skilldesigner`, web `/dashboard/skills/new`): mirrors `agentdesigner.Flow`'s shape — FSM (`StateIdle → StateAwaitingResume → StateDescribing → StateDesigning → StateVerifying → StateDone`), SSE progress, 7-day drafts (`skill_drafts` table, one per user), strict/forgiving approval triggers (same split as the agent designer). Flow:
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
- **`[STATE]...[/STATE]`** — JSON merged into `state.json` (null = delete key). Inline and multi-line forms accepted.
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
- **`WithAPIConfig(provider, model, baseURL, apiKeySecretName)`** — switches the coder to the in-process API engine (`coder_kind=="api"`). Once set, `Generate`/`Chat`/`Ping` dispatch to the tool-calling loop instead of a subprocess. **`WithSecretsLookup(f)`** attaches the lazy provider-key resolver (the API engine fetches its own key by secret name at run time, so every call site authenticates regardless of env injection); **`WithVault(v)`** attaches the vault for the host file tools; **`WithProgress(f)`** streams per-tool-call milestones to the run SSE; **`IsAPI()`** reports the kind. `Ping(ctx, workspaceID)` now takes a workspace id (needed to resolve the API key).

**`CoderBackend`** (`internal/coder/backend.go`): `claudeBackend` (Claude CLI: `--output-format json`, `--setting-sources ""`) and `genericCLIBackend` (any other CLI, plain-text stdout). The API engine is not a `CoderBackend` — it bypasses the subprocess path entirely.

**Critical:** `--setting-sources ""` + no `--allowedTools` = subprocess hangs indefinitely (CLI engine only).

### API coder engine (`coder_kind == "api"`)

A workspace can run its coder as a **direct LLM provider API** instead of a host CLI binary. `Coder.runAPI`/`runToolLoop` (`internal/coder/api_engine.go`) drive an in-process loop: `Complete → execute host tools against the vault → feed results back → Complete`, until the model emits a final answer (no tool calls), the turn budget is spent, or the deadline passes. The model's final text carries the same `[CHAT]`/`[STATE]`/`[SILENT]` protocol markers, so the runner's parser is unchanged.

- **Host tools** (`hosttools.go`, `hostToolSet`): `read_file`/`write_file`/`edit_file`/`list_dir` are vault-path-safe (relative to workDir/vault root, escapes rejected). Two **always-on read-only discovery tools** (NOT exec-gated — safe in chat, closing the API-chat gap with the CLI chat's `Grep`/`Glob`): `search_files(query)` exposes the existing `vault.Searcher` (ripgrep + pure-Go fallback, case-insensitive fixed-string, 5 matches/file, skips `.kb`) so "find the note where I mentioned the dentist" is a TIER-1 lookup instead of a `read_file` walk; `glob(pattern)` finds files by name/pattern (`*`/`?`/`**`) across the vault via `compileGlob`→anchored regexp, skipping dotfiles + `.kb`; an **absolute-within-vault** path passed as the pattern is relativized first (mirror `resolveVault`) so a weak model that types the full vault path still matches, and an absolute path outside the vault is rejected. Both search the **whole vault root** (not workDir) and return a non-`error:` empty-result notice on no matches (so they never trip the oscillation guard). Three **exec tools** are gated behind `includeExecTools` (agent builds+runs only — workDir ≠ vault root; excluded from chat for CLI-parity): `run_script` (`python3`) and `bash` both run sandboxed via Landlock (`buildScriptCommand`) with the agent's secrets in env (provider key stripped), reporting stdout+stderr on failure; `web_fetch(url)` is an HTTP(S) client in the **host process** (no sandbox — it adds no capability agents lack via run_script/bash) that returns text (HTML reduced to readable text via a stdlib stripper), **retries transient 429/5xx/network internally** so a blip never trips the loop-guard, and **cannot carry secrets** (authenticated calls use run_script/bash); `web_search(query)` is the discovery complement — a keyless DuckDuckGo HTML scrape (`ddgHTMLEndpoint`, browser `User-Agent`) returning numbered title/url/snippet entries (real URL decoded from the `uddg` redirect param via `parseDDGResults`/`decodeDDGRedirect`, HTML stripped), with the same transient-retry contract as `web_fetch` and a 200-but-no-results page yielding `"(no search results)"` (non-error) so the model falls back to `web_fetch` without tripping the guard. `ddgBaseURL` (empty→production) lets tests point at an httptest server. All results are byte-capped and never empty (an empty tool result breaks strict serializers). This closes the CLI-vs-API capability gap: a simple public fetch/find is now TIER 1 via `web_fetch`/`web_search`/`search_files`/`glob` (see the network-split + file-discovery tier guidance in `prompts.agentArchitectureGateBlock`), matching a CLI coder. **Caveat:** an arbitrary `bash` string is sandboxed but NOT AST-scanned the way an authored `tools/*.py` is at build.
- **Turn budgets**: `maxAPITurns` (25) for runs/chat; `maxBuildAPITurns` (40) + `buildMaxTokens` (8192) for builds. A budget-exhausted loop gets one grace turn to wrap up: `[BLOCKED]` for a build (parsed by the designer), plain language for a run/chat.
- **Build-time script verification** (weak-model hardening, build only): the engine refuses to "finish" a build while the model authored a helper script that never once returned real output — `verifyFinishNudge` drives it to run/inspect/fix (bounded by `maxVerifyNudges`), or report the failure in plain language. Seeded Composio helpers don't count as verification. Plus a loop-guard (`recentFails` ring + `consecutiveFails`) that short-circuits repeated/oscillating failing calls.
- **Design conversations vs one-off chat** (`Chat` split by `noTools`): `chatAPI` (text-only single completion, real alternating user/assistant turns so the model doesn't re-ask its opening question) vs `chatToolsAPI` (adds the host file tools, minus the exec tools `run_script`/`bash`/`web_fetch`, for on-demand KB read/write — parity with the CLI chat's file-only set).
- **Providers** (`internal/llm`): `openai`, `openrouter`, `anthropic`, `generic` (any OpenAI-compatible endpoint; base URL required). Not probed — always available in the settings picker via `coder.APIProviders()`.

### Usage-limit / rate-limit detection

`coder.ErrUsageLimit` — CLI: non-zero exit with empty stdout+stderr; API: provider 402 (credits/quota exhausted, `ErrQuotaExhausted`). `coder.ErrRateLimited` — API transient 429 that didn't clear within the retry budget (distinct so the message says "try again in a moment", not "out of quota"). `coder.ErrAPIAuth` (bad/missing key) and `coder.ErrMaxTurns` (budget exhausted) are config/run errors, not usage limits. `agentrunner.friendlyRunError` converts each to a user-facing message sent via `input.SendOutput` on every run failure. Also handled softly during generation and design conversation turns. API token usage is accumulated across the loop (`coder.Usage`) and persisted per run.

### Guardrails

`internal/agentdesigner/guardrails.go`:
- `CheckEthics(code, "")` — blocklist (rm -rf, drop table, bitcoin wallet, etc.). Used on AGENT.md.
- `RunFullGuardrails(code, "")` — ethics + AST. Used on `tools/*.py`. AST blocks: `eval`, `exec`, `compile`, `__import__`, `os.system`, `subprocess.*`, `socket.socket`.
- Composio: blocks SDK imports, hardcoded keys, wrong host, v1/v2 endpoints.

### Per-workspace coder isolation

- `CLAUDE_CONFIG_DIR` → `<data_dir>/claude-homes/<workspaceID>/.claude/`
- `HOME` → `<data_dir>/claude-homes/<workspaceID>/`
- `--setting-sources ""` — suppresses `settings.json` and all `CLAUDE.md` traversal.
- `.credentials.json` copied from operator's `~/.claude/` on every invocation.

**`ANTHROPIC_CONFIG_DIR` does NOT work** — only `CLAUDE_CONFIG_DIR` redirects config.

### Coder filesystem confinement (Landlock)

`internal/sandbox` adds preventive filesystem confinement via Linux Landlock LSM. No external deps, no setuid, no namespaces.

**Mechanism:** `coder.buildCommand()` wraps the real command as `simple-agents __sandbox-exec <base64-spec>`. The helper applies `landlock.V5.BestEffort().RestrictPaths(...)` then `syscall.Exec`s the real command. Inherited by all children (`claude`→`bash`→`python`).

**Allowed:** RW: per-workspace HOME + agent workdir. RO: system paths, coder binary dir, the workspace's vault root. Denied: SQLite DB, config.yaml, other workspaces' vaults.

`config.SandboxConfig.Enabled` (default true; `SA_SANDBOX=0` disables). With Landlock unavailable, the sandbox is not applied and nothing physically prevents writes outside the vault — agents/chat run trusted within the user's own vault.

### Database

SQLite via `modernc.org/sqlite` (CGo-free). WAL mode + foreign keys set on open. Migrations in alphabetical order from `migrations/`.

The base schema was consolidated into `migrations/001_initial_schema.up.sql` during the workspace
refactor (the old incremental migrations were collapsed; data was wiped and re-created fresh);
incremental migrations resume from there — `002_coder_api` adds `workspaces.coder_base_url`, and
`003_agent_runs_usage` adds `agent_runs.{prompt,completion,total}_tokens` for the API coder.
Tables: `owner` (single row), `workspaces` (replaces `users`; carries `about` + inlined coder
config: `coder_kind`/`coder_bin`/`coder_timeout_s`/`coder_backend_type` + the now-active API-coder
fields `coder_provider`/`coder_model`/`coder_api_key_secret`/`coder_base_url`), `platform_connections`,
`platform_identities`, `agents`, `agent_schedules`, `agent_runs`, `secrets`, `chats`, `reminders`,
`workspace_permissions` (was `user_permissions`), `mcp_servers`, `workspace_settings` (was
`user_settings`), `system_settings` (owner/system-level, not tenant-scoped, no FK),
`audit_logs` (records active `workspace_id`; owner is the implicit actor), `schema_migrations`,
`chat_messages` (FK `chat_id`→`chats`), `skills`, `agent_skills` (keyed by `(agent_id, skill_name)`),
`agent_drafts`/`skill_drafts` (one row per workspace; 7-day TTL). Every tenant table keys off
`workspace_id`. There is **no** `coders` table — coder config is inlined on `workspaces`.

### Web UI routes

```
/login, /logout                     # owner login/logout
/change-password                    # owner password (requireOwner)
/dashboard/setup                    # per-workspace onboarding wizard (basics → master_password → coder → profile → connector → composio → done)
/workspace/leave                    # POST: leave the active workspace (owner stays logged in)
/dashboard                          # active-workspace home
/dashboard/agents                   # list agents
/dashboard/agents/new               # conversational agent creation
/dashboard/agents/design            # POST: drives design FSM turn-by-turn
/dashboard/agents/design/cancel     # POST: cancel active design session
/dashboard/agents/design/progress   # GET SSE: generation milestone events
/dashboard/agents/:id               # detail: AGENT.md editor, state, logs, schedule, skills
/dashboard/agents/:id/edit          # conversational agent editing
/dashboard/agents/:id/edit/start    # POST: starts an edit design session
/dashboard/agents/:id/delete
/dashboard/agents/:id/run           # POST: starts background run, 303-redirects (PRG)
/dashboard/agents/:id/run/progress  # GET SSE: live [CHAT] output
/dashboard/agents/:id/schedule[/delete]
/dashboard/agents/:id/agent-md      # POST: save AGENT.md (ethics check)
/dashboard/agents/:id/skills        # POST: update agent skill assignments
/dashboard/skills                     # list: your skills + core skills (always-on) + draft-resume card
/dashboard/skills/new                 # conversational skill creator (chat UI)
/dashboard/skills/design              # POST: drive skill-creator FSM turn-by-turn (JSON {name,message})
/dashboard/skills/design/cancel       # POST: cancel active skill-design session
/dashboard/skills/design/resume       # POST: resume a saved skill draft
/dashboard/skills/design/dismiss      # POST: discard a saved skill draft
/dashboard/skills/design/progress     # GET SSE: skill-generation milestone events
/dashboard/skills/core/:slug         # GET: read-only view of an embedded core skill
/dashboard/skills/:id                 # user skill detail (edit/delete)
/dashboard/secrets
/dashboard/connectors
/dashboard/chats                     # list chats; per-chat detail has composer (send msg), resume/stop/delete
/dashboard/chats/:id                # chat detail: history + message composer
/dashboard/chats/:id/messages        # POST: send one message (AJAX JSON {message} → {response}|{error}; coder one-off-chat path with KB tools, persists turn)
/dashboard/chats/:id/resume          # POST: resume a stopped chat
/dashboard/reminders
/dashboard/memory                   # redirects to /dashboard/kb?path=memory
/dashboard/kb                       # knowledge-base file browser
/dashboard/kb/view|edit|raw         # GET: render / edit / download note
/dashboard/kb/save|new|delete|rename# POST: mutate notes/folders
/dashboard/kb/search                # GET: ripgrep full-text search
/dashboard/settings                 # user profile + change master password
/dashboard/settings                 # workspace profile + name/about + coder config + change master password
/dashboard/settings/workspace       # POST: save workspace name + about
/dashboard/settings/coder           # POST: save workspace coder config
/dashboard/settings/master-password # POST: change workspace master password (re-encrypts secrets)
/admin                              # owner dashboard: workspace cards + stats + recent audit
/admin/workspaces, /admin/workspaces/:id
/admin/workspaces/:id/enter         # POST: master-password gate → set active workspace
/admin/workspaces/:id/delete
/admin/workspaces/:id/permissions[/:perm/revoke]
/admin/settings                    # system settings
/admin/audit
```

### Owner vs. workspace separation

- **Owner**: only `/admin/*` (guarded by `requireOwner`) — manages workspaces, system settings, audit.
- **Active workspace**: `/dashboard/*` (guarded by `requireOwner` + `requireActiveWorkspace`) — agents, secrets, connectors, chats, reminders, KB, scoped to the entered workspace.
- Middleware (`web/server.go`): `requireOwner` (session `owner_id` → `c.Set("owner")`), `requireActiveWorkspace` (session `active_workspace_id` → `c.Set("workspace")`; redirects to `/admin` if none), `requireSetupComplete` (redirects to `/dashboard/setup` while `needs_setup`). Handlers read `c.Get("workspace").(*db.Workspace)`.
- Entering/switching: `handleEnterWorkspace` decrypts the workspace's `encrypted_master_password` with the system key and compares to the typed one (an access gate — the stored form must remain so the scheduler can decrypt for headless cron runs). Re-prompts on every switch; `handleLeaveWorkspace` clears the active workspace.

### Per-workspace coder

Each workspace inlines its own coder config on the `workspaces` row (`coder_kind` `local`/`api`,
`coder_bin`, `coder_timeout_s`, `coder_backend_type`, and for `api`:
`coder_provider`/`coder_model`/`coder_api_key_secret`/`coder_base_url`). `coder.ForWorkspace(w, …)`
builds a `*coder.Coder` from it — a **local** CLI coder or the **api** engine — falling back to the
system defaults when unset; `coder.DetectInstalled()` probes PATH + `~/.local/bin` for supported
binaries (claude/claude-code, opencode, codex, cursor) and `coder.APIProviders()` lists the direct-API
providers to populate the picker. The web `coderForWorkspace(id)` and the runner's injected coder
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
- **Discord adapter** — not implemented.
- **`/remind` list/delete via Telegram** — only create is wired.
- **Skill creator via Telegram** — `internal/skilldesigner.Flow` supports the Telegram states (`StateDescribing`, `StateAwaitingResume`) but the gateway router has no `/skill` command route; the skill creator is web-only for now (platform-parity gap).
- **MCP servers** — `mcp_servers` table exists; MCP tool execution not implemented.
