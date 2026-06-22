# Plan: Reliable Composio Integration — v3 REST API, Real Validation, Multi-Service Guidance

## Context

The initial Composio integration is complete (UI, secrets storage, presence check, setup wizard),
but agents fail to work because:

1. **Broken SDK guidance** — the `<connected_services>` prompt block told coders to use
   `ComposioToolSet.execute_action()` (composio-core SDK). Live testing proved the SDK v0.7.21
   uses internal v1/v2 endpoints that now return HTTP 410. The SDK cannot be upgraded (missing
   libjpeg dependency). The correct approach is the **v3 REST API directly** via `requests`.

2. **No real validation** — during agent generation, secrets aren't injected into the coder
   subprocess (`runGeneration` never calls `WithExtraEnv`). So `COMPOSIO_API_KEY` is always
   missing during the test phase, and the coder generates mock output instead of real data.
   The implementation prompt explicitly says "use mock values if secret is missing."

3. **No multi-service guidance** — coders don't know tool slugs, the `arguments` field format,
   or how to handle connection errors for any service beyond what was hand-tested.

4. **No user error protocol** — when a service isn't connected in Composio, agents output a raw
   API error instead of actionable guidance telling the user what to do.

**Proven v3 REST pattern** (live-tested against real Notion connection):
- Base: `https://backend.composio.dev/api/v3`
- Auth: `x-api-key: {COMPOSIO_API_KEY}` header
- Connected account discovery: `GET /connected_accounts?limit=100` → filter by `toolkit.slug` + `status=ACTIVE`
- Execution: `POST /tools/execute/{TOOL_SLUG}` with body `{"connected_account_id":"ca_xxx","user_id":"...","arguments":{...}}`
- Field is `arguments` (NOT `input` or `params`) — live-tested and confirmed
- Response: `result["data"]["response_data"]` or `result["data"]` depending on tool
- Tool slug discovery: `GET /tools?toolkit_slug=notion&limit=50`

---

## What Needs to Be Changed

| Component | What |
|-----------|------|
| `internal/prompts/prompts.go` | Replace `<connected_services>` block with v3 REST pattern + service reference + error protocol |
| `internal/prompts/prompts.go` | Update `BuildImplementationPrompt` to require real API calls when secrets are present |
| `internal/agentdesigner/flow.go` | Add `secretsLoader` to `Flow`; inject secrets in `runGeneration` via `WithExtraEnv` |
| `cmd/simple-agents/main.go` | Wire `secretsLoader` from system key + DB when constructing the Flow |
| `internal/agentdesigner/guardrails.go` | Block SDK import patterns; update `ak_` key pattern |
| `web/templates/dashboard/composio.html` | Fix the code example (currently shows broken SDK pattern) |

No DB migration, no new routes — the UI and secrets storage are already done.

---

## 1. `<connected_services>` Prompt Block — Complete Rewrite

**File:** `internal/prompts/prompts.go` — the `if p.ComposioEnabled` block in `BuildDesignSystemPrompt`

Replace the entire block with:

```
<connected_services>
This user has configured Composio (composio.dev) and connected external services to their
personal Composio account. Their COMPOSIO_API_KEY is available as an environment variable.

━━━ DO NOT USE THE COMPOSIO-CORE SDK ━━━
The composio-core Python package (ComposioToolSet, Action enum) uses deprecated v1/v2
endpoints that return HTTP 410. FORBIDDEN:
  ❌ from composio import ComposioToolSet, Action
  ❌ from composio import Composio; composio.create(...)
  ❌ toolset.execute_action(...)
  ❌ requests.post(".../api/v1/..." or ".../api/v2/...")

━━━ USE THE v3 REST API DIRECTLY ━━━

## Step 1 — Always generate tools/composio_helper.py

```python
import os, json, requests

COMPOSIO_BASE = "https://backend.composio.dev/api/v3"

def _headers(api_key):
    return {"x-api-key": api_key, "Content-Type": "application/json"}

def composio_get(path, api_key=None):
    api_key = api_key or os.environ["COMPOSIO_API_KEY"]
    r = requests.get(f"{COMPOSIO_BASE}{path}", headers=_headers(api_key), timeout=20)
    r.raise_for_status()
    return r.json()

def composio_execute(tool_slug, conn_id, user_id, arguments, api_key=None):
    api_key = api_key or os.environ["COMPOSIO_API_KEY"]
    r = requests.post(
        f"{COMPOSIO_BASE}/tools/execute/{tool_slug}",
        headers=_headers(api_key),
        json={"connected_account_id": conn_id, "user_id": user_id, "arguments": arguments},
        timeout=30,
    )
    r.raise_for_status()
    return r.json()

def get_connection(toolkit_slug, api_key=None):
    """Returns (conn_id, user_id) for the first ACTIVE connection. Raises ConnectionError if none."""
    api_key = api_key or os.environ["COMPOSIO_API_KEY"]
    data = composio_get("/connected_accounts?limit=100", api_key)
    for acc in data.get("items", []):
        if acc.get("toolkit", {}).get("slug") == toolkit_slug and acc.get("status") == "ACTIVE":
            return acc["id"], acc["user_id"]
    raise ConnectionError(
        f"No active {toolkit_slug} connection found. "
        f"Go to app.composio.dev/connections → add {toolkit_slug} → run this agent again."
    )
```

## Step 2 — Discover tool slugs before writing any code

Before hardcoding a tool slug, discover the available slugs for the target service:
  GET /api/v3/tools?toolkit_slug=APPNAME&limit=50   → items[].slug, items[].name

Common toolkit slugs: notion, slack, google-drive, gmail, google-calendar, github, linear, jira

## Step 3 — Execute pattern (use in every tool script)

```python
from composio_helper import get_connection, composio_execute
import json, sys

try:
    conn_id, user_id = get_connection("notion")   # ← replace with target toolkit_slug
except ConnectionError as e:
    print(json.dumps({"error": str(e)}))
    sys.exit(1)

result = composio_execute("NOTION_SEARCH_NOTION_PAGE", conn_id, user_id,
                          {"query": "My Database"})

if not result.get("successful", True) or result.get("error"):
    raise RuntimeError(result.get("error") or "unknown error")

# Extract response — two patterns (try both):
resp = result.get("data", {}).get("response_data") or result.get("data", {})
```

## Step 4 — Connection error output (copy this pattern verbatim in AGENT.md)

When a ConnectionError is caught, the agent must output:
  [CHAT] ❌ Could not access [ServiceName]: {error_message}

The error_message from get_connection() already contains the actionable fix
("Go to app.composio.dev/connections → add {service} → run this agent again.")

## Verified tool slugs

Notion:       NOTION_SEARCH_NOTION_PAGE, NOTION_FETCH_BLOCK_CONTENTS,
              NOTION_QUERY_DATABASE, NOTION_CREATE_NOTION_PAGE, NOTION_UPDATE_BLOCK,
              NOTION_APPEND_BLOCK_CHILDREN, NOTION_DELETE_BLOCK, NOTION_ADD_PAGE_CONTENT,
              NOTION_CREATE_DATABASE, NOTION_DUPLICATE_PAGE, NOTION_FETCH_BLOCK_METADATA
Slack:        SLACK_SENDS_A_MESSAGE, SLACK_LIST_CHANNELS, SLACK_FETCH_MESSAGE
Google Drive: GOOGLEDRIVE_FIND_FOLDER, GOOGLEDRIVE_LIST_FILES_IN_FOLDER, GOOGLEDRIVE_UPLOAD
Gmail:        GMAIL_FETCH_EMAILS, GMAIL_SEND_EMAIL, GMAIL_LIST_THREADS
Google Cal:   GOOGLECALENDAR_LIST_EVENTS, GOOGLECALENDAR_CREATE_EVENT
GitHub:       GITHUB_LIST_ISSUES, GITHUB_CREATE_AN_ISSUE, GITHUB_LIST_PULL_REQUESTS
Linear:       LINEAR_GET_ISSUES, LINEAR_CREATE_ISSUE, LINEAR_UPDATE_ISSUE
Jira:         JIRACLOUD_GET_ISSUES, JIRACLOUD_CREATE_ISSUE

IMPORTANT: Slugs may change. Always verify via GET /api/v3/tools?toolkit_slug=APPNAME&limit=50
before coding. If a slug returns 404, fetch the list and find the closest match by name.

## Connection status values

Only proceed when status == "ACTIVE":
INITIALIZING, INITIATED — auth in progress; FAILED, EXPIRED, REVOKED — re-auth needed;
INACTIVE — disabled. All of these → direct user to app.composio.dev/connections.

## Response data notes

- Always check `result.get("successful", True)` — tools return HTTP 200 even on failure
- Check `result.get("error")` first
- Most tools: `result["data"]["response_data"]` contains the payload
- Some tools: `result["data"]` directly contains `http_error`, `message`, `status_code`

## Natural language argument inference (optional)

If you're unsure what arguments a tool needs:
  POST /api/v3/tools/execute/{tool_slug}/input
  Body: {"connected_account_id":"ca_xxx","user_id":"...","text":"describe what you want"}
This converts plain text to the correct arguments object.

## Testing

COMPOSIO_API_KEY IS in your environment. Make REAL API calls — do NOT mock.
If a connection is not active, output the guidance in [TEST_OUTPUT].
A failed-but-guiding output is better than fake mock success.
</connected_services>
```

---

## 2. `BuildImplementationPrompt` — Real API Call Requirement

**File:** `internal/prompts/prompts.go` — the SECRETS step in the implementation prompt

Current (wrong — forces mock data when key is missing):
```
SECRETS: If a required secret is missing from the environment, substitute a
realistic mock value FOR THIS TEST ONLY. Do NOT abort — demonstrate the output format.
```

New:
```
SECRETS: Read all secrets via os.environ.get('SECRET_NAME', '').
If COMPOSIO_API_KEY is present in the environment, make REAL API calls — produce REAL
output, not mock data. If a Composio connection fails, output the real error in
[TEST_OUTPUT] and guide the user what to fix (e.g. "go to app.composio.dev/connections").
For other missing secrets (non-Composio), substitute a realistic mock value for the test
only. Do NOT abort.
```

---

## 3. Secrets Injection During Generation

**Problem:** `runGeneration` in `internal/agentdesigner/flow.go` (~line 846):
```go
result, err := coderSvc.WithDir(workDir).WithAllowedTools("Bash,Write,Edit,Read").Generate(...)
```
No `WithExtraEnv` → `COMPOSIO_API_KEY` not in subprocess → coder uses mock data.

**Solution:** Add a `secretsLoader` to `Flow` and call it in `runGeneration`.

### A. `internal/agentdesigner/flow.go`

Add field to `Flow` struct:
```go
secretsLoader func(ctx context.Context, userID string) (map[string]string, error)
```

Add method:
```go
func (f *Flow) WithSecretsLoader(fn func(ctx context.Context, userID string) (map[string]string, error)) *Flow {
    f.secretsLoader = fn
    return f
}
```

In `runGeneration`, replace the single-line coder call with:
```go
generationCoder := coderSvc.WithDir(workDir).WithAllowedTools("Bash,Write,Edit,Read")
if f.secretsLoader != nil {
    if env, err := f.secretsLoader(genCtx, userID); err == nil && len(env) > 0 {
        generationCoder = generationCoder.WithExtraEnv(env)
    }
}
result, err := generationCoder.Generate(genCtx, userID, prompt)
```

### B. `cmd/simple-agents/main.go`

After constructing `designFlow`, wire the loader. `secrets.DecryptMasterPassword` is at
`internal/secrets/service.go:216`. The `systemKey` derivation is already in `main.go`
(same call used by `web.NewServer`):

```go
designFlow.WithSecretsLoader(func(ctx context.Context, userID string) (map[string]string, error) {
    user, err := database.GetUserByID(userID)
    if err != nil || user.EncryptedMasterPassword == "" {
        return nil, err
    }
    masterPw, err := secrets.DecryptMasterPassword(user.EncryptedMasterPassword, systemKey)
    if err != nil {
        return nil, err
    }
    svc := secrets.New(database, userID, masterPw, user.SecretsSalt)
    return svc.GetAll(ctx)
})
```

The loader silently returns nil on any error — generation still works without real API calls,
the coder then hits 401 and reports it in `[TEST_OUTPUT]` with the connection guidance.

---

## 4. Guardrails Update

**File:** `internal/agentdesigner/guardrails.go`

### A. Block SDK import patterns (add to `checkEthics`):

```go
var composioSDKPattern = regexp.MustCompile(`(?m)^from composio import|^import composio`)
if composioSDKPattern.MatchString(code) {
    return fmt.Errorf("use the Composio v3 REST API directly (requests library) — the composio-core SDK uses deprecated endpoints")
}
```

### B. Update literal key regex to cover `ak_` prefix (new key format):

```go
var composioKeyLiteral = regexp.MustCompile(
    `(?i)(["'])(c_live_|c_test_|ak_)[A-Za-z0-9]{8,}["']`,
)
```

---

## 5. `composio.html` Code Example Fix

**File:** `web/templates/dashboard/composio.html` lines 93–111

The `<pre>` block shows the broken SDK pattern (`from composio import ComposioToolSet, Action`).
Replace with the v3 REST pattern from `composio_helper.py` so users see the correct approach.

---

## Verification

1. `go build ./... && go test ./... -count=1 -timeout 120s` — no regressions
2. Start a design session with `COMPOSIO_API_KEY` set → system prompt must contain `composio_helper.py` and `FORBIDDEN: from composio import`
3. Create a Notion agent → approve → `[TEST_OUTPUT]` must show real Notion data
4. Create agent for unconnected service → `[TEST_OUTPUT]` must contain `app.composio.dev/connections` guidance
5. POST AGENT.md with `from composio import ComposioToolSet` → must be rejected by guardrail
6. Secrets injection test: create agent that prints `os.environ.get('COMPOSIO_API_KEY','MISSING')` → `[TEST_OUTPUT]` must show the real key, not MISSING
