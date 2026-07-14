# Connector Catalog B4: form-encoding providers (Stripe, Twilio, Trello)

**Date:** 2026-07-14
**Status:** Design approved; ready for implementation plan
**Package:** `internal/connectors` (+ `web/handlers_services.go`)

## Context

Final batch of the Catalog effort on the merged Foundation engine. Foundation, B1, B2, B3 are on
`main` (25 providers). **B4** adds the three "engine-work" providers: Stripe, Twilio, Trello.

**Sub-project D (AWS SigV4, PostgreSQL) is dropped** — out of scope indefinitely per the owner;
not planned.

Analysis showed the auth for all three is already covered by existing/B3 primitives; the only new
mechanism is **form-encoded request bodies** (`application/x-www-form-urlencoded`), needed by Stripe
and Twilio. Trello uses query-param auth + params and needs nothing new.

## Decisions (from brainstorming)

- **Form-encoding shape:** a flat `form:` map (key → `{{arg}}`), keys may use literal bracket
  notation for nested params (`"metadata[order_id]"`); array-valued args expand to repeated keys.
- **Auth (reuses existing primitives):** Stripe Basic (secret key as username); Twilio Basic
  (`account_sid` username via B3 `basic_user_template`, `auth_token` password); Trello api-key +
  user token as two query params (query placement + `connect_inputs`), avoiding OAuth 1.0a.
- **Action depth:** ~8-10 per provider. **Verification:** unit/rendering only, live deferred.

## Engine primitive: form-encoded bodies

### `RequestTemplate.Form map[string]string` (additive)
```yaml
request:
  method: POST
  url: "https://api.stripe.com/v1/customers"
  form:
    email: "{{email}}"
    name: "{{name}}"
    "metadata[source]": "{{source}}"
```

### `renderForm` (new, `render.go`)
For each `form` entry: substitute the value from args (`subst`, no conn vars needed — conn values
belong in the URL). Skip entries whose value resolves empty. If an arg is an array (`[]any`), add
one `key=<v>` per element (repeated-key form). Build `url.Values`, `Encode()`, return body +
`Content-Type: application/x-www-form-urlencoded`. A new `case len(a.Request.Form) > 0` in
`renderRequest` (placed after `body_arg`/`BodyBuilder`, before `Body`/`BodyJSON`).

No change to auth: Stripe/Twilio use `placement: basic` (existing + B3 templated username); the form
body is orthogonal to auth.

## Providers, auth & actions (~8-10 each; Composio-verified in the plan)

| Provider | Auth | Base URL | Representative actions |
|---|---|---|---|
| **Stripe** | api-key, `placement: basic` (secret key = Basic username, empty password) | `https://api.stripe.com/v1` | `create_customer`, `list_customers`, `get_customer`, `create_payment_intent`, `list_payment_intents`, `create_refund`, `list_charges`, `get_charge`, `get_balance` |
| **Twilio** | api-key, `placement: basic`, `basic_user_template: "{{conn.account_sid}}"` (auth_token = password); `connect_inputs: [account_sid]` | `https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}` | `send_sms`, `list_messages`, `get_message`, `make_call`, `list_calls`, `list_incoming_phone_numbers`, `get_account` |
| **Trello** | api-key, `placement: query` (`param_name: token`); `connect_inputs: [trello_key]` (each action's `query` carries `key: "{{conn.trello_key}}"`) | `https://api.trello.com/1` | `list_boards`, `get_board`, `list_lists`, `list_cards`, `create_card`, `update_card`, `create_list`, `add_comment`, `get_member` |

**Per-provider notes:**
- **Stripe** writes are `form:` bodies (create_customer/payment_intent/refund); reads are GET with
  query params; `get_balance` GET no params. Basic auth username = the secret key (`sk_...`); the
  existing `placement: basic` path (`SetBasicAuth(credential, "")`) already does this.
- **Twilio** `send_sms` = POST `.../Messages.json` form `{To, From, Body}`; reads GET `.json`. The
  account SID appears both in the Basic username (`basic_user_template`) and the base URL
  (`{{conn.account_sid}}`), both fed by `connect_inputs[account_sid]`; the pasted credential is the
  Auth Token (Basic password). Response extract per Twilio's envelope (e.g. `$.messages`).
- **Trello** uses query params for everything (reads GET, writes POST with query params — **no
  body/form**). `applyAuth` query placement adds `token=<credential>`; each action's `query` adds
  `key: "{{conn.trello_key}}"` + the action params. `create_card`/`update_card`/`create_list`/
  `add_comment` are `mutating: true`.

Mutating actions (create/update/send/make_call/refund) set `mutating: true`.

## UI

Add Stripe, Twilio, Trello to `availableServiceProviders`. All three are api-key providers → the
existing paste-key card (with `connect_inputs` rendering from B3) handles Twilio's `account_sid` and
Trello's `trello_key` fields. No new UI code.

## Testing (unit/rendering only; live deferred)

- **`renderForm`:** flat key/value; a literal bracket key (`metadata[source]`); an array-valued arg
  → repeated keys; empty-value omission; `Content-Type: application/x-www-form-urlencoded`.
- **Auth:** Twilio Basic username templating (`{{conn.account_sid}}` + auth_token password — reuses
  B3's `applyAuth` templated-Basic path); Stripe Basic (key as username); Trello `token` (from
  applyAuth) + `key` (from `{{conn.trello_key}}`) both present in the rendered query.
- **Providers:** LoadBundled for all 3; representative renders (Stripe create_customer form, Twilio
  send_sms form + templated base URL, Trello create_card query with key+token); registry counts.

## Structure (~4 tasks)

1. `Form` field + `renderForm` primitive + **Stripe** provider/actions.
2. **Twilio** provider/actions (reuses B3 basic_user_template + connect_inputs + the form primitive).
3. **Trello** provider/actions (query-param auth + params; no new mechanism).
4. UI: add the 3 to `availableServiceProviders` + all-load test.

## Explicitly deferred / out of scope

- Live E2E for all 3; Stripe dynamic line-item arrays (`line_items[0][price]` with variable length)
  beyond the curated actions; Twilio media/MMS + subresources; Trello attachments/webhooks.
- **Sub-project D (AWS, PostgreSQL): dropped** — not planned.
- With B4, the Catalog effort (the owner's 30-integration goal, minus AWS/PostgreSQL) is complete.
