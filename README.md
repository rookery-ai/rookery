# Rookery

**Self-hosted AI agents that live on your knowledge base and act through your connected services.**

Rookery is a single-binary control plane for AI agents you own. Agents read and
write an Obsidian-style markdown vault, reach 91 external services through a
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
- **Connectors** — 91 providers, ~471 curated actions, self-managed OAuth. No
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
a real hostname — `rookery.sh` is the documented example — or `http://localhost`.

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
