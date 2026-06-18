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
2. Creates secrets service, coder, agent designer, agent runner
3. Starts `GatewayManager` (loads all `platform_connections` from DB, starts per-user adapters)
4. Starts scheduler and reminder background goroutines
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
      → /session → db.ChatSession (start/list/stop)
      → /memory → memory.Store
      → plain text → one-off chat (coder.Coder)
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
| `internal/agentdesigner` | `Flow` FSM (Describing→Designing→Done); conversational design shared between web and Telegram; auto-schedule; `RunFullGuardrails` (ethics + AST only) |
| `internal/agentrunner` | Load agent → decrypt secrets into env via `WithExtraEnv` → coder subprocess → capture `[CHAT]` lines → send via GatewayManager; timestamped run logs |
| `internal/sandbox` | Firejail wrapper (available for future use; not used by coder agents) |
| `internal/scheduler` | Cron scheduler: polls `agent_schedules`, fires runner, decrypts stored master password for secret injection; `WithSender()` delivers output to users |
| `internal/reminder` | Creates/lists/fires reminders; background polling goroutine |
| `internal/session` | `ChatSession` create/list/stop; 30-min idle auto-stop |
| `internal/prompts` | Central home for all LLM prompt construction: `BuildDesignSystemPrompt`, `BuildImplementationPrompt`, `BuildEditImplementationPrompt`, `BuildCoderPrompt`, `BuildChildAgentFollowUpPrompt`, `BuildSkillMetaPrompt`. No inline prompt text exists outside this package. |
| `internal/memory` | Per-user append-only JSONL store; `ContextString()` formats entries as a bullet list for LLM injection; injected into agent design sessions and agent run prompts via `WithMemory()` on both `Flow` and `Runner` |
| `internal/audit` | Structured audit event writer → `audit_logs` table |
| `internal/profile` | Per-user personalization (name, email, location, timezone, tone, language, notes); stored in the generic `settings` table; `Load()`/`Save()`/`ContextString()` for LLM injection; `LoadLocation()` for timezone-aware reminder parsing; `IsComplete()`/`MarkComplete()` sentinel for setup wizard |
| `internal/skillstore` | `SkillStore`: install/load/delete SKILL.md based skills per user |
| `web/` | Echo v4 web server; full user dashboard + admin UI |

### Agent file layout on disk

```
<data_dir>/agents/<userID>/<agentID>/
├── agent.json          # manifest: id, name, required_secrets, skills, created_at (no type field)
├── AGENT.md            # agent instructions (read by coder on every run)
├── state.json          # persistent key-value state, starts as {}
├── tools/              # optional Python helper scripts (placed here by system)
│   └── fetch_price.py
└── logs/
    └── run_log_20060102_150405.txt  # one timestamped file per run, all kept
```

Path helpers in `internal/agentdesigner/manifest.go`:
- `AgentDescPath(dir, userID, agentID)` → `AGENT.md`
- `AgentStatePath(dir, userID, agentID)` → `state.json`
- `AgentLogsDir(dir, userID, agentID)` → `logs/`
- `AgentLogPath(dir, userID, agentID, t)` → timestamped log file

Runner falls back to `CLAUDE.md` if `AGENT.md` is missing (legacy agent support).

### Unified conversational agent creation

Agent creation uses a single `agentdesigner.Flow` FSM shared between the Telegram bot and the web UI. There is **no agent type** — every agent is the same structure.

**FSM states:**
- `StateDescribing` — Telegram only: waiting for description after `/agent create <name>`
- `StateDesigning` — free-form Q&A with the coder until user says "approve"
- `StateVerifying` — test run shown to user; waiting for confirmation or change requests
- `StateDone`

**Web path:** `StartDesign(ctx, userID, agentName, firstMessage)` — creates session in `StateDesigning` directly, returns first coder response.

**Telegram path:** `Start(userID, agentName)` → `StateDescribing` → `Step()` transitions to `StateDesigning`.

**Step returns:** `(response string, isDone bool, agentID string, err error)`

**Approval triggers** (exact match only — "yes"/"ok" do NOT trigger):
`"approve"`, `"go ahead"`, `"build it"`, `"create it"`, `"/approve"`

**On approval:** `runGeneration()` creates the agent directory on disk, then calls the coder **with full tools** (`WithDir(agentDir).WithAllowedTools("Bash,Write,Edit,Read")`). Claude Code writes AGENT.md and `tools/*.py` directly to disk, runs the scripts via Bash, fixes any errors, and outputs `[TEST_OUTPUT]...[/TEST_OUTPUT]` with the verified result. The flow reads AGENT.md back from disk, runs guardrails, stores content in `PendingAgentMD`/`PendingTools`, and moves to `StateVerifying` — showing the test output to the user. **`StateVerifying` is only entered when `[TEST_OUTPUT]` is present**; if the coder omits it, the user stays in `StateDesigning` and "approve" retries generation rather than saving unverified content.

**Generation failure handling:** `runGeneration` converts failure conditions into soft responses (not Go errors) so the user stays in `StateDesigning`:
- `[BLOCKED]...[/BLOCKED]` marker: coder signals an impossible task; `parseBlockedOutput()` extracts the explanation + alternatives and returns them to the user.
- `ErrUsageLimit`: friendly "hit its usage limit — try again in a while" message.
- Timeout (`"timed out"` in error text): friendly "too complex — try simpler steps" message.
- `callCoder()` (design Q&A turns) also handles `ErrUsageLimit` softly — no HTTP 500 for a usage limit hit during a plain conversation turn.

**SSE progress during generation:** `DesignSession` carries a buffered `progressCh chan string` and `cancelGenerate context.CancelFunc`. `SetProgressHandler(userID, fn)` lets the Telegram router register a callback before `Step()` blocks on generation. `GetProgressChan(userID)` lets the web SSE handler stream milestone events to the browser. `Cancel()` kills the in-flight subprocess via `cancelGenerate()`.

**`WithNoTools()`** is used for design conversation turns only. Generation uses full tools so Claude Code can actually write and execute the agent before showing results.

**`StateVerifying`:** The user sees real test output and approves or requests changes. On approval → `finalizeAgent()` → `saveAndFinish()` saves `PendingAgentMD`/`PendingTools` via `SaveAgent()`. On change request → back to `StateDesigning`.

**Auto-schedule:** If AGENT.md starts with `# Suggested schedule: */10 * * * *`, `parseSuggestedSchedule()` validates the cron expression and calls `db.UpsertAgentSchedule()` — the schedule is set immediately on agent creation.

**`dbDesignStore` interface** (implemented by `*db.DB`):
```go
type dbDesignStore interface {
    ListSkills(userID string) ([]*db.Skill, error)
    ListUserPlatformConnections(userID string) ([]*db.PlatformConnection, error)
    UpsertAgentSchedule(s *db.AgentSchedule) error
    GetAgent(id string) (*db.Agent, error)
    GetScheduleForAgent(agentID string) (*db.AgentSchedule, error)
    DeleteAgentSchedule(agentID string) error
    GetSetting(userID, key string) (string, error)
}
```

**System prompt context injected into every design turn** (assembled by `prompts.BuildDesignSystemPrompt`):
- Connected platforms (e.g. Telegram) — coder knows to use `[CHAT]` not raw Telegram API
- User profile (`[User profile]` block) — name, location, timezone, tone, language, notes from `internal/profile`; loaded once on session start via `loadUserProfile()`; empty if no fields saved
- Secrets guidance — tell user to add to Secrets store; never paste in chat; reference as `os.environ['NAME']`
- Scheduling guidance — detect frequency from conversation; propose cron; auto-set on creation
- Non-technical user style — explain API keys and cron in plain language; one/two questions per turn

### Conversational agent editing

Editing reuses the same `Flow` FSM as creation via `DesignSession.IsEdit` — no parallel "EditFlow". Entry points mirror the create path:

- **Telegram:** `/agent edit <name>` → `Flow.StartEdit(userID, agentID)` → `StateDescribing`.
- **Web:** `GET /dashboard/agents/:id/edit` (chat UI) → `POST /dashboard/agents/:id/edit/start` → `Flow.StartEditDesign(ctx, userID, agentID, firstMessage)` → `StateDesigning` directly. Continuation reuses the existing generic `POST /dashboard/agents/design` stepper unchanged (it only checks `req.Name` when no session exists yet) and the existing `POST /dashboard/agents/design/cancel`.

Both loaders go through `loadAgentForEdit(userID, agentID)`, which reads the live `AGENT.md` and then **reconciles its `# Suggested schedule:` first line against the real `agent_schedules` row** before the coder ever sees it. This matters because the web schedule-editor form (`handleSaveSchedule`) writes the DB directly and never touches `AGENT.md` — the two can drift. After reconciliation, `AGENT.md` is the single source of truth for the rest of the edit conversation and for finalize.

**Generation never touches the live agent dir before approval** (it may be scheduled and running unattended). `runGeneration` branches on `IsEdit`:
- Create (unchanged): writes directly into the not-yet-saved `agentDir`.
- Edit: `copyAgentWorkspace()` copies `AGENT.md` (the reconciled version), `state.json`, and `tools/*.py` into a sibling staging dir (`<agentID>-edit-staging`); the coder runs there with `prompts.BuildEditImplementationPrompt()` (tells it to `Read` the existing files first, then edit). Content is read back from staging into `PendingAgentMD`/`PendingTools`, and the staging dir is `RemoveAll`'d — on both success and failure — before the response ever reaches the user.

**On approval**, `finalizeAgent` branches: create calls `saveAndFinish()` (`AgentDesigner.SaveAgent` → `db.CreateAgent`, INSERT); edit calls `updateAndFinish()` (`AgentDesigner.UpdateAgent` → `db.UpdateAgentDescription`, UPDATE — calling `CreateAgent` again would violate the PK/`UNIQUE(user_id,name)` constraint). Schedule changes on edit go through `reconcileScheduleOnSave()`, which **always reuses the existing schedule row's ID** on upsert (there's no unique constraint on `agent_id`, so a fresh `uuid.New()` would insert a duplicate row and fire the agent twice per tick) and deletes the row outright if the line resolves to "none"/invalid where one existed.

`AgentDesigner.SaveAgent` and `UpdateAgent` both delegate to a shared `writeAgentContent()`: it wipes and fully rewrites `tools/` from the incoming map (so an edit that removes a script actually deletes the file, not just stops referencing it), writes `state.json` only if missing (never clobbers a live agent's persisted state), and preserves the manifest's original `CreatedAt` across edits by loading the existing `agent.json` first.

Unit-tested in `internal/agentdesigner/edit_test.go` against a real migrated SQLite DB: schedule-drift reconciliation, the duplicate-schedule-row/double-fire guard, schedule deletion on "none", `state.json`/`CreatedAt` preservation, stale-tool wipe, and — the core safety property — `copyAgentWorkspace` proven to leave the live agent dir byte-for-byte untouched. The coder subprocess round-trip itself (real edit → `[TEST_OUTPUT]` → approve/reject) is exercised manually, not by an automated test.

### Agent output protocol (AGENT.md)

The coder reads AGENT.md and uses these markers in its output:

- **`[CHAT]`** — sends a message to the user. Continuation lines immediately after (no blank line between them) are joined into the same message:
  ```
  [CHAT] BTC-USDT Price Update
  Current price: $66,527.99
  ```
  A blank line or next protocol marker ends the block.

- **`[STATE]...[/STATE]`** — JSON merged into `state.json` (null value = delete key). Supports both multi-line and inline forms:
  ```
  [STATE]
  {"last_price": 66527.99}
  [/STATE]
  ```
  or inline: `[STATE]{"last_price": 66527.99}[/STATE]`

- **`[CALL: <agent-name>]`** — invoke another agent synchronously (max depth 3, cycle detection)

`parseCoderOutput()` in `runner.go` handles all three forms. Multi-line `[CHAT]` and inline `[STATE]` were added after the original single-line-only implementation proved insufficient.

### Secret injection

Secrets are stored encrypted in the `secrets` table. At runtime `runCoderAgent()` needs `RunInput.MasterPw` to decrypt them:
1. Gets the user's `SecretsSalt` from DB
2. Creates a `secrets.Service` with the master password
3. Calls `svc.GetAll(ctx)` to decrypt all secrets → `map[string]string`
4. Uses `r.coderSvc.WithExtraEnv(allSecrets)` to inject them as environment variables

The agent accesses secrets via `os.environ['SECRET_NAME']` in Python. Values are **never** written to disk, logs, or LLM prompts.

`secrets.GetAll()` is the only bulk-decrypt method. `secrets.Proxy()` is still used for `${NAME}` placeholder substitution in legacy contexts.

**Two sources of `MasterPw`, both required for secret injection to actually happen:**
- **Scheduled (cron) runs** — `scheduler.go` decrypts the user's stored `EncryptedMasterPassword` (encrypted at rest with the server's `systemKey`) and passes it through. Always available once the user has completed `/setup`.
- **Manual runs ("Run Now" in the web UI)** — `handleRunAgent()` decrypts the same stored `EncryptedMasterPassword` the same way. There is **no password-entry field on the run form** — agent execution doesn't require live re-entry the way viewing a secret's plaintext value does. (This was previously broken: the handler read a non-existent `master_password` form value, so manual runs always got `MasterPw=""` and silently skipped secret injection. Fixed by mirroring the scheduler's decrypt-from-stored-password approach.)

### Coder tool isolation

`internal/coder/coder.go` provides these modifiers (all return a shallow copy — original unchanged):

- **`WithNoTools()`** — adds `--allowedTools ""`, disabling all tools. Used for design conversation turns so claude outputs plain text.
- **`WithAllowedTools(tools string)`** — adds `--allowedTools <tools>`, pre-approving specific tools. **Required** whenever `--setting-sources ""` is active (which suppresses all settings including tool allowlists) — without this, the subprocess blocks forever on interactive permission prompts in non-interactive mode. Generation uses `"Bash,Write,Edit,Read"`; agent runs use `"Bash,WebFetch,Read,Write,Edit"`.
- **`WithDir(dir string)`** — overrides `cmd.Dir` for the subprocess CWD without changing `HOME`/`CLAUDE_CONFIG_DIR`. Used both by generation (`agentDir` during creation) **and by `runCoderAgent()` on every run** — without it, `cmd.Dir` defaults to the *shared* per-user home (`claude-homes/<userID>/`), so the agent sees and can write to other agents' files instead of its own `tools/`/`state.json`. (This was missing from the run path until it caused exactly that cross-contamination — one agent's self-corrected script got written into the shared home instead of back into its own directory, and a different agent then read the wrong `tools/*.py`.)
- **`WithExtraEnv(env map[string]string)`** — merges additional env vars (e.g. decrypted secrets). System overrides (`HOME`, `CLAUDE_CONFIG_DIR`) always take precedence.
- **`WithBackendType(t string)`** — forces a specific backend (`"claude"`, `"generic"`, or `""` for auto-detect by binary name). Used by `coderForUser()` to honour the admin-configured `BackendType` per coder profile.
- **`Name() string`** — returns `filepath.Base(bin)` (e.g. `"claude"`). Used for user-facing messages so they never hardcode a specific coder's name (the system supports multiple coder profiles with different binaries).

**`CoderBackend` interface** (`internal/coder/backend.go`): abstracts CLI-specific behaviour. Two implementations:
- `claudeBackend` — Claude CLI: `--output-format json`, `--setting-sources ""`, `--allowedTools`, copies `.credentials.json`, sets `CLAUDE_CONFIG_DIR`.
- `genericCLIBackend` — any other CLI: passes prompt as last argument, reads plain-text stdout, injects known auth env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.).

Auto-detection: binary name containing `"claude"` → `claudeBackend`; otherwise → `genericCLIBackend`. Explicit `WithBackendType` overrides detection.

**Critical:** `--setting-sources ""` + no `--allowedTools` = subprocess hangs indefinitely. Always pair them.

### Usage-limit detection

`coder.ErrUsageLimit` is a sentinel returned by `Generate()` when the underlying CLI account/session hits its usage limit. Detected via `looksLikeUsageLimit()`: empirically, the claude CLI's signature for this is a **non-zero exit with completely empty stdout and stderr** (no error text at all) — that combination is treated as a limit hit. Explicit text matches (`"usage limit"`, `"rate limit"`, `"quota exceeded"`, `"limit reached"`) are also checked as a fallback in case the CLI does emit a message.

`agentrunner.friendlyRunError(err, coderName)` converts this into a user-facing message — `"⚠️ This agent run was skipped — <coderName> hit its usage limit. It will retry automatically on the next scheduled run."` — instead of a raw `exit status 1`. `runCoderAgent()` sends this via `input.SendOutput` on **every** run failure (not just limit hits), which fixed a real gap: cron-triggered failures previously only went to `slog` — the user had no way to find out an agent failed at all unless they checked server logs.

`ErrUsageLimit` is also handled during **agent generation** in `runGeneration` and during **design conversation turns** in `callCoder` — both return soft user-facing strings (not Go errors) so the web UI and Telegram router show a helpful message rather than HTTP 500 or a raw error prefix.

### Guardrails

`internal/agentdesigner/guardrails.go`:
- `CheckEthics(code, "")` — blocklist check (rm -rf, drop table, bitcoin wallet, etc.). Used on AGENT.md.
- `RunFullGuardrails(code, "")` — ethics + AST check. Used on `tools/*.py` scripts. **Does NOT check template markers** (the old `# ======= USER LOGIC =======` format is gone).
- AST check blocks: `eval`, `exec`, `compile`, `__import__`, `os.system`, `subprocess.*`, `socket.socket`.

### Per-user coder isolation

Every coder subprocess runs in a fully isolated environment.

**How it works (`internal/coder/coder.go`):**
- `CLAUDE_CONFIG_DIR` → `<data_dir>/claude-homes/<userID>/.claude/`
- `HOME` → `<data_dir>/claude-homes/<userID>/`
- `--setting-sources ""` — suppresses `settings.json` and all `CLAUDE.md` traversal. `HOME` override alone is **not** sufficient.
- `.credentials.json` copied from operator's `~/.claude/` on every invocation (fresh OAuth tokens).

**`ANTHROPIC_CONFIG_DIR` does NOT work** — only `CLAUDE_CONFIG_DIR` redirects config. Verified empirically.

### Database

SQLite via `modernc.org/sqlite` (CGo-free, bundles SQLite 3.49). WAL mode and foreign keys set on open in `db.Open()`. Migrations applied in alphabetical order from `migrations/`.

**Migrations:**
| File | Change |
|------|--------|
| `001_initial_schema.up.sql` | All core tables (13 total) |
| `002_chat_messages.up.sql` | `chat_messages` table |
| `002_coders.up.sql` | `coders` table, `users.coder_id` FK |
| `003_agent_type_skills.up.sql` | `skills`, `agent_skills` tables; added `agents.type` column |
| `004_drop_agent_type.up.sql` | Drops `agents.type` (no agent types in the unified model) |
| `005_coder_backend_type.up.sql` | Adds `backend_type TEXT NOT NULL DEFAULT ''` to `coders` |

`db.HasPlatformIdentity(userID)` was added to the repositories layer — returns true when a user has at least one linked platform. Used by the scheduler and reminder service to skip runs/reminders for users with no way to receive output, preventing wasted API quota.

Schema tables: `users`, `platform_connections`, `platform_identities`, `agents`, `agent_schedules`,
`agent_runs`, `secrets`, `chat_sessions`, `reminders`, `user_permissions`, `mcp_servers`,
`settings`, `audit_logs`, `schema_migrations`, `chat_messages`, `coders`, `skills`, `agent_skills`.

### Web UI routes

```
/login, /logout
/setup                              # forced first-login wizard (5 steps: password → master_password → profile → connector → done)
/change-password                    # forced if must_change_password=1
/dashboard                          # user home
/dashboard/agents                   # list agents
/dashboard/agents/new               # conversational agent creation (chat UI)
/dashboard/agents/design            # POST JSON API: drives design FSM turn-by-turn
/dashboard/agents/design/cancel     # POST: cancel active design session
/dashboard/agents/design/progress   # GET SSE: streams generation milestone events to the browser
/dashboard/agents/:id               # detail: AGENT.md editor, state, logs, schedule, skills
/dashboard/agents/:id/edit          # conversational agent editing (chat UI)
/dashboard/agents/:id/edit/start    # POST JSON API: starts an edit design session
/dashboard/agents/:id/delete
/dashboard/agents/:id/run
/dashboard/agents/:id/schedule
/dashboard/agents/:id/schedule/delete
/dashboard/agents/:id/agent-md      # POST: save AGENT.md (ethics check applied)
/dashboard/agents/:id/skills        # POST: update agent skill assignments
/dashboard/skills                   # list skills
/dashboard/skills/:id               # view/edit SKILL.md
/dashboard/secrets                  # list, create, delete (master-password gated)
/dashboard/connectors               # connect/disconnect Telegram bot token
/dashboard/sessions                 # chat sessions
/dashboard/reminders                # natural language reminder creation
/dashboard/memory                   # view/add entries
/dashboard/settings                 # full user profile (name, email, location, timezone, tone, language, notes) + change master password
/admin                              # admin dashboard
/admin/users, /admin/users/:id      # create user, grant permissions, reset password
/admin/users/:id/coder[/unassign]   # assign coder profile to user
/admin/coders, /admin/coders/:id    # list + create/edit/delete coder profiles
/admin/settings                     # system settings (claude_bin, firejail_bin, timeouts)
/admin/audit                        # audit log viewer
```

### Admin vs. user separation

- **Admin** (`role="admin"`): only `/admin/*` — manages users, coder profiles, settings, audit. No agents, secrets, reminders, or memory.
- **Regular users**: only `/dashboard/*` — agents, secrets, connectors, sessions, reminders, memory.
- `requireUserOnly` middleware on `/dashboard/*` redirects admins to `/admin`.
- `requireAdmin` middleware on `/admin/*` rejects non-admins with 403.

### Multi-coder system

Admin creates named **Coder Profiles** (`coders` table) each with a `claude_bin` path, `timeout_s`, and `backend_type` (`""` = auto-detect, `"claude"`, or `"generic"`). Users are assigned a coder via `users.coder_id` FK. `coderForUser(userID)` on the Server builds the right `*coder.Coder` per user (via `.WithBackendType(profile.BackendType)`), falling back to system defaults when unassigned.

The `designFlow` is constructed with a resolver `func(userID string) *coder.Coder` so it picks up per-user profiles during agent design.

### Natural language reminders

`internal/reminder/timeparser.go` — `ParseNaturalTime(text, now, loc)` parses expressions like `"in 10 minutes"`, `"tomorrow at 3pm"`, `"next Tuesday at noon"` using regex only. Used by both the web UI and Telegram router. Telegram syntax: `/remind in 10 minutes to check oven`.

Both callers pass `profile.LoadLocation(db, userID)` as `loc` so reminders fire relative to the user's saved timezone, not UTC. Falls back to `time.UTC` when no timezone is set.

---

## Build Status

Build: **PASS**. `go vet`: **PASS**. Tests: **PASS** (`internal/agentdesigner`, `internal/secrets`, `internal/profile`, `web/`).

Manual web round-trip and `/agent edit` on Telegram for the edit flow have not been exercised end-to-end with a real coder subprocess — the schedule/state/staging-isolation logic is unit-tested, the coder interaction itself is not.

### Commit history

| Commit | Phase |
|--------|-------|
| `46946e4` | Phase 1 — scaffold, DB, auth, web login/setup |
| `ead1ea5` | Phase 2 — secrets: AES-256-GCM, Argon2id, master password, Proxy() |
| `d03e24a` | Phase 3 — gateway: Telegram adapter, GatewayManager, Router, identity resolver |
| `9a49076` | Phase 4 — coder: claude CLI subprocess, per-user HOME isolation |
| `bac6a94` | Phase 5 — agentdesigner: FSM wizard + guardrails |
| `504ad44` | Phase 6 — agentrunner + Firejail sandbox + secrets proxy injection |
| `0894420` | Phase 7 — scheduler (cron), reminders, sessions, memory |
| `b572ff5` | Phase 8 — web UI: full user dashboard (DaisyUI/Tailwind) |
| `50936af` | Polish — /remind, /session commands; web code editor; settings persistence; RBAC |
| `45f8cdc` | Docs — initial CLAUDE.md |
| `f89cb2e` | UI overhaul, admin/user separation, coder profiles, SSE agent creation, NL reminders |
| `b51e929` | Per-user coder isolation (CLAUDE_CONFIG_DIR, --setting-sources, credential copy) |
| `da6eb6a` | Unified conversational agent creation; no agent types; AGENT.md+tools/ layout; secret env injection; WithNoTools/WithExtraEnv; auto-schedule from conversation; platform-aware designer; skills UI |
| `12f0461` | StateVerifying + full-tools generation (write/run/fix before showing user); WithDir/WithAllowedTools on Coder; runner tool permissions fix; multi-line [CHAT] + inline [STATE] parser |
| `92d37be` | runCoderAgent runs inside agentDir (was the shared per-user home — caused cross-agent file contamination); manual "Run Now" decrypts stored master password instead of a non-existent form field; coder.ErrUsageLimit detection + coder-agnostic friendly error messages sent to the user on every run failure |
| `81c6baf` | Conversational agent editing (Telegram `/agent edit` + web Edit button), reusing the create FSM via `DesignSession.IsEdit`; schedule-line reconciliation against the real `agent_schedules` row; edit generation runs in a staging dir copy so the live agent is never touched before approval; `AgentDesigner.UpdateAgent`/`db.UpdateAgentDescription` for the UPDATE-not-INSERT save path; `reconcileScheduleOnSave` reuses the existing schedule row's ID to avoid duplicate/double-firing schedules |
| `aa269a7` | User profile system (`internal/profile`): name, email, location, timezone, tone, language, notes stored in `settings` table; injected as `[User profile]` block into agent designer system prompt; setup wizard gains profile step (step 3 of 5); Settings page expanded to full profile editor; reminder timezone now uses per-user saved timezone via `profile.LoadLocation()` (both web + Telegram) |
| `d29c5bd` | User memory injection: `memory.ContextString()` now injected as `[User memory]` block into agent design sessions and agent run prompts; `Flow.WithMemory()` and `Runner.WithMemory()` wire the store via a local `memoryStore` interface in each package |
| `cb0273d` | Prompt centralization: all LLM prompt builders moved to `internal/prompts`; no inline prompt text remains in `agentdesigner`, `agentrunner`, or `web` packages |
| (current) | Multi-backend coder: `CoderBackend` interface + `claudeBackend`/`genericCLIBackend` in `backend.go`; `WithBackendType()` on Coder; `backend_type` field on coder profiles (migration 005); designer flow failure handling: `[BLOCKED]` protocol for impossible tasks (3-attempt limit, `parseBlockedOutput`), `ErrUsageLimit`/timeout soft responses in `runGeneration` and `callCoder`, FSM state ordering fix (StateVerifying only entered when `[TEST_OUTPUT]` is present); SSE progress channel + `cancelGenerate` in DesignSession; `sendProgress` callback threaded through GatewayManager → Router → handleText; scheduler/reminder skip runs for users with no platform connected (`HasPlatformIdentity`) |

### Known gaps

- **Tests** — `internal/agentdesigner`, `internal/secrets`, `internal/profile`, and `web/` (template smoke tests) have test files; no integration or e2e coverage. The agent-editing logic is unit-tested around the coder boundary (schedule reconciliation, staging-dir isolation, file invariants) but the coder subprocess round-trip itself (real edit → test → approve/reject) has no automated test
- **Discord adapter** — in the original plan; not implemented
- **`/remind` list/delete via Telegram** — only create is wired; no list or cancel command
- **`/memory` Telegram command** — memory store exists but no `/memory` chat command in Router
- **MCP servers** — `mcp_servers` table exists but MCP tool execution is not implemented
