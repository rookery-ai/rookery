# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for a security problem.**

Report it privately through GitHub's [private vulnerability reporting](https://github.com/rookery-ai/rookery/security/advisories/new),
which is enabled on this repository. If you cannot use that, email
**security@rookery.cloud**.

Please include the version (`rookery version`), how the instance is deployed
(native binary, container, `.deb`/`.rpm`), and enough detail to reproduce.

We aim to acknowledge a report within 72 hours.

## Supported versions

Rookery is pre-1.0. Only the latest release receives fixes.

## Scope

Rookery is **self-hosted**: you run the server, hold the keys, and own the data.
There is no Rookery-operated service to attack. Reports we are most interested in:

- Anything crossing a workspace boundary — one workspace reaching another's vault, secrets, or connections
- Escaping the coder sandbox (Landlock) or the vault path guard (`vault.Resolve`)
- Leaking a decrypted secret, OAuth token, or master password into a log, prompt, API response, or agent-visible file
- Bypassing the owner/workspace session split, or the approval gate on public-write connector actions
- The private-address dial guard (`internal/nethttp`) failing to block a request built from untrusted content

Known and documented, so not vulnerabilities in themselves — see `CLAUDE.md`:

- **Connectors and MCP deliberately reach private addresses.** Self-hosted services live at RFC1918 and Tailscale addresses; the URL comes from vendored YAML or from the owner's own typing.
- **The Python AST guardrail is a filter, not a security boundary.** Landlock and the skill-vetter audit are the enforcement.
- **Off Linux there is no filesystem sandbox at all.** `/healthz` reports this.
