---
name: composio-toolkit
description: Use this skill whenever the user wants to act on a connected external service (Gmail, Google Calendar/Drive, GitHub, Slack, Notion, Linear, etc.) through Composio — send an email, create a calendar event, open an issue, post a message, list files. Triggers include "send an email via gmail", "create a calendar event", "open a github issue", "post to slack", "search my drive".
version: 1.0.0
license: MIT-0
category: Integrations
metadata:
  openclaw:
    requires:
      env: [COMPOSIO_API_KEY]
    install:
      - kind: pip
        package: requests
---

# Composio Toolkit

Drive 250+ external services through the **Composio v3 REST API**. This is an
LLM+REST skill — you list the user's connected accounts, look up the right tool
slug, and execute it with a plain HTTP call. Never use the deprecated
`composio-core` SDK or the v1/v2 endpoints.

## Requirements

- `requests` (Python) — `python3 -m pip install --user requests`.
- `COMPOSIO_API_KEY` env var — read from `os.environ`, **never hardcode**.
- A connected account for the target service (the user connects accounts in the
  dashboard / via Composio).

## v3 API (the only supported version)

- Base: `https://backend.composio.dev/api/v3`
- Auth: header `x-api-key: <COMPOSIO_API_KEY>`
- List connected accounts: `GET /connected_accounts?limit=100`
- Execute a tool: `POST /tools/execute/{TOOL_SLUG}` with JSON body
  `{"connected_account_id": "<id>", "input": { ... }}`
- List available tools for a service: `GET /tools?limit=200&toolkits=<service>`

The runtime environment block tells you `COMPOSIO_API_KEY` is available (or marks
it missing).

## Discover + execute

```python
import requests, os, json, sys

API = "https://backend.composio.dev/api/v3"
KEY = os.environ["COMPOSIO_API_KEY"]
H = {"x-api-key": KEY, "Content-Type": "application/json"}

# 1. Find the user's connected account for a service (e.g. gmail)
accts = requests.get(f"{API}/connected_accounts?limit=100", headers=H).json()
gmail_acct = next((a for a in accts.get("items", []) if a.get("toolkit_name")=="gmail"
                  or "gmail" in (a.get("toolkit_name") or "").lower()), None)
if not gmail_acct:
    sys.exit("No connected Gmail account. Ask the user to connect one.")

# 2. Execute a tool
r = requests.post(f"{API}/tools/execute/GMAIL_SEND_EMAIL", headers=H,
                  json={"connected_account_id": gmail_acct["id"],
                        "input": {"recipient_email":"user@example.com",
                                  "subject":"Hello","body":"Sent via Composio"}})
print(json.dumps(r.json()))
```

## How to act (LLM-driven)

1. Identify the target service and the action the user wants.
2. List connected accounts; pick the matching one (by `toolkit_name`). If none,
   tell the user to connect the account — do not improvise credentials.
3. Look up the exact tool slug (`GET /tools?toolkits=<service>`) if unsure.
4. Execute; inspect the response; report success/failure to the user.

## Best practices

- **Never hardcode** `COMPOSIO_API_KEY` or any `ak_`/`c_live_` key — always
  `os.environ`.
- Always use `backend.composio.dev/api/v3` — the old `api.composio.dev` host
  and `/v1/`, `/v2/` paths return 410 Gone.
- Confirm destructive actions (delete, send) with the user before executing.
- If a tool slug is wrong, search `GET /tools?toolkits=<service>` rather than
  guessing.