# Chat app onboarding parity: one linking flow, and a visible reason when it stalls

**Date:** 2026-08-05
**Status:** Draft, awaiting review

Direct successor to `2026-08-04-chat-app-onboarding-design.md`. That work rebuilt the
**connections page** wizard so "connected" could no longer mean "unusable". It never
touched the **setup wizard**, which still runs the old flow. This spec closes that gap and
adds the diagnostics that would have made the incident below self-service.

## The reported incident, and what was actually wrong

The operator connected Discord, added the bot to a server, confirmed the server's Privacy
Settings permitted direct messages, sent `/start`, and nothing happened. The UI waited
indefinitely. Separately, the app crashed, and it was unclear whether the crash was cause
or coincidence.

**It was the cause.** Discord was configured correctly the entire time. Verified against
the live Discord API with the stored token:

| Check | Result |
|---|---|
| Bot token | Valid — bot `rookery`, application `1533811180223729734` |
| MESSAGE CONTENT INTENT | **Enabled** — app flag `1<<19` (`GATEWAY_MESSAGE_CONTENT_LIMITED`) set |
| Gateway `IDENTIFY` with `DirectMessages\|MessageContent` | **Accepted** — a disabled intent closes with code `4014` |
| Mutual guild | Present — bot is in guild `Rookery` |

The server process was dead. It started at 10:11:42 after a restart; `platform_identities`
records the Discord link at `08:12:49` UTC — **67 seconds later**. A `/start` sent in a DM
linked immediately once a process existed to receive it.

The crash itself left no panic and no OOM kill; the log ends mid-traffic. The proximate
mechanism is an environment footgun rather than a product defect and is recorded at the end
of this document, not designed against here.

### Why the product could not say any of this

Three separate silences compounded:

1. **The link step polls forever.** `LinkStep` renders `Spinner("Waiting for you to send
   /start…")` with no timeout and no upper bound. Against a dead server the poll fails and
   the spinner is unchanged — identical to the healthy "not yet" state.
2. **Nothing reports whether the bot is running.** `connectorPlatformList` reports whether
   credentials are *saved*, never whether `GatewayManager` currently holds a live adapter.
3. **`StartAll` logs only failures** (`gateway.go:173`). A successfully started adapter
   emits nothing, so `logs/server.log` cannot answer "was Discord connected?" — which is
   most of why diagnosing this took a full investigation rather than one grep.

## The onboarding parity gap

`SetupWizard.tsx` step 5 is **setup steps → credentials → Connect → Done**. The connections
page `ChatAppWizard` is **Setup → Credentials → Test → Link**. Onboarding is missing the
last two entirely: no live token test, no invite-to-server button, no open-DM button, no
`/start` polling, no linked confirmation.

The result on Discord is worse than a missing step. The Done screen reads
`telegram_bot_username` (`api_settings.go:445`), and `saveConnector` writes that key **only
when `platform == "telegram"`** (`handlers_connectors.go:66`). So a Discord user finishes
onboarding at a bare "🎉 You're set up" — with no bot name, no `/start` instruction, and no
mention of Discord at all. The connection is left saved-but-unlinked, exactly as reported.

### The mechanical reason it was never fixed in place

Two constraints make "just call the same hooks" impossible, and both must be designed
around rather than discovered during implementation:

- **All six connector routes sit on the setup-gated group.** `api_connectors.go:17-22`
  registers them on `dash`, which carries `requireSetupCompleteAPI`. During onboarding
  `NeedsSetup` is true, so `GET /api/v1/connectors` returns 403. `api_connectors.go:85`
  already documents this — it is why step 5 inlines `connectorPlatformList` in its payload.
- **`setupStep` flips 5 → 7 the instant a connection row exists** (`handlers_setup.go:28`).
  So the moment credentials save, `GET /api/v1/setup` stops returning `platforms`. Even the
  inlined data disappears precisely when the link step would need it.

## Design

### 1. Two setup-scoped routes

```
GET  /api/v1/setup/platforms                 → { platforms: connectorPlatformList(w) }
POST /api/v1/setup/platforms/:platform/test  → shared core with apiTestConnector
```

`requireSetupCompleteAPI` exempts by **prefix** (`strings.HasPrefix(c.Path(),
"/api/v1/setup")`, `api.go:77`), so these are reachable during onboarding with **no guard
change**. `c.Path()` is the registered route pattern, so the match is on the pattern, not
user input.

Deliberately **not** exempting `/api/v1/connectors` instead: that group also carries `POST
/connectors`, `DELETE /connectors/:platform` and `DELETE /connectors/:platform/identity`.
Exempting it hands a half-configured workspace delete-and-resave powers it has no reason to
hold, to save two thin handlers.

### 2. Extract the shared steps

`TestStep` and `LinkStep` move out of `ChatAppWizard` into shared components. Neither may
call `useConnectors` directly; each takes an **injected source**:

```ts
type PlatformSource = {
  usePlatform: (platform: string, opts: { poll: boolean }) => ConnectorPlatform | undefined;
  useTest: () => TestMutation;
};
```

- **Connections page** injects the `useConnectors` / `useTestConnector` source.
- **Setup wizard** injects a source backed by the two routes above.

This is the property the whole design turns on: one implementation, two transports. The
current bug is precisely what two implementations of the same flow produce.

Step 5 becomes client-side phases — *choose → setup → credentials → test → link* — then
Done. The load-bearing change: on a successful `POST /api/v1/setup {step:5}` the wizard
**stashes** `next_step` and advances to the test phase instead of navigating to Done. Done
is reached from the link step's **Done** or its escape hatch.

The step chips gain no new server steps. Test and link are phases *within* step 5, so
`setupStep`'s 5 → 7 transition is untouched and a resumed wizard still lands correctly.

### 3. Platform-aware Done screen

Step 7's payload drops `telegram_bot_username` for the platform-keyed
`bot_identity.<platform>` that `connectorPlatformList` already reads, plus the platform
label and the linked flag. Copy branches on the real state:

- **Linked** — "Linked as \<operator\> ✓", no instruction to repeat.
- **Connected, unlinked** — the platform's own name, its bot name, and the invite/DM links.
  Never the word Telegram on a Discord install.
- **Skipped** — unchanged.

`telegram_bot_username` remains written for Telegram only, as the settings page still reads
it; this spec removes the *setup wizard's* dependency on it, not the key.

### 4. Make a stalled link legible

The escape hatch and the "no green state while unlinked" invariant from the prior spec are
preserved exactly. Added on top:

- **`bot_online` on the platform DTO** — true iff `GatewayManager` holds a running adapter
  for that workspace+platform. Requires a `GatewayManager.IsRunning(workspaceID, platform)`
  reading the existing `m.gateways` map under `m.mu`. When false the link step states the
  bot is not running rather than implying the user has not acted yet. This is the single
  change that would have resolved the reported incident without investigation.
- **Escalation on elapsed time** — after **45 seconds** unlinked, expand a "Not working?"
  panel: send it as a **direct message**, not a server channel; confirm the mutual server;
  confirm Privacy Settings permit DMs.
- **Bounded polling** — stop after **5 minutes** and offer explicit **Retry** rather than
  polling for the life of the panel. The 2-second interval is unchanged.
- **One `slog.Info` per started adapter** in `GatewayManager.start`, carrying platform and
  workspace id. `StartAll`'s failure-only logging is why the log could not answer whether
  Discord was connected.

`bot_online` is advisory, not a gate. A true value proves an adapter object exists, not
that Discord's gateway is currently reachable; the link step must keep treating the
identity row as the only proof of success.

### 5. Register `/start` as a real Discord application command

Typing `/` in Discord opens the slash-command picker. The bot registers no commands, so the
product's central instruction fights the client's own UI, and `/start` typed in a guild
channel is discarded by `mapDiscordDM` (`guildID != ""`) with **no reply whatsoever** —
while a wrong DM at least answers *"This is a private bot. Send /start to link your
account."* Absolute silence for the most likely first action is the defect.

Register a global `start` application command and handle `INTERACTION_CREATE`:

- Appears in the picker, so `/start` becomes discoverable instead of adversarial.
- Works in **both** DM and guild.
- Arrives as an interaction, needing **neither** `IntentMessageContent` **nor**
  `IntentGuildMessages` — the adapter's DM-only intent set is unchanged. This is why the
  slash command is preferred over replying to guild messages: that alternative would have
  the bot receive every message in every server it joins, a real privacy widening for a
  self-hosted product.
- Replies **ephemerally**, so linking never posts the operator's business into a channel.

Two required supporting changes:

- **The invite URL must request `applications.commands`.** `LinkURLs` currently emits
  `&scope=bot&permissions=0` (`discord.go:74`). A guild authorized with `bot` alone will not
  surface the app's commands. New scope: `bot applications.commands`. `permissions=0` stays
  — guild permissions do not govern DMs, and the consent screen should keep asking for
  nothing.
- **Existing installs must re-invite.** The `Rookery` guild was authorized without
  `applications.commands`, so the command will not appear there until the updated invite is
  used. The link step already renders an invite button; it needs copy acknowledging that an
  already-added bot may need re-adding once.

The message-based `/start` path is **kept**, not replaced. It is the only path on Telegram
and Slack, it already works, and a global command's propagation is not instantaneous. The
interaction handler and the message handler converge on the same `handleStart`.

Resolving the interacting user differs by context — `i.User` in a DM, `i.Member.User` in a
guild — and the handler must read both; a DM-only read returns nil in a guild and would
reintroduce the silence this section removes.

## Testing

- **Unit (Go)** — the two setup routes reachable while `NeedsSetup` is true and returning
  the same DTO as `apiListConnectors`; `IsRunning` true after `start` and false after
  `stop`; the interaction handler resolving the user id from both DM and guild shapes;
  `handleStart` reached identically from a message and an interaction.
- **Guard (Go)** — Discord's `InviteURL` contains `applications.commands`. This regressed
  silently once already in the form of the impossible `OR` branch the prior spec removed;
  prose and URL parameters are exactly what nothing catches.
- **Component (vitest)** — the shared `LinkStep` mounted against a stub source renders the
  unlinked → linked transition, the `bot_online: false` message, and the escalation panel
  after the elapsed threshold; the setup wizard does not navigate to Done on a successful
  step-5 POST; the Done screen renders no Telegram string for a Discord platform.
- **Regression** — the prior spec's invariant still holds: no Done button and no green
  state while unlinked, in **both** hosts of the shared component.

## Out of scope

- **Accepting bare `start` without the slash.** Considered and declined; the slash command
  addresses the same friction without changing command grammar across three platforms.
- **Replying in-channel to guild messages.** Superseded by the slash command, which reaches
  guilds without widening the adapter's intents.
- **Slack Socket Mode reconnect supervision.** Still a real gap (CLAUDE.md), still
  unrelated to onboarding — though note it shares this incident's shape: inbound stops and
  nothing says so. `bot_online` does not detect it, because the adapter object survives a
  fatal reconnect.
- **The crash itself.** `make stop` runs `pkill -f '[b]in/rookery serve'`, which matches
  **globally** — `make deploy` or `make stop` from any git worktree kills the server running
  from the main checkout. A worktree built a binary inside the window the server died in.
  This is an environment footgun worth fixing in the Makefile, but it is not part of this
  feature and should not ride along with it.
