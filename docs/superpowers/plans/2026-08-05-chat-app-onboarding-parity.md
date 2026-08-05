# Chat App Onboarding Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the setup wizard's chat-app step run the same test-and-link flow as the connections page, and make a stalled link state its reason instead of spinning forever.

**Architecture:** The test and link steps become one shared React implementation with an injected data source, mounted by both hosts. Onboarding's source is backed by two new `/api/v1/setup/*` routes (the setup guard exempts that prefix already). A `bot_online` flag from `GatewayManager` makes a dead adapter visible. Discord gains a real `/start` application command so the instruction stops fighting Discord's slash-command picker.

**Tech Stack:** Go 1.24 (Echo v4, discordgo), React 19 + TypeScript, TanStack Query, Vitest, Tailwind v4.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-05-chat-app-onboarding-parity-design.md`.
- **No green state and no Done button while unlinked** — the invariant from the prior spec; holds in BOTH hosts of the shared component.
- `bot_online` is **advisory, not a gate**. The `platform_identities` row stays the only proof of success.
- Escalation threshold: **45 seconds**. Poll interval **2 seconds**, bounded at **5 minutes**.
- The message-based `/start` path is **kept**, not replaced. Interaction and message handlers converge on the same `handleStart`.
- Discord invite scope becomes `bot applications.commands`; `permissions=0` stays.
- Do not exempt `/api/v1/connectors` from `requireSetupCompleteAPI`.
- `telegram_bot_username` keeps being written for Telegram (the settings page reads it); only the setup wizard's dependency on it is removed.
- Every new API route must be added to the `want` table in `web/api_parity_test.go` or the merge gate fails.
- Conventional Commits. Run `make ci-test` (Go) and `npm run test` in `web/ui` before each commit.

---

### Task 1: `GatewayManager.IsRunning` + a startup log line

**Files:**
- Modify: `internal/gateway/gateway.go` (add method; add `slog.Info` in `start`)
- Test: `internal/gateway/isrunning_test.go` (create)

**Interfaces:**
- Produces: `func (m *GatewayManager) IsRunning(workspaceID, platform string) bool`

- [ ] **Step 1: Write the failing test**

```go
package gateway

import (
	"context"
	"testing"
)

type fakeGW struct{ platform, ws string }

func (f *fakeGW) Platform() string                 { return f.platform }
func (f *fakeGW) OwnerUserID() string              { return f.ws }
func (f *fakeGW) Start(ctx context.Context) error  { <-ctx.Done(); return nil }
func (f *fakeGW) Stop() error                      { return nil }
func (f *fakeGW) Send(_, _ string) error           { return nil }

func TestIsRunningReflectsStartAndStop(t *testing.T) {
	key := make([]byte, 32)
	tok, err := EncryptToken("t0ken", key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	RegisterAdapter("fakeplat", func(_, _, ws string, _ DispatchFunc) (Gateway, error) {
		return &fakeGW{platform: "fakeplat", ws: ws}, nil
	})

	m := &GatewayManager{
		systemKey: key,
		gateways:  map[string]Gateway{},
		cancels:   map[string]context.CancelFunc{},
	}

	if m.IsRunning("ws1", "fakeplat") {
		t.Fatal("expected not running before start")
	}
	if err := m.start(context.Background(), &dbPlatformConn("ws1", "fakeplat", tok)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !m.IsRunning("ws1", "fakeplat") {
		t.Fatal("expected running after start")
	}
	if m.IsRunning("ws2", "fakeplat") {
		t.Fatal("another workspace must not report running")
	}
	m.stop("ws1", "fakeplat")
	if m.IsRunning("ws1", "fakeplat") {
		t.Fatal("expected not running after stop")
	}
}
```

Replace the `dbPlatformConn` placeholder with a real literal once you read the
`db.PlatformConnection` field names — it is `&db.PlatformConnection{WorkspaceID: "ws1", Platform: "fakeplat", EncryptedToken: tok, Active: true}` and needs the `internal/db` import.

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/gateway/ -run TestIsRunning -v`
Expected: FAIL — `m.IsRunning undefined`.

- [ ] **Step 3: Implement**

```go
// IsRunning reports whether a live adapter is currently held for this
// workspace+platform. Advisory only: it proves an adapter object exists, not
// that the platform's gateway is reachable right now, so callers must keep
// treating the platform_identities row as the only proof of a completed link.
func (m *GatewayManager) IsRunning(workspaceID, platform string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.gateways[key(platform, workspaceID)]
	return ok
}
```

In `start`, immediately after the `m.mu.Unlock()` that follows the map writes, add:

```go
	slog.Info("gateway: adapter started", "platform", conn.Platform, "workspace_id", conn.WorkspaceID)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/gateway/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/isrunning_test.go
git commit -m "feat(gateway): report adapter liveness and log a started adapter"
```

---

### Task 2: `bot_online` on the platform DTO

**Files:**
- Modify: `web/api_connectors.go` (DTO field + `connectorPlatformList`)
- Modify: `web/ui/src/lib/connections.ts` (`ConnectorPlatform` type)
- Test: `web/api_connectors_online_test.go` (create)

**Interfaces:**
- Consumes: `GatewayManager.IsRunning` (Task 1)
- Produces: `apiConnectorPlatform.BotOnline` → JSON `bot_online`; TS `ConnectorPlatform.bot_online: boolean`

- [ ] **Step 1: Add the DTO field**

In `apiConnectorPlatform`, after `InviteURL`:

```go
	// BotOnline reports whether a live adapter is running for this platform
	// right now. A saved connection whose server is down is otherwise
	// indistinguishable from one simply waiting for /start — which is the
	// exact ambiguity that made a dead server look like a Discord problem.
	BotOnline bool `json:"bot_online"`
```

- [ ] **Step 2: Populate it**

In `connectorPlatformList`, inside the `if conn, err := s.db.GetPlatformConnection(...)` block, after `entry.Connected = conn.Active`:

```go
			// nil gateway is the test/no-wiring case: report offline rather
			// than claiming a liveness we cannot observe.
			entry.BotOnline = conn.Active && s.gateway != nil &&
				s.gateway.IsRunning(u.ID, spec.Platform)
```

- [ ] **Step 3: Mirror the type in TypeScript**

In `web/ui/src/lib/connections.ts`, in `ConnectorPlatform`, after `invite_url: string;`:

```ts
  // Whether a live adapter is running server-side right now. Advisory: the
  // link is only ever proven by `linked`.
  bot_online: boolean;
```

- [ ] **Step 4: Write the test**

`web/api_connectors_online_test.go` — follow the existing harness in `web/api_connectors_test.go` (reuse its server/workspace helpers verbatim; read that file first). Assert that with `s.gateway == nil` a connected platform reports `bot_online: false`, and that the field is present in the JSON body.

- [ ] **Step 5: Run tests**

Run: `go test ./web/ -run TestConnector -count=1` and `cd web/ui && npx tsc -b`
Expected: PASS. Existing frontend fixtures that construct a `ConnectorPlatform` will fail to typecheck — add `bot_online: false` to each.

- [ ] **Step 6: Commit**

```bash
git add web/api_connectors.go web/api_connectors_online_test.go web/ui/src/lib/connections.ts web/ui/src
git commit -m "feat(web/connections): report whether a chat adapter is live"
```

---

### Task 3: Setup-scoped platform routes

**Files:**
- Modify: `web/api_settings.go` (register + handlers)
- Modify: `web/api_parity_test.go` (`want` table)
- Test: `web/api_setup_platforms_test.go` (create)

**Interfaces:**
- Produces: `GET /api/v1/setup/platforms` → `{"platforms":[...]}`; `POST /api/v1/setup/platforms/:platform/test` → `apiTestConnectorResponse`

- [ ] **Step 1: Register the routes**

In `registerSettingsAPI`, beside the existing setup routes:

```go
	// Setup-scoped mirrors of two connector endpoints. The connector routes
	// themselves sit on the setup-gated group, so the wizard cannot reach them
	// while needs_setup is true. Mirroring exactly these two — read + test —
	// keeps a half-configured workspace away from the delete and re-save
	// endpoints that share that group. requireSetupCompleteAPI exempts by
	// PREFIX ("/api/v1/setup"), so these need no guard change.
	g.GET("/setup/platforms", s.apiSetupPlatforms)
	g.POST("/setup/platforms/:platform/test", s.apiSetupTestPlatform)
```

- [ ] **Step 2: Implement the handlers**

```go
// apiSetupPlatforms serves the same catalog as apiListConnectors, reachable
// while the setup wizard is still running.
func (s *Server) apiSetupPlatforms(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	return c.JSON(http.StatusOK, apiConnectorListResponse{Platforms: s.connectorPlatformList(w)})
}

// apiSetupTestPlatform is apiTestConnector for the setup wizard.
func (s *Server) apiSetupTestPlatform(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")
	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}
	identity, err := s.testConnectorIdentity(w.ID, platform)
	if err != nil {
		return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: false, Error: err.Error()})
	}
	return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: true, Identity: identity.Username})
}
```

Add `"github.com/ilijad1/rookery/internal/gateway"` to the imports if absent.

- [ ] **Step 3: Add both routes to the parity `want` table**

In `web/api_parity_test.go`, beside the existing `"GET /api/v1/setup"` entry:

```go
		"GET /api/v1/setup/platforms", "POST /api/v1/setup/platforms/:platform/test",
```

- [ ] **Step 4: Write the test**

`web/api_setup_platforms_test.go` — reuse the harness from `web/api_settings_test.go` (read it first; it already builds a workspace with `NeedsSetup` true and lands the wizard on step 5). Assert `GET /api/v1/setup/platforms` returns **200** — not 403 — while `NeedsSetup` is true, and that its `platforms` array matches `apiListConnectors`' shape.

- [ ] **Step 5: Run tests**

Run: `go test ./web/ -run "TestAPIParityInventory|TestSetupPlatforms" -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/api_settings.go web/api_parity_test.go web/api_setup_platforms_test.go
git commit -m "feat(web/setup): expose platform status and test during onboarding"
```

---

### Task 4: Extract the shared Test and Link steps

**Files:**
- Create: `web/ui/src/components/chat-connect/source.ts`
- Create: `web/ui/src/components/chat-connect/LinkStep.tsx`
- Create: `web/ui/src/components/chat-connect/notes.tsx`
- Modify: `web/ui/src/pages/connections/ChatAppWizard.tsx`
- Test: `web/ui/src/components/chat-connect/LinkStep.test.tsx` (create)

**Interfaces:**
- Consumes: `ConnectorPlatform.bot_online` (Task 2), the two setup routes (Task 3)
- Produces:
  - `type PlatformSource = { usePlatform(platform, opts: {poll: boolean}): ConnectorPlatform | undefined; useTest(): UseMutationResult<TestConnectorResponse, unknown, string> }`
  - `connectorsSource: PlatformSource`, `setupSource: PlatformSource`
  - `<LinkStep platform source onFinishLater onDone />`

- [ ] **Step 1: Write `source.ts`**

```ts
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { ConnectorPlatform, TestConnectorResponse } from "@/lib/connections";
import { useConnectors, useTestConnector } from "@/lib/connections";

export const POLL_MS = 2000;
export const ESCALATE_MS = 45_000;
export const POLL_LIMIT_MS = 5 * 60_000;

/**
 * How a host feeds live platform status to the shared steps.
 *
 * It exists because the two hosts cannot share a transport: every
 * /api/v1/connectors route sits behind requireSetupCompleteAPI, so during
 * onboarding they 403. Injecting the source — rather than forking the
 * component — is what stops the two flows drifting again.
 */
export type PlatformSource = {
  usePlatform: (platform: string, opts: { poll: boolean }) => ConnectorPlatform | undefined;
  useTest: () => UseMutationResult<TestConnectorResponse, unknown, string>;
};

export const connectorsSource: PlatformSource = {
  usePlatform: (platform, { poll }) => {
    const { data } = useConnectors({ refetchInterval: poll ? POLL_MS : false });
    return data?.platforms?.find((p) => p.platform === platform);
  },
  useTest: () => useTestConnector(),
};

export const setupSource: PlatformSource = {
  usePlatform: (platform, { poll }) => {
    const { data } = useQuery({
      queryKey: ["setup", "platforms"],
      queryFn: () => api.get<{ platforms: ConnectorPlatform[] }>("/api/v1/setup/platforms"),
      refetchInterval: poll ? POLL_MS : false,
    });
    return data?.platforms?.find((p) => p.platform === platform);
  },
  useTest: () => {
    const qc = useQueryClient();
    return useMutation({
      mutationFn: (platform: string) =>
        api.post<TestConnectorResponse>(`/api/v1/setup/platforms/${platform}/test`),
      onSuccess: () => qc.invalidateQueries({ queryKey: ["setup", "platforms"] }),
    });
  },
};
```

- [ ] **Step 2: Move the note primitives into `notes.tsx`**

Cut `ErrorNote`, `WarningNote`, `OkNote` and `Spinner` verbatim out of `ChatAppWizard.tsx` into `web/ui/src/components/chat-connect/notes.tsx`, exporting each. Re-import them in `ChatAppWizard.tsx`. No behaviour change — this is purely so both hosts share one set.

- [ ] **Step 3: Write the failing component test**

`LinkStep.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { LinkStep } from "./LinkStep";
import type { PlatformSource } from "./source";
import type { ConnectorPlatform } from "@/lib/connections";

const base: ConnectorPlatform = {
  platform: "discord", label: "Discord", blurb: "", setup_steps: [], fields: [],
  connected: true, identity: "rookery", linked: false, linked_identity: "",
  primary: false, dm_url: "https://discord.com/users/1", invite_url: "https://invite",
  bot_online: true,
};

function sourceFor(p: ConnectorPlatform): PlatformSource {
  return {
    usePlatform: () => p,
    useTest: () => ({ mutate: () => {} }) as never,
  };
}

describe("LinkStep", () => {
  it("shows no Done button while unlinked", () => {
    render(<LinkStep platform={base} source={sourceFor(base)} onFinishLater={() => {}} onDone={() => {}} />);
    expect(screen.queryByRole("button", { name: /^Done$/ })).toBeNull();
  });

  it("states the bot is not running when offline", () => {
    const off = { ...base, bot_online: false };
    render(<LinkStep platform={off} source={sourceFor(off)} onFinishLater={() => {}} onDone={() => {}} />);
    expect(screen.getByText(/isn't running/i)).toBeTruthy();
  });

  it("confirms the link once the identity lands", () => {
    const on = { ...base, linked: true, linked_identity: "tickbrick" };
    render(<LinkStep platform={on} source={sourceFor(on)} onFinishLater={() => {}} onDone={() => {}} />);
    expect(screen.getByText(/Linked as tickbrick/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Done$/ })).toBeTruthy();
  });
});
```

- [ ] **Step 4: Run it and watch it fail**

Run: `cd web/ui && npx vitest run src/components/chat-connect/LinkStep.test.tsx`
Expected: FAIL — module `./LinkStep` not found.

- [ ] **Step 5: Write `LinkStep.tsx`**

Port the existing `LinkStep` from `ChatAppWizard.tsx` with four changes:
1. Take `source: PlatformSource` and read live status via `source.usePlatform(platform.platform, { poll: !linked && !expired })` instead of calling `useConnectors` directly.
2. When `live.bot_online === false`, render a `WarningNote`: `The bot isn't running — the server may be down. Start it, then send /start again.`
3. Track elapsed time with a `useEffect` interval. After `ESCALATE_MS`, render a "Not working?" block listing: send it as a **direct message**, not a message in a server channel; confirm the bot shares a server with you; confirm the server's Privacy Settings allow direct messages.
4. After `POLL_LIMIT_MS`, stop polling (`expired`) and render a **Retry** button that resets elapsed to zero.

Keep the invite card, the DM card, the `/start` code block and the "Finish later" escape exactly as they are. **Do not** add a Done button or any green state to the unlinked branch.

- [ ] **Step 6: Run tests**

Run: `cd web/ui && npx vitest run src/components/chat-connect/`
Expected: PASS (3 tests).

- [ ] **Step 7: Rewire `ChatAppWizard` to the shared component**

Delete the local `LinkStep` from `ChatAppWizard.tsx`; import the shared one and pass `source={connectorsSource}` at both call sites (`ConnectWizard`'s `link` step and `ManageWizard`'s unlinked branch).

- [ ] **Step 8: Run the full frontend suite**

Run: `cd web/ui && npx tsc -b && npx vitest run`
Expected: PASS — including the existing `ChatAppWizard.test.tsx` invariants.

- [ ] **Step 9: Commit**

```bash
git add web/ui/src/components/chat-connect web/ui/src/pages/connections/ChatAppWizard.tsx
git commit -m "refactor(web/ui): share the chat-app link step behind an injected source"
```

---

### Task 5: Onboarding runs test + link, and Done goes platform-aware

**Files:**
- Modify: `web/ui/src/pages/setup/SetupWizard.tsx`
- Modify: `web/api_settings.go` (`apiGetSetup` step 7 payload)
- Test: `web/ui/src/pages/setup/SetupWizard.test.tsx`

**Interfaces:**
- Consumes: `setupSource`, `LinkStep` (Task 4); `GET /api/v1/setup/platforms` (Task 3)
- Produces: step 7 payload gains `platform`, `platform_label`, `bot_identity`, `linked`

- [ ] **Step 1: Replace the Telegram-only step-7 payload**

In `apiGetSetup`, replace the `case 7:` body:

```go
	case 7:
		// Was telegram_bot_username, which saveConnector writes ONLY for
		// Telegram — so a Discord install reached Done with no bot name and no
		// linking instruction at all. Read the platform-keyed identity the
		// connectors list already uses.
		for _, p := range s.connectorPlatformList(w) {
			if p.Connected {
				resp["platform"] = p.Platform
				resp["platform_label"] = p.Label
				resp["bot_identity"] = p.Identity
				resp["linked"] = p.Linked
				resp["dm_url"] = p.DMURL
				resp["invite_url"] = p.InviteURL
				break
			}
		}
```

- [ ] **Step 2: Give step 5 its test and link phases**

In `SetupWizard.tsx`, add to `ChatAppStep` a `phase` state of `"form" | "test" | "link"`, starting at `"form"`.

In `submit`, replace `onNext(res.next_step)` with:

```ts
      setNextStep(res.next_step);
      setPhase("test");
```

`nextStep` is stashed, **not** navigated to — `setupStep` flips 5→7 the moment the connection row exists, so navigating here is exactly what skipped test and link.

Render, when `phase === "test"`, the auto-fired test (mirror `ConnectWizard`'s `useEffect` + `TestResult`, using `setupSource.useTest()`), advancing to `"link"` on `ok === true` and offering Retry otherwise. When `phase === "link"`, render:

```tsx
<LinkStep
  platform={selected}
  source={setupSource}
  onFinishLater={() => onNext(nextStep ?? 7)}
  onDone={() => onNext(nextStep ?? 7)}
/>
```

- [ ] **Step 3: Make `DoneScreen` platform-aware**

Replace the `botUsername: string` prop with `{ platform, platformLabel, botIdentity, linked, dmUrl, inviteUrl }`, wired from the new step-7 payload in `applyExtras`. Branch the copy:

- `linked` → `Linked as … ✓`, no instruction to repeat.
- connected + unlinked → name the real platform and bot, show the invite and DM links, and the `/start` instruction.
- nothing connected → today's plain "You're set up".

Never emit the string `Telegram` unless `platform === "telegram"`.

- [ ] **Step 4: Write the tests**

Add to `SetupWizard.test.tsx`: a successful step-5 POST leaves the wizard on step 5 (does **not** render the Done screen); and the Done screen for a Discord platform renders no `/Telegram/` text. Read the file first and reuse its existing MSW/fetch stubbing style.

- [ ] **Step 5: Run tests**

Run: `cd web/ui && npx tsc -b && npx vitest run` and `go test ./web/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/setup/SetupWizard.tsx web/ui/src/pages/setup/SetupWizard.test.tsx web/api_settings.go
git commit -m "feat(web/setup): test and link the chat app during onboarding"
```

---

### Task 6: Discord `/start` slash command + invite scope

**Files:**
- Modify: `internal/gateway/discord.go`
- Test: `internal/gateway/discord_test.go`

**Interfaces:**
- Consumes: `Router.handleStart` via the existing `DispatchFunc`
- Produces: `discordInteractionUserID(*discordgo.InteractionCreate) string`

- [ ] **Step 1: Write the failing tests**

Add to `discord_test.go`:

```go
func TestInviteURLRequestsCommandsScope(t *testing.T) {
	spec, ok := CredSpecFor("discord")
	if !ok {
		t.Fatal("discord credspec missing")
	}
	got := spec.LinkURLs(BotIdentity{UserID: "123"}).InviteURL
	if !strings.Contains(got, "applications.commands") {
		t.Fatalf("invite URL must request applications.commands, got %q", got)
	}
	if !strings.Contains(got, "permissions=0") {
		t.Fatalf("invite URL must keep permissions=0, got %q", got)
	}
}

func TestInteractionUserIDFromBothContexts(t *testing.T) {
	dm := &discordgo.InteractionCreate{Interaction: discordgo.Interaction{
		User: &discordgo.User{ID: "dm-user"},
	}}
	if got := discordInteractionUserID(dm); got != "dm-user" {
		t.Fatalf("dm: got %q", got)
	}
	guild := &discordgo.InteractionCreate{Interaction: discordgo.Interaction{
		Member: &discordgo.Member{User: &discordgo.User{ID: "guild-user"}},
	}}
	if got := discordInteractionUserID(guild); got != "guild-user" {
		t.Fatalf("guild: got %q", got)
	}
	if got := discordInteractionUserID(&discordgo.InteractionCreate{}); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/gateway/ -run "TestInvite|TestInteraction" -v`
Expected: FAIL — missing `applications.commands`; `discordInteractionUserID` undefined.

- [ ] **Step 3: Widen the invite scope**

In `LinkURLs`, change the scope. Note the space must be percent-encoded:

```go
				// applications.commands is required for the app's slash
				// commands to appear in a guild — `bot` alone authorizes the
				// bot user but surfaces no commands. permissions=0 stays:
				// guild permissions do not govern DMs, and the consent screen
				// should keep asking for nothing.
				InviteURL: "https://discord.com/api/oauth2/authorize?client_id=" +
					url.QueryEscape(b.UserID) + "&scope=bot%20applications.commands&permissions=0",
```

- [ ] **Step 4: Resolve the interacting user from both contexts**

```go
// discordInteractionUserID resolves the invoking user. Discord populates
// Interaction.User in a DM and Interaction.Member.User in a guild; reading
// only one returns nil in the other context, which would reintroduce exactly
// the silence the slash command exists to remove.
func discordInteractionUserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.User != nil {
		return i.User.ID
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}
```

- [ ] **Step 5: Register the command and handle the interaction**

In `NewDiscord`, after `sess.AddHandler(g.onMessageCreate)`:

```go
	sess.AddHandler(g.onInteractionCreate)
```

Add to `DiscordGateway`:

```go
// startCommand is the application command registered on Open. Typing "/" in
// Discord opens the command picker, so a message-only /start fights the
// client's own UI — and a /start typed in a guild channel is dropped by
// mapDiscordDM with no reply at all. An application command is delivered as an
// interaction, so it works in BOTH contexts and needs neither the message
// content nor the guild message intent.
var startCommand = &discordgo.ApplicationCommand{
	Name:        "start",
	Description: "Link your account to this workspace",
}

func (g *DiscordGateway) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand || i.ApplicationCommandData().Name != "start" {
		return
	}
	userID := discordInteractionUserID(i)
	if userID == "" {
		return
	}
	// Acknowledge ephemerally first: the dispatch below hits the DB and the
	// router, and Discord expires an unacknowledged interaction in 3 seconds.
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Linking your account — check your direct messages.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	g.dispatch(context.Background(), Message{
		Platform:       "discord",
		PlatformUserID: userID,
		WorkspaceID:    g.ownerWorkspaceID,
		Text:           "/start",
	})
}
```

In `Start`, after a successful `g.session.Open()`:

```go
	// Best-effort: a registration failure must not stop the adapter, because
	// the message-based /start path still works and is the only path on
	// Telegram and Slack.
	if g.session.State != nil && g.session.State.User != nil {
		if _, err := g.session.ApplicationCommandCreate(g.session.State.User.ID, "", startCommand); err != nil {
			slog.Warn("gateway: discord /start command registration failed", "err", err)
		}
	}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/gateway/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/gateway/discord.go internal/gateway/discord_test.go
git commit -m "feat(gateway/discord): register /start as a real application command"
```

---

### Task 7: Full gate and PR

- [ ] **Step 1: Run the whole local gate**

Run: `make ci`
Expected: fmt, vet, `-race` tests, the six-target cross-compile and the UI build all pass.

- [ ] **Step 2: Deploy and smoke-test**

```bash
make deploy
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/v1/auth/session
grep "adapter started" logs/server.log
```

Expected: 200s, and one `adapter started` line per active connection — the observability this work adds.

- [ ] **Step 3: Push and open the PR**

```bash
git push -u origin worktree-discord-onboarding-parity
gh pr create --draft --title "feat(web/setup): chat app onboarding parity and link diagnostics" --body "…"
```

The PR title must be a valid Conventional Commit — squash-merge makes it the commit that lands on `main`, and release-please reads it.

## Self-review

Spec coverage: §1 → Task 3; §2 → Task 4; §3 → Task 5; §4 → Tasks 1, 2, 4; §5 → Task 6. Testing section → covered per task plus Task 7's gate. Out-of-scope items are not implemented.

Type consistency: `PlatformSource` is defined once in Task 4 and consumed by name in Tasks 4 and 5; `bot_online`/`BotOnline` matches across Go and TS; `IsRunning`'s signature is identical in Tasks 1 and 2.
