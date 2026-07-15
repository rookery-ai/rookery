# Multi-coder CLI backends — design

**Date:** 2026-07-15
**Status:** Approved (design); ready for implementation plan
**Area:** `internal/coder`

## Problem

The platform supports per-workspace *local CLI coders*, but only **Claude Code** actually
works. **OpenCode** is listed but broken, and the intent is to add **Cursor**, **Codex**, and
**Gemini CLI** as autodetected, pre-configured local coders across **Linux, macOS, and Windows**.

Root cause of "OpenCode doesn't work" (verified empirically on this host):
`internal/coder/backend.go`'s `genericCLIBackend` invokes every non-Claude coder as
`<bin> -p <prompt>`. But OpenCode's prompt is a **positional arg after the `run` subcommand**, and
its `-p` flag means **basic-auth password**. The prompt was silently swallowed as a password and
OpenCode ran with an empty message. This is the textbook failure of guessing a CLI convention and
being wrong *silently*.

`-p` is not portable at all across these tools:

| Tool | Meaning of `-p` |
|---|---|
| Claude | prompt |
| Gemini | prompt |
| OpenCode | basic-auth **password** (prompt is positional after `run`) |
| Cursor | **print mode** (prompt is positional) |

A single "generic" backend therefore cannot work. Each coder differs on prompt-passing,
output format, per-workspace config/auth isolation, and the flag that prevents it hanging on an
interactive approval prompt.

## Tenancy / isolation intent

"The coders are **pre-configured by the user on the host** before we use them. We only do the
config separation to be separate for each workspace like we do now." This is precisely the existing
**Claude model**: isolate the coder's mutable *state* directory per workspace, **seed only the
operator's credential** into it, and inherit the operator's host setup. Sessions/history/DBs are
**not** seeded → each workspace starts fresh; only auth is shared from the operator's login.

## Scope decisions (confirmed with the owner)

- **OS targets:** Linux, macOS, **and Windows** are all in v1.
- **Unverified coders:** Author all five. OpenCode is fixed and verified end-to-end on this host;
  Cursor/Codex/Gemini adapters are authored from **current official docs** and land
  **clearly marked "authored, unverified"** with a fail-loud smoke test, because they cannot be
  executed on this host (their binaries are not installed).

## Non-goals

- No YAML/DSL coder manifests (rejected — see Alternatives). Coder invocation is genuinely
  non-uniform; a declarative layer would be thin and leaky.
- No change to the **API coder** engine (`coder_kind == "api"`). This work is entirely the
  `coder_kind == "local"` CLI path.
- No new DB migration — `workspaces.coder_backend_type` already exists and simply carries new values.
- Not attempting to unify each coder's own internal sandbox with our Landlock layer.

## Approach: per-coder Go backend structs (extend the existing `CoderBackend` seam)

The `CoderBackend` interface in `internal/coder/backend.go` already cleanly holds Claude. We add one
small struct per coder and **generalize the one Claude-hardcoded concern: config isolation +
credential seeding**.

### Interface changes

Today `setupHome(homeDir, sysConfigDir)` and `extraEnvForUser(homeDir)` bake in Claude's
`CLAUDE_CONFIG_DIR` + `.credentials.json` copy. Replace those two Claude-specific hooks with a
declarative, per-coder **isolation contract** while keeping the genuinely per-coder behavior as
methods:

```go
type CoderBackend interface {
    // Invocation (per-coder; -p is NOT portable).
    buildArgs(prompt string, noTools bool, allowedTools string) []string

    // Output parsing (per-coder; single-JSON vs NDJSON-event streams differ).
    parseOutput(stdout []byte) (text string, isError bool, err error)

    // Usage/rate-limit detection.
    looksLikeLimit(stdout, stderr string) bool

    // NEW — declarative isolation contract (generalizes Claude's hardcoded setup):
    // configEnv returns env vars that redirect this coder's config/state dir into the
    // per-workspace home (cross-platform: prefers explicit dir env vars over HOME).
    configEnv(workspaceHome string) map[string]string
    // seedFiles returns the operator credential/config file(s) to copy from the
    // operator's real config into the isolated dir on each invocation (auth only —
    // never session DBs/history).
    seedFiles(operatorConfigRoot, workspaceHome string) []seedSpec
}

type seedSpec struct {
    From string // absolute path in the operator's real config
    To   string // absolute path inside the per-workspace isolated home
    Mode os.FileMode
}
```

`ensureUserHome` / `buildEnv` become coder-agnostic: they call `configEnv` for env overrides and
`seedFiles` for credential seeding, replacing the current Claude-only `setupHome`/`extraEnvForUser`.
Backends remain **stateless** (constructed per `Generate` call, as today).

### Per-coder adapter table

Invocation and flags below are from current official docs (see Sources). JSON shapes drive the
parser choice: **single-object** parsers extract one field; **NDJSON-event** parsers scan
newline-delimited events and keep the last assistant/text event (and surface `type:"error"` events
with their status code for limit/auth detection).

| Coder | `backend_type` | Bins | Invocation | Output | Isolation env → seed file | Hang-guard | Status |
|---|---|---|---|---|---|---|---|
| Claude | `claude` | `claude`, `claude-code` | `-p <prompt> --output-format json --setting-sources ""` | single JSON, `.result` | `CLAUDE_CONFIG_DIR` → `.claude/.credentials.json` | `--allowedTools` | ✅ verified (reference) |
| OpenCode | `opencode` | `opencode` | `run <prompt> --format json [-m <prov/model>]` | **NDJSON** events (`type` discriminator) | `XDG_DATA_HOME`+`XDG_CONFIG_HOME` → `opencode/auth.json` | `run` is non-interactive by default | 🟡 fixed + verified on this host |
| Codex | `codex` | `codex` | `exec <prompt> --json` | NDJSON events (one per state change) | `CODEX_HOME` → `auth.json` | exec auto-downgrades approval → `never` (no TTY) | ⚠️ authored, unverified |
| Gemini | `gemini` | `gemini` | `-p <prompt> --output-format json --yolo` | single JSON, `.response` | `HOME`+`USERPROFILE` → `.gemini/` (no dedicated dir env) | `--yolo` / `--approval-mode` | ⚠️ authored, unverified |
| Cursor | `cursor` | `cursor-agent`, `cursor` | `-p <prompt> --output-format json --trust [--model <m>]` | single JSON, aggregated final result | own login store → seed TBD on a real host | `--trust` + print mode | ⚠️ authored, unverified; **no official Windows build (community patch only)** |

Notes:
- **Model selection.** OpenCode boots with no model unless one is configured. Drive it via the
  existing per-workspace `coder_model` field → `-m <provider/model>` when set (cleaner than seeding
  `opencode.json`); if unset, rely on the operator's seeded/default config. Cursor `--model` likewise
  fed from `coder_model` when present.
- **`genericCLIBackend`** is retained ONLY as a last-resort fallback for an unrecognized binary; it is
  no longer the path for any known coder.

### Fail-loud detection & smoke test

- **`DetectInstalled`** (`internal/coder/detect.go`): extend `knownCoders` so each entry carries its
  real `backend_type` (not blanket `"generic"`). Probe order unchanged (PATH then `~/.local/bin`; on
  Windows also `%LOCALAPPDATA%`/`%APPDATA%` install locations and `.exe`/`.cmd` suffixes via
  `exec.LookPath`).
- **`Coder.Smoke(ctx, workspaceID) (string, error)`** — runs a trivial prompt (`"Reply with exactly
  PONG"`) through the full isolated pipeline (seed → env → invoke → parse) and validates a sane
  structured reply. Surfaced as a **"Test coder"** action on `/dashboard/settings/coder`.
  This is the safety net that makes an unverified adapter *announce* failure instead of silently
  feeding garbage into a run. It catches **both**:
  - a wrong CLI convention (the original silent-swallow bug), and
  - bad/expired operator auth — observed live: OpenCode's stored login currently returns
    `401 User not found`, which the NDJSON error-event parser reports cleanly.

  `Ping` (version probe) stays for cheap reachability; `Smoke` is the real end-to-end check.

### Prompt/backend mapping

`prompts.MapCoderBackend` maps all five CLI backend types to `BackendFullCoder` (each is an
autonomous, file-capable coder), so runtime/design prompts describe direct tool access as they do
for Claude today. `coder_backend_type` on the workspace row carries the new values; no migration.

### Cross-platform handling

- Prefer **explicit config-dir env vars** (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `XDG_*`) — identical
  across OSes — over POSIX `HOME`. Only Gemini keys purely off the home dir, so its `configEnv`
  overrides `HOME` **and** `USERPROFILE`.
- Operator-config discovery via `os.UserConfigDir()` / `os.UserHomeDir()` (cross-platform).
- The Landlock sandbox already auto-skips when unsupported (`sandbox.Supported()` is false on
  non-Linux); no change. On Windows/macOS the coder runs unsandboxed, exactly as the codebase already
  documents for hosts without Landlock — trusted within the workspace's own vault/home.
- **Honest caveat:** `cursor-agent` has no official native Windows binary (community patch only);
  the spec marks Cursor Windows-unverified. Codex/Gemini/OpenCode should work on Windows via their
  config-dir env vars but are untestable from this host.

## Testing

- **Unit (all five):**
  - arg building per coder (asserts the exact flag set — e.g. OpenCode uses `run <prompt>` and never
    bare `-p <prompt>`; Cursor uses `-p` as print-mode with a positional prompt);
  - output parsing over captured fixtures — a single-object fixture and an NDJSON-event fixture
    (including a `type:"error"` `401` event) per relevant coder;
  - limit/auth detection from those fixtures;
  - `configEnv` + `seedFiles` resolution (correct env keys, seed source→dest paths, no session-DB
    seeding), including a Windows path case (`USERPROFILE` set for Gemini).
- **Integration (this host):** OpenCode fixed and driven end-to-end through the new interface, plus
  `Smoke`. **Caveat:** the operator's OpenCode login is currently `401`; `Smoke` reports it
  correctly. A fully-green completion requires re-authing the operator's OpenCode login — a runtime
  config action, out of scope for the code change.
- **Unverified adapters:** Codex/Gemini/Cursor land with unit tests only, each explicitly labeled
  "authored, unverified — must pass `Smoke` on a host with the binary before relied upon."
- Existing coder tests (`forworkspace_test.go`, API-engine tests, etc.) must stay green; the API path
  is untouched.

## Alternatives considered

- **B — YAML coder manifests (connectors-style).** Rejected: connectors' YAML paid off because HTTP
  requests are uniform across 28 providers. Coder invocation is not uniform (subcommand vs flag,
  single-JSON vs NDJSON, tool-specific credential seeding, per-coder hang-guards). Every coder would
  need a Go escape hatch anyway; the declarative layer would be thin and leaky.
- **C — Minimal fix (special-case OpenCode inside `genericCLIBackend`).** Rejected: fixes OpenCode
  but leaves Codex/Gemini/Cursor on the same silently-wrong `-p` path. Does not deliver the task
  (expand + reliably autodetect several coders).

## Risks / open items

- **Unverifiable-here adapters (Codex/Gemini/Cursor):** flags, JSON shapes, config-dir seeding, and
  crucially *hang-on-approval* behavior cannot be confirmed from docs alone. Mitigation: the fail-loud
  `Smoke` gate + explicit "unverified" labeling; must be exercised on a host with each binary.
- **Cursor credential seeding** (`seedFiles`) is TBD — cursor-agent's on-disk login store location
  needs confirmation on a real install; until then Cursor may rely on `CURSOR_API_KEY` env passthrough.
- **Operator OpenCode auth is currently 401** — verification of a *successful* OpenCode completion is
  blocked on a re-auth outside this change; the smoke-test failure path is verified instead.
- **Double sandboxing** (Codex/Cursor have their own sandbox modes under our Landlock) is not
  addressed here; exec/print modes are used as-is.

## Sources

- OpenCode: verified empirically on this host (`opencode run --help`; `--format json` emits NDJSON
  events with a `type` discriminator; observed `type:"error"` `401` event).
- Codex: [Non-interactive mode](https://developers.openai.com/codex/noninteractive),
  [CLI reference](https://developers.openai.com/codex/cli/reference) — `codex exec`, `--json`,
  approval auto-downgraded to `never`, `CODEX_HOME`, `CODEX_API_KEY` (exec only).
- Gemini CLI: [Headless mode](https://google-gemini.github.io/gemini-cli/docs/cli/headless.html),
  [Configuration](https://google-gemini.github.io/gemini-cli/docs/get-started/configuration.html) —
  `-p/--prompt`, `--output-format json` (`.response`), `--yolo`/`--approval-mode`, `~/.gemini` +
  `GEMINI_API_KEY`/`GOOGLE_API_KEY`.
- Cursor CLI: [Output format](https://cursor.com/docs/cli/reference/output-format) — `-p` print mode,
  `--output-format json` (single aggregated object), `--trust`, `--model`, `CURSOR_API_KEY`;
  [Windows community patch](https://github.com/gitcnd/cursor-agent-cli-windows).
