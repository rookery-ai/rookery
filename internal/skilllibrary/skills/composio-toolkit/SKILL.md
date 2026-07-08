---
name: composio-toolkit
description: Use this skill whenever the user wants to act on a connected external service (Gmail, Google Calendar/Drive, GitHub, Slack, Notion, Linear, etc.) through Composio — send an email, create a calendar event, open an issue, post a message, list files. Triggers include "send an email via gmail", "create a calendar event", "open a github issue", "post to slack", "search my drive".
version: 2.0.0
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

Drive 250+ external services through the **Composio v3 REST API**. Never use the
deprecated `composio-core` SDK or the v1/v2 endpoints.

## The helper is already provided — do not write your own

`tools/composio_helper.py` (agents) or `scripts/composio_helper.py` (skills) already
exists in your working directory — the platform seeds it deterministically, verified
against the live Composio v3 API. Do not recreate it or hand-roll `requests.post` calls to
`backend.composio.dev` yourself:

```python
from composio_helper import get_connection, composio_execute, list_tools, ComposioError
```

- `get_connection(toolkit_slug)` → `(connected_account_id, user_id)` for the first ACTIVE
  connection, or raises `ComposioError` with an actionable message if none exists.
- `list_tools(toolkit_slug, query=None, limit=50)` → discovers the real, currently-valid
  tool slugs for ANY connected service (Composio full-text-search over name/description).
  **Always use this instead of guessing or recalling a slug from memory** — Composio has
  250+ services, slugs are not fixed or memorable, and picking the wrong one silently does
  the wrong thing (e.g. sending instead of drafting).
- `composio_execute(tool_slug, connected_account_id, user_id, arguments)` → executes a
  tool action; raises `ComposioError` on failure. During a build-time generation/
  verification pass, this refuses (raises `BuildTimeSendBlocked`) to run an action that
  looks like it delivers/removes something for real (SEND/PUBLISH/DELETE-shaped slugs) —
  that's a deliberate safety backstop, not a bug.

Also available: `python3 tools/composio_discover.py --toolkit <slug> --query "<action>"` —
the same discovery as `list_tools()`, runnable directly as a script (costs one call).

## Discover + execute

```python
from composio_helper import get_connection, composio_execute, list_tools, ComposioError
import sys

toolkit = "gmail"
try:
    # 1. Find the right action — never hardcode a slug you recall from training data.
    candidates = list_tools(toolkit, query="send an email")
    tool_slug = candidates[0]["slug"]  # read name/description; pick the one that matches

    # 2. Get the user's connected account for this toolkit.
    conn_id, user_id = get_connection(toolkit)

    # 3. Execute.
    result = composio_execute(tool_slug, conn_id, user_id, {
        "recipient_email": "user@example.com",
        "subject": "Hello",
        "body": "Sent via Composio",
    })
except ComposioError as e:
    sys.exit(str(e))
```

## How to act (LLM-driven)

1. Identify the target service (toolkit slug, e.g. `gmail`, `notion`, `slack`) and the
   action the user wants, in plain language.
2. Call `list_tools(toolkit_slug, query="<the action, in plain words>")` — read the
   name/description of each candidate. Pay close attention to similarly-named actions that
   do different things (a "create draft"-shaped action vs. a "send"-shaped action are
   usually separate slugs) — pick the one that actually matches what the user asked for.
3. `get_connection(toolkit_slug)` — if it raises `ComposioError`, tell the user to connect
   the account (the error message already says how); do not improvise credentials.
4. `composio_execute(...)` the chosen slug; inspect the result; report success/failure.

## Best practices

- **Never hardcode** `COMPOSIO_API_KEY` or any `ak_`/`c_live_` key — always `os.environ`
  (already handled inside `composio_helper.py`).
- Always use `backend.composio.dev/api/v3` — the old `api.composio.dev` host and
  `/v1/`, `/v2/` paths return 410 Gone. `composio_helper.py` already does this correctly.
- Never substitute a `print()` of what you'd do for actually calling
  `composio_execute()` — a script that only prints a payload and exits 0 does not perform
  the action.
- Confirm destructive/delivery actions (delete, send) with the user before executing for
  real; the build-time safety guard in `composio_execute()` blocks these during generation
  regardless, as a backstop.
