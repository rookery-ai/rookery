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
      → /agent  → agentdesigner.Flow (multi-turn FSM wizard)
      → /run    → agentrunner.Runner
      → /secret → SecretStore
      → /remind → reminder.Service (duration parser: 30m/1h/2h30m/1d)
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
| `internal/secrets` | AES-256-GCM store; Argon2id key derivation; `Proxy()` resolves `${NAME}` in-memory only |
| `internal/gateway` | `Gateway` interface, `GatewayManager`, `Router`, `IdentityResolver`, `TelegramGateway` |
| `internal/coder` | `ClaudeCoder`: runs `claude -p` subprocess with per-user HOME isolation |
| `internal/agentdesigner` | `Flow` FSM (Describing→Clarifying→Generating→Reviewing→Saving→Done); `RunFullGuardrails`; `GenerateAndSave` for web UI |
| `internal/agentrunner` | Load agent → `secrets.Proxy()` → Firejail → capture `[CHAT]` lines → send via GatewayManager |
| `internal/sandbox` | Firejail wrapper; `NetworkPolicy` enum (None/Restricted/Full) |
| `internal/scheduler` | Cron scheduler: polls `agent_schedules`, fires runner, decrypts stored master password for secret injection; `WithSender()` delivers output to users |
| `internal/reminder` | Creates/lists/fires reminders; background polling goroutine |
| `internal/session` | `ChatSession` create/list/stop; 30-min idle auto-stop |
| `internal/memory` | Per-user append-only JSONL store |
| `internal/audit` | Structured audit event writer → `audit_logs` table |
| `web/` | Echo v4 web server; full user dashboard + admin UI |

### Secrets proxy invariant

`secrets.Proxy()` is called **only** inside `agentrunner.Runner.Run()` — never in the coder or
designer. The resolved script is passed directly to the Firejail subprocess and never written to
disk or logs. Do not break this invariant when touching runner or secrets code.

### Agent file layout on disk

```
<data_dir>/agents/<userID>/<agentID>/
├── main.py       # generated Python; USER LOGIC zone + SYSTEM INJECTED zone
└── config.json   # name, description, sandbox overrides
```

`agentdesigner.AgentCodePath(dir, userID, agentID)` returns the canonical path.

### Database

SQLite via `modernc.org/sqlite` (CGo-free). Single migration file: `migrations/001_initial_schema.up.sql`.
All 13 tables created there. WAL mode and foreign keys are set on open in `db.Open()`.

Schema tables: `users`, `platform_connections`, `platform_identities`, `agents`, `agent_schedules`,
`agent_runs`, `secrets`, `chat_sessions`, `reminders`, `user_permissions`, `mcp_servers`,
`settings`, `audit_logs`.

### Web UI routes

```
/login, /logout
/setup                         # forced first-login wizard (change pw → master pw → connect platform)
/change-password               # forced if must_change_password=1
/dashboard                     # user home
/dashboard/agents              # list agents
/dashboard/agents/new          # create (calls GenerateAndSave via claude)
/dashboard/agents/:id          # detail, run, edit code, save/delete schedule
/dashboard/secrets             # list, create, delete (master-password gated)
/dashboard/connectors          # connect/disconnect Telegram bot token
/dashboard/memory              # view/add entries
/dashboard/settings            # display name, change master password
/admin/users                   # create user, grant permissions, reset password
/admin/users/:id               # user detail
/admin/settings                # system settings (claude_bin, firejail_bin, timeouts)
/admin/audit                   # audit log viewer
```

---

## Build Status

All 8 implementation phases complete and committed. Build: **PASS**. `go vet`: **PASS**.

### Committed phases

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
| `50936af` | Polish — /remind, /session commands; web code editor; real settings persistence; RBAC |

### Known gaps (not yet implemented)

- **Tests** — only `internal/agentdesigner` and `internal/secrets` have test files; no integration or e2e coverage
- **Discord adapter** — in the original plan; not implemented
- **`/remind` list/delete commands** — only `/remind <dur> <msg>` exists; no list or cancel via Telegram
- **`/memory` Telegram command** — memory store exists but no `/memory` chat command wired in Router
- **Admin audit log template** — route exists; template may be stub
- **Web agent creation UX** — `GenerateAndSave()` blocks the HTTP request while claude runs (no streaming/async progress)
