# Multi-platform Chat Adapters — Phase 1: Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple all platform-specific formatting and single-token credential assumptions out of the chat gateway, so the four upcoming adapters (Discord, Slack, Mattermost, Matrix) can be dropped in without touching core routing — while Telegram behaves byte-for-byte identically.

**Architecture:** The router stops emitting Telegram MarkdownV2 and emits neutral **CommonMark**; a new `internal/gateway/render` subsystem renders CommonMark to each platform's markup (Phase 1 ships only the Telegram MarkdownV2 renderer + a passthrough). `GatewayManager.start()`'s hard-coded `switch` becomes a **registry** of adapter factories, and adapters receive an injected `dispatch` callback instead of reaching into the manager. Credentials gain a multi-field `encrypted_config` JSON blob (migration `008`) alongside the existing `encrypted_token`. This phase is a **regression-guarded pure refactor**: no user-visible change.

**Tech Stack:** Go 1.26, `github.com/yuin/goldmark` v1.8.2 (already a dependency), SQLite (`modernc.org/sqlite`), Echo v4, `gopkg.in/telebot.v4`.

## Global Constraints

- Module path is `github.com/ilijad1/simple-agents`. All import paths use it verbatim.
- **Neutral dialect is CommonMark.** Bold = `**x**`, italic = `_x_`, inline code = `` `x` ``, links = `[text](url)`. No Telegram MarkdownV2 escaping (`\.`, `\!`, `\<`) appears in `router.go` after this phase.
- **MarkdownV2 special characters** (must be escaped in Telegram *text* nodes, never inside code spans): `_ * [ ] ( ) ~ ` > # + - = | { } . !`. (Note `<`/`>`: only `>` is special.)
- **Telegram output must remain semantically identical.** The renderer corpus test (Task 3) is the acceptance gate.
- `encrypted_config` and `encrypted_token` are both encrypted with the **system key** (`gateway.EncryptToken`/`DecryptToken`), headless-decryptable (no master password).
- **DeletableGateway is mandatory** for every real adapter (enforced from Phase 2 on; Phase 1 only establishes the interface).
- Run the full suite with `go test ./... -count=1 -timeout 120s`. Build with `go build -o bin/simple-agents ./cmd/simple-agents`.
- Follow existing code style: package-level doc comments, no new external deps beyond those already in `go.mod` for this phase.

---

### Task 1: `render` package — interface, passthrough, registry

**Files:**
- Create: `internal/gateway/render/render.go`
- Test: `internal/gateway/render/render_test.go`

**Interfaces:**
- Produces:
  - `type Renderer interface { Render(commonMark string) string }`
  - `type RendererFunc func(string) string` (adapts a func to `Renderer`; `Render` calls it)
  - `func Passthrough() Renderer` — returns the input unchanged (for Discord/Mattermost in later phases)
  - `var ErrUnknown = errors.New("render: unknown platform")`
  - `func For(platform string) Renderer` — returns the registered renderer, or `Passthrough()` if none registered (safe default)
  - `func Register(platform string, r Renderer)` — registers a renderer; called from `init()` in platform renderer files

- [ ] **Step 1: Write the failing test**

```go
package render

import "testing"

func TestPassthroughReturnsInputUnchanged(t *testing.T) {
	got := Passthrough().Render("**bold** and `code`")
	if got != "**bold** and `code`" {
		t.Fatalf("passthrough altered input: %q", got)
	}
}

func TestForUnknownPlatformFallsBackToPassthrough(t *testing.T) {
	got := For("nonexistent-platform").Render("hello .world!")
	if got != "hello .world!" {
		t.Fatalf("For(unknown) should passthrough, got %q", got)
	}
}

func TestRegisterAndFor(t *testing.T) {
	Register("upper-test", RendererFunc(func(s string) string { return s + "!!" }))
	if got := For("upper-test").Render("x"); got != "x!!" {
		t.Fatalf("registered renderer not used, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/render/ -run TestPassthrough -v`
Expected: FAIL — package/symbols do not exist yet.

- [ ] **Step 3: Write minimal implementation**

```go
// Package render converts neutral CommonMark (emitted by the gateway router)
// into each chat platform's native markup. The router is platform-agnostic;
// each adapter renders on the way out.
package render

import "sync"

// Renderer converts neutral CommonMark into a platform's markup.
type Renderer interface {
	Render(commonMark string) string
}

// RendererFunc adapts an ordinary function to the Renderer interface.
type RendererFunc func(string) string

// Render implements Renderer.
func (f RendererFunc) Render(s string) string { return f(s) }

// passthrough returns its input unchanged (CommonMark-native platforms).
var passthrough = RendererFunc(func(s string) string { return s })

// Passthrough returns a Renderer that emits CommonMark unchanged.
func Passthrough() Renderer { return passthrough }

var (
	mu        sync.RWMutex
	registry  = map[string]Renderer{}
)

// Register associates a renderer with a platform name. Call from init().
func Register(platform string, r Renderer) {
	mu.Lock()
	defer mu.Unlock()
	registry[platform] = r
}

// For returns the renderer registered for platform, or Passthrough if none.
func For(platform string) Renderer {
	mu.RLock()
	defer mu.RUnlock()
	if r, ok := registry[platform]; ok {
		return r
	}
	return passthrough
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/render/ -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/render/render.go internal/gateway/render/render_test.go
git commit -m "feat(gateway): add render package (Renderer interface + registry + passthrough)"
```

---

### Task 2: Telegram MarkdownV2 renderer (goldmark AST)

**Files:**
- Create: `internal/gateway/render/telegram.go`
- Test: `internal/gateway/render/telegram_test.go`

**Interfaces:**
- Consumes: `Register`, `RendererFunc` (Task 1); `github.com/yuin/goldmark` AST.
- Produces: `func RenderTelegram(commonMark string) string` (also registered as `"telegram"` via `init()`).

**Why AST, not regex:** MarkdownV2 requires escaping `.` `!` `-` etc. in text nodes but **not** inside code spans. A regex cannot tell text from code; the goldmark AST can.

- [ ] **Step 1: Write the failing test**

```go
package render

import "testing"

func TestRenderTelegram(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain with dot and bang", "Hello world. Done!", "Hello world\\. Done\\!"},
		{"bold", "**bold**", "*bold*"},
		{"italic", "_italic_", "_italic_"},
		{"inline code not escaped inside", "run `a.b()!` now.", "run `a.b()!` now\\."},
		{"link", "[docs](https://x.io/a_b)", "[docs](https://x.io/a_b)"},
		{"hyphen and paren in text", "a-b (c)", "a\\-b \\(c\\)"},
		{"plus and equals", "1 + 1 = 2", "1 \\+ 1 \\= 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderTelegram(tc.in); got != tc.want {
				t.Fatalf("RenderTelegram(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderTelegramRegistered(t *testing.T) {
	if got := For("telegram").Render("a."); got != "a\\." {
		t.Fatalf("telegram renderer not registered/used, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/render/ -run TestRenderTelegram -v`
Expected: FAIL — `RenderTelegram` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package render

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func init() { Register("telegram", RendererFunc(RenderTelegram)) }

// mdV2Special are the characters MarkdownV2 requires escaping in text nodes.
const mdV2Special = "_*[]()~`>#+-=|{}.!"

// escapeMDV2Text backslash-escapes every MarkdownV2-special rune in plain text.
func escapeMDV2Text(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(mdV2Special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeMDV2Code escapes only backslash and backtick (code-span content rules).
func escapeMDV2Code(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`")
	return r.Replace(s)
}

// RenderTelegram converts neutral CommonMark to Telegram MarkdownV2.
func RenderTelegram(commonMark string) string {
	src := []byte(commonMark)
	root := goldmark.DefaultParser().Parse(text.NewReader(src))
	var b strings.Builder
	renderNodeTelegram(&b, root, src)
	return strings.TrimRight(b.String(), "\n")
}

func renderNodeTelegram(b *strings.Builder, n ast.Node, src []byte) {
	switch node := n.(type) {
	case *ast.Text:
		b.WriteString(escapeMDV2Text(string(node.Segment.Value(src))))
		if node.SoftLineBreak() || node.HardLineBreak() {
			b.WriteByte('\n')
		}
		return
	case *ast.String:
		b.WriteString(escapeMDV2Text(string(node.Value)))
		return
	case *ast.CodeSpan:
		b.WriteByte('`')
		b.WriteString(escapeMDV2Code(string(node.Text(src))))
		b.WriteByte('`')
		return
	case *ast.Emphasis:
		delim := "_" // italic (Level 1)
		if node.Level == 2 {
			delim = "*" // bold
		}
		b.WriteString(delim)
		renderChildrenTelegram(b, node, src)
		b.WriteString(delim)
		return
	case *ast.Link:
		b.WriteByte('[')
		renderChildrenTelegram(b, node, src)
		b.WriteString("](")
		b.WriteString(string(node.Destination)) // URL: MarkdownV2 needs only ) and \ escaped
		b.WriteByte(')')
		return
	case *ast.Paragraph:
		renderChildrenTelegram(b, node, src)
		b.WriteString("\n\n")
		return
	case *ast.Heading:
		// No heading syntax in MarkdownV2: bold the line.
		b.WriteByte('*')
		renderChildrenTelegram(b, node, src)
		b.WriteString("*\n\n")
		return
	}
	renderChildrenTelegram(b, n, src)
}

func renderChildrenTelegram(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderNodeTelegram(b, c, src)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/render/ -v`
Expected: PASS. If a case fails, the goldmark AST node type differs from the code above — inspect with a debug print of `n.Kind()` and adjust the `switch` (this is the TDD loop; the test table is the spec).

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/render/telegram.go internal/gateway/render/telegram_test.go
git commit -m "feat(gateway): Telegram MarkdownV2 renderer via goldmark AST"
```

---

### Task 3: Re-author router to CommonMark + route Telegram sends through the renderer

This is the regression-critical task. The router currently hard-codes MarkdownV2. We convert every send-site to CommonMark and make the Telegram adapter render on the way out. A corpus test proves equivalence.

**Files:**
- Modify: `internal/gateway/router.go` (all ~36 `escapeMarkdown` sites + ~77 escaped literals; delete the `escapeMarkdown` func at line ~948)
- Modify: `internal/gateway/telegram.go` (`Send`, `SendMessageGetID`, `EditMessage` render CommonMark → MarkdownV2 before sending)
- Create: `internal/gateway/render/corpus_test.go` (regression corpus)

**Interfaces:**
- Consumes: `render.RenderTelegram` / `render.For("telegram")` (Task 2).
- Produces: router emits CommonMark to the `send` callback; no downstream signature changes.

**Transformation recipe (mechanical, applied to every send-site in `router.go`):**
1. Remove every `\` that escapes a MarkdownV2 special in a string literal: `"Done\\."` → `"Done."`, `"/agent create \\<name\\>"` → `"/agent create <name>"`, `"linked\\!"` → `"linked!"`.
2. Convert bold `*x*` → `**x**`. (MarkdownV2 `*` = bold; CommonMark `*` = italic. This is the one non-mechanical rule — every intended-bold `*...*` becomes `**...**`.)
3. Keep italic `_x_` as-is (same in both).
4. Replace `escapeMarkdown(x)` calls with bare `x` — the Telegram renderer now escapes dynamic values. `send(escapeMarkdown(err.Error()))` → `send(err.Error())`.
5. Delete the `escapeMarkdown` function definition.

**Concrete before/after examples (from the current file):**

```go
// router.go:143  BEFORE
send(fmt.Sprintf("Hi *%s*\\! Your Telegram account is now linked\\. Send /help to see what you can do\\.", escapeMarkdown(w.Name)))
// AFTER
send(fmt.Sprintf("Hi **%s**! Your Telegram account is now linked. Send /help to see what you can do.", w.Name))

// router.go:165  BEFORE
send("You have no agents yet\\. Use /agent create \\<name\\> to build one\\.")
// AFTER
send("You have no agents yet. Use /agent create <name> to build one.")

// router.go:175-177  BEFORE
b.WriteString(fmt.Sprintf("%s *%s*", status, escapeMarkdown(a.Name)))
b.WriteString(" — " + escapeMarkdown(a.Description))
// AFTER
b.WriteString(fmt.Sprintf("%s **%s**", status, a.Name))
b.WriteString(" — " + a.Description)
```

- [ ] **Step 1: Capture the current Telegram output as a golden corpus (BEFORE any router edit)**

Create `internal/gateway/render/corpus_test.go`. Each entry is `{neutral CommonMark, expected Telegram MarkdownV2}`. Populate `wantTelegram` by reading the CURRENT `router.go` literal for that message (its existing escaped form is the golden value; adjust only where the old code over-escaped a non-special like `<`, which MarkdownV2 does not escape — the renderer's output is correct there and becomes the golden).

```go
package render

import "testing"

// corpus pins representative router messages: neutral CommonMark in, the
// MarkdownV2 the Telegram bot must still send out. Derived from router.go's
// pre-refactor literals. This is the regression gate for the router rewrite.
var corpus = []struct{ neutral, wantTelegram string }{
	{
		"You have no agents yet. Use /agent create <name> to build one.",
		"You have no agents yet\\. Use /agent create <name> to build one\\.",
	},
	{
		"Hi **Ilija**! Your Telegram account is now linked. Send /help to see what you can do.",
		"Hi *Ilija*\\! Your Telegram account is now linked\\. Send /help to see what you can do\\.",
	},
	{
		"Usage: /run <agent_name>",
		"Usage: /run <agent\\_name>",
	},
	{
		"Agent `daily-digest` not found.",
		"Agent `daily-digest` not found\\.",
	},
}

func TestTelegramCorpus(t *testing.T) {
	for _, tc := range corpus {
		if got := RenderTelegram(tc.neutral); got != tc.wantTelegram {
			t.Errorf("RenderTelegram(%q)\n got: %q\nwant: %q", tc.neutral, got, tc.wantTelegram)
		}
	}
}
```

- [ ] **Step 2: Run the corpus test to verify it passes against the renderer**

Run: `go test ./internal/gateway/render/ -run TestTelegramCorpus -v`
Expected: PASS. (If a case fails, the renderer — not the corpus — is wrong; fix Task 2's renderer. The corpus encodes the required Telegram output.)

- [ ] **Step 3: Route Telegram sends through the renderer**

In `internal/gateway/telegram.go`, add `"github.com/ilijad1/simple-agents/internal/gateway/render"` to imports and render before every send. Replace the three send methods' text handling:

```go
// Send delivers a text message to a Telegram chat ID.
func (g *TelegramGateway) Send(platformUserID, text string) error {
	chatID, err := strconv.ParseInt(platformUserID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat id %q: %w", platformUserID, err)
	}
	chat := &telebot.Chat{ID: chatID}
	rendered := render.For("telegram").Render(text)
	_, err = g.bot.Send(chat, rendered, telebot.ModeMarkdownV2)
	if err != nil {
		_, err = g.bot.Send(chat, text) // plain-text fallback uses the neutral source
	}
	return err
}
```

Apply the identical `render.For("telegram").Render(text)` step in `SendMessageGetID` and `EditMessage` (render before the `ModeMarkdownV2` call; keep the plain-text fallback sending the un-rendered `text`).

- [ ] **Step 4: Re-author `router.go` per the transformation recipe**

Apply rules 1–5 to every send-site. Then delete the `escapeMarkdown` function (lines ~948+). Verify none remain:

Run: `grep -n 'escapeMarkdown\|\\\\\\.' internal/gateway/router.go`
Expected: no matches (no `escapeMarkdown` calls, no `\.`-style escaped literals).

- [ ] **Step 5: Build and run the full suite**

Run: `go build ./... && go test ./internal/gateway/... -count=1 -v`
Expected: builds clean; render + corpus tests PASS.

- [ ] **Step 6: Manual Telegram smoke (record in commit body)**

Run `make deploy`, then in Telegram send `/agent list` and `/help` to a connected bot; confirm bold renders as bold and no stray backslashes appear.

- [ ] **Step 7: Commit**

```bash
git add internal/gateway/router.go internal/gateway/telegram.go internal/gateway/render/corpus_test.go
git commit -m "refactor(gateway): router emits neutral CommonMark; Telegram renders on send

Removes MarkdownV2 escaping from the router. Telegram output verified
unchanged by render corpus test + manual smoke (/agent list, /help)."
```

---

### Task 4: Adapter registry + injected dispatch callback

Decouple adapters from `*GatewayManager` so they can live in subpackages without an import cycle, and replace the hard-coded `switch conn.Platform` with a registry.

**Files:**
- Modify: `internal/gateway/gateway.go` (add `DispatchFunc`, `AdapterFactory`, registry; rewrite `start()`)
- Modify: `internal/gateway/telegram.go` (constructor takes a `DispatchFunc` instead of `*GatewayManager`; register a factory)
- Test: `internal/gateway/registry_test.go`

**Interfaces:**
- Produces:
  - `type DispatchFunc func(ctx context.Context, msg Message)`
  - `type AdapterFactory func(token, config, ownerWorkspaceID string, dispatch DispatchFunc) (Gateway, error)` — `config` is the decrypted `encrypted_config` JSON (empty for single-token platforms)
  - `func RegisterAdapter(platform string, f AdapterFactory)`
  - `func (m *GatewayManager) dispatchFunc() DispatchFunc` — returns `m.dispatch` bound as a `DispatchFunc`
- Consumes (Telegram): `NewTelegram(token, ownerWorkspaceID string, dispatch DispatchFunc)`.

- [ ] **Step 1: Write the failing test**

```go
package gateway

import (
	"context"
	"testing"
)

type fakeGW struct{ platform string }

func (f *fakeGW) Platform() string                    { return f.platform }
func (f *fakeGW) OwnerUserID() string                 { return "ws1" }
func (f *fakeGW) Start(ctx context.Context) error     { <-ctx.Done(); return nil }
func (f *fakeGW) Stop() error                         { return nil }
func (f *fakeGW) Send(platformUserID, text string) error { return nil }

func TestRegisterAndLookupAdapter(t *testing.T) {
	RegisterAdapter("fake", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		return &fakeGW{platform: "fake"}, nil
	})
	f, ok := adapterFactory("fake")
	if !ok {
		t.Fatal("factory not registered")
	}
	gw, err := f("t", "", "ws1", func(context.Context, Message) {})
	if err != nil || gw.Platform() != "fake" {
		t.Fatalf("factory produced wrong gateway: %v %v", gw, err)
	}
}

func TestUnknownAdapterNotFound(t *testing.T) {
	if _, ok := adapterFactory("does-not-exist"); ok {
		t.Fatal("expected unknown adapter to be absent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestRegisterAndLookupAdapter -v`
Expected: FAIL — `RegisterAdapter`/`adapterFactory` undefined.

- [ ] **Step 3: Implement registry + dispatch decoupling**

In `gateway.go`, add near the top:

```go
// DispatchFunc is the callback an adapter invokes for each inbound message.
type DispatchFunc func(ctx context.Context, msg Message)

// AdapterFactory builds a Gateway from decrypted credentials. config is the
// decrypted encrypted_config JSON ("" for single-token platforms).
type AdapterFactory func(token, config, ownerWorkspaceID string, dispatch DispatchFunc) (Gateway, error)

var (
	adapterMu       sync.RWMutex
	adapterRegistry = map[string]AdapterFactory{}
)

// RegisterAdapter registers a platform's factory. Call from an adapter's init()
// or from main() during wiring.
func RegisterAdapter(platform string, f AdapterFactory) {
	adapterMu.Lock()
	defer adapterMu.Unlock()
	adapterRegistry[platform] = f
}

func adapterFactory(platform string) (AdapterFactory, bool) {
	adapterMu.RLock()
	defer adapterMu.RUnlock()
	f, ok := adapterRegistry[platform]
	return f, ok
}

func (m *GatewayManager) dispatchFunc() DispatchFunc {
	return func(ctx context.Context, msg Message) { m.dispatch(ctx, msg) }
}
```

Rewrite `GatewayManager.start()`'s body (the `switch conn.Platform`) to use the registry:

```go
func (m *GatewayManager) start(ctx context.Context, conn *db.PlatformConnection) error {
	token, err := DecryptToken(conn.EncryptedToken, m.systemKey)
	if err != nil {
		return fmt.Errorf("decrypt token: %w", err)
	}
	var config string
	if conn.EncryptedConfig != "" {
		if config, err = DecryptToken(conn.EncryptedConfig, m.systemKey); err != nil {
			return fmt.Errorf("decrypt config: %w", err)
		}
	}
	factory, ok := adapterFactory(conn.Platform)
	if !ok {
		return fmt.Errorf("unsupported platform: %s", conn.Platform)
	}
	gw, err := factory(token, config, conn.WorkspaceID, m.dispatchFunc())
	if err != nil {
		return fmt.Errorf("new %s: %w", conn.Platform, err)
	}
	gwCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	k := key(conn.Platform, conn.WorkspaceID)
	m.gateways[k] = gw
	m.cancels[k] = cancel
	m.mu.Unlock()
	go func() {
		if err := gw.Start(gwCtx); err != nil && gwCtx.Err() == nil {
			fmt.Printf("gateway: %s for user %s stopped with error: %v\n", conn.Platform, conn.WorkspaceID, err)
		}
	}()
	return nil
}
```

(`conn.EncryptedConfig` is added in Task 5; if compiling Task 4 before Task 5, temporarily read `""`. Order Task 5 first if preferred — they are adjacent.)

Update `NewTelegram` to take a `DispatchFunc`:

```go
func NewTelegram(token, ownerWorkspaceID string, dispatch DispatchFunc) (*TelegramGateway, error) {
	// ... unchanged bot init ...
	return &TelegramGateway{bot: bot, ownerWorkspaceID: ownerWorkspaceID, dispatch: dispatch}, nil
}
```

Change the struct field `manager *GatewayManager` → `dispatch DispatchFunc`, and in `handle()` replace `g.manager.dispatch(ctx, msg)` with `g.dispatch(ctx, msg)`. Register the factory (bottom of `telegram.go`):

```go
func init() {
	RegisterAdapter("telegram", func(token, config, ws string, d DispatchFunc) (Gateway, error) {
		return NewTelegram(token, ws, d)
	})
}
```

- [ ] **Step 4: Run tests + build**

Run: `go build ./... && go test ./internal/gateway/ -v`
Expected: PASS. Fix any remaining `g.manager` references.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/telegram.go internal/gateway/registry_test.go
git commit -m "refactor(gateway): adapter registry + injected dispatch callback (breaks manager coupling)"
```

---

### Task 5: Migration 008 — `encrypted_config` column + model field

**Files:**
- Create: `migrations/008_platform_connection_config.up.sql`
- Create: `migrations/008_platform_connection_config.down.sql`
- Modify: `internal/db/models.go:49-57` (add `EncryptedConfig string`)
- Modify: `internal/db/repositories.go` (`UpsertPlatformConnection`, `scanPlatformConnection`, and the SELECT column lists in `GetPlatformConnection`/`ListActivePlatformConnections`/`ListWorkspacePlatformConnections`)
- Test: `internal/db/platform_connection_test.go`

**Interfaces:**
- Produces: `db.PlatformConnection.EncryptedConfig string` round-trips through upsert/get.

- [ ] **Step 1: Write the migration**

`migrations/008_platform_connection_config.up.sql`:
```sql
ALTER TABLE platform_connections ADD COLUMN encrypted_config TEXT NOT NULL DEFAULT '';
```

`migrations/008_platform_connection_config.down.sql`:
```sql
ALTER TABLE platform_connections DROP COLUMN encrypted_config;
```

- [ ] **Step 2: Write the failing test**

```go
package db

import "testing"

func TestPlatformConnectionEncryptedConfigRoundTrip(t *testing.T) {
	d := newTestDB(t) // existing test helper; see other *_test.go in this package
	ws := createTestWorkspace(t, d)
	conn := &PlatformConnection{
		ID: "pc1", WorkspaceID: ws, Platform: "slack",
		EncryptedToken: "tok", EncryptedConfig: `{"app_token":"x"}`, Active: true,
	}
	if err := d.UpsertPlatformConnection(conn); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetPlatformConnection(ws, "slack")
	if err != nil {
		t.Fatal(err)
	}
	if got.EncryptedConfig != `{"app_token":"x"}` {
		t.Fatalf("config not persisted: %q", got.EncryptedConfig)
	}
}
```

(Confirm the exact test-DB + workspace helpers by reading an existing `internal/db/*_test.go`; reuse them rather than inventing names.)

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestPlatformConnectionEncryptedConfig -v`
Expected: FAIL — `EncryptedConfig` field/column missing.

- [ ] **Step 4: Add the model field + repository wiring**

`models.go` struct gains `EncryptedConfig string` (after `EncryptedToken`).

In `repositories.go`: add `encrypted_config` to the INSERT column list + `VALUES` + the `ON CONFLICT ... DO UPDATE SET` clause of `UpsertPlatformConnection`; add it to the `SELECT` column list of all three read helpers; add the scan target in `scanPlatformConnection` (matching column order). Read the current function bodies first and mirror their exact style.

- [ ] **Step 5: Run test + full db suite**

Run: `go test ./internal/db/ -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/008_platform_connection_config.up.sql migrations/008_platform_connection_config.down.sql internal/db/models.go internal/db/repositories.go internal/db/platform_connection_test.go
git commit -m "feat(db): platform_connections.encrypted_config (migration 008) for multi-field creds"
```

---

### Task 6: Credential-spec framework (per-platform field sets + validation)

Give each platform a declarative credential spec the connector UI and `saveConnector` can drive generically. Phase 1 registers only Telegram; adapters register their own specs in later phases.

**Files:**
- Create: `internal/gateway/credspec.go`
- Test: `internal/gateway/credspec_test.go`

**Interfaces:**
- Produces:
  - `type CredField struct { Key, Label, Placeholder string; Secret bool }`
  - `type CredSpec struct { Platform string; Fields []CredField; SetupURL string; SetupSteps []string; Validate func(values map[string]string) (identity string, err error) }`
  - `func RegisterCredSpec(s CredSpec)` / `func CredSpecFor(platform string) (CredSpec, bool)` / `func CredSpecs() []CredSpec`
  - Convention: the field with `Key == "token"` maps to `encrypted_token`; all other fields are JSON-serialized into `encrypted_config`.
  - `func SplitCreds(spec CredSpec, values map[string]string) (token, configJSON string, err error)`

- [ ] **Step 1: Write the failing test**

```go
package gateway

import "testing"

func TestSplitCredsSeparatesTokenFromConfig(t *testing.T) {
	spec := CredSpec{Platform: "slack", Fields: []CredField{
		{Key: "token", Label: "Bot Token", Secret: true},
		{Key: "app_token", Label: "App Token", Secret: true},
	}}
	token, cfg, err := SplitCreds(spec, map[string]string{"token": "xoxb", "app_token": "xapp"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "xoxb" {
		t.Fatalf("token = %q", token)
	}
	if cfg != `{"app_token":"xapp"}` {
		t.Fatalf("config = %q", cfg)
	}
}

func TestRegisterAndGetCredSpec(t *testing.T) {
	RegisterCredSpec(CredSpec{Platform: "cs-test", Fields: []CredField{{Key: "token"}}})
	if _, ok := CredSpecFor("cs-test"); !ok {
		t.Fatal("spec not registered")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/ -run TestSplitCreds -v`
Expected: FAIL — symbols undefined.

- [ ] **Step 3: Implement**

```go
package gateway

import (
	"encoding/json"
	"sort"
	"sync"
)

type CredField struct {
	Key, Label, Placeholder string
	Secret                  bool
}

type CredSpec struct {
	Platform   string
	Fields     []CredField
	SetupURL   string
	SetupSteps []string
	Validate   func(values map[string]string) (identity string, err error)
}

var (
	credMu    sync.RWMutex
	credSpecs = map[string]CredSpec{}
)

func RegisterCredSpec(s CredSpec) {
	credMu.Lock()
	defer credMu.Unlock()
	credSpecs[s.Platform] = s
}

func CredSpecFor(platform string) (CredSpec, bool) {
	credMu.RLock()
	defer credMu.RUnlock()
	s, ok := credSpecs[platform]
	return s, ok
}

func CredSpecs() []CredSpec {
	credMu.RLock()
	defer credMu.RUnlock()
	out := make([]CredSpec, 0, len(credSpecs))
	for _, s := range credSpecs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Platform < out[j].Platform })
	return out
}

// SplitCreds maps the "token" field to encrypted_token and all other fields to
// a stable-key-ordered JSON object for encrypted_config.
func SplitCreds(spec CredSpec, values map[string]string) (token, configJSON string, err error) {
	extra := map[string]string{}
	for _, f := range spec.Fields {
		if f.Key == "token" {
			token = values[f.Key]
			continue
		}
		extra[f.Key] = values[f.Key]
	}
	if len(extra) == 0 {
		return token, "", nil
	}
	b, err := json.Marshal(extra)
	if err != nil {
		return "", "", err
	}
	return token, string(b), nil
}
```

Register Telegram's spec (in `telegram.go`'s `init()`), wiring the existing validation:

```go
	RegisterCredSpec(CredSpec{
		Platform: "telegram",
		Fields:   []CredField{{Key: "token", Label: "Bot Token", Placeholder: "123456:ABC-...", Secret: true}},
		SetupURL: "https://t.me/BotFather",
		SetupSteps: []string{
			"Open @BotFather in Telegram", "Send /newbot and follow the prompts",
			"Copy the token it gives you and paste it here",
		},
	})
```

(`json.Marshal` of a `map[string]string` sorts keys, so `SplitCreds` output is deterministic — matching the test.)

- [ ] **Step 4: Run test + build**

Run: `go build ./... && go test ./internal/gateway/ -run 'TestSplitCreds|TestRegisterAndGetCredSpec' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/credspec.go internal/gateway/credspec_test.go internal/gateway/telegram.go
git commit -m "feat(gateway): declarative credential specs (field sets + token/config split)"
```

---

### Task 7: Generalized connector UI + `saveConnector`

Drive the connectors page and setup wizard from `CredSpec` instead of Telegram-only hard-coding. Phase 1 shows only Telegram (the only registered adapter), but through the generic path — so adding an adapter later needs no UI edit.

**Files:**
- Modify: `web/handlers_connectors.go` (`saveConnector` accepts a `map[string]string`; `supportedPlatforms` derives from registered specs; store `encrypted_config`)
- Modify: `web/handlers_setup.go:132` (`handleSetupConnector` passes the field map)
- Modify: `web/templates/dashboard/connectors.html` (render fields from `CredSpec`)
- Test: `web/connectors_test.go` (or extend existing web test file)

**Interfaces:**
- Consumes: `gateway.CredSpecs()`, `gateway.CredSpecFor`, `gateway.SplitCreds`, `db.PlatformConnection.EncryptedConfig` (Task 5), `gateway.EncryptToken`.
- Produces: `saveConnector(workspaceID, platform string, values map[string]string) (identity string, botStartErr, err error)`.

- [ ] **Step 1: Write the failing test**

```go
package web

import "testing"

func TestSaveConnectorStoresConfigForMultiField(t *testing.T) {
	s := newTestServer(t) // reuse existing web test harness
	ws := createTestWorkspace(t, s.db)
	gateway.RegisterCredSpec(gateway.CredSpec{Platform: "cs-multi", Fields: []gateway.CredField{
		{Key: "token"}, {Key: "server_url"},
	}}) // no Validate → skips network probe
	_, _, err := s.saveConnector(ws, "cs-multi", map[string]string{"token": "t", "server_url": "https://mm.example"})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.GetPlatformConnection(ws, "cs-multi")
	if err != nil {
		t.Fatal(err)
	}
	if conn.EncryptedConfig == "" {
		t.Fatal("expected encrypted_config to be populated")
	}
}
```

(Confirm `newTestServer`/`createTestWorkspace` names against the existing web test files; reuse them.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run TestSaveConnectorStoresConfig -v`
Expected: FAIL — `saveConnector` signature mismatch.

- [ ] **Step 3: Rewrite `saveConnector` generically**

```go
func (s *Server) saveConnector(workspaceID, platform string, values map[string]string) (identity string, botStartErr error, err error) {
	spec, ok := gateway.CredSpecFor(platform)
	if !ok {
		return "", nil, fmt.Errorf("unsupported platform: %s", platform)
	}
	if spec.Validate != nil {
		if identity, err = spec.Validate(values); err != nil {
			return "", nil, fmt.Errorf("invalid credentials: %w", err)
		}
	}
	token, configJSON, err := gateway.SplitCreds(spec, values)
	if err != nil {
		return "", nil, err
	}
	encToken, err := gateway.EncryptToken(token, s.systemKey)
	if err != nil {
		return "", nil, fmt.Errorf("encrypt token: %w", err)
	}
	encConfig := ""
	if configJSON != "" {
		if encConfig, err = gateway.EncryptToken(configJSON, s.systemKey); err != nil {
			return "", nil, fmt.Errorf("encrypt config: %w", err)
		}
	}
	if err := s.db.UpsertPlatformConnection(&db.PlatformConnection{
		ID: uuid.New().String(), WorkspaceID: workspaceID, Platform: platform,
		EncryptedToken: encToken, EncryptedConfig: encConfig, Active: true,
	}); err != nil {
		return "", nil, fmt.Errorf("save connector: %w", err)
	}
	if platform == "telegram" && identity != "" {
		_ = s.db.SetSetting(workspaceID, "telegram_bot_username", "@"+identity)
	}
	if s.gateway != nil {
		botStartErr = s.gateway.Reload(context.Background(), workspaceID, platform)
	}
	return identity, botStartErr, nil
}
```

Move Telegram's `getMe` probe into its `CredSpec.Validate` (in `telegram.go`): `Validate: func(v map[string]string) (string, error) { return testTelegramTokenExported(v["token"]) }` — export or relocate `testTelegramToken` so the gateway package can host it, OR keep the probe in `web` by setting `spec.Validate` at wiring time. Simplest: set Telegram's `Validate` from `web` during server init (`gateway.CredSpecFor` + re-register), keeping `testTelegramToken` in `web`. Choose one and keep it consistent.

Set `supportedPlatforms` from `gateway.CredSpecs()`:
```go
func supportedPlatformNames() []string {
	var out []string
	for _, s := range gateway.CredSpecs() {
		out = append(out, s.Platform)
	}
	return out
}
```
Replace uses of the old `supportedPlatforms` var. Update the two call sites of `saveConnector` (connectors POST handler + `handleSetupConnector`) to build the `values` map from the posted form fields (`c.FormValue(field.Key)` for each `spec.Fields`).

- [ ] **Step 4: Update the template to render fields from the spec**

In `connectors.html`, iterate the platform's `CredSpec.Fields`, emitting one `<input>` per field (`type=password` when `Secret`), plus `SetupSteps` as guidance. Pass `[]gateway.CredSpec` into `connectorsPageData`.

- [ ] **Step 5: Run tests + build + manual smoke**

Run: `go build ./... && go test ./web/ ./internal/gateway/ -count=1 -v`
Expected: PASS. Then `make deploy` and confirm the Telegram connect form still saves + links (`/start`).

- [ ] **Step 6: Commit**

```bash
git add web/handlers_connectors.go web/handlers_setup.go web/templates/dashboard/connectors.html web/connectors_test.go
git commit -m "feat(web): credential-spec-driven connector UI (generic field sets), config storage"
```

---

## Self-Review

**Spec coverage (design §3–§10, Phase 1 only):**
- §3 import-cycle fix + registry → Task 4. ✅
- §3 dispatch callback → Task 4. ✅
- §4 neutral CommonMark + goldmark renderers (Telegram + passthrough) → Tasks 1–3. ✅
- §4 regression golden guard → Task 3 corpus test. ✅
- §5 `encrypted_config` migration + credential structs → Tasks 5, 6. ✅
- §8 generalized connector UI + setup wizard → Task 7. ✅
- **Deferred to adapter phases (correctly not in Phase 1):** Discord/Slack/Matrix/Mattermost adapters, Slack mrkdwn + Matrix HTML renderers, mandatory-delete enforcement, identity/reply-target per-adapter guarantees, reconnect loops. Moving Telegram into a `telegram/` subpackage (design §3) is deferred to the first adapter phase, where the leaf-package split is actually forced — Phase 1 keeps Telegram in-package (registry + callback already remove the coupling), avoiding a move with no consumer yet. Noted intentionally.

**Placeholder scan:** No TBD/TODO. Two tasks (5, 7) instruct reading existing test helpers (`newTestDB`, `newTestServer`) rather than inventing names — that's a correctness safeguard, not a placeholder; the assertion code is fully specified.

**Type consistency:** `DispatchFunc`, `AdapterFactory`, `CredSpec`/`CredField`, `SplitCreds`, `render.Renderer`/`For`/`Register`, `RenderTelegram` used consistently across tasks. `EncryptedConfig` field name matches column `encrypted_config` throughout. `saveConnector`'s new `map[string]string` signature is reflected at both call sites in Task 7.

---

## Execution Handoff

Phase 1 (Foundation) is the prerequisite for all four adapter plans. Each subsequent phase (Discord → Slack → Mattermost → Matrix) will be its own plan document, written after this one lands, building on the registry, renderer, and credential-spec framework established here.
