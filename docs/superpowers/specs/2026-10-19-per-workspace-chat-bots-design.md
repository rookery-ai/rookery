# Per-workspace chat bots — design

*2026-10-19 · approved by Ilija in conversation*

## The bug that started this

Ilija connected the **Personal** workspace to a new Telegram bot, sent `/start`,
and nothing happened. His Telegram account (one user id) had been linked to the
**Demo** workspace through Demo's bot on 2026-10-13.

Two things combined:

- `GatewayManager.dispatch` resolved the sender with
  `GetPlatformIdentity(platform, platformUserID)` — globally, ignoring which bot
  the message arrived on — and **overwrote** the adapter's own workspace with the
  identity's. A message to the Personal bot was routed into Demo.
- `Router.handleStart` then found that global identity and answered
  "You're already linked!" — sent through the gateway keyed by the *rewritten*
  workspace, i.e. **out of Demo's bot**, not the bot he had messaged.

The schema made it permanent: `platform_identities` carries
`UNIQUE(platform, platform_user_id)`, so one chat account could belong to exactly
one workspace.

## Intent

**A workspace is bound to its BOT, not to the person's account.** The bot that
received a message decides which workspace it belongs to. One Telegram account
can talk to three bots for three workspaces; two different accounts can each run
their own bots.

Applies to Telegram, Discord and Slack identically — they share `dispatch`.

## Goals

1. Inbound routing uses the adapter's workspace (`msg.WorkspaceID`, stamped by
   every adapter from its `ownerWorkspaceID`). Dispatch only checks that the
   sender is the chat linked to *that* workspace; it never rewrites the workspace.
2. `/start` links the chat to the receiving bot's workspace, regardless of links
   the same account holds elsewhere. A workspace still links **one chat per
   platform**; a second chat is refused.
3. **Telegram groups are a supported link target.** Identity was always the chat
   id (`telegram.go` uses `chat.ID`), so a group linked by `/start` sent inside it
   lets every member use the workspace. Three things make that actually work:
   - `/cmd@ThisBot` is understood. telebot already drops a command addressed to a
     *different* bot; the adapter strips its own `@username` from the text before
     dispatch, so two workspace bots sharing a group never both answer.
   - An **unlinked group is silent** except for `/start`. Otherwise, with privacy
     mode off, every message in a stray group draws "This is a private bot".
   - When a basic group upgrades to a supergroup Telegram changes its chat id;
     the adapter's `OnMigration` handler re-keys the link so the group keeps
     working.
4. The Telegram setup steps tell the owner how to let the bot see ordinary
   (non-command) messages in a group: disable privacy mode in BotFather, or make
   the bot a group admin.

## Non-goals

- Several individually-linked accounts per bot (per-person access control). Ilija
  chose groups instead; everyone in a linked group has full access to the
  workspace, including `/secret`, `/approve` and agent runs.
- Discord server channels / Slack channels. Both adapters stay DM-only.
- Any change to the connect screen beyond the extra setup step.
- One bot shared by two workspaces. `ensureBotUnused` keeps refusing it — two
  workspaces holding the same token would each open a session and answer every
  message twice. Only its comment changes, because it justified itself with the
  global unique constraint this change removes.

## Anti-goals

- **A stranger must never be able to replace a linked chat.** The new insert must
  not be an upsert keyed on `(workspace_id, platform)` — that would let a racing
  `/start` from another chat overwrite the link. It is a plain insert that fails
  on conflict, and `handleStart` reports the refusal.
- Outbound delivery must not change: it already resolves per workspace
  (`ListPlatformIdentities(workspaceID, …)` → `Send(platform, workspaceID, …)`).

## Data model

Migration `021_per_workspace_identities` rebuilds `platform_identities` without
`UNIQUE(platform, platform_user_id)`, keeping `UNIQUE(workspace_id, platform)`
and the `workspaces` foreign key. Existing rows carry over unchanged. Statements
run one by one on a pooled connection, so there is no `BEGIN`/`COMMIT`; the order
is create-new → copy → drop-old → rename. Nothing references this table.

The down migration restores the global constraint, keeping the earliest-linked
row per `(platform, platform_user_id)`.

**The migration is one-way in practice:** pre-021 code upserts with
`ON CONFLICT(platform, platform_user_id)`, which SQLite rejects against the new
schema, so running an older binary on a migrated database breaks `/start`.
Rollback = restore the pre-deploy database snapshot.

## API changes

- `db.GetPlatformIdentity(workspaceID, platform, platformUserID)`.
- `db.UpsertPlatformIdentity` → `db.LinkPlatformIdentity`, a plain insert that
  returns `db.ErrAlreadyLinked` when the workspace already has a link on that
  platform. Renamed because it no longer upserts.
- `db.RekeyPlatformIdentity(platform, oldID, newID)` for the supergroup move.
- `gateway.Message` gains `Group bool` (set by the adapter; dispatch stays
  platform-agnostic) and `MigrateFromID string` (a control message carrying the
  old chat id; `PlatformUserID` holds the new one).

## Known limitations

- Linking is per chat: a workspace is either a DM or one group, not both. Moving
  from DM to a group means unlinking in the UI first.
- With privacy mode on, the bot sees only commands in a group, so plain-text chat
  needs the BotFather step.
