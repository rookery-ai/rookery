# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

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

# Bootstrap admin user (first run only)
./bin/simple-agents admin bootstrap

# Database migration
./bin/simple-agents db migrate
```

AST guardrail tests shell out to `python3`. If Python is not available, those tests self-skip.

## Architecture

### Entry point & wiring

`cmd/simple-agents/main.go` loads `config.yaml` via `internal/config`, wires all services, and
delegates subcommands via `github.com/urfave/cli/v3`. The `serve` subcommand:
1. Opens/migrates SQLite DB
2. Creates secrets service, coder, agent designer, agent runner, skill designer (`skilldesigner.Flow`)
3. Starts `GatewayManager` (loads all `platform_connections` from DB, starts per-user adapters)
4. Starts scheduler and reminder background goroutines (nightly GC also sweeps expired `skill_drafts` + orphaned staging dirs)
5. Starts Echo web server

### Inbound message pipeline

```
Telegram adapter (per-user bot instance)
  → GatewayManager.route()
    → IdentityResolver  (platform_user_id → internal user_id via platform_identities table)
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
| `internal/auth` | CreateUser, Authenticate, ChangePassword, GenerateTempPassword, bcrypt |
| `internal/rbac` | `CanPerform(db, userID, permission)` — reads `user_permissions` table |
| `internal/secrets` | AES-256-GCM store; Argon2id key derivation; `GetAll()` decrypts all for env injection; `Proxy()` resolves `${NAME}` in-memory only |
| `internal/gateway` | `Gateway` interface, `GatewayManager`, `Router`, `IdentityResolver`, `TelegramGateway` |
| `internal/coder` | `Coder`: runs coder CLI subprocess with full per-user isolation; `CoderBackend` interface abstracts Claude vs. generic CLIs; `WithNoTools()` for text-only calls; `WithExtraEnv()` for secret injection |
| `internal/agentdesigner` | `Flow` FSM (Describing→Designing→Verifying→Done); conversational design shared between web and Telegram; auto-schedule; `RunFullGuardrails`/`RunToolGuardrails` (ethics + AST only); `toolstree.go` recursive path-safe `WriteToolsTree`/`ReadToolsTree` for multi-file projects; `isTestArtifact` classifier + `cleanupTestArtifacts` (post-save junk removal) |
| `internal/skilldesigner` | Conversational skill-creator wizard mirroring `agentdesigner.Flow` (FSM Idle→AwaitingResume→Describing→Designing→Verifying→Done, SSE progress, 7-day drafts, approval triggers); `SkillSaver` writes SKILL.md+scripts/ to vault + DB upsert; generation runs with the `skill-creator` core skill, vetting runs the `skill-vetter` core skill as a text-only audit; `vettingBlocksSave()` parses the verdict line. Web-wired only (Telegram route not yet added). |
| `internal/skilllibrary` | Embedded core skill catalog (`go:embed skills/*/SKILL.md`) — always-on for every user, no DB rows, no admin gate. `LoadBundled()`, `CoreSkillContent(slug)`, `IsCoreSkill()`, `ParseMeta()` (Anthropic+openclaw YAML frontmatter: requires.bins/anyBins/env, install specs). Supersedes the admin-catalog approach dropped in migration 009. |
| `internal/agentrunner` | Load agent → decrypt secrets into env via `WithExtraEnv` → coder subprocess → capture `[CHAT]` lines → send via GatewayManager; timestamped run logs; `RunInput.OnProgress` per-turn hook for live SSE streaming. Skills pool = core skills (embedded) + user skills; `resolveSkillBins` resolves declared tools' paths for the runtime `<skill_environment>` block; `loadDeclaredSkillContent` reads core skills from the embed. **Reliable delivery**: `parseCoderOutput` (blank lines don't end `[CHAT]`; empty `[CHAT]` dropped; `[SILENT]` detected) + `extractProseMessage` fallback when no `[CHAT]` emitted and not silent → visible warning when nothing deliverable. Covered by `runner_test.go`. |
| `internal/sandbox` | Self-contained Landlock filesystem confinement for coder subprocesses (Linux). `Spec`, `Supported()`, `Wrap()` (re-exec via the hidden `__sandbox-exec` helper), `Exec()` (applies Landlock + rlimits, then `execve`). No external dependency. |
| `internal/scheduler` | Cron scheduler: polls `agent_schedules`, fires runner, decrypts stored master password for secret injection; `WithSender()` delivers output to users |
| `internal/reminder` | Creates/lists/fires reminders; background polling goroutine. Reminders live only in the DB and the reminders UI tab — they are NOT reflected to the vault. |
| `internal/chat` | `Chat` create/list/stop/resume/delete; 30-min idle auto-stop; `BuildUserContext` (shared **identity-only** context builder for one-off chat — profile/memory/agents/MCP; the broader KB is retrieved on demand via tools, not injected here) |
| `internal/prompts` | Central home for all LLM prompt construction: `BuildDesignSystemPrompt` (+ `<knowledge_base>` block + `KBManifest`), `BuildImplementationPrompt`, `BuildEditImplementationPrompt` (diagnose-before-fix), `BuildCoderPrompt` (+ `<skill_instructions>` + `<skill_environment>` blocks), `BuildChatSystemPrompt` (chat read+write KB instruction), `BuildChildAgentFollowUpPrompt`, `BuildSkillMetaPrompt`, `BuildReminderParsePrompt`, skill-creator prompts (`BuildSkillDesignSystemPrompt`, `BuildSkillImplementationPrompt`, `BuildSkillVettingPrompt`, `SkillEnvBlock`). `SkillRef`/`SkillBin` types. No inline prompt text exists outside this package. Shared single-source blocks: `agentPhilosophyBlock` (three-tier), `platformContextBlock`, `coderCapabilitiesBlock` (backend-aware), `agentArchitectureGateBlock`, `testingRulesBlock` (one bounded smoke test + dry run; real secrets at build time, no outbound sends), `shellSafetyBlock`, `scriptRobustnessBlock`, `composioServicesBlock`. `ChatAppsForPlatforms` + `MapCoderBackend` bridge callers to prompt params. |
| `internal/memory` | Per-user structured context store. Memory lives as named `.md` files in `memory/` (`USER.md`, `SOUL.md`, `GENERAL.md`, etc.) — editable via the KB browser. `ContextString()` reads all files, skips placeholder-only ones, and returns sectioned markdown for LLM injection. `Append/List/Delete` target GENERAL.md bullet lines (used by Telegram `/memory` command). `MigrateToStructuredFiles()` consolidates legacy UUID-keyed entries at startup. |
| `internal/vault` | Per-user Obsidian-style knowledge base: `Vault` (paths + `Resolve` safety + file IO), `Reflector` (chats→markdown+sidecar), `LinkIndex` ([[wikilinks]]), `Searcher` (ripgrep), `Guard` (post-run write-scope enforcement), `MigrateLegacyLayout`, `MigrateSessionsToChats`. |
| `internal/audit` | Structured audit event writer → `audit_logs` table |
| `internal/profile` | Per-user personalization (name, email, location, timezone, tone, language, notes); stored in the generic `settings` table; `Load()`/`Save()`/`ContextString()` for LLM injection; `LoadLocation()` for timezone-aware reminder parsing |
| `internal/skillstore` | `SkillStore`: install/load/delete SKILL.md based skills per user. `SkillDir(base, userID, name)` is the path helper shared with the skill designer (staging dirs use the `.staging-<name>` convention). |
| `web/` | Echo v4 web server; full user dashboard + admin UI |

### Per-user knowledge base (vault)

Every user has one Obsidian-style vault — a single directory of interlinked markdown notes.
`internal/vault` owns all vault path/IO/safety logic. The SQLite DB remains the system-of-record;
chats are *reflected* into the vault as markdown + JSON sidecars.

```
<data_dir>/vaults/<userID>/
├── README.md                       # vault home note (scaffolded)
├── notes/                          # user-authored notes/journals/plans/todos
├── memory/
│   ├── USER.md                     # user profile — name, location, role, background
│   ├── SOUL.md                     # communication style and preferences
│   ├── GENERAL.md                  # quick notes added via /memory Telegram command
│   └── <any>.md                    # additional context files the user creates
├── skills/<name>/SKILL.md          # per-user skills
├── agents/<agentID>/               # an agent's OWN writable area
│   ├── AGENT.md  agent.json  state.json
│   ├── tools/*.py  notes/*.md
│   └── logs/run_<ts>.md
├── chats/<id>.md                   # reflected chat transcripts
└── .kb/                            # internal: db-export/ JSON sidecars, links.json (hidden)
```

`claude-homes/<userID>/` stays OUTSIDE the vault (holds `.claude` credentials — never backed up).

**Memory injection.** All `.md` files in `memory/` are automatically injected into every LLM
context (design sessions, agent runs, one-off chat) via `memory.ContextString()`. Files whose
body is only headings and HTML placeholder comments are silently skipped until the user fills
them in. `EnsureScaffold` creates `USER.md` and `SOUL.md` with placeholder content on first visit.

Key types in `internal/vault`:
- **`Vault`** — `Root/AgentsDir/AgentDir/MemoryDir/SkillsDir`; **`Resolve(userID, rel)`** is the security primitive every read/write path uses (rejects `..`/absolute escapes); `WriteNote` (atomic), `Read/Delete/Rename/List/EnsureScaffold`. `List` hides dotfiles.
- **`Reflector`** — `ReflectChat/ReflectAgentRun`: markdown note + `.kb/db-export/<table>/<id>.json` sidecar. Reminders are NOT reflected.
- **`LinkIndex`** — `[[wikilink]]` parsing/resolution + `RenderHTMLLinks`; `Backlinks`.
- **`Searcher`** — `ripgrepSearcher` (rg `--json`, pure-Go fallback).
- **`Guard`** — detective post-run write-scope enforcement (snapshot/revert). No longer wired into agent runs (the policy changed to let agents edit the KB directly — see "Agent access model"); the type + tests remain as a reusable utility.
- **`MigrateLegacyLayout()`** — idempotent startup migration of pre-vault `agents/`, `memory/` (jsonl→md), `skills/` into vaults.

**Agent access model.** An agent's run CWD is its own vault dir; the coder prompt (`BuildCoderPrompt`, `<knowledge_base>` block) tells it to READ the whole vault and WRITE to both its own dir and the user's knowledge base (notes, memory, user files) — durable knowledge is persisted into the KB across runs. The Landlock sandbox grants RW over the whole vault root (confined to that user's vault + HOME; the DB, config, and other users' vaults stay out of reach). System-managed dirs (`.kb/`, `chats/`, other agents' `agents/<id>/`) are off-limits by prompt, not hard-enforced. The chat uses the same model (see `prompts.BuildChatSystemPrompt`).

**Chat knowledge-base access (on-demand retrieval + editing).** The one-off chat coder runs with `WithDir(vaultRoot).WithAllowedTools("Read,Write,Edit,Glob,Grep")` and a system instruction (`prompts.BuildChatSystemPrompt`) naming the vault root. The LLM retrieves and edits the user's notes **on demand** — only on turns that touch the KB — instead of having the vault injected every prompt. `chat.BuildUserContext` now returns identity-only context (profile/memory/agents/MCP); the old always-on `[Related knowledge base]` keyword-snippet block was removed. The tool set is file-only (no `Bash`/`WebFetch`): the chat can create/edit/read notes but cannot delete, rename, or run shell commands. The same applies to agents (RW over the vault via the sandbox). The detective `Guard` is no longer wired into agent runs — it would revert the KB edits that are now intentional — so agent/chat KB edits persist.

**Agent designer KB awareness.** The designer is text-only (`WithNoTools`) but its system prompt (`BuildDesignSystemPrompt`, `<knowledge_base>` block) now knows the app has a built-in vault that agents read/write, and is told to prefer it over Notion/external note apps for the user's own knowledge. Each design turn injects a fresh manifest of the user's existing note paths via `Flow.WithKBLister` → `vault.NotePaths(userID)` → `DesignSystemParams.KBManifest`, so the designer can reference the user's actual notes.

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

**SSE progress:** `DesignSession` carries `progressCh chan string` + `cancelGenerate`. `GetProgressChan(userID)` lets the web SSE handler stream milestone events to the browser.

**Auto-schedule:** If AGENT.md starts with `# Suggested schedule: */10 * * * *`, `parseSuggestedSchedule()` calls `db.UpsertAgentSchedule()` immediately.

**Skills selection via `# Skills:` header.** `DesignSession.Skills` is now `[]prompts.SkillRef` (name+description), loaded once on start as **core skills (embedded) + the user's own skills**. The designer's `<available_skills>` block lists each skill with its description and instructs the coder to emit a `# Skills: skill-one, skill-two` header line in AGENT.md (alongside the schedule line). `parseSkillsLine(agentMD, installed)` reads that header, validates names against the installed set, and returns only known names; if the line is absent, `saveAndFinish`/`updateAndFinish` fall back to all installed skills. This is how an agent declares which skills it actually uses at run time.

### Prompt architecture (coder-agnostic, three-tier)

All prompts live in `internal/prompts` (single source). The designer produces **coder-agnostic** AGENT.md — it says WHAT to do, never runtime-specific tool names (so it works on a full coder like claude-code/codex OR a basic model call like OpenRouter GLM). HOW the coder acts on files is injected separately based on `BackendType`:

- **`platformContextBlock(chatApps, vaultRoot)`** — full Simple Agents primer (flexible ever-growing KB with USER-REORGANIZABLE vs SYSTEM-WRITTEN fixed locations, secrets store, chats, reminders, connected chat apps + commands, output protocol, schedule). Injected into design, implementation, and runtime prompts.
- **`coderCapabilitiesBlock(backendType)`** — `BackendFullCoder` → direct tool access; `BackendBasicModel` → `[READ_FILE]`/`[WRITE_FILE]`/`[RUN_SCRIPT]` output markers. `MapCoderBackend()` maps the coder's backend type to these.
- **`agentPhilosophyBlock()`** — three-tier taxonomy (TIER 1 reasoning-only / TIER 2 one script / TIER 3 multi-file) with NOT-TO-DO lists; forces the coder to pick the simplest tier that solves the task (prevents writing a script for trivial reasoning work).
- **`agentArchitectureGateBlock()`** — mandatory TASK ANALYSIS → TIER DECISION → NOTIFICATION DECISION → SCHEDULE DECISION before any file is created. Supports no-notification (`[SILENT]`) and no-schedule (`none`) agents.
- **`ChatAppsForPlatforms()`** — central platform→`ChatAppInfo` (name + commands) mapping; callers load via `db.ListUserPlatformConnections` (no GatewayManager method needed).
- Design UX is non-technical: a jargon blocklist (FORBIDDEN: AGENT.md, Python, script, vault, cron, JSON, shell, Bash, webhook, endpoint); asks notification preference + schedule; emits a `[TECHNICAL SPEC]` for the code generator.

### Composio integration

Composio connects agents to 250+ external services via the **v3 REST API** (not the deprecated SDK).

- `composioServicesBlock()` in `internal/prompts/prompts.go` is the **single source** of the v3 spec — injected into design, create, and edit prompts.
- Base: `https://backend.composio.dev/api/v3` · Auth: `x-api-key` header
- Connected accounts: `GET /connected_accounts?limit=100` · Execute: `POST /tools/execute/{TOOL_SLUG}`
- Guardrails block: SDK imports (`from composio import`), hardcoded keys (`ak_...`), wrong host (`api.composio.dev`), old versions (`/v1/`, `/v2/`).

### Skill system (core + user skills)

Two pools of skills, both surfaced to the agent designer and the runner as `[]prompts.SkillRef`:

- **Core skills** — embedded in the binary (`internal/skilllibrary/skills/*/SKILL.md`, `go:embed`). Always-on for every user: no DB rows, no disk seeding, no admin gate. `LoadBundled()` enumerates metadata; `CoreSkillContent(slug)` returns the full SKILL.md (frontmatter+body) for agent-context injection when an agent declares the skill; `IsCoreSkill(slug)` is the reserved-name guard. `ParseMeta()` reads Anthropic+openclaw YAML frontmatter (name, description, version, license, category, `metadata.openclaw.requires.{bins,anyBins,env}`, `metadata.openclaw.install[]`). 12 bundled skills: csv, pdf, docx, pptx, xlsx, markdown, web-search, web-scraper, playwright-browser, google-workspace, github-integration, composio-toolkit, cli-tool-installer, skill-creator, skill-vetter.
- **User skills** — created via the skill creator (below) or imported (ZIP/pasted SKILL.md), per-user, written to `<vault>/skills/<name>/SKILL.md` (+ `scripts/`), tracked in the `skills` table. Loaded from disk by `skillstore`.

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
- **`WithDir(dir)`** — overrides subprocess CWD. Used by generation AND every agent run — without it the agent writes to the shared per-user home and contaminates other agents.
- **`WithExtraEnv(env)`** — merges additional env vars. System overrides always take precedence.
- **`WithBackendType(t)`** — forces `"claude"`, `"generic"`, or `""` (auto-detect by binary name).

**`CoderBackend`** (`internal/coder/backend.go`): `claudeBackend` (Claude CLI: `--output-format json`, `--setting-sources ""`) and `genericCLIBackend` (any other CLI, plain-text stdout).

**Critical:** `--setting-sources ""` + no `--allowedTools` = subprocess hangs indefinitely.

### Usage-limit detection

`coder.ErrUsageLimit` — non-zero exit with empty stdout+stderr. `agentrunner.friendlyRunError` converts to a user-facing message sent via `input.SendOutput` on every run failure. Also handled softly during generation and design conversation turns.

### Guardrails

`internal/agentdesigner/guardrails.go`:
- `CheckEthics(code, "")` — blocklist (rm -rf, drop table, bitcoin wallet, etc.). Used on AGENT.md.
- `RunFullGuardrails(code, "")` — ethics + AST. Used on `tools/*.py`. AST blocks: `eval`, `exec`, `compile`, `__import__`, `os.system`, `subprocess.*`, `socket.socket`.
- Composio: blocks SDK imports, hardcoded keys, wrong host, v1/v2 endpoints.

### Per-user coder isolation

- `CLAUDE_CONFIG_DIR` → `<data_dir>/claude-homes/<userID>/.claude/`
- `HOME` → `<data_dir>/claude-homes/<userID>/`
- `--setting-sources ""` — suppresses `settings.json` and all `CLAUDE.md` traversal.
- `.credentials.json` copied from operator's `~/.claude/` on every invocation.

**`ANTHROPIC_CONFIG_DIR` does NOT work** — only `CLAUDE_CONFIG_DIR` redirects config.

### Coder filesystem confinement (Landlock)

`internal/sandbox` adds preventive filesystem confinement via Linux Landlock LSM. No external deps, no setuid, no namespaces.

**Mechanism:** `coder.buildCommand()` wraps the real command as `simple-agents __sandbox-exec <base64-spec>`. The helper applies `landlock.V5.BestEffort().RestrictPaths(...)` then `syscall.Exec`s the real command. Inherited by all children (`claude`→`bash`→`python`).

**Allowed:** RW: per-user HOME + agent workdir. RO: system paths, coder binary dir, user's vault root. Denied: SQLite DB, config.yaml, other users' vaults.

`config.SandboxConfig.Enabled` (default true; `SA_SANDBOX=0` disables). With Landlock unavailable, the sandbox is not applied and nothing physically prevents writes outside the vault — agents/chat run trusted within the user's own vault.

### Database

SQLite via `modernc.org/sqlite` (CGo-free). WAL mode + foreign keys set on open. Migrations in alphabetical order from `migrations/`.

Schema tables: `users`, `platform_connections`, `platform_identities`, `agents`, `agent_schedules`, `agent_runs`, `secrets`, `chats`, `reminders`, `user_permissions`, `mcp_servers`, `settings`, `audit_logs`, `schema_migrations`, `chat_messages` (FK `chat_id`→`chats`), `coders`, `skills`, `agent_skills`, `skill_drafts` (one row per user; 7-day TTL; FK `user_id`→`users`). Note: migration 008 created an admin `skill_library` catalog table; migration 009 dropped it (and `skills.library_slug`/`library_version`) when core skills pivoted to embedded-only.

### Web UI routes

```
/login, /logout
/setup                              # first-login wizard (password → master_password → profile → connector → done)
/change-password
/dashboard                          # user home
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
/admin, /admin/users, /admin/users/:id
/admin/users/:id/coder[/unassign]
/admin/coders, /admin/coders/:id
/admin/settings
/admin/audit
```

### Admin vs. user separation

- **Admin** (`role="admin"`): only `/admin/*` — manages users, coder profiles, settings, audit.
- **Regular users**: only `/dashboard/*` — agents, secrets, connectors, chats, reminders, KB.
- `requireUserOnly` on `/dashboard/*` redirects admins to `/admin`.
- `requireAdmin` on `/admin/*` rejects non-admins with 403.

### Multi-coder system

Admin creates **Coder Profiles** (`coders` table) with `claude_bin`, `timeout_s`, `backend_type`. Users assigned via `users.coder_id` FK. `coderForUser(userID)` builds the right `*coder.Coder`, falling back to system defaults when unassigned.

### Natural language reminders

`internal/reminder/timeparser.go` — `ParseNaturalTime(text, now, loc)` parses expressions like `"in 10 minutes"`, `"tomorrow at 3pm"`, `"next Tuesday at noon"`. Both web UI and Telegram use `profile.LoadLocation(db, userID)` so reminders fire in the user's timezone.

---

## Known gaps

- No integration or e2e test coverage — unit tests cover logic boundaries; coder subprocess round-trips (real edit → test → approve) are exercised manually.
- **Discord adapter** — not implemented.
- **`/remind` list/delete via Telegram** — only create is wired.
- **Skill creator via Telegram** — `internal/skilldesigner.Flow` supports the Telegram states (`StateDescribing`, `StateAwaitingResume`) but the gateway router has no `/skill` command route; the skill creator is web-only for now (platform-parity gap).
- **MCP servers** — `mcp_servers` table exists; MCP tool execution not implemented.
