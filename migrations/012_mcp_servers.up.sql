-- MCP server integration, wave 1: HTTP transport, static token auth, tools only.
--
-- The mcp_servers table already existed but held only (name, url, enabled) and was
-- never executed against — chat merely listed the names as prose. It grows up here
-- into the peer of service_connections.
--
-- Unlike a connector, NOTHING about an MCP server ships in the binary: the owner
-- pastes a URL and the server itself is asked what tools it has (tools/list). That is
-- what mcp_tools caches.

-- slug is the tool-name namespace: exposed tools are mcp__<slug>__<tool>. It is OURS,
-- not the server's own serverInfo.name — the spec states outright that serverInfo.name
-- is not guaranteed unique across servers and must not be relied on for
-- disambiguation. UNIQUE(workspace_id, slug) is what makes the namespace real.
ALTER TABLE mcp_servers ADD COLUMN slug TEXT NOT NULL DEFAULT '';

-- 'http' today. The column exists so stdio becomes a VALUE later rather than another
-- migration — but stdio is deliberately not implemented: a stdio server is spawned by
-- the host process, which holds the DB and the system key and is unsandboxed, making
-- it strictly more privileged than the coder's own Landlock-confined bash.
ALTER TABLE mcp_servers ADD COLUMN transport TEXT NOT NULL DEFAULT 'http';

-- none|bearer|header. MCP authorization is OPTIONAL in the spec, so 'none' is a real
-- and common case for a server on a trusted LAN.
ALTER TABLE mcp_servers ADD COLUMN auth_kind TEXT NOT NULL DEFAULT 'none';
ALTER TABLE mcp_servers ADD COLUMN header_name TEXT NOT NULL DEFAULT '';

-- Encrypted under the SYSTEM key, not the workspace master password — the background
-- refresh and cron runs must decrypt headlessly, exactly as connector tokens do.
ALTER TABLE mcp_servers ADD COLUMN encrypted_token TEXT NOT NULL DEFAULT '';

-- ACTIVE|NEEDS_AUTH|UNREACHABLE.
--
-- The two failure states are deliberately distinct. Only a definitive 401 from the
-- server flips to NEEDS_AUTH; 5xx, 429 and network failures become UNREACHABLE, which
-- does NOT alert and does NOT leave the retry path. This applies the DBTokenStore
-- lesson on day one: a DNS blip must not cost a working server until the owner
-- reconnects it by hand.
ALTER TABLE mcp_servers ADD COLUMN status TEXT NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE mcp_servers ADD COLUMN last_error TEXT NOT NULL DEFAULT '';

-- tools_ttl_ms comes from the tools/list response when the server supplies it, so the
-- refresh cadence has an upstream signal rather than a guessed interval. 0 = absent,
-- fall back to the fixed default.
ALTER TABLE mcp_servers ADD COLUMN tools_synced_at TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_servers ADD COLUMN tools_ttl_ms INTEGER NOT NULL DEFAULT 0;

-- serverInfo + the negotiated protocol version, as JSON. Operator triage: "which
-- protocol version did we actually settle on with this server".
ALTER TABLE mcp_servers ADD COLUMN server_info TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_servers_ws_slug ON mcp_servers(workspace_id, slug);

-- One row per tool discovered from a server's tools/list.
--
-- This is a CACHE of a remote response, but it is not disposable: the three owner
-- columns (read_only, approval_mode, enabled) are authored here and must survive a
-- re-sync. UNIQUE(server_id, name) is what makes reconcile an upsert rather than a
-- delete-and-reinsert that would discard them.
CREATE TABLE IF NOT EXISTS mcp_tools (
    id           TEXT PRIMARY KEY,
    server_id    TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,

    -- The server's own name, verbatim — what tools/call is invoked with. MCP allows
    -- dots and up to 128 characters here, neither of which an LLM tool name permits.
    name         TEXT NOT NULL,
    -- The slugged, truncated, uniqueness-suffixed mcp__<slug>__<tool> exposed to the
    -- model. Stored rather than recomputed so a rename upstream cannot silently
    -- re-point a name the model already learned within a run.
    tool_name    TEXT NOT NULL,

    title        TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    input_schema TEXT NOT NULL DEFAULT '{"type":"object"}',

    -- Seeded from the server's readOnlyHint annotation, then OWNED by the owner. The
    -- MCP spec requires clients to treat annotations as untrusted, so the hint is a
    -- default and this column is the authority. Execute's build-phase guard reads
    -- THIS, never the annotation.
    read_only     INTEGER NOT NULL DEFAULT 0,
    -- auto|approve — the per-tool half of the approval gate; the binding row carries
    -- the per-agent half.
    approval_mode TEXT NOT NULL DEFAULT 'auto',

    -- Whether the tool is offered to the model at all.
    --
    -- The default is 0 because it governs tools appearing on a LATER sync: a server
    -- must not be able to grow a live tool between runs. The FIRST sync of a server
    -- enables its tools explicitly in code, because the owner is adding that server
    -- and reading its tool list right then — making them tick 30 boxes is friction
    -- with no security payoff. That asymmetry is the actual control.
    enabled      INTEGER NOT NULL DEFAULT 0,

    -- A tool that vanished upstream is marked, never deleted: deleting would discard
    -- the owner's overrides on a server restart.
    missing      INTEGER NOT NULL DEFAULT 0,

    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(server_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mcp_tools_server ON mcp_tools(server_id, enabled);

-- Per-agent binding, mirroring agent_connections. Builds expose every enabled server;
-- runs expose only what the agent is bound to.
CREATE TABLE IF NOT EXISTS agent_mcp_servers (
    agent_id      TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    server_id     TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    approval_mode TEXT NOT NULL DEFAULT 'auto',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (agent_id, server_id)
);

-- Sibling of pending_used_connections: persists the MCP server ids a build actually
-- called, so auto-bind survives a restart or a resumed keep-as-is draft.
ALTER TABLE agent_drafts ADD COLUMN pending_used_mcp_servers TEXT NOT NULL DEFAULT '';
