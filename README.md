<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hero-banner-dark.svg">
    <img src="docs/assets/hero-banner.svg" alt="Rookery — self-hosted AI agents that run on your own machine, around the clock" width="100%">
  </picture>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-a94c1c"></a>
  <a href="https://github.com/rookery-ai/rookery/releases"><img alt="Release" src="https://img.shields.io/github/v/release/rookery-ai/rookery?color=a94c1c"></a>
  <a href="https://github.com/rookery-ai/rookery/pkgs/container/rookery"><img alt="Container" src="https://img.shields.io/badge/ghcr.io-rookery-a94c1c"></a>
</p>

Everything you know in one markdown knowledge base — what you write, and what
your connected services bring in. Ask it anything, send an agent out for what
isn't there yet, or hand over the whole job and let it run on a schedule you set.

Full documentation lives at **[rookery.cloud/docs](https://rookery.cloud/docs)**.

## Quickstart

```bash
curl -fsSL https://rookery.cloud/install.sh | sh
rookery onboard
```

Then open `http://localhost:8080`, log in, and create your first workspace.

Later, `rookery upgrade` moves to the latest release (or `--version v0.1.4` to a
named one), and `rookery uninstall` removes the service and binary — keeping your
data unless you pass `--purge`. Both refuse to touch a `.deb`/`.rpm` install and
name your package manager's command instead.

<details>
<summary>Windows</summary>

```powershell
irm https://rookery.cloud/install.ps1 | iex
rookery onboard
```

`iex` cannot pass arguments, so a specific version needs a script block:
`& ([scriptblock]::Create((irm https://rookery.cloud/install.ps1))) -Version v0.2.0`.

Windows has no filesystem confinement and no service registration yet — see
[the Windows guide](https://rookery.cloud/docs/installation/windows).
</details>

<details>
<summary>Container</summary>

```bash
docker run -d --name rookery -p 8080:8080 \
  -v rookery-data:/data ghcr.io/rookery-ai/rookery:latest
```

The image is slim — no CLI coder binary, `ROOKERY_CODER_MODE=slim` — so
workspaces must use the `api` coder kind.
</details>

<details>
<summary>Build from source</summary>

Requires Go 1.26 and Node 24.

```bash
make build
./bin/rookery owner bootstrap -u <username> -p <password>
./bin/rookery serve
```
</details>

## What you get

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/features-dark.svg">
  <img src="docs/assets/features.svg" alt="Workspaces, knowledge base, agents, skills, connections, MCP servers, chat, notifications, models, secrets, scheduling and sandboxing" width="100%">
</picture>

**100+ services** and **900+ curated actions**, over OAuth or an API key you
paste — never a broker. **21 skills** built in, plus any you create the same
conversational way.

Agents and chat can also drive a **real headless browser**, so a page that only
exists after JavaScript runs is readable like any other — and an agent you have
given permission can sign in, fill forms and click through a flow, using
passwords you have stored as secrets and never see in a transcript. Anything
irreversible — paying, ordering, deleting — asks you first.

Read more —
[Workspaces](https://rookery.cloud/docs/concepts/workspaces) ·
[Knowledge base](https://rookery.cloud/docs/concepts/knowledge-base) ·
[Agents](https://rookery.cloud/docs/concepts/agents) ·
[Skills](https://rookery.cloud/docs/concepts/skills) ·
[Connections](https://rookery.cloud/docs/concepts/connections) ·
[MCP servers](https://rookery.cloud/docs/concepts/mcp-servers) ·
[Chat](https://rookery.cloud/docs/concepts/chat) ·
[Notifications](https://rookery.cloud/docs/concepts/notifications) ·
[Models](https://rookery.cloud/docs/concepts/models) ·
[Secrets](https://rookery.cloud/docs/concepts/secrets) ·
[Scheduling](https://rookery.cloud/docs/concepts/scheduling)

## How it fits together

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/architecture-dark.svg">
  <img src="docs/assets/architecture.svg" alt="Telegram, Discord, Slack and a browser talk to one binary on your machine, holding the knowledge base, chat, agents, skills and secrets, all of them running on the coder — a CLI tool, a hosted provider or a local model — and both reading and writing the connected services and any MCP server" width="100%">
</picture>

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ROOKERY_HOST` | `0.0.0.0` | bind address; `127.0.0.1` for loopback-only |
| `ROOKERY_PORT` | `8080` | listen port |
| `ROOKERY_DATA_DIR` | `~/.rookery` | data root; also relocates the database |
| `ROOKERY_SESSION_KEY` | generated, then pinned to `<data_dir>/session.key` | hex 32-byte session key |
| `ROOKERY_SYSTEM_KEY` | generated | hex key encrypting stored credentials |
| `ROOKERY_PUBLIC_URL` | — | externally reachable base URL for OAuth callbacks |
| `ROOKERY_SANDBOX` | `1` | `0`/`false`/`off` disables Landlock confinement |
| `ROOKERY_BROWSER_ALLOW_PRIVATE` | `0` | `1`/`true`/`on` lets the headless browser reach private/loopback addresses (e.g. a self-hosted dashboard). Off by default — see the browser section |
| `ROOKERY_CODER_MODE` | `full` | `slim` removes the local CLI coder kind |
| `ROOKERY_CODER_BIN` | `claude` | default coder binary for workspaces that have not chosen one |
| `ROOKERY_CLAUDE_BIN` | — | **deprecated** alias for `ROOKERY_CODER_BIN`; still honoured, warns at startup |

`ROOKERY_PUBLIC_URL` matters more than it looks: OAuth providers reject redirect
URIs on non-public hostnames, so a `.lan` address fails Google's validation. Use
a real hostname or `http://localhost`.

### The browser

Reading JavaScript-rendered pages needs a headless browser, which is a few
hundred megabytes and therefore **not installed by default**. Without it
everything else works and only those pages are unreadable; `/healthz` reports
which state you are in.

`rookery onboard` offers it during setup on Linux, macOS and Windows alike, and
says nothing about it if you already have it. To install it separately:

```bash
rookery browser install     # Node driver + Chromium, once
rookery browser status
```

It runs confined by the same Landlock sandbox as the coder, and all of its
traffic goes through a proxy that refuses private and loopback addresses — the
browser follows links chosen from search results and page content, and the
loopback interface is where this server's own bridges and their tokens live.
`ROOKERY_BROWSER_ALLOW_PRIVATE=1` turns that guard off if you specifically want
an agent to read something on your own network.

Agents read pages and interact with them — clicking, filling forms, signing in
with passwords you have stored as secrets — with no configuration. **One thing
needs your permission: an action that cannot be undone**, such as paying,
ordering or deleting. When an agent's job involves one, its page says so and asks;
until you allow it, the agent goes up to that step, stops, and tells you what it
would have done.

Note the limit of that: it guards the browser. An agent can already make web
requests with a script, and no browser permission covers those.

## Platform support

| Target | Sandbox | Service |
|---|---|---|
| linux amd64/arm64 | Landlock | systemd user unit |
| container (linux) | Landlock | runtime-managed |
| darwin amd64/arm64 | none | launchd (not yet shipped) |
| windows amd64/arm64 | none | Task Scheduler logon task |

**Off Linux there is no filesystem sandbox** — coder subprocesses run
unconfined. `GET /healthz` reports that, along with version, coder mode and
host-tool presence; a `python3` warning there is not cosmetic, because without
it the agent-tool AST guardrail self-skips.

## Contributing

Branch off `main`; `main` only ever advances through merged pull requests. Use
[Conventional Commits](https://www.conventionalcommits.org/) — the PR title
becomes the squashed commit and drives release versioning. Run the full gate
locally first with `make ci`.

The three README images are generated —
edit `scripts/gen-readme-assets.py`, never `docs/assets/*.svg`.

## License

Apache-2.0. See [LICENSE](LICENSE).
