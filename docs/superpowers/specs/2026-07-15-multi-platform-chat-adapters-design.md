# Multi-platform chat adapters — Design

**Date:** 2026-07-15
**Status:** Approved (design), pending implementation plan
**Scope:** Add Discord, Slack, Mattermost, and Matrix (full E2EE) as chat platforms alongside Telegram, so every workspace can chat, run agents, and query its knowledge base from any of them with full command parity.

---

## 1. Goal & non-goals

### Goal

Extend the existing per-workspace chat-bot layer beyond Telegram to four additional platforms — **Discord, Slack, Mattermost, Matrix** — with **full command parity**: `/agent`, `/run`, `/chat`, `/secret`, `/remind`, `/memory`, `/start`, `/help`, and plain-text one-off chat all work identically on every platform.

Each platform is a **private 1:1 DM assistant** (matching Telegram today): one linked identity per workspace, self-serve bot credentials pasted by the workspace owner, no public inbound endpoint.

### Non-goals (explicitly deferred)

- **Webhook-based platforms** (WhatsApp, Viber, Teams, Messenger, Google Chat, LINE) — require a public inbound HTTP endpoint + per-platform app review. Separate future project.
- **Rocket.Chat** — dropped from this project. Its realtime receive path (DDP/Meteor-over-WS) has only a stale community Go SDK; its reliable fallback is an outgoing webhook, which belongs to the deferred webhook project.
- **Signal** — no official bot API; requires a `signal-cli`/`signald` daemon bound to a dedicated phone number per workspace. Poor fit.
- **Channel / server / @-mention support** — every adapter is DM-only, matching the current `UNIQUE(workspace, platform)` single-identity model. Multi-user channel support would fork that model (multiple identities, per-message reply targets, permissions) and is a later project.
- **End-to-end / integration test coverage of live WebSocket round-trips** — consistent with the repo's existing "no e2e" known gap; live round-trips are exercised manually.

### Platform decision record

| Platform | Included | Receive model | Notes |
|---|---|---|---|
| Telegram | existing | long-poll | Baseline; unchanged behaviour required |
| Discord | ✅ | WebSocket gateway | Reference adapter |
| Slack | ✅ | Socket Mode (WS) | Two tokens (bot + app) |
| Mattermost | ✅ | WebSocket | Official Go client |
| Matrix | ✅ | `/sync` long-poll | **Full E2EE** — largest adapter |
| Rocket.Chat | ❌ dropped | DDP (WS) | Stale Go SDK; deferrable |
| Viber/WhatsApp/Teams/… | ❌ deferred | webhook | Separate webhook project |
| Signal | ❌ rejected | daemon | Phone-number-per-workspace |

---

## 2. Current architecture (what already works)

The command/routing layer is already platform-agnostic:

- **`gateway.Gateway`** interface (`Platform/OwnerUserID/Start/Stop/Send`) with optional `TypingGateway` (typing + edit) and `DeletableGateway` (delete) mix-ins.
- **`gateway.Router.Handle()`** is fully platform-neutral — every command works through generic `send` / `deleteIncoming` / `sendAutoDelete` / `updatePlaceholder` callbacks.
- **`GatewayManager`** loads all active `platform_connections` at boot, starts one adapter per row, and `dispatch()` handles the typing/placeholder-edit/delete/auto-delete UX generically.
- **Per-workspace model:** each workspace stores its own encrypted bot token in `platform_connections`; `/start` in the chat app creates a `platform_identities` row linking `platform_user_id → workspace_id`.
- Adding a platform is (in principle) implementing one adapter + one `case` in `GatewayManager.start()`.

### Two blockers this design resolves

1. **Telegram MarkdownV2 formatting is baked into the router** — 77 lines of `\.`/`\!`/`\<` escaping, 36 `escapeMarkdown()` calls, and `*bold*` (MarkdownV2 bold, which is *italic* in CommonMark). The router's "neutral" text is actually Telegram-specific. This is pre-existing platform-parity debt.
2. **`platform_connections` holds a single `encrypted_token`** — insufficient for Slack (bot + app token), Matrix (homeserver URL + access token), and Mattermost (server URL + bot token).

---

## 3. Architecture & component boundaries

```
internal/gateway/
├── message.go        LEAF pkg content: Message + Gateway/Renderer/capability interfaces (no SDK imports)
├── gateway.go        GatewayManager: registry-driven start(); dispatch() logic unchanged
├── router.go         Emits NEUTRAL CommonMark; all MarkdownV2 escaping removed
├── render/           Neutral CommonMark → per-platform renderers (goldmark AST)
│   ├── renderer.go   Renderer interface + registry
│   ├── telegram.go   → MarkdownV2 (AST-driven escaping)
│   ├── slack.go      → mrkdwn
│   ├── matrix.go     → HTML
│   └── passthrough.go→ CommonMark (Discord, Mattermost)
├── telegram/         Telegram adapter (moved out of the flat telegram.go)
├── discord/          bwmarrin/discordgo
├── slack/            slack-go/slack (Socket Mode)
├── mattermost/       official mattermost/.../model WS client
└── matrix/           mautrix/go + crypto
```

### Import-cycle resolution

The adapter subpackages must import the shared `Message` + interface types **without** importing `GatewayManager` (which imports them). Resolution:

- Put `Message`, `Gateway`, `Renderer`, `TypingGateway`, `DeletableGateway` in a **leaf** location (either the `gateway` package's `message.go` with no adapter imports, or a dedicated `internal/gateway/adapter` package if the `gateway` package would otherwise create a cycle).
- Each adapter receives an injected **`dispatch func(context.Context, Message)`** callback instead of holding a `*GatewayManager` pointer. Today `telegram.go` calls `g.manager.dispatch` directly; the new adapters get the callback at construction.
- The `switch conn.Platform` in `GatewayManager.start()` becomes a **registry**: `map[string]AdapterFactory` populated by `main` at wiring time (`AdapterFactory = func(creds, dispatch, renderer) (Gateway, error)`). Adding a platform = registering a factory; no edits to `gateway.go`.

### Unchanged

`Router.Handle`, all command handlers, `dispatch()`'s typing/edit/delete/auto-delete plumbing, `platform_identities`, `IdentityResolver`, `SendToUser`.

---

## 4. Formatting subsystem

The single largest piece. Removing Telegram-specific formatting from the router and rendering per platform.

### Neutral dialect: CommonMark

- Re-author the router's ~36 send-sites from Telegram MarkdownV2 to **CommonMark**: `*bold*` → `**bold**`, keep `_italic_`, backtick code spans, and **delete every `\.`/`\!`/`\<`/`\(` escape and every `escapeMarkdown()` call**.
- **Critical correctness note:** MarkdownV2 `*x*` means **bold**; CommonMark `*x*` means *italic*. A naive de-escape would silently turn the router's bold into italic on Discord/Mattermost. The re-authoring must map bold → `**x**` deliberately.

### `render.Renderer` per platform (goldmark AST)

Each renderer parses the neutral CommonMark with **goldmark** and renders from the AST (not regex):

- **Telegram → MarkdownV2.** Escape `.`, `!`, `-`, `(`, `)`, etc. in **text nodes only**, never inside code spans — this text-vs-code distinction is exactly what a regex gets wrong, so it must be AST-driven. Plain-text fallback on send error is retained.
- **Discord / Mattermost → CommonMark passthrough** (rendered natively by the client).
- **Slack → mrkdwn:** `**x**` → `*x*`, `[text](url)` → `<url|text>`, list/code handling per Slack's mrkdwn.
- **Matrix → HTML:** populate `formatted_body` with `org.matrix.custom.html` + a plain-text `body` fallback.

The `Send`/`SendMessageGetID`/`EditMessage` paths in each adapter call their renderer before hitting the SDK.

### Regression guard (required)

Golden-file test: a representative set of router messages must produce **byte-identical** Telegram output before and after the router re-authoring + Telegram renderer. This is the safety net that lets Phase 1 ship with zero user-visible change.

---

## 5. Credentials & schema

### Migration `008_platform_connection_config`

Add a nullable `encrypted_config TEXT` column to `platform_connections` — a JSON credential blob encrypted with the **system key** (same scheme as `encrypted_token`; headless-decryptable so scheduler/reminder delivery works without a master password). `encrypted_token` is retained unchanged so existing Telegram/Discord single-token rows keep working.

```sql
ALTER TABLE platform_connections ADD COLUMN encrypted_config TEXT;
```

### Per-platform credential structs (JSON → `encrypted_config`)

| Platform | Fields |
|---|---|
| Telegram | `{token}` (may remain in `encrypted_token`) |
| Discord | `{token}` |
| Slack | `{bot_token, app_token}` (app_token = Socket Mode) |
| Mattermost | `{server_url, bot_token}` |
| Matrix | `{homeserver_url, access_token}` (crypto store on disk, not DB) |

Each adapter owns:
- **`ParseConfig([]byte) (Creds, error)`** — deserialize its blob.
- **`Validate(Creds) error`** — a connect-time probe (the generic analog of `testTelegramToken`'s `getMe`): Discord `users/@me`, Slack `auth.test`, Mattermost `users/me`, Matrix `whoami`.

---

## 6. Adapter framework & identity model

### Interfaces

`Gateway` unchanged. `TypingGateway` stays optional (Slack has no true typing indicator — it degrades gracefully via the existing interface check; the "⏳ Thinking → edit" placeholder still works through `chat.update`).

**`DeletableGateway` becomes mandatory for every new adapter.** The master-password redaction (`deleteIncoming`) and the 30-second secret auto-delete rely on it; an adapter without delete would silently leave secrets in chat history. All four platforms support message deletion (Matrix via redaction), so this is enforceable — a per-adapter spec checklist item.

### Identity vs. reply-target

`PlatformUserID` is currently both the identity key **and** the send target — which only coincides on Telegram DMs. Rule for every adapter:

- Store the **DM channel/room id** as the opaque `PlatformUserID`.
- Each adapter must guarantee that id is: (1) **stable** per human, (2) **1:1** with that human, (3) **send-capable with no inbound context** — because the headless `SendToUser` path (reminders, scheduled agent output) sends with no incoming message to reply to.
- Platform specifics: Slack uses `conversations.open` to resolve the user's DM channel; Matrix uses a persistent DM room; Discord/Mattermost use the DM channel id. Verified per-adapter, not assumed.

### Reconnection

- telebot (Telegram), discordgo, slack-socketmode self-reconnect.
- **Matrix `/sync` and the Mattermost WS client need explicit retry loops** with backoff — a per-adapter requirement.

### `/start` linking

Unchanged and generic: an unlinked sender may only use `/start`, which creates the `platform_identities` row. All other messages from unlinked senders get the "private bot" rejection.

---

## 7. Capability matrix

| Platform | Library | Receive | Edit | Typing | Delete | Reconnect | Risk |
|---|---|---|---|---|---|---|---|
| Telegram | telebot.v4 | long-poll | ✓ | ✓ | ✓ | built-in | baseline |
| Discord | bwmarrin/discordgo | WS gateway | ✓ | ✓ | ✓ | built-in | low (reference) |
| Slack | slack-go/slack (Socket Mode) | WS | ✓ `chat.update` | none* | ✓ | built-in | low |
| Mattermost | official `.../model` | WS | ✓ | ✓ | ✓ | **explicit** | low |
| Matrix | mautrix/go (+crypto) | `/sync` | ✓ | ✓ | ✓ redaction | **explicit** | **high (E2EE)** |

*Slack: no true typing indicator over the API — degrade gracefully; the placeholder-edit UX still works.

---

## 8. Onboarding UI

- **`supportedPlatforms`** = `{telegram, discord, slack, mattermost, matrix}`.
- **Connectors page (`/dashboard/connectors`)** renders a **per-platform credential form** driven by each adapter's declared field set (single-token vs. multi-field) with per-platform setup guidance (where to create the bot, which scopes/tokens to copy) — mirroring the existing service-connector page's `setup_steps` pattern. Submit → adapter `Validate()` probe → store encrypted in `encrypted_config` → `GatewayManager.Reload(workspace, platform)`.
- **Setup wizard connector step** (`handleSetupConnector`) generalizes the same way (currently Telegram-only).
- Post-connect instruction stays generic: "Send `/start` to your bot to link your account."

---

## 9. Testing

- **Formatting:** table-driven renderer tests per platform (bold/italic/code/links/mixed, escaping-inside-code-span) + the **Telegram golden-file regression** proving unchanged output pre/post router re-authoring.
- **Router:** existing tests stay green after de-escaping — assertions on `\.`-escaped strings move to the Telegram *renderer's* golden test; router tests assert neutral CommonMark.
- **Adapters:** `ParseConfig`/`Validate` unit tests; inbound-event → `Message` mapping tests with faked SDK payloads; identity/reply-target mapping tests. Live WS round-trips exercised manually.
- **Framework:** registry wiring + import-cycle (compilation is the proof).

---

## 10. Implementation phases

Each phase is independently shippable.

1. **Foundation (zero user-visible change).** Leaf interface package + adapter registry + `dispatch` callback decoupling; move Telegram into `telegram/`; `render/` subsystem (goldmark) + Telegram renderer + router re-authoring to CommonMark + golden regression test; migration `008` + `encrypted_config` + per-platform credential structs; generalized connector UI + setup wizard. Telegram must behave identically.
2. **Discord** — reference adapter; proves the framework end-to-end (WS gateway, DM channel identity, passthrough renderer, mandatory delete).
3. **Slack** — Socket Mode; `conversations.open` for DM channel; mrkdwn renderer; two-token credentials.
4. **Mattermost** — official WS client + explicit reconnect loop; passthrough renderer.
5. **Matrix** — `/sync` + explicit reconnect + **full E2EE** (mautrix crypto store on disk per workspace, device keys, session handling); HTML renderer. Built last and isolated so its complexity cannot destabilize the others.

**Risk ordering rationale:** Phase 1 is a regression-guarded pure refactor that de-risks everything after it; Discord validates the abstraction on the easiest case; Matrix (the one genuinely hard adapter) comes last, after the framework is proven.

---

## 11. New dependencies

- `github.com/yuin/goldmark` — CommonMark AST parsing for renderers
- `github.com/bwmarrin/discordgo`
- `github.com/slack-go/slack`
- `github.com/mattermost/mattermost/server/public/model` (official client)
- `maunium.net/go/mautrix` (+ its crypto packages)
