---
name: api-integration
description: Use this skill when calling a REST API that is not one of the connected services — authenticating with a stored secret, paginating, handling rate limits, and failing cleanly. Triggers include "call the API", "fetch from their endpoint", "use my API key for", "integrate with", "pull data from their service".
version: 1.0.0
license: MIT-0
category: Integrations
---

# API Integration

Before writing any HTTP code, check whether this is already covered by a
**connected service**. This platform has 28 built-in connectors — Gmail,
Google Drive/Sheets/Docs, GitHub, Slack, Notion, Outlook, Jira, Stripe,
Twilio, and more — exposed as native typed tools when bound to the agent.

## Check for a connector FIRST

If the service is Gmail, Drive, Sheets, Docs, GitHub, Slack, Notion, Outlook,
Teams, Jira, HubSpot, Dropbox, Zoom, Calendly, Asana, ClickUp, Airtable,
Intercom, SendGrid, Monday, Salesforce, Shopify, Mailchimp, Zendesk, Stripe,
Twilio, or Trello — **use the connector tool, not raw HTTP.** These come with
auth already handled (OAuth or API key, refreshed automatically), typed
parameters, and normalized errors. Do not hunt for an API key, do not install
an SDK, do not write request code by hand for these services — the tool is
already there. If the agent doesn't have the connection bound yet, say so in
plain language rather than working around it with a manual key.

Only fall back to raw HTTP calls (this skill) when the service genuinely
ISN'T one of those 28.

## Authenticating with a stored secret

Secrets live as environment variables — never ask the user to paste a key
into chat, and never print or log a secret value once you have it.

```python
import os
api_key = os.environ["WEATHER_API_KEY"]
# use it in the request; never print(api_key), never write it to a log file
headers = {"Authorization": f"Bearer {api_key}"}
```

If the variable is missing, say so in plain language ("this needs an API key
stored as WEATHER_API_KEY") — don't guess a value or hardcode one.

## Pagination

Never assume the first page is the whole result. Follow whatever pagination
the API defines — a `next` link, a page token, or an offset/limit pair — until
it signals no more pages:

```python
import requests

items = []
url = "https://api.example.com/v1/items"
params = {"limit": 100}
while url:
    resp = requests.get(url, headers=headers, params=params, timeout=30)
    resp.raise_for_status()
    data = resp.json()
    items.extend(data["items"])
    url = data.get("next_page_url")  # None when done
    params = None  # next_page_url usually carries its own query string
```

## Rate limits: honour Retry-After, back off on 429

A `429 Too Many Requests` is not a failure to report — it's a signal to slow
down and retry. Honour a `Retry-After` header if present; otherwise back off
exponentially with a cap:

```python
import time

def get_with_backoff(url, headers, max_retries=5):
    delay = 1
    for attempt in range(max_retries):
        resp = requests.get(url, headers=headers, timeout=30)
        if resp.status_code == 429:
            wait = int(resp.headers.get("Retry-After", delay))
            time.sleep(min(wait, 60))
            delay *= 2
            continue
        resp.raise_for_status()
        return resp
    raise RuntimeError(f"gave up after {max_retries} retries on {url}")
```

## 4xx vs 5xx: not the same failure

- **4xx (except 429)** — the request itself is wrong: bad auth, bad params,
  not found. Retrying the identical request will fail identically. Fix the
  request or report the problem; don't loop.
- **429** — rate limited. Retry with backoff (above).
- **5xx** — the server's problem, usually transient. A bounded retry with
  backoff is reasonable; if it keeps failing, stop and report it rather than
  retrying forever. See the `resilient-runs` skill for the general
  transient-vs-permanent framework this maps onto.

## Keep the script thin — except for bulk jobs

For a normal call: the script's job is to **fetch and print the raw result**;
reasoning about what it means belongs in the agent, not in string-parsing
logic buried in a script.

```python
resp = get_with_backoff(url, headers)
print(resp.text)  # let the agent read and interpret this
```

**Exception — bulk jobs:** when the job is to process many items (paginate
through thousands of records, transform them, write a file), do the WHOLE job
in the script and print a summary, not the raw dump. Reasoning per-item in the
agent context for a few thousand items wastes the agent's budget on
transport, not judgment.

```python
# bulk job: script does the full loop, prints only a summary
print(f"processed {len(items)} items, {skipped} skipped, wrote out.csv")
```

## Never in the agent's message

Don't put the raw API response, a stack trace, or the endpoint URL in a
user-facing notification — translate it (see the `notification-writing`
skill). "Couldn't reach the weather service — it returned a server error" is
fine; the exception text is not.
