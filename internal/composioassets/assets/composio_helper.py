"""Composio v3 REST API helper — platform-provided, do not edit.

This file is written deterministically by the simple-agents host before your
generation/run starts. Do NOT rewrite, duplicate, or hand-roll requests to
backend.composio.dev yourself — import from this file instead:

    from composio_helper import get_connection, composio_execute, list_tools, ComposioError

All failures raise ComposioError (or one of its subclasses below) with an
already-actionable message — catching ComposioError catches everything. The
subclasses exist so you can tell APART a genuine network problem from Composio's
API responding with an error, if you want to react differently (e.g. only retry
on a connection error):
  - ComposioConnectionError — the request never got a response at all (DNS/connect/
    timeout). A REAL "can't reach the network" condition. Rare, and NOT the same
    thing as a bad request or a missing connection.
  - ComposioServerError — Composio responded, but with 429/5xx, repeatedly. The
    network is fine; Composio's own service was failing or throttling.
  - ComposioError (base) — everything else: no active connection for a toolkit
    (get_connection), a bad request / bad arguments (4xx), or a tool that reported
    failure in its own result body.
Do NOT describe a ComposioServerError as "the network is unreachable" — it isn't;
read the actual exception message, it already says what happened.

Any changes you make to THIS file are discarded and re-seeded on the next
generation/edit, so there is no point editing it — put your own logic in your
own scripts and import these functions.

Verified against the live Composio v3 API docs (docs.composio.dev):
  - POST /tools/execute/{tool_slug} body: {"connected_account_id", "user_id", "arguments"}
  - GET  /tools?toolkit_slug=...&query=...&limit=...   (toolkit_slug is singular; "query" is
    Composio's full-text search over tool name/description — use it to FIND the right slug
    for ANY connected service instead of guessing or recalling one from memory)
  - GET  /connected_accounts?limit=100  → items[].toolkit.slug (nested), items[].status
"""
import json
import os
import time
import requests

COMPOSIO_BASE = "https://backend.composio.dev/api/v3"

# Set by the host only during a build-time generation/verification pass (never during a
# real scheduled/manual run). When present, composio_execute() refuses to call an action
# that looks like it delivers something to a real person, as a code-level backstop against
# accidentally sending/posting/publishing/deleting for real while the agent is still being
# built and tested.
BUILD_PHASE_ENV_VAR = "SA_BUILD_PHASE"
BUILD_PHASE_GENERATION = "generation"

# Conservative, deliberately non-exhaustive: these substrings appearing in a tool slug mean
# "this action reaches or removes something outside the sandbox" (sends a message/email,
# publishes a post, deletes/removes a record, invites someone, etc). This is defense-in-depth,
# not the primary safeguard — the primary safeguard is picking the right action via
# list_tools() in the first place. A script can override with allow_at_build_time=True for a
# confirmed false positive (e.g. a service whose "SEND" action is actually a safe internal
# operation).
_BUILD_TIME_BLOCKED_SUBSTRINGS = (
    "SEND", "PUBLISH", "POST", "DELIVER", "NOTIFY",
    "DELETE", "REMOVE", "COMMENT", "REPLY", "INVITE",
)


class ComposioError(RuntimeError):
    """Raised for Composio API/connection failures. The message is written to be shown
    directly to the user via [CHAT] or [TEST_OUTPUT] — it already explains what to do."""


class ComposioConnectionError(ComposioError):
    """Raised ONLY when the request never got a response at all — DNS failure, connection
    refused, or a timeout with no reply (a genuine "can't reach the network" condition).
    Distinct from ComposioError so a script (or the model reading the message) can tell a
    real connectivity problem apart from Composio returning a real HTTP error — those are
    NOT the same thing and need different reactions (retry the whole run later for this;
    report the specific error for that)."""


class ComposioServerError(ComposioError):
    """Raised when Composio's API responded but kept returning 5xx/429 until the retry
    budget ran out. The connection worked fine — Composio's own service was failing or
    throttling. Distinct from ComposioConnectionError: do NOT describe this as
    "unreachable" — the request reached Composio and got an error response back."""


class BuildTimeSendBlocked(ComposioError):
    """Raised by composio_execute() when SA_BUILD_PHASE=generation and the requested tool
    looks like it delivers to a real person/service. Not a bug — this IS the safety net.
    If you hit this and you're sure the action doesn't reach a real person, pass
    allow_at_build_time=True. Otherwise: this action must not run for real during a build
    smoke test — print what you WOULD send/do instead, and stop there."""


def _headers(api_key):
    return {"x-api-key": api_key, "Content-Type": "application/json"}


def _request(method, path, api_key=None, json_body=None, params=None, max_attempts=3):
    """Shared HTTP call with a short retry-on-429/5xx backoff. Raises a SPECIFIC exception
    type depending on what actually failed — never a bare requests exception, and never a
    generic message that conflates a real network problem with Composio's API being up but
    erroring:
      - ComposioConnectionError: the request never got a response (DNS/connect/timeout —
        genuinely no path to the server).
      - ComposioServerError: Composio responded, but with 429/5xx, repeatedly.
      - ComposioError (4xx): Composio responded with a real error for this specific
        request (bad auth, bad arguments, etc.) — raised immediately, no retry, since
        retrying an identical bad request will not help.
    Kept deliberately short (3 attempts, low timeout) so a persistent failure surfaces
    FAST with a precise cause — a tool-calling loop has a limited turn budget, and a slow,
    vague failure eats into it while telling the model nothing useful. If Composio really
    is just briefly blipping, the model/agent can always call the tool again next turn.
    """
    api_key = api_key or os.environ.get("COMPOSIO_API_KEY")
    if not api_key:
        raise ComposioError(
            "COMPOSIO_API_KEY is not set. Ask the user to connect Composio "
            "(app.composio.dev) and add the key under Secrets, then run this agent again."
        )
    url = f"{COMPOSIO_BASE}{path}"
    last_conn_exc = None
    last_server_err = None
    for attempt in range(max_attempts):
        try:
            r = requests.request(
                method, url, headers=_headers(api_key), json=json_body, params=params, timeout=15
            )
        except requests.RequestException as e:
            last_conn_exc = e
            if attempt < max_attempts - 1:
                time.sleep(min(2 ** attempt, 4))
            continue
        if r.status_code == 429 or r.status_code >= 500:
            last_server_err = f"{r.status_code}: {r.text[:300]}"
            if attempt < max_attempts - 1:
                retry_after = r.headers.get("Retry-After")
                time.sleep(float(retry_after) if retry_after else min(2 ** attempt, 4))
            continue
        if r.status_code >= 400:
            raise ComposioError(f"Composio API {r.status_code} on {method} {path}: {r.text[:500]}")
        return r.json()
    if last_server_err is not None:
        raise ComposioServerError(
            f"Composio's API responded but kept returning errors on {method} {path} after "
            f"{max_attempts} tries: {last_server_err}. This is Composio's service failing or "
            f"throttling, not a network problem — the request DID reach Composio. If this "
            f"keeps happening it may be transient; report the error above rather than guessing "
            f"at an unrelated cause."
        )
    raise ComposioConnectionError(
        f"Could not reach Composio at all on {method} {path} after {max_attempts} tries "
        f"(no response received — DNS failure, connection refused, or timeout): {last_conn_exc!r}. "
        f"This means the network request itself never completed; it is not a Composio API error "
        f"and not a bug in your script's file paths."
    )


def composio_get(path, api_key=None, params=None):
    return _request("GET", path, api_key=api_key, params=params)


def composio_post(path, body, api_key=None):
    return _request("POST", path, api_key=api_key, json_body=body)


def list_tools(toolkit_slug, query=None, limit=50, api_key=None):
    """Discover the real, currently-valid tool slugs for ANY connected Composio service.

    Call this BEFORE picking a tool_slug for composio_execute — never guess or recall a
    slug from training data; slugs vary by service and change over time. `query` is a
    plain-language description of the action you want (e.g. "create a draft", "send a
    message", "list open issues") — Composio full-text-searches tool names/descriptions.

    Returns a list of {"slug": ..., "name": ..., "description": ...} — read the
    description of each candidate and pick the one that actually matches what the user
    asked for (pay close attention to DRAFT/CREATE vs SEND/PUBLISH — they are usually
    different actions with different slugs).
    """
    params = {"toolkit_slug": toolkit_slug, "limit": limit}
    if query:
        params["query"] = query
    data = composio_get("/tools", params=params, api_key=api_key)
    out = []
    for t in data.get("items", []):
        out.append({
            "slug": t.get("slug"),
            "name": t.get("name"),
            "description": t.get("description"),
        })
    return out


def get_connection(toolkit_slug, api_key=None):
    """Returns (connected_account_id, user_id) for the first ACTIVE connection to
    toolkit_slug. Raises ComposioError (with an actionable message) if none is found."""
    data = composio_get("/connected_accounts", params={"limit": 100}, api_key=api_key)
    for acc in data.get("items", []):
        if acc.get("toolkit", {}).get("slug") == toolkit_slug and acc.get("status") == "ACTIVE":
            return acc["id"], acc.get("user_id", "default")
    raise ComposioError(
        f"No active {toolkit_slug} connection found. "
        f"Go to app.composio.dev/connections -> add {toolkit_slug} -> run this agent again."
    )


def composio_execute(tool_slug, connected_account_id, user_id, arguments, api_key=None,
                      allow_at_build_time=False):
    """Executes a Composio tool action. This is the ONLY function that should ever call
    POST /tools/execute/... — never hand-roll that request elsewhere.

    During a build-time generation/verification pass (SA_BUILD_PHASE=generation), this
    refuses to run an action whose slug looks like it delivers/removes something for real
    (see _BUILD_TIME_BLOCKED_SUBSTRINGS) unless allow_at_build_time=True. This is a
    code-level backstop: it does not depend on the model remembering a prompt rule.
    """
    if os.environ.get(BUILD_PHASE_ENV_VAR) == BUILD_PHASE_GENERATION and not allow_at_build_time:
        upper = tool_slug.upper()
        hit = next((s for s in _BUILD_TIME_BLOCKED_SUBSTRINGS if s in upper), None)
        if hit:
            raise BuildTimeSendBlocked(
                f"SUCCESS — the build-time test reached the send step and everything up to it "
                f"worked. This is NOT an error and NOT an authentication problem. {tool_slug} was "
                f"held back only because its name contains '{hit}', which usually means it "
                f"delivers/removes something for real (an email, a message, a post, a delete), and "
                f"build-time tests must never do that on the user's behalf. Treat this as a PASSING "
                f"test: print the exact action/content you WOULD perform, put it in [TEST_OUTPUT], "
                f"and finish the build normally — the action runs for real the next time the agent "
                f"actually runs. Do NOT report this as a failure, retry it, or loop. "
                f"If {tool_slug} does NOT actually reach a real person/record (false positive), "
                f"call composio_execute(..., allow_at_build_time=True)."
            )
    body = {"connected_account_id": connected_account_id, "user_id": user_id, "arguments": arguments}
    result = composio_post(f"/tools/execute/{tool_slug}", body, api_key=api_key)
    if not result.get("successful", True) or result.get("error"):
        raise ComposioError(f"{tool_slug} failed: {result.get('error') or json.dumps(result)[:400]}")
    return result
