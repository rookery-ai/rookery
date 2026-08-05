# Chat-app slash-command menus (Telegram, Discord, Slack)

**Date:** 2026-08-05
**Status:** Design approved, implementation not started

## Summary

Rookery's router handles twelve chat commands, but none of the three chat
platforms advertises them. Telegram shows no command menu, Discord registers
exactly one command (`/start`), and Slack registers none. Users discover
commands only by sending `/help`, which they have to know about first.

This spec adds native command menus to all three platforms from a single
canonical command table, and takes the opportunity to remove the command-list
duplication that already exists.

## Problem

### The command list is already duplicated, and this change would multiply it

Three copies exist today, none derived from another:

- `internal/gateway/router.go:164-189` — the `switch` that actually dispatches:
  `start`, `help`, `agent`, `skill`, `secret`, `remind`, `run`, `chat`,
  `memory`, `pending`, `approve`, `reject`.
- `internal/gateway/router.go:1265` — `helpText`, a hand-maintained 27-line
  string listing commands at subcommand granularity.
- `internal/gateway/discord.go:180` — `startCommand`, the one registered
  Discord application command.

Adding Telegram, Discord and Slack registration naively would make five
independent copies. Any design here has to collapse them first.

### Each platform advertises nothing

- **Telegram** never calls `setMyCommands`, so the client's Menu button is
  empty.
- **Discord** registers only `/start` (`discord.go:268`), using
  `ApplicationCommandCreate`, which cannot remove a command that is later
  dropped.
- **Slack** registers nothing, and `handleEvent` (`slack.go:296-304`) only
  handles `EventTypeEventsAPI`, so a slash command would not be received even
  if one were declared.

## Platform constraints (verified)

| | API | Delivery of a registered command | Namespace |
|---|---|---|---|
| Telegram | `telebot.Bot.SetCommands` → `setMyCommands` | Unchanged — still an ordinary text message | Per-bot |
| Discord | `discordgo.Session.ApplicationCommandBulkOverwrite` (v0.29.0) | **Interaction**, not a message | Per-application |
| Slack | Declared in the app manifest; **not** settable with a bot token | `socketmode.EventTypeSlashCommand` (v0.27.0) | **Workspace-global** |

Three consequences drive the design:

1. **Telegram is purely cosmetic.** Registration changes discoverability and
   nothing else. Names must match `^[a-z0-9_]{1,32}$` — Telegram permits only
   lowercase letters, digits and underscores (`telebot/commands.go:7-9`), which
   is why the Slack prefix below cannot be applied uniformly.
2. **Discord is a routing change.** Once a command is registered the client
   sends an interaction instead of message text, so registering without
   handling would *break* commands that work today.
3. **Slack needs no new credential, but does need a manifest.** Automating
   declaration requires an app-configuration token (rotating, 12-hour), a third
   credential beyond the bot and app-level tokens Rookery already collects. The
   existing setup is a seven-step manual "From scratch" flow
   (`slack.go:47-55`); replacing it with a manifest paste declares the commands
   *and* shortens onboarding, at no credential cost.

Additionally, Slack's slash-command namespace is shared with Slack's own
built-ins and every other installed app. `/help` and `/remind` collide with
built-ins.

## Design

### Component 1 — One canonical command table

A new `internal/gateway/commands.go` holds the single source of truth. Per
command: the canonical `Name` the router dispatches on, a one-line
`Description`, a `UsageHint` (the argument shape, e.g. `create <name>`), an
optional per-platform name override, and the subcommand lines `/help` prints.

`helpText` renders from this table, replacing the literal string at
`router.go:1265`.

**The router's `switch` is deliberately not refactored into a map.** Its
handlers have divergent signatures — some take `sendProgress`, some
`deleteIncoming` — and rewriting dispatch to unify them is churn this change
does not need. Parity is guaranteed behaviourally instead: a test drives
`Router.Handle` with each table entry and asserts the reply is not the
`Unknown command /x — try /help` branch (`router.go:192`), and drives a
deliberately bogus name asserting that it *is*. That catches a command added to
the router but not the table, and a command listed in the table that nothing
dispatches, without touching the dispatch code.

### Component 2 — Telegram

On adapter start, call `SetCommands` with the table's names and descriptions.
This populates the client's Menu button. Commands continue to arrive as
ordinary text messages, so the router is untouched.

Registration is best-effort: a failure is logged and the adapter continues,
matching the stance already documented for Discord at `discord.go:262-266` — a
bot that cannot advertise its commands is degraded, not broken.

A test pins every name against Telegram's `^[a-z0-9_]{1,32}$` rule and
descriptions against its 3-256 character bound.

### Component 3 — Discord

`startCommand` is replaced by a table-derived `[]*discordgo.ApplicationCommand`.
Each command carries one optional string option, `args`, whose description is
the usage hint — the flat shape, not a subcommand tree.

Registration switches to `ApplicationCommandBulkOverwrite(appID, "", cmds)`.
Bulk overwrite is the whole point: `ApplicationCommandCreate` can add a command
but never remove one, so a command dropped from the table would otherwise
linger in every user's client indefinitely. Registration stays global (empty
guild id) because guild-scoped commands are unavailable in DMs, which is where
this bot operates.

`onInteractionCreate` drops the `!= startCommand.Name` filter
(`discord.go:224`) and instead resolves the invoked name against the table.
It acks ephemerally inside Discord's three-second window, then synthesizes
`"/<name> <args>"` (trimmed when `args` is empty) and dispatches through the
existing path. This generalizes exactly the flow `/start` already proves at
`discord.go:236-251`. `/start` keeps its specific "I'll reply in your direct
messages" ack; every other command gets a generic one.

The plain-text command path is retained as a fallback, because global command
registration does not propagate to clients instantly.

Descriptions are capped at Discord's 100-character limit by a test.

### Component 4 — Slack

**Naming.** Commands are unprefixed, except the two that collide with Slack
built-ins: `/help` becomes `/rookery-help` and `/remind` becomes
`/rookery-remind`. These live in the table's Slack override column, so the
Slack handler maps the received name back to the canonical one before
dispatching.

**Manifest.** A generator emits the app manifest YAML from the table plus the
adapter's fixed requirements: display info, the bot scopes `chat:write`,
`im:history`, `im:write` and `files:read`, Socket Mode enabled, the `message.im`
bot event subscription, the App Home messages tab, and a `slash_commands` entry
per command using the Slack name, description and usage hint. The connect
wizard renders it as copyable text and `SetupSteps` collapses from seven steps
to roughly four: create an app *From an app manifest*, paste, install to the
workspace, copy the two tokens.

**Receiving.** `handleEvent` gains an `EventTypeSlashCommand` branch
(`socketmode.EventTypeSlashCommand`, carrying a `slack.SlashCommand`). It acks
the request immediately, maps the Slack name to the canonical name, synthesizes
`"/<name> <text>"`, and dispatches with `PlatformUserID = cmd.UserID`.

Because a slash command can be invoked from a channel while Rookery replies in
DM, the ack is ephemeral and says the reply is coming by direct message —
otherwise an in-channel invocation looks like it did nothing.

## Testing

- **Parity:** every table entry dispatches; a bogus name hits the unknown-command
  branch.
- **Telegram:** names satisfy `^[a-z0-9_]{1,32}$`; descriptions within 3-256
  characters.
- **Discord:** the bulk-overwrite payload contains one entry per table command,
  each description ≤100 characters and carrying a single optional string
  option; interaction→text synthesis is correct with arguments and with none.
- **Slack:** the generated manifest parses as YAML and declares one slash
  command per table entry; the Slack-name override round-trips to the canonical
  name; a slash-command event synthesizes the expected router text.

## Risks

**Unprefixed Slack names are not guaranteed.** Another installed app can claim
`/agent` or `/run` first, and Slack can add a built-in later. The failure is
per-workspace and presents as a command that silently belongs to something
else. The override column reduces the fix to a one-line change — that is
mitigation, not prevention, and is accepted deliberately.

**Existing Slack installs gain nothing automatically.** An app created through
the old manual steps has no slash commands declared, and Rookery cannot add
them with the tokens it holds. The setup panel must state that an existing app
needs its manifest updated, or the feature will look broken for exactly the
users who onboarded earliest.

**Discord registration is a behavioural cutover.** Registering a command
changes how it is delivered. The interaction handler and the registration must
ship together; shipping registration alone would break every command that works
today.

## Out of scope

- Discord subcommand trees with typed options. The flat one-string-option shape
  was chosen for uniformity across the three platforms and a single mapping to
  maintain.
- Automating Slack manifest updates through app-configuration tokens.
- Mattermost and Matrix adapters, which do not exist yet.
