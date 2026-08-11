# MCP server integration — design

**Date:** 2026-08-11
**Status:** approved, wave 1

## Why

Rookery ships 91 vendored connectors covering the SaaS surface. MCP does not replace
them and must not be positioned as though it does. Its marginal value concentrates in
three places a YAML connector cannot reach:

1. **Capability servers with no HTTP API to wrap** — browser automation, local
   databases, docker. These are not REST endpoints, so no request template can express
   them.
2. **Services Rookery has not vendored** — the long tail, including vendor-published
   endpoints that stay current without anyone writing YAML.
3. **The user's own servers** — and this is the strategic one: **a user adds an
   integration without waiting for a Rookery release.** That is the property the
   connector model structurally cannot have, however many waves ship.

The honest counter-case belongs here rather than buried: per provider, vendored YAML is
cheaper, testable against golden fixtures, and controllable. A connector exposes ~5
curated actions chosen from a 200-endpoint API; an MCP server exposes whatever it
advertises. **MCP is the escape hatch beside connectors, never their replacement.**
A future contributor who reads this document should not start converting connectors to
MCP.

This wave also closes a live bug. `chat.BuildUserContext` already injects a
`[User's MCP tools]` block listing server names from the vestigial `mcp_servers` rows,
so the chat model is currently told about tools it cannot call.

## Decisions

| Decision | Choice | Rejected |
|---|---|---|
| Transport | HTTP (streamable) only | stdio — see Deferred |
| Auth | static bearer / custom header, encrypted under the system key | full MCP OAuth — see Deferred |
| Mutating/approval metadata | server hints seed the default, **owner override is the authority** | trusting hints outright; all-mutating-until-ticked |
| MCP client | official `github.com/modelcontextprotocol/go-sdk` v1.7.0 | hand-rolled JSON-RPC |
| Primitives | tools only | resources, prompts — see Deferred |
| CLI coder path | the existing loopback bridge | native `--mcp-config` passthrough |

Verified before committing to the SDK: v1.7.0 cross-compiles for all six release
GOOS/GOARCH pairs with `CGO_ENABLED=0`, and pulls 11 transitive dependencies. It already
carries multiple protocol versions with tracked deprecation windows (2025-11-25,
2026-07-28), which is the argument that settles it — SigV4 is a frozen algorithm and was
worth hand-rolling; MCP is not frozen, and version negotiation, transport fallback and
session semantics all move.

## Where servers and tools come from

Two separate things, easily conflated:

- **Which servers exist:** the owner pastes a URL. Nothing is compiled into the binary
  and nothing is fetched from any directory. Adding a server is the same gesture as
  configuring a self-hosted connector's `base_url` today.
- **What tools a server has:** discovered from *that server*, at runtime, via
  `tools/list`.

```
initialize            → protocolVersion, capabilities, serverInfo
notifications/initialized
tools/list  (cursor)  → { tools: [...], nextCursor, ttlMs, cacheScope }
```

The SDK's `session.Tools(ctx, nil)` returns an `iter.Seq2[*Tool, error]` that follows
`nextCursor`, so pagination is not ours to write. Each `Tool` carries `name`, `title`,
`description`, `inputSchema` (a JSON Schema object — this becomes `ToolDef.Params`
unchanged), optional `outputSchema`, and optional `annotations`.

An official public registry exists (`registry.modelcontextprotocol.io`) and is
deliberately **not** used — see Deferred.

## Architecture

`internal/mcp` is a peer of `internal/connectors`, mirroring it shape-for-shape so both
coder paths and every gate work identically:

| Connectors | MCP | Note |
|---|---|---|
| `Registry` (vendored YAML) | `Catalog` (DB-cached `tools/list` per server) | MCP has no vendored manifest |
| `ToolDefs` / `ResolveTool` | same names, same contract | naming defined exactly once |
| `Execute(…, Policy{BuildPhase, Parker})` | same shape | the single typed choke point |
| `ActiveBoundConns` | `ActiveBoundServers` | chat gets all enabled |
| `agent_connections` | `agent_mcp_servers` | per-agent binding |
| `ConnectorError` taxonomy | same taxonomy + `KindUnreachable` | actionable messages |
| `maxBridgeResult` (8 KiB) | reused verbatim | one cap, not two |

The SDK sits behind a narrow `internal/mcp/client.go` exposing only
`Connect`/`ListTools`/`CallTool` over Rookery types, so no `go-sdk` type appears anywhere
else in the codebase. Swapping the client later is a one-file change.

### Tool naming

Tool names are `mcp__<server-slug>__<tool>`. Unlike connectors there is no bare-name
case, because MCP tool names collide both with connector tools (`search`, `list_files`)
and between servers.

Three constraints come from the spec rather than from caution:

- MCP tool names legally contain dots (`admin.tools.list`) and run to 128 characters,
  while an LLM tool name must match `^[a-zA-Z0-9_-]{1,64}$`. **Slugging plus truncation
  with a uniqueness suffix is required**, not defensive.
- `serverInfo.name` is explicitly *not* guaranteed unique across servers and the spec
  tells aggregating clients not to rely on it for disambiguation. The namespace is
  therefore Rookery's own `mcp_servers.slug`, unique per workspace by constraint.
- A tool whose `inputSchema` carries a malformed `x-mcp-header` **must be excluded from
  the list** rather than failing the whole response — one bad definition must not take
  out the other 29 tools on that server.

### Sessions

Streamable HTTP is stateful (initialize → session id), so sessions are **pooled**, keyed
by `(workspace, server)`, with idle eviction and **one automatic reconnect-and-retry on a
dead session**. The retry is load-bearing: a homelab server that slept will have dropped
an idle session, and the first call after must not surface to the user as a failure.

### Catalog

The catalog is a per-workspace, per-server snapshot of one `tools/list` response,
normalized into `mcp_tools` rows. It is DB-cached rather than live for three reasons:
building an agent's tool list must not block on a down server; the per-tool owner
overrides need a stable row to hang off; and the SPA renders the tool list without
reaching the server.

Sync runs on connect (the Test & sync action), on manual re-sync, and on a background
refresh that honours the server's `ttlMs` when present and falls back to a fixed interval
when absent.

**Reconcile rules:**

- A tool that has vanished is marked `missing`, never deleted — the owner's `read_only`
  and `approval_mode` overrides survive a server restart.
- On the **first** sync, tools arrive **enabled** (up to the cap). The owner is actively
  adding this server and reviewing its tool list in the wizard; making them tick 30 boxes
  is friction with no security payoff.
- On **every later** sync, a newly appeared tool arrives **disabled**. Initial trust is a
  deliberate act; a server growing a tool three weeks later is not. **This asymmetry is
  the actual control** — a server cannot silently grow a live tool between runs.
- Enabled tools are capped per server by a fixed constant beside `maxBridgeResult`. A
  server advertising 80 tools would swamp the model's tool list and degrade selection for
  every *other* tool the agent has. Over the cap, sync enables up to it and the UI states
  plainly how many were held back — never a silent truncation.

## Data model — migration `012_mcp_servers`

`mcp_servers` (the existing vestigial table) gains:

| Column | Purpose |
|---|---|
| `slug` | the tool-name namespace; ours, not `serverInfo.name` |
| `transport` | `http`; the column exists so stdio is later a value, not a migration |
| `auth_kind` | `none` \| `bearer` \| `header` |
| `header_name` | for `auth_kind=header` |
| `encrypted_token` | encrypted under the **system key** |
| `status` | `ACTIVE` \| `NEEDS_AUTH` \| `UNREACHABLE` |
| `last_error` | operator triage |
| `tools_synced_at`, `tools_ttl_ms` | refresh policy |
| `server_info` | `serverInfo` + negotiated protocol version |

The token is encrypted under the system key rather than the workspace master password
for the same reason connector tokens are: the background refresh and cron runs must
decrypt headlessly.

`mcp_tools` — one row per discovered tool: `server_id`, the server's verbatim `name`, the
slugged `tool_name` exposed to the model, `title`, `description`, `input_schema`, the
three owner-authority columns `read_only` / `approval_mode` (`auto`|`approve`) /
`enabled`, and `missing`. `UNIQUE(server_id, name)` makes reconcile an upsert.

`agent_mcp_servers` — `(agent_id, server_id, approval_mode)`, mirroring
`agent_connections`.

`agent_drafts` gains `pending_used_mcp_servers`, the sibling of
`pending_used_connections`, so auto-bind survives a restart or a keep-as-is.

## Execution and gating

`mcp.Execute(ctx, cat, store, ref, tool, args, Policy{BuildPhase, Parker})`:

1. Validate args against the cached `input_schema`.
2. Re-check `enabled` — defense in depth; the catalog can change between the tool list
   being built and the call landing.
3. **Build-phase guard:** refuse unless the tool is `read_only`. The owner override is
   the authority here and the server's `readOnlyHint` was only the default that seeded
   it. The spec itself requires clients to treat annotations as untrusted.
4. **Park** if the binding or the tool says `approve` — written to `pending_actions`, and
   the ticket is returned as a **success**, never an `error:` string, or the coder's tool
   loop retries it.
5. Session from the pool, one reconnect-and-retry.
6. `tools/call` with a timeout.
7. Map the result, then `capBridgeData` verbatim.

### Result mapping

MCP results are richer than a connector's JSON body, so the rule is explicit:

- prefer `structuredContent` when present (already JSON);
- otherwise concatenate the `text` blocks;
- **replace image and audio blocks with a placeholder naming the mimeType** — a single
  screenshot's base64 would consume the whole 8 KiB budget and teach the model nothing;
- keep `resource_link` as uri + name.

### Two error channels, mapped opposite ways

MCP separates *tool execution* errors (`isError: true` — bad date format, value out of
range) from *protocol* errors (unknown tool, malformed request). The spec says clients
should hand execution errors to the model so it can self-correct.

- `isError: true` → returned as plain text **without** the `error:` prefix, so the API
  engine's oscillation guard does not count it as a failing call.
- protocol error or transport failure → `error:` prefix, counted by the guard.

Getting this backwards either kills legitimate retry-with-fixed-args, or lets a genuinely
dead server spin until the turn budget runs out.

### Capabilities Rookery declines

Rookery advertises **neither sampling nor elicitation** at `initialize`. A server can
otherwise ask the client to run an LLM completion (sampling) or to prompt the human
(`input_required`). A scheduled run at 03:00 has no human, and sampling would let a
third-party server spend the owner's tokens. A server returning `input_required` anyway
gets an actionable error naming the server. This is a security property, not an omission.

## Coder paths

**API engine.** `hostToolSet` gains `mcpTools()` / `resolveMCPTool()` /
`executeMCPTool()`, a near-copy of `connectortools.go`.

**CLI coders reach the same `Execute` through the bridge that already exists** — not a
second one. `bridgeSession` gains `boundMCP []BoundServer`, the listener gains a
`/mcp/exec` route, and the same per-run token scopes both families. A new subcommand
`rookery mcp exec <tool> --args '<json>'` reads `ROOKERY_MCP_URL` / `ROOKERY_MCP_TOKEN`,
set to the same values as the connector pair but under their own names so the two are
never coupled if they later split. CLI chat gets the scoped `Bash(<bin> mcp exec:*)`
grant beside the existing connector one.

### Rejected: native `--mcp-config` passthrough

Handing claude-code or codex an MCP config and letting it speak MCP itself is the obvious
shortcut. It is recorded here as rejected so it is not re-proposed as an optimization:

1. It bypasses `Execute`, so the build-phase refusal, the approval parker and the 8 KiB
   cap all vanish.
2. It makes the **coder-kind setting silently change the security posture** — precisely
   the failure `Bridge.RegisterGated` exists to prevent.
3. Only some backends support it; opencode, cursor and the generic fallback would need a
   different story regardless.
4. **It requires writing the MCP bearer token into a config file the sandboxed subprocess
   reads.** Today's invariant is that tokens never leave the host process. This would put
   credentials on disk inside the workspace home — a real regression, not a preference.

Note also that `--setting-sources ""` plus the redirected `XDG_CONFIG_HOME` already keep
the operator's own MCP config out of workspaces deliberately; native passthrough would
reverse that isolation decision.

## Binding

An agent's MCP access is bound three ways, mirroring connections exactly so a weak model
cannot quietly produce an agent with no access:

1. An `# MCP:` header in AGENT.md, parsed with the same tolerant matcher shape as
   `parseConnectionsLine`.
2. **Auto-bind** from the server ids the build actually called, persisted in
   `agent_drafts.pending_used_mcp_servers`. Never all, never clobbering an existing
   binding; an explicit header always wins.
3. The **Attach MCP servers** card on the agent page — the reliable manual path.

Builds expose all enabled servers; runs expose only bound ones. Chat gets all enabled
servers (`ActiveBoundServers`), since chat is not an agent and has no binding.

## Health and failure isolation

**A down server must never fail an agent run.** Its tools stay offered from cache and a
call returns a definitive error naming the server and its status.

*Alternative considered and rejected:* withholding a down server's tools. The agent then
silently loses capability with no explanation, and the run output looks as though it
chose not to do the task.

**The status flip is gated.** Only a definitive 401 from the server flips to
`NEEDS_AUTH`; 5xx, 429 and network failures become `UNREACHABLE`, which does not alert
and does not leave the retry path. This applies the `DBTokenStore.refresh` lesson on day
one instead of after the bug: a DNS blip must not cost a working server until the owner
reconnects it by hand.

Alerting reuses **`internal/connalert`** rather than a new package — it is already
"deliver an action-required notice to the inbox *and* chat" behind a narrow `SendToUser`
interface, and its stated reason for existing (needs DB and gateway; the integration
package knows neither) applies verbatim.

**Private addresses stay reachable.** `internal/mcp` does not use the `nethttp` dial
guard, mirroring `connectors.TestExecuteReachesPrivateAddresses` and its recorded
reasoning: the URL is owner-typed and self-hosted servers live at RFC1918 or Tailscale
addresses. `mcp.TestExecuteReachesPrivateAddresses` pins it and carries the same
revisit-if-multi-tenant note.

**Audit matches connectors**: management actions (add, delete, sync, bind) write audit
rows; individual tool calls do not. Per-call auditing would flood `audit_logs` from a
chatty agent, and diverging from connectors here would be a difference with no
justification.

## Prompts

`connectedToolsBlock` gains MCP awareness, backend-aware (native tools vs
`rookery mcp exec`), single-sourced in `internal/prompts` per the standing rule that no
prompt text lives outside that package.

The `[User's MCP tools]` prose block is **removed** from `chat.BuildUserContext`. Once
tools are real they arrive as tool definitions; the prose block described tools that did
not work.

**Accepted risk, stated rather than implied:** MCP tool descriptions are untrusted remote
text landing in the model's tool list — a prompt-injection surface with no connector
analogue. Mitigations are the ones already in this design: owner-typed URLs only,
per-tool enable with the description visible at review time, bounded description length,
and new tools disabled on every sync after the first.

## Surfaces

**`/connections` gains an MCP servers section.** Structurally it sits where **Chat apps**
sits, not where a service category sits: service categories are *derived* from the
vendored registry via `groupByCategory(filteredServices)`, while MCP is a hand-placed
section with its own data source. It gets a row in the context-pane nav with a count, and
the same scroll-to-section behaviour.

Each server renders as a card (name, URL, status, tool count, last sync). Expanding it
reveals the tool table: name, description, and three ticks — enabled, read-only, needs
approval.

The add flow is a wizard sibling to `ServiceWizard`: paste URL, pick auth kind, paste
token, **Test & sync**. Test is a hard gate, not a convenience — it runs the real
`initialize` + `tools/list`, and the tool list it returns *is* the review step where the
owner reads the descriptions before anything is enabled. A server that cannot complete
the handshake is never saved as usable. This matters because the real risk in this
feature is not our code but **real-world server conformance**, which varies.

**Agent page** gains an "Attach MCP servers" card beside the existing Attach-connections
card, same shape, same handler pattern.

### API routes

All on the workspace-scoped group (`requireOwnerAPI` → `requireActiveWorkspaceAPI` →
`requireSetupCompleteAPI`):

```
GET|POST      /api/v1/mcp/servers
GET|PUT|DEL   /api/v1/mcp/servers/:id
POST          /api/v1/mcp/servers/:id/test     # initialize — validate URL + auth
POST          /api/v1/mcp/servers/:id/sync     # tools/list + reconcile
GET           /api/v1/mcp/servers/:id/tools
PUT           /api/v1/mcp/servers/:id/tools/:toolID
GET|PUT       /api/v1/agents/:id/mcp           # binding
```

Every one must be added to `web/api_parity_test.go`'s `want` table — a merge gate, not a
nicety.

DTO slice fields initialise as `[]T{}`, never a nil slice: a Go nil slice marshals to
JSON `null`, and a TypeScript default parameter substitutes only for `undefined`. This is
the `flattenRequires` bug and it must not be reintroduced here.

## Testing

The SDK ships a server implementation, so tests run a **real in-process MCP server** over
`httptest` — no mocks, no golden fixtures.

Coverage that matters:

- naming: dot-bearing names, 128-char truncation with uniqueness, two servers both
  exposing `search`;
- `ToolDefs` → `ResolveTool` round-trip;
- build-phase refusal on a non-read-only tool, allowance on a read-only one;
- park returns success, not an `error:` string;
- result mapping: `structuredContent` preferred, image block replaced, cap applied;
- `isError` takes no `error:` prefix, protocol error does;
- reconcile: overrides preserved, vanished tool marked `missing`, new tool disabled on a
  second sync but enabled on the first;
- a malformed `x-mcp-header` excludes only its own tool;
- an unreachable server returns an actionable error and does **not** flip to
  `NEEDS_AUTH`;
- `TestExecuteReachesPrivateAddresses`.

## Deferred, with reasons

- **stdio transport.** A stdio server is spawned by the *host* process, which holds the
  DB and the system key and is unsandboxed — strictly more privileged than the coder's
  own Landlock-confined `bash`. It needs its own sandboxing story, not a transport
  switch. The `transport` column exists so it becomes a value later, not a migration.
- **MCP OAuth** — RFC 9728 protected-resource-metadata discovery, RFC 8414/OIDC
  authorization-server discovery, client registration (Client ID Metadata Documents
  preferred; DCR deprecated but retained), PKCE, RFC 8707 resource indicators, RFC 9207
  `iss` validation. Its own wave and its own spec; it shares nothing with the connector
  OAuth path, which assumes a hand-registered per-provider app. Until then a server
  requiring OAuth gets a clear "this server needs OAuth, not yet supported" error rather
  than an opaque 401.
- **Resources and prompts primitives.** Resources are attractive for the self-hosted case
  but are a read surface with no connector analogue: new UI, new KB ingest path, own
  `iolimit` cap. Prompts cut against the standing rule that all prompt text lives in
  `internal/prompts`.
- **Public registry browse picker.** Purely additive — it prefills the URL field and
  touches neither catalog, gating nor execution. Held back because a browsable list
  changes the trust story the whole gating design rests on ("the owner typed this URL
  deliberately").
- **`notifications/tools/list_changed` push.** Needs a held subscription stream;
  TTL-aware polling plus manual sync covers this wave.
- **Native CLI passthrough.** Rejected above with reasons.

## Documentation obligation

A new CLI subcommand, a new `/api/v1` route family and two new `ROOKERY_*` variables each
carry a documentation obligation in both repositories. The `docs-sync` skill runs before
the PR, and `make docs-sync-check` mechanises the checkable half. `CLAUDE.md` needs an
`internal/mcp` architecture entry shaped like the connectors one, and its Known gaps line
"MCP servers — `mcp_servers` table exists; MCP tool execution not implemented" must be
replaced with an accurate statement of what this wave shipped and what it deferred.
