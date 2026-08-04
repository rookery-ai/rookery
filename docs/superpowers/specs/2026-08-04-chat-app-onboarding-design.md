# Chat app onboarding: make "connected" mean "usable"

**Date:** 2026-08-04
**Status:** Approved, ready for implementation planning

## Problem

A user connected Discord, saw the wizard report success, and could not use it.

The wizard's final step calls `validateDiscordToken` (`internal/gateway/discord.go:25`),
which issues `GET /users/@me` with the bot token. That proves the token authenticates
and nothing else. On success the UI renders a green **"Connected as \<botname\> ✓"** and a
**Done** button.

At that moment the integration is unusable. Three things remain, and the UI names none of
them:

1. **The bot must be invited to a server.** Discord only permits a DM between parties
   sharing a guild. This is a platform rule with no configuration override.
2. **The invite has no instructions.** The setup step reads "Invite the bot to a server",
   with no mention of OAuth2 → URL Generator, the `bot` scope, or permissions.
3. **The server's Privacy Settings → Direct Messages must permit DMs**, or the bot's
   message never arrives.

The Discord setup steps additionally contain an instruction that cannot work
(`discord.go:60`):

> "Invite the bot to a server **OR just DM it after connecting**; send /start to link"

The `OR` branch is impossible. A user who follows it reaches a dead end with no error.

### The user-install escape hatch does not apply

Discord's user-install context lets a user add an app to their account with no server.
It is unavailable here: DM interactions for user-installed apps are delivered **only** via
an Interactions Endpoint URL — an inbound webhook. Rookery's adapters are outbound-only by
design (bot dials out, zero inbound port), a deliberate property for self-hosted installs
behind NAT. The mutual-server path is therefore the only one available, and the
instructions must describe it accurately.

## The gap is not Discord-specific

`web/api_connectors.go` never references `PlatformIdentity`. **No endpoint reports whether
a human is linked.** The UI cannot show it because nothing exposes it.

Consequently, on every platform:

- "Connected" means "the token authenticates."
- The identity displayed is the *bot's* username, not the operator's.
- The real linking step — `/start` in a DM — happens entirely off-screen, with no
  feedback and no way to confirm it worked.

Per-platform state of the linking instruction today:

| Platform | Mentions `/start` | Accuracy |
|---|---|---|
| Telegram | No | Silent on linking entirely |
| Discord | Yes | Preceded by an impossible `OR` branch |
| Slack | Yes | Correct, but buried in a dense 6-step list |

## Two further defects found

**Unprompted delivery is nondeterministic.** `SendToUser` (`internal/gateway/gateway.go:223`)
iterates linked identities first-success-wins, and `ListPlatformIdentities`
(`internal/db/repositories.go:364`) has **no `ORDER BY`**. With Telegram and Discord both
linked, the recipient of a scheduled agent run or reminder is undefined and
unconfigurable.

Note the scope: inbound replies already return to the platform the message arrived on
(`dispatch` routes via `msg.Platform`). `SendToUser` governs **unprompted** delivery only.

**`handleStart` hardcodes the platform name** (`internal/gateway/router.go:231`): linking
via Discord or Slack replies *"Your **Telegram** account is now linked."*

## Design

### 1. `SetupSteps` become credentials-only

Setup steps currently mix "obtain a token" with "now link yourself", inconsistently and in
Discord's case incorrectly. Split the concerns: **`SetupSteps` explain only how to obtain
credentials.** Linking moves to wizard step 4, identical across platforms.

Discord's rewritten steps:

```
1  Discord Developer Portal → New Application
2  Bot tab → Reset Token → copy it
3  Bot tab → enable MESSAGE CONTENT INTENT
```

The invite is removed from the prose because it can be **generated**. `GET /users/@me`
returns the bot's `id`, which for a bot is the application ID; the code already calls this
endpoint and currently parses only `username`. Capturing `id` lets step 4 render a
ready-made invite button:

```
https://discord.com/api/oauth2/authorize?client_id=<id>&scope=bot&permissions=3072
```

(`3072` = View Channels + Send Messages.) This replaces the single hardest instruction with
one click.

Slack's dense step 3 (four scopes, install, copy token) splits into discrete steps.
Telegram's three steps are already correct and stay as they are.

### 2. Step 4 — "Link your account"

A fourth wizard step, uniform across platforms, that resolves only when a
`platform_identities` row exists for this operator. It supplies:

- a deep link to open the DM — `https://t.me/<bot_username>`,
  `https://discord.com/users/<bot_user_id>`, and for Slack
  `slack://user?team=<team_id>&id=<bot_user_id>` with an `https://app.slack.com/client/...`
  fallback for browser-only users
- the literal text to send (`/start`)
- for Discord: the generated invite button, plus the Privacy Settings → Direct Messages
  note stated as a precondition

#### Persisting the bot identity

The deep links need the bot's username / user id, and today those are unavailable.
`CredSpec.Validate` returns an identity string, `saveConnector` passes it to its caller, and
it is then **discarded** — `db.PlatformConnection` has no field for it
(`internal/db/models.go:42`).

Resolve this **without a migration** by writing the bot's identifiers into the existing
`encrypted_config` JSON blob. That column already carries per-platform extras (Slack's
app-level token), and `testConnectorIdentity` already decrypts and rebuilds a values map
from it, so read-back follows an established path. Decryption is local AES — no network
call, so the polling constraint above still holds.

This requires two small changes:

- `CredSpec.Validate` returns structured identifiers (username, user id, and Slack's team
  id) rather than a bare display string. Discord's validator must additionally parse `id`
  from `GET /users/@me`; it currently reads only `username`. That `id` is the application
  id, which is what the generated invite URL needs.
- `SplitCreds` accepts these derived values alongside the user-entered fields when building
  the config JSON.

Persisting rather than re-probing matters because the Manage panel must offer linking to a
user who closed the wizard and returned later. Without persistence that path would need a
fresh third-party probe on every open.

```
Step 4 — Link your account

  Open a DM with @rookery-bot  [Open in Discord ↗]
  Send:  /start

  ◌ Waiting for you to send /start…
  ───────────────────────────────────
  ✓ Linked as ilija#4821
  [ Done ]
```

**Backend:** extend the existing `GET /api/v1/connectors` DTO, per platform entry, with:

| Field | Meaning |
|---|---|
| `linked` | bool — a `platform_identities` row exists for this workspace + platform |
| `linked_identity` | string — the **operator's** platform user id, empty when unlinked (distinct from the existing `identity`, which is the *bot's* username) |
| `primary` | bool — this platform receives unprompted delivery |

No new route — the SPA already loads this list, and the wizard polls it via a react-query
`refetchInterval` while step 4 is open.

**Constraint:** that endpoint must remain DB-only. It must not probe the platform for a
token check, or polling becomes a third-party network call every few seconds. Any
token-probing work stays in the existing explicit *Test connection* action.

Green then means *you can use this*, not *the token parses*.

**Step 4 is escapable.** An "I'll do this later" action closes the wizard and leaves the
card reading "Not linked yet". Blocking outright would strand a user who wants to save a
token now and link later; the honest status signal is preserved either way.

### 3. Primary app for unprompted delivery

Store `chat.primary_platform` as a row in the generic `workspace_settings` table. **No
schema migration.**

`SendToUser` resolves the primary first, then falls back through the remaining identities.
`ListPlatformIdentities` gains `ORDER BY linked_at, platform` regardless, so the fallback
is deterministic rather than dependent on SQLite rowid order. An unset primary means "first
linked" — defined, not arbitrary.

The connections page renders a radio per linked app, with a line stating where agent runs
and reminders are delivered. All linked apps remain fully usable for chatting; the setting
governs unprompted delivery only.

### 4. Two fixes in the linking path

- `handleStart` takes its label from `CredSpecFor(msg.Platform).Label` rather than the
  hardcoded `"Telegram"` string.
- The *"This bot is already linked to another account. Contact your administrator"*
  rejection is a dead end in a single-owner product, where no administrator exists. Add an
  **Unlink** action to the Manage panel so a wrong link is self-serviceable.

## Testing

- **Unit** — `SendToUser` primary selection and fallback ordering; deterministic
  `ListPlatformIdentities` order; `handleStart` label per platform; link-status DTO shape;
  bot identifiers surviving a `SplitCreds` → encrypt → decrypt → read-back round trip, and
  Discord's validator parsing `id` as well as `username`.
- **Component** — step 4's waiting → linked transition; the "I'll do this later" escape
  leaving the card in "Not linked yet".
- **Guard** — assert Discord's steps name the MESSAGE CONTENT INTENT, and that no
  platform's `SetupSteps` instruct the user to DM the bot. The false `OR` branch is exactly
  the kind of prose that regressed unnoticed; a test is the only thing that would have
  caught it.

## Out of scope

- **A new chat platform.** Ranked by steps the *user* must perform: Telegram 2, Matrix 3,
  Zulip 4, Discord 6, Mattermost 6, Slack 8. Nothing beats Telegram. Matrix is the most
  promising candidate — no mutual-server rule, token auth — but Element creates DMs
  **encrypted by default**, and a plaintext-only adapter silently receives nothing in them.
  Shipping that would reproduce the exact silent dead-end this work removes; doing it
  properly requires E2EE via `-tags goolm` to keep the build CGo-free. Signal is excluded
  outright: `signal-cli` is a JVM dependency incompatible with single-binary distribution.
  The adapter registry accepts a new platform via `init()` registration alone, so deferring
  costs nothing.
- **Slack Socket Mode reconnect supervision.** A real gap already recorded in CLAUDE.md
  (inbound stops after a fatal reconnect failure until the connector is re-saved), but
  unrelated to onboarding.
