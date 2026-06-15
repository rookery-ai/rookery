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
| `internal/coder` | `Coder`: runs coder CLI subprocess with full per-user isolation; `WithNoTools()` for text-only calls; `WithExtraEnv()` for secret injection |
| `internal/agentdesigner` | `Flow` FSM (Describing→Designing→Done); conversational design shared between web and Telegram; auto-schedule; `RunFullGuardrails` (ethics + AST only) |
| `internal/agentrunner` | Load agent → decrypt secrets into env via `WithExtraEnv` → coder subprocess → capture `[CHAT]` lines → send via GatewayManager; timestamped run logs |
| `internal/sandbox` | Firejail wrapper (available for future use; not used by coder agents) |
| `internal/scheduler` | Cron scheduler: polls `agent_schedules`, fires runner, decrypts stored master password for secret injection; `WithSender()` delivers output to users |
| `internal/reminder` | Creates/lists/fires reminders; background polling goroutine |
| `internal/session` | `ChatSession` create/list/stop; 30-min idle auto-stop |
| `internal/memory` | Per-user append-only JSONL store |
| `internal/audit` | Structured audit event writer → `audit_logs` table |
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
- `StateDone`

**Web path:** `StartDesign(ctx, userID, agentName, firstMessage)` — creates session in `StateDesigning` directly, returns first coder response.

**Telegram path:** `Start(userID, agentName)` → `StateDescribing` → `Step()` transitions to `StateDesigning`.

**Step returns:** `(response string, isDone bool, agentID string, err error)`

**Approval triggers** (exact match only — "yes"/"ok" do NOT trigger):
`"approve"`, `"go ahead"`, `"build it"`, `"create it"`, `"/approve"`

**On approval:** `runGeneration()` calls `coder.WithNoTools().Generate()` with full conversation history embedded as context. The coder outputs AGENT.md and optional `[TOOL: filename.py]...[/TOOL]` blocks as plain text. The flow parses the output, runs guardrails, saves via `AgentDesigner.SaveAgent()`, and auto-creates the schedule from `# Suggested schedule: <cron>` on the first line of AGENT.md.

**`WithNoTools()`** is used for ALL design and generation calls so the coder outputs plain text and never attempts file writes or permission prompts.

**Auto-schedule:** If AGENT.md starts with `# Suggested schedule: */10 * * * *`, `parseSuggestedSchedule()` validates the cron expression and calls `db.UpsertAgentSchedule()` — the schedule is set immediately on agent creation.

**`dbDesignStore` interface** (implemented by `*db.DB`):
```go
type dbDesignStore interface {
    ListSkills(userID string) ([]*db.Skill, error)
    ListUserPlatformConnections(userID string) ([]*db.PlatformConnection, error)
    UpsertAgentSchedule(s *db.AgentSchedule) error
}
```

**System prompt context injected into every design turn:**
- Connected platforms (e.g. Telegram) — coder knows to use `[CHAT]` not raw Telegram API
- Secrets guidance — tell user to add to Secrets store; never paste in chat; reference as `os.environ['NAME']`
- Scheduling guidance — detect frequency from conversation; propose cron; auto-set on creation
- Non-technical user style — explain API keys and cron in plain language; one/two questions per turn

### Agent output protocol (AGENT.md)

The coder reads AGENT.md and uses these markers in its output:
- `[CHAT] <text>` — sends a message to the user via the connected platform
- `[STATE]...[/STATE]` — JSON block merged into `state.json` (null value = delete key)
- `[CALL: <agent-name>]` — invoke another agent synchronously (max depth 3, cycle detection)

### Secret injection

Secrets are stored encrypted in the `secrets` table. At runtime the scheduler decrypts them using the user's master password and passes `MasterPw` in `RunInput`. `runCoderAgent()` then:
1. Gets the user's `SecretsSalt` from DB
2. Creates a `secrets.Service` with the master password
3. Calls `svc.GetAll(ctx)` to decrypt all secrets → `map[string]string`
4. Uses `r.coderSvc.WithExtraEnv(allSecrets)` to inject them as environment variables

The agent accesses secrets via `os.environ['SECRET_NAME']` in Python. Values are **never** written to disk, logs, or LLM prompts.

`secrets.GetAll()` is the only bulk-decrypt method. `secrets.Proxy()` is still used for `${NAME}` placeholder substitution in legacy contexts.

### Coder tool isolation

`internal/coder/coder.go` provides two modifiers:

- **`WithNoTools()`** — adds `--allowedTools ""` to the claude CLI args, disabling all file-write, bash, and edit tools. Used for design conversations and generation so claude outputs pure text without attempting file operations.
- **`WithExtraEnv(env map[string]string)`** — merges additional env vars (e.g. decrypted secrets) into the subprocess environment. System overrides (`HOME`, `CLAUDE_CONFIG_DIR`) always take precedence.

Both return a shallow copy of `*Coder` — the original is unchanged.

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

Schema tables: `users`, `platform_connections`, `platform_identities`, `agents`, `agent_schedules`,
`agent_runs`, `secrets`, `chat_sessions`, `reminders`, `user_permissions`, `mcp_servers`,
`settings`, `audit_logs`, `schema_migrations`, `chat_messages`, `coders`, `skills`, `agent_skills`.

### Web UI routes

```
/login, /logout
/setup                              # forced first-login wizard
/change-password                    # forced if must_change_password=1
/dashboard                          # user home
/dashboard/agents                   # list agents
/dashboard/agents/new               # conversational agent creation (chat UI)
/dashboard/agents/design            # POST JSON API: drives design FSM turn-by-turn
/dashboard/agents/design/cancel     # POST: cancel active design session
/dashboard/agents/:id               # detail: AGENT.md editor, state, logs, schedule, skills
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
/dashboard/settings                 # display name, change master password
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

Admin creates named **Coder Profiles** (`coders` table) each with a `claude_bin` path and `timeout_s`. Users are assigned a coder via `users.coder_id` FK. `coderForUser(userID)` on the Server builds the right `*coder.Coder` per user, falling back to system defaults when unassigned.

The `designFlow` is constructed with a resolver `func(userID string) *coder.Coder` so it picks up per-user profiles during agent design.

### Natural language reminders

`internal/reminder/timeparser.go` — `ParseNaturalTime(text, now, loc)` parses expressions like `"in 10 minutes"`, `"tomorrow at 3pm"`, `"next Tuesday at noon"` using regex only. Used by both the web UI and Telegram router. Telegram syntax: `/remind in 10 minutes to check oven`.

---

## Build Status

Build: **PASS**. `go vet`: **PASS**. Tests: **PASS** (`internal/agentdesigner`, `internal/secrets`).

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
| (current) | Unified conversational agent creation; no agent types; AGENT.md+tools/ layout; secret env injection; WithNoTools/WithExtraEnv; auto-schedule from conversation; platform-aware designer; skills UI |

### Known gaps

- **Tests** — only `internal/agentdesigner` and `internal/secrets` have test files; no integration or e2e coverage
- **Discord adapter** — in the original plan; not implemented
- **`/remind` list/delete via Telegram** — only create is wired; no list or cancel command
- **`/memory` Telegram command** — memory store exists but no `/memory` chat command in Router
- **MCP servers** — `mcp_servers` table exists but MCP tool execution is not implemented
