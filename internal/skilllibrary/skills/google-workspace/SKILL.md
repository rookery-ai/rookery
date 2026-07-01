---
name: google-workspace
description: Use this skill whenever the user wants to act on Google Workspace — send/search Gmail, create/list Google Calendar events, search/download Google Drive files, read a Sheet. Triggers include "send an email", "search my inbox", "create a calendar event", "find a file in my drive", "what's on my calendar".
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

# Google Workspace

Act on Gmail, Google Calendar, and Google Drive. On this platform the
**preferred path is the Composio v3 REST API** (the user connects Google once;
no OAuth-certificate management). This is an LLM+REST skill.

## Requirements

- `COMPOSIO_API_KEY` env var — read from `os.environ`, **never hardcode**.
- A connected Google account via Composio (the user connects it in the
  dashboard).
- `requests` (Python) — `python3 -m pip install --user requests`.

## Composio v3 (preferred)

- Base: `https://backend.composio.dev/api/v3` · Auth: header `x-api-key`
- List connected accounts: `GET /connected_accounts?limit=100`
- Execute a tool: `POST /tools/execute/{TOOL_SLUG}` with
  `{"connected_account_id": "<id>", "input": { ... }}`

```python
import requests, os, json, sys
API = "https://backend.composio.dev/api/v3"
H = {"x-api-key": os.environ["COMPOSIO_API_KEY"], "Content-Type": "application/json"}

accts = requests.get(f"{API}/connected_accounts?limit=100", headers=H).json()
g_acct = next((a for a in accts.get("items", [])
               if (a.get("toolkit_name") or "").lower().startswith("gmail")), None)
if not g_acct:
    sys.exit("No connected Google account via Composio. Ask the user to connect one.")

# Send mail
r = requests.post(f"{API}/tools/execute/GMAIL_SEND_EMAIL", headers=H,
                  json={"connected_account_id": g_acct["id"],
                        "input": {"recipient_email":"user@example.com",
                                  "subject":"Hi","body":"from simple-agents"}})
print(json.dumps(r.json()))

# List upcoming calendar events uses a GOOGLE_CALENDAR_* tool slug —
# look it up: GET /tools?toolkits=google_calendar
```

## How to act (LLM-driven)

1. Identify the Workspace app (Gmail / Calendar / Drive) and the action.
2. Find the matching connected account by `toolkit_name`; if none, ask the user
   to connect Google via Composio — do not improvise credentials.
3. Look up the exact tool slug (`GET /tools?toolkits=<app>`) if unsure.
4. Execute; inspect the response; report a concise summary + relevant links/IDs.

## Best practices

- **Never hardcode** `COMPOSIO_API_KEY` or any OAuth refresh token — always
  `os.environ`.
- Always use `backend.composio.dev/api/v3` — `api.composio.dev` and `/v1/`,
  `/v2/` are gone (410).
- Confirm send/delete actions with the user before executing.
- For Drive, prefer search (`GOOGLE_DRIVE_SEARCH_FILES`) over listing all files.