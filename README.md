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

Agents that work on your behalf, on hardware you own — your knowledge, your
accounts, and the pages that have no API.

Rookery is a platform for agents that work on your behalf, running on hardware
you own. You install one program. It gives you a place to keep what you know, as
plain markdown on your own disk, a way to build agents by describing them in
plain language, and a way for those agents to reach the services you already
use — email, calendars, repositories, home automation, whatever you connect.
They run on a schedule, or when you ask, and they tell you what happened. The
whole thing is designed to sit on a machine that stays on, so the work carries
on while you are not watching.

It is not a workflow builder. There is no canvas and no nodes to wire together;
you describe the outcome, not the path to it.

<!--
SCREENSHOT SLOT — not captured yet. Two shots, in this order.

1. THE RUN TIMELINE (here). The ordered steps of a real run, with the approval
   gate visible IN THE SAME FRAME as the payment step. Capability and restraint
   together read as competence; the same capability with the restraint explained
   underneath reads as recklessness discovered. Secret fields must show the
   PLACEHOLDERS (${CARD_NUMBER}, ${CVV}) and never values — the only safe shot,
   and the more convincing one, because it is the visible evidence that the
   model never receives them.

2. THE BUILD CONVERSATION (under "What you get"). The designer asking its
   clarifying questions and proposing a plan, with "Allow and build" visible.

Use a test card, a sandbox account or a throwaway. Never real payment data: a
screenshot cannot be redacted, which is exactly why agents are never given one.

Markup, ready to uncomment once the file exists. It is commented rather than
live because an <img> pointing at a missing file renders as a broken image on
GitHub, which is worse than no image at all:

<p align="center">
  <img src="docs/assets/run-timeline.png" alt="A run timeline: opened the page, signed in, found one unpaid bill for the previous month, filled the card fields from stored secrets, then stopped before paying and asked for approval" width="100%">
</p>
-->

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

Windows has no filesystem confinement. Autostart is a Task Scheduler logon task
that `rookery service` registers for you — see
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
  <img src="docs/assets/features.svg" alt="Knowledge base, agents, browser control, skills, connections, MCP servers, chat, notifications, models, secrets, scheduling and sandboxing" width="100%">
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
[Browser](https://rookery.cloud/docs/concepts/browser) ·
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
becomes the squashed commit and drives release versioning. Push the branch and
read the result from CI rather than reproducing it locally; `make ci-fmt`,
`ci-vet`, `ci-ui` and `ci-docs` are the quick targeted checks worth running
first.

The three README **diagrams** are generated — edit
`scripts/gen-readme-assets.py`, never `docs/assets/*.svg`. Screenshots are not:
those are captured by hand, and the brief for each is in a comment beside its
slot at the top of this file.

## License

**Apache-2.0, permanently.** See [LICENSE](LICENSE).

There is no contributor licence agreement. Without one, nobody — the maintainers
included — can relicense this project unilaterally, which makes the line above a
structural fact about the repository rather than a statement of intent.
