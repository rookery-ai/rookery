# Multi-platform Chat Adapters — Phase 2: Discord Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a working Discord DM bot adapter (full command parity with Telegram) and generalize the connectors UI to render every registered platform from its `CredSpec` — so Discord (and future adapters) appear and connect through one data-driven page.

**Architecture:** Discord is a `DiscordGateway` in `package gateway` (mirroring `telegram.go`), built on `github.com/bwmarrin/discordgo`'s self-reconnecting gateway WebSocket. It plugs into the Phase-1 seams: registers an `AdapterFactory` + a `CredSpec` via `init()`, receives the injected `dispatch` callback, and renders outbound text through `render.For("discord")` (which falls back to CommonMark passthrough — Discord renders `**bold**`/`_italic_`/`` `code` `` natively). Identity is the Discord **user ID**; the DM channel is resolved on demand via `UserChannelCreate` (idempotent), so headless `SendToUser` (reminders/agent output) works with no inbound message. First, message IDs are generalized `int → string` across the optional gateway interfaces (Telegram's `int` was leaking; Discord/Slack/Matrix all use string IDs).

**Tech Stack:** Go 1.26, `github.com/bwmarrin/discordgo` v0.29.0 (already in the module cache), `github.com/yuin/goldmark` (Phase 1), Echo v4, SQLite.

## Global Constraints

- Module path is `github.com/ilijad1/simple-agents`. Import paths verbatim.
- Discord is **DM-only**, one linked identity per workspace (matches Telegram + the `UNIQUE(workspace, platform)` model). Ignore guild/channel messages and the bot's own/other bots' messages.
- `PlatformUserID` for Discord = the Discord **user ID** (stable, 1:1, resolvable to a DM channel headlessly). It is BOTH the identity key and the send target; the adapter resolves the DM channel via `session.UserChannelCreate(userID)`.
- **`DeletableGateway` is mandatory** for Discord (master-password redaction + secret auto-delete rely on it). Discord supports `ChannelMessageDelete`.
- Message IDs are **opaque strings** across all adapters after Task 1 (`""` = none).
- Outbound text is rendered via `render.For("discord")` — no `"discord"` renderer is registered, so it uses CommonMark passthrough (Discord-native). Do NOT register a Telegram-style renderer.
- No secret/token is ever logged.
- Discord validation hits `GET {discordAPIBase}/users/@me` with header `Authorization: Bot <token>`; `discordAPIBase` is a package var (default `https://discord.com/api/v10`) so tests can point at an httptest server.
- Live Discord WebSocket round-trips are **operator-verified** (needs a real bot token + "Message Content Intent" enabled). Unit tests cover config/validation/message-mapping/UI; the live send/receive loop is a documented operator TODO.
- Build: `go build -o bin/simple-agents ./cmd/simple-agents`. Tests: `go test ./... -count=1 -timeout 120s`.

---

## File structure

| File | Responsibility | Task |
|---|---|---|
| `internal/gateway/gateway.go` | msgID `int→string` in `Message`, `TypingGateway`, `DeletableGateway`, `dispatch()` | 1 |
| `internal/gateway/telegram.go` | Telegram impl adapts to string msgIDs via `strconv` | 1 |
| `internal/gateway/credspec.go` | add `Label`/`Blurb` display fields to `CredSpec` | 2 |
| `internal/gateway/discord.go` (new) | `DiscordGateway` adapter + `validateDiscordToken` + CredSpec + factory | 2, 3 |
| `internal/gateway/discord_test.go` (new) | validate + message-mapping unit tests | 2, 3 |
| `web/handlers_connectors.go` | generic `handleTestConnector`; pass `CredSpecs` to the page | 4 |
| `web/templates/dashboard/connectors.html` | render a card per registered `CredSpec` | 4 |
| `CLAUDE.md` | document the multi-platform architecture + Discord | 5 |

---

### Task 1: Generalize message IDs from `int` to `string`

Discord/Slack/Matrix message IDs are string snowflakes/timestamps/event-ids; Telegram's `int` leaked into the shared interfaces. Make message IDs opaque strings everywhere.

**Files:**
- Modify: `internal/gateway/gateway.go` (`Message.MessageID`, `TypingGateway`, `DeletableGateway`, `dispatch()`)
- Modify: `internal/gateway/telegram.go` (`strconv` adapt in `SendMessageGetID`/`EditMessage`/`DeleteMessage`/`handle`)
- Test: `internal/gateway/msgid_test.go` (interface-satisfaction compile guard)

**Interfaces:**
- Produces (changed signatures):
  - `Message.MessageID string`
  - `TypingGateway { SendTyping(platformUserID string) error; SendMessageGetID(platformUserID, text string) (string, error); EditMessage(platformUserID, msgID, text string) error }`
  - `DeletableGateway { DeleteMessage(platformUserID, msgID string) error }`

- [ ] **Step 1: Write the failing test** (locks the new string-based interface)

`internal/gateway/msgid_test.go`:
```go
package gateway

import "testing"

// fakeTyping asserts the TypingGateway/DeletableGateway interfaces are string-based.
type fakeTyping struct{ lastID string }

func (f *fakeTyping) SendTyping(string) error                    { return nil }
func (f *fakeTyping) SendMessageGetID(_, _ string) (string, error) { return "msg-123", nil }
func (f *fakeTyping) EditMessage(_, msgID, _ string) error       { f.lastID = msgID; return nil }
func (f *fakeTyping) DeleteMessage(_, msgID string) error        { f.lastID = msgID; return nil }

func TestMessageIDsAreStrings(t *testing.T) {
	var tg TypingGateway = &fakeTyping{}
	id, _ := tg.SendMessageGetID("u", "hi")
	if id != "msg-123" {
		t.Fatalf("want string id, got %q", id)
	}
	var dg DeletableGateway = &fakeTyping{}
	if err := dg.DeleteMessage("u", "snowflake-999"); err != nil {
		t.Fatal(err)
	}
	var m Message
	m.MessageID = "abc" // must compile as string
	_ = m
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestMessageIDsAreStrings`
Expected: FAIL to COMPILE — interfaces still use `int`.

- [ ] **Step 3: Change the interfaces + `Message` + `dispatch()` in `gateway.go`**

- `Message.MessageID int` → `MessageID string`.
- `TypingGateway`: `SendMessageGetID(...) (int, error)` → `(string, error)`; `EditMessage(platformUserID string, msgID int, text string)` → `EditMessage(platformUserID, msgID, text string)`.
- `DeletableGateway`: `DeleteMessage(platformUserID string, msgID int)` → `DeleteMessage(platformUserID, msgID string)`.
- In `dispatch()`: change `var placeholderID int` → `var placeholderID string`; `var sentMsgID int` → `string`; replace every `placeholderID == 0` → `placeholderID == ""`, `placeholderID != 0` → `placeholderID != ""`, `placeholderID = 0` → `placeholderID = ""`, and `sentMsgID == 0` → `sentMsgID == ""`. The incoming-redact call `dg.DeleteMessage(msg.PlatformUserID, msg.MessageID)` now passes a string (guard it with `if msg.MessageID != ""` where the old code guarded `!= 0`).

- [ ] **Step 4: Adapt Telegram in `telegram.go`** (Telegram's telebot IDs are `int`)

- `SendMessageGetID`: return `strconv.Itoa(sent.ID)` instead of `sent.ID`.
- `EditMessage(platformUserID, msgID, text string)`: `id, err := strconv.Atoi(msgID); if err != nil { return fmt.Errorf(...) }`; use `&telebot.Message{ID: id, ...}`.
- `DeleteMessage(platformUserID, msgID string)`: same `strconv.Atoi`.
- `handle()`: set `MessageID: strconv.Itoa(tc.Message().ID)`.
- Ensure `strconv` is imported (it already is).

- [ ] **Step 5: Verify build + tests**

Run: `go build ./... && go test ./internal/gateway/ -count=1 -v`
Expected: builds; `TestMessageIDsAreStrings` + all existing gateway tests PASS. Also `go test ./... -count=1` (confirm no other package read `Message.MessageID` as int — grep `MessageID` if the build complains).

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/telegram.go internal/gateway/msgid_test.go
git commit -m "refactor(gateway): opaque string message IDs (was Telegram-int-specific)"
```

---

### Task 2: Discord credential spec + token validation

**Files:**
- Modify: `internal/gateway/credspec.go` (add `Label`, `Blurb` to `CredSpec`)
- Modify: `internal/gateway/telegram.go` (set `Label`/`Blurb` on the telegram spec)
- Create: `internal/gateway/discord.go` (`validateDiscordToken` + `discordAPIBase` + Discord `CredSpec` in `init()`)
- Create: `internal/gateway/discord_test.go`

**Interfaces:**
- Produces:
  - `CredSpec` gains `Label string` (display name) and `Blurb string` (one-line description) — used by the UI card.
  - `var discordAPIBase = "https://discord.com/api/v10"`
  - `func validateDiscordToken(token string) (username string, err error)`
  - Registered `CredSpec{Platform: "discord", ...}` with `Validate: func(v) (string,error){ return validateDiscordToken(v["token"]) }`.

- [ ] **Step 1: Add display fields to `CredSpec` (credspec.go)**

Add two fields to the `CredSpec` struct (after `Platform`):
```go
	Label      string // human display name, e.g. "Discord"
	Blurb      string // one-line description for the connector card
```
Update the telegram spec in `telegram.go` to set `Label: "Telegram"`, `Blurb: "Chat with your agents via a personal Telegram bot"`.

- [ ] **Step 2: Write the failing test** (`discord_test.go`)

```go
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateDiscordToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"401: Unauthorized"}`))
			return
		}
		w.Write([]byte(`{"id":"1","username":"MyBot"}`))
	}))
	defer srv.Close()
	old := discordAPIBase
	discordAPIBase = srv.URL
	defer func() { discordAPIBase = old }()

	name, err := validateDiscordToken("good-token")
	if err != nil || name != "MyBot" {
		t.Fatalf("good token: name=%q err=%v", name, err)
	}
	if _, err := validateDiscordToken("bad-token"); err == nil {
		t.Fatal("bad token must error")
	}
}

func TestDiscordSpecRegistered(t *testing.T) {
	spec, ok := CredSpecFor("discord")
	if !ok {
		t.Fatal("discord spec not registered")
	}
	if spec.Label != "Discord" || len(spec.Fields) != 1 || spec.Fields[0].Key != "token" {
		t.Fatalf("unexpected discord spec: %+v", spec)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run 'TestValidateDiscordToken|TestDiscordSpecRegistered'`
Expected: FAIL — symbols undefined.

- [ ] **Step 4: Implement `validateDiscordToken` + spec registration (discord.go)**

```go
package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// discordAPIBase is the Discord REST base; overridable in tests.
var discordAPIBase = "https://discord.com/api/v10"

// validateDiscordToken confirms a bot token by fetching the bot user, returning its username.
func validateDiscordToken(token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, discordAPIBase+"/users/@me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("discord api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord rejected token (status %d)", resp.StatusCode)
	}
	var out struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Username == "" {
		return "", fmt.Errorf("invalid response from discord")
	}
	return out.Username, nil
}

func init() {
	RegisterCredSpec(CredSpec{
		Platform: "discord",
		Label:    "Discord",
		Blurb:    "Chat with your agents via a personal Discord bot (DMs)",
		Fields:   []CredField{{Key: "token", Label: "Bot Token", Placeholder: "your bot token", Secret: false}},
		SetupURL: "https://discord.com/developers/applications",
		SetupSteps: []string{
			"Open the Discord Developer Portal and create a New Application",
			"Open the Bot tab, click Reset Token, and copy the token",
			"On the Bot tab, enable the MESSAGE CONTENT INTENT (Privileged Gateway Intents)",
			"Invite the bot to a server OR just DM it after connecting; send /start to link",
		},
		Validate: func(v map[string]string) (string, error) { return validateDiscordToken(v["token"]) },
	})
}
```

- [ ] **Step 5: Run tests + build**

Run: `go build ./... && go test ./internal/gateway/ -count=1 -v`
Expected: PASS (validate + spec-registered + all prior).

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/credspec.go internal/gateway/telegram.go internal/gateway/discord.go internal/gateway/discord_test.go
git commit -m "feat(gateway): Discord credential spec + bot-token validation"
```

---

### Task 3: Discord adapter (DiscordGateway)

**Files:**
- Modify: `internal/gateway/discord.go` (add the adapter + a pure `mapDiscordDM` helper + `RegisterAdapter`)
- Modify: `internal/gateway/discord_test.go` (message-mapping tests)
- Modify: `go.mod` / `go.sum` (add discordgo)

**Interfaces:**
- Consumes: `DispatchFunc`, `RegisterAdapter`, `render.For` (Phase 1); `Message` (string `MessageID`, Task 1).
- Produces: `DiscordGateway` implementing `Gateway` + `TypingGateway` + `DeletableGateway`; `func mapDiscordDM(authorID, guildID, content, msgID, botUserID string, isBot bool) (Message, bool)`.

- [ ] **Step 1: Add the discordgo dependency**

Run:
```bash
go get github.com/bwmarrin/discordgo@v0.29.0
```
Expected: `go.mod`/`go.sum` updated (module is already in the local cache; no network needed).

- [ ] **Step 2: Write the failing test** (pure mapping logic — the testable core)

Add to `discord_test.go`:
```go
func TestMapDiscordDM(t *testing.T) {
	// A real DM from a human → dispatched.
	msg, ok := mapDiscordDM("user-1", "", "hello", "msg-9", "bot-1", false)
	if !ok {
		t.Fatal("human DM should dispatch")
	}
	if msg.Platform != "discord" || msg.PlatformUserID != "user-1" || msg.Text != "hello" || msg.MessageID != "msg-9" {
		t.Fatalf("bad mapping: %+v", msg)
	}
	// The bot's own message → skipped.
	if _, ok := mapDiscordDM("bot-1", "", "echo", "m", "bot-1", false); ok {
		t.Fatal("bot's own message must be skipped")
	}
	// Another bot → skipped.
	if _, ok := mapDiscordDM("user-2", "", "x", "m", "bot-1", true); ok {
		t.Fatal("other bots must be skipped")
	}
	// A guild (non-DM) message → skipped (GuildID non-empty).
	if _, ok := mapDiscordDM("user-1", "guild-1", "x", "m", "bot-1", false); ok {
		t.Fatal("guild messages must be skipped (DM-only)")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestMapDiscordDM`
Expected: FAIL — `mapDiscordDM` undefined.

- [ ] **Step 4: Implement the adapter (discord.go)**

Add imports `"context"`, `"sync"`, `"github.com/bwmarrin/discordgo"`, and `"github.com/ilijad1/simple-agents/internal/gateway/render"`. Add:

```go
// DiscordGateway is one workspace's Discord bot instance (DM-only).
type DiscordGateway struct {
	session          *discordgo.Session
	ownerWorkspaceID string
	dispatch         DispatchFunc

	mu         sync.Mutex
	dmChannels map[string]string // userID → DM channel ID (cache)
}

// mapDiscordDM converts an inbound Discord message to a Message, returning ok=false
// for the bot's own messages, other bots, and non-DM (guild) messages.
func mapDiscordDM(authorID, guildID, content, msgID, botUserID string, isBot bool) (Message, bool) {
	if isBot || authorID == botUserID || guildID != "" {
		return Message{}, false
	}
	return Message{
		Platform:       "discord",
		PlatformUserID: authorID,
		Text:           content,
		MessageID:      msgID,
	}, true
}

// NewDiscord creates (does not start) a Discord adapter.
func NewDiscord(token, ownerWorkspaceID string, dispatch DispatchFunc) (*DiscordGateway, error) {
	sess, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("discord session init: %w", err)
	}
	sess.Identify.Intents = discordgo.IntentDirectMessages | discordgo.IntentMessageContent
	g := &DiscordGateway{session: sess, ownerWorkspaceID: ownerWorkspaceID, dispatch: dispatch, dmChannels: map[string]string{}}
	sess.AddHandler(g.onMessageCreate)
	return g, nil
}

func (g *DiscordGateway) Platform() string    { return "discord" }
func (g *DiscordGateway) OwnerUserID() string  { return g.ownerWorkspaceID }

func (g *DiscordGateway) Start(ctx context.Context) error {
	if err := g.session.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}
	<-ctx.Done()
	return g.session.Close()
}

func (g *DiscordGateway) Stop() error { return g.session.Close() }

func (g *DiscordGateway) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	msg, ok := mapDiscordDM(m.Author.ID, m.GuildID, m.Content, m.ID, botID, m.Author.Bot)
	if !ok {
		return
	}
	msg.WorkspaceID = g.ownerWorkspaceID
	g.dispatch(context.Background(), msg)
}

// resolveDM returns the DM channel id for a user id, opening (idempotently) + caching it.
func (g *DiscordGateway) resolveDM(userID string) (string, error) {
	g.mu.Lock()
	if ch, ok := g.dmChannels[userID]; ok {
		g.mu.Unlock()
		return ch, nil
	}
	g.mu.Unlock()
	ch, err := g.session.UserChannelCreate(userID)
	if err != nil {
		return "", fmt.Errorf("discord open DM: %w", err)
	}
	g.mu.Lock()
	g.dmChannels[userID] = ch.ID
	g.mu.Unlock()
	return ch.ID, nil
}

// Send delivers a message to a Discord user (via their DM channel).
func (g *DiscordGateway) Send(platformUserID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	_, err = g.session.ChannelMessageSend(ch, render.For("discord").Render(text))
	return err
}

func (g *DiscordGateway) SendTyping(platformUserID string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	return g.session.ChannelTyping(ch)
}

func (g *DiscordGateway) SendMessageGetID(platformUserID, text string) (string, error) {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return "", err
	}
	sent, err := g.session.ChannelMessageSend(ch, render.For("discord").Render(text))
	if err != nil {
		return "", err
	}
	return sent.ID, nil
}

func (g *DiscordGateway) EditMessage(platformUserID, msgID, text string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	_, err = g.session.ChannelMessageEdit(ch, msgID, render.For("discord").Render(text))
	return err
}

func (g *DiscordGateway) DeleteMessage(platformUserID, msgID string) error {
	ch, err := g.resolveDM(platformUserID)
	if err != nil {
		return err
	}
	return g.session.ChannelMessageDelete(ch, msgID)
}
```

Register the adapter factory in the existing `init()` in discord.go (append after `RegisterCredSpec`):
```go
	RegisterAdapter("discord", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		return NewDiscord(token, ws, d)
	})
```
(`config` is unused for Discord — single token.) Add `"fmt"` to imports if not already present.

- [ ] **Step 5: Run tests + build**

Run: `go build ./... && go test ./internal/gateway/ -count=1 -v`
Expected: builds with discordgo; `TestMapDiscordDM` + all prior PASS. If the intent constant name differs in v0.29 (e.g. `IntentsDirectMessages`), fix per the compile error — the two intents needed are DirectMessages + MessageContent.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/gateway/discord.go internal/gateway/discord_test.go
git commit -m "feat(gateway): Discord DM adapter (discordgo gateway, user-id identity, mandatory delete)"
```

---

### Task 4: Generalize the connectors UI + test handler

Render one card per registered `CredSpec` (Telegram + Discord now), driven by data. Make `handleTestConnector` generic via `spec.Validate`.

**Files:**
- Modify: `web/handlers_connectors.go` (`showConnectors` passes `[]gateway.CredSpec`; rewrite `handleTestConnector`)
- Modify: `web/templates/dashboard/connectors.html` (card-per-spec loop)
- Modify: `web/connectors_test.go` (generic test-handler unit test)

**Interfaces:**
- Consumes: `gateway.CredSpecs()`, `gateway.CredSpecFor`, `gateway.CredSpec` (`.Platform/.Label/.Blurb/.Fields/.SetupURL/.SetupSteps`), `gateway.DecryptToken`.
- Produces: `connectorsPageData` gains `Specs []gateway.CredSpec`.

- [ ] **Step 1: Write the failing test** (generic test handler validates via spec)

Add to `web/connectors_test.go`:
```go
func TestTestConnectorUsesSpecValidate(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../migrations")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { d.Close() })
	ws := uuid.New().String()
	if err := d.CreateWorkspace(&db.Workspace{ID: ws, Name: "tester"}); err != nil { t.Fatal(err) }

	// A spec whose Validate echoes a fixed identity, no network.
	gateway.RegisterCredSpec(gateway.CredSpec{Platform: "cs-validate", Label: "CSV", Fields: []gateway.CredField{{Key: "token"}},
		Validate: func(v map[string]string) (string, error) { return "bot-ident", nil }})
	enc, _ := gateway.EncryptToken("tok", make([]byte, 32))
	if err := d.UpsertPlatformConnection(&db.PlatformConnection{ID: uuid.New().String(), WorkspaceID: ws, Platform: "cs-validate", EncryptedToken: enc, Active: true}); err != nil {
		t.Fatal(err)
	}
	s := &Server{db: d, systemKey: make([]byte, 32)}
	id, err := s.testConnectorIdentity(ws, "cs-validate")
	if err != nil || id != "bot-ident" {
		t.Fatalf("testConnectorIdentity = %q, %v", id, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run TestTestConnectorUsesSpecValidate`
Expected: FAIL — `testConnectorIdentity` undefined.

- [ ] **Step 3: Extract a generic `testConnectorIdentity` + rewrite `handleTestConnector`**

In `web/handlers_connectors.go`, add a helper and make the HTTP handler call it:
```go
// testConnectorIdentity decrypts a saved connection's credentials and runs the
// platform's CredSpec.Validate, returning the bot identity (e.g. username).
func (s *Server) testConnectorIdentity(workspaceID, platform string) (string, error) {
	spec, ok := gateway.CredSpecFor(platform)
	if !ok {
		return "", fmt.Errorf("unsupported platform")
	}
	conn, err := s.db.GetPlatformConnection(workspaceID, platform)
	if err != nil {
		return "", fmt.Errorf("connector not found")
	}
	token, err := gateway.DecryptToken(conn.EncryptedToken, s.systemKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt token")
	}
	values := map[string]string{"token": token}
	if conn.EncryptedConfig != "" {
		cfg, err := gateway.DecryptToken(conn.EncryptedConfig, s.systemKey)
		if err == nil {
			var extra map[string]string
			if json.Unmarshal([]byte(cfg), &extra) == nil {
				for k, v := range extra {
					values[k] = v
				}
			}
		}
	}
	if spec.Validate == nil {
		return "", nil // nothing to probe; treat as ok
	}
	return spec.Validate(values)
}
```
Rewrite `handleTestConnector` to use it:
```go
func (s *Server) handleTestConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")
	ident, err := s.testConnectorIdentity(u.ID, platform)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"status": "error", "message": err.Error()})
	}
	out := map[string]string{"status": "ok", "platform": platform}
	if ident != "" {
		out["bot"] = "@" + ident
	}
	return c.JSON(http.StatusOK, out)
}
```
Ensure `"encoding/json"` is imported in the file.

- [ ] **Step 4: Pass specs to the page + rewrite the template**

In `showConnectors`, populate `Specs`:
```go
	return c.Render(http.StatusOK, "dashboard/connectors.html", &connectorsPageData{
		pageData:    s.page(c, "Chat Connectors"),
		Connections: connections,
		Specs:       gateway.CredSpecs(),
	})
```
Add `Specs []gateway.CredSpec` to `connectorsPageData` (keep the existing `Connections`; drop the now-unused `Platforms` field and its populate site if present). Loading `connections`: iterate `supportedPlatformNames()` (from Phase 1) as before.

Rewrite `web/templates/dashboard/connectors.html` to loop over `.Specs` (replace the hardcoded Telegram card + "Discord coming soon" card). Keep the branded icon by switching on platform:
```html
{{template "head" .}}
{{template "navbar" .}}

<div class="container mx-auto px-4 py-8 max-w-3xl">
  {{template "alert" .}}

  <div class="mb-8">
    <h1 class="text-2xl font-bold">📱 Chat Connectors</h1>
    <p class="text-base-content/50 text-sm mt-1">Connect a personal bot to chat with your agents, run them, and query your knowledge base.</p>
  </div>

  {{range .Specs}}
  {{$p := .Platform}}
  {{$conn := false}}
  {{$active := false}}
  {{range $.Connections}}{{if eq .Platform $p}}{{$conn = true}}{{$active = .Active}}{{end}}{{end}}
  <div class="card bg-base-100 shadow-sm border border-base-300 mb-4">
    <div class="card-body p-5">
      <div class="flex items-center gap-3 mb-4">
        <div class="rounded-xl p-3 {{if eq $p "telegram"}}bg-blue-500/20 text-blue-400{{else if eq $p "discord"}}bg-indigo-500/20 text-indigo-400{{else}}bg-base-300 text-base-content{{end}}">
          {{if eq $p "telegram"}}
          <svg xmlns="http://www.w3.org/2000/svg" class="h-6 w-6" fill="currentColor" viewBox="0 0 24 24"><path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z"/></svg>
          {{else if eq $p "discord"}}
          <svg xmlns="http://www.w3.org/2000/svg" class="h-6 w-6" fill="currentColor" viewBox="0 0 24 24"><path d="M20.317 4.37a19.791 19.791 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057.1 18.08.11 18.102.12 18.12a19.88 19.88 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028 14.09 14.09 0 0 0 1.226-1.994.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03z"/></svg>
          {{else}}
          <span class="text-xl">💬</span>
          {{end}}
        </div>
        <div class="flex-1">
          <h2 class="font-semibold text-lg">{{.Label}}</h2>
          <p class="text-sm text-base-content/50">{{.Blurb}}</p>
        </div>
        {{if $conn}}
        <span class="badge {{if $active}}badge-success{{else}}badge-warning{{end}}">{{if $active}}Connected{{else}}Inactive{{end}}</span>
        {{else}}
        <span class="badge badge-ghost">Not connected</span>
        {{end}}
      </div>

      {{if $conn}}
      <div class="bg-success/10 border border-success/30 rounded-lg p-3 mb-4 text-sm">
        <p class="font-semibold text-success">✅ {{.Label}} bot connected</p>
        <p class="text-base-content/60 text-xs mt-1">Send <code>/help</code> to your bot to get started.</p>
      </div>
      <div id="test-result-{{$p}}" class="mb-3"></div>
      <div class="flex gap-2">
        <button onclick="testConnector('{{$p}}', event)" class="btn btn-outline btn-sm">Test Connection</button>
        <form method="POST" action="/dashboard/connectors/{{$p}}/delete"
          onsubmit="return confirm('Disconnect {{.Label}} bot? Your agents will stop responding there.')">
          <button type="submit" class="btn btn-error btn-outline btn-sm">Disconnect</button>
        </form>
      </div>
      {{else}}
      {{if .SetupSteps}}
      <div class="bg-base-200 rounded-lg p-4 text-sm border border-base-300 mb-4">
        <p class="font-semibold mb-2">How to set up:</p>
        <ol class="list-decimal list-inside space-y-1.5 text-base-content/60">
          {{range .SetupSteps}}<li>{{.}}</li>{{end}}
        </ol>
        {{if .SetupURL}}<a href="{{.SetupURL}}" target="_blank" class="link link-primary text-xs font-medium mt-2 inline-block">Open setup page →</a>{{end}}
      </div>
      {{end}}
      <form method="POST" action="/dashboard/connectors" class="flex flex-col gap-2">
        <input type="hidden" name="platform" value="{{$p}}">
        {{range .Fields}}
        <input type="{{if .Secret}}password{{else}}text{{end}}" name="{{.Key}}" placeholder="{{.Placeholder}}"
          class="input input-bordered input-sm flex-1" autocomplete="off" {{if eq .Key "token"}}required{{end}}>
        {{end}}
        <button type="submit" class="btn btn-primary btn-sm self-start">Connect</button>
      </form>
      {{end}}
    </div>
  </div>
  {{end}}
</div>

<script>
async function testConnector(platform, event) {
  const btn = event.target;
  const resultEl = document.getElementById('test-result-' + platform);
  btn.disabled = true; btn.textContent = 'Testing...';
  try {
    const res = await fetch(`/dashboard/connectors/${platform}/test`, {method: 'POST'});
    const data = await res.json();
    if (data.status === 'ok') {
      resultEl.innerHTML = '<div role="alert" class="alert alert-success py-2 text-sm">✅ Connection successful!</div>';
    } else {
      resultEl.innerHTML = `<div role="alert" class="alert alert-error py-2 text-sm">❌ ${data.message || 'Test failed'}</div>`;
    }
  } catch(e) {
    resultEl.innerHTML = `<div role="alert" class="alert alert-error py-2 text-sm">❌ ${e.message}</div>`;
  } finally {
    btn.disabled = false; btn.textContent = 'Test Connection';
    setTimeout(() => { resultEl.innerHTML = ''; }, 5000);
  }
}
</script>

{{template "foot" .}}
```

- [ ] **Step 5: Run tests + build + template smoke**

Run: `go build ./... && go test ./web/ -count=1 -v`
Expected: PASS incl. `TestTestConnectorUsesSpecValidate` and the existing template-smoke test (which renders `connectors.html`). If `connectorsPageData.Platforms` was referenced elsewhere, remove those references.

- [ ] **Step 6: Commit**

```bash
git add web/handlers_connectors.go web/templates/dashboard/connectors.html web/connectors_test.go
git commit -m "feat(web): data-driven connectors UI (card per CredSpec) + generic test handler"
```

---

### Task 5: Document the multi-platform architecture in CLAUDE.md

**Files:**
- Modify: `CLAUDE.md` (the `internal/gateway` row, the inbound-pipeline diagram, and Known gaps)

**Interfaces:** none (docs).

- [ ] **Step 1: Update the gateway description**

In the `internal/gateway` table row, note it now holds a **render subsystem** (`render.Renderer` per platform; neutral CommonMark from the router), an **adapter registry** (`RegisterAdapter`/`AdapterFactory`/`DispatchFunc`), a **declarative `CredSpec`** credential framework (multi-field → `encrypted_config`), and adapters: `TelegramGateway`, `DiscordGateway` (DM-only, opaque string message IDs). Update the inbound-pipeline line "Telegram adapter (per-workspace bot instance)" to "Per-workspace chat adapter (Telegram, Discord)".

- [ ] **Step 2: Update Known gaps**

Remove/replace "Discord adapter — not implemented." with: "Discord adapter — implemented (DM-only); live WS round-trip is operator-verified. Slack/Mattermost/Matrix adapters — not yet implemented (framework ready)." Note the connectors UI is now `CredSpec`-driven.

- [ ] **Step 3: Verify + commit**

Run: `go build ./...` (sanity; docs-only change)
```bash
git add CLAUDE.md
git commit -m "docs: multi-platform gateway architecture + Discord adapter"
```

---

## Self-Review

**Spec coverage (design §3–§7 for the Discord adapter phase):**
- §3/§6 adapter framework (registry + dispatch callback) → consumed by Tasks 2–3 (Discord registers via `init()`). ✅
- §4 Discord renderer → CommonMark passthrough via `render.For("discord")` fallback (no registration needed; Global Constraints + Task 3 Send). ✅
- §5 credentials (single token, `encrypted_config` unused for Discord) → Task 2 CredSpec. ✅
- §6 mandatory delete → `DiscordGateway.DeleteMessage` (Task 3). ✅
- §6 identity/reply-target (user id + `UserChannelCreate`) → Task 3 `resolveDM`. ✅
- §6 reconnection → discordgo self-reconnects (no manual loop needed; noted in Architecture). ✅
- §7 capability matrix (edit/typing/delete) → Task 3 implements all three. ✅
- §8 onboarding UI (per-platform form + setup guidance) → Task 4. ✅
- **Message-ID generalization** (Task 1) is a prerequisite not in the original spec but required because Discord IDs are strings — added as Task 1.
- **Operator-deferred:** live Discord WS send/receive (needs a real bot token + Message Content Intent) — documented, consistent with the repo's no-e2e gap.

**Placeholder scan:** No TBD/TODO. Every code step has complete code. Task 3 notes the one possible intent-constant-name adjustment as an explicit TDD/build-loop item, not a placeholder.

**Type consistency:** `mapDiscordDM` signature identical in Task 3 test + impl. `Message.MessageID`/`SendMessageGetID`/`EditMessage`/`DeleteMessage` string-typed consistently across Tasks 1 & 3. `CredSpec.Label`/`Blurb` added in Task 2, consumed in Task 4. `testConnectorIdentity` signature identical in Task 4 test + impl. `discordAPIBase`/`validateDiscordToken` consistent across Task 2 test + impl.

---

## Execution Handoff

Phase 2 (Discord). After it lands and is operator-verified with a real bot token, Phase 3 (Slack — Socket Mode, two-token credentials via `encrypted_config`, mrkdwn renderer) is the next plan, reusing this same framework + the now-generalized connectors UI.
