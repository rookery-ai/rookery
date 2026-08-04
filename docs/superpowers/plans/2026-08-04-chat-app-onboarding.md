# Chat App Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a chat app report "connected" only when the operator can actually use it — by verifying the linking round trip, and by making unprompted delivery deterministic when several apps are linked.

**Architecture:** A platform's `CredSpec` gains a structured `BotIdentity` return from its `Validate` probe plus a `LinkURLs` builder, so all platform-specific knowledge (setup prose, deep links, invite URLs) stays in `internal/gateway` and the SPA renders whatever it is handed. Bot identifiers are persisted as a plain workspace setting so the connectors list endpoint can stay DB-only and therefore cheap to poll. A new wizard step polls that endpoint until a `platform_identities` row appears — the row proves the inbound path end to end.

**Tech Stack:** Go 1.x (Echo v4, `modernc.org/sqlite`), React 19 + TypeScript + Vite, TanStack Query, Tailwind v4, vitest + Testing Library.

## Global Constraints

- **No schema migration.** Bot identifiers and the primary-platform choice both use the existing generic settings table via `db.GetSetting`/`db.SetSetting`. `platform_identities` and `platform_connections` keep their current columns.
- **`GET /api/v1/connectors` must stay DB-only.** No third-party network call may be added to it. The wizard polls it every 2s; token probing stays in the explicit `POST /connectors/:platform/test` action.
- **Discord invite URL uses `permissions=0`.** Guild permissions do not govern 1:1 DMs; requesting none also creates no role on join.
- **`SetupSteps` are credentials-only.** No platform's setup steps may instruct the user to DM the bot or send `/start` — that is wizard step 4's job. A guard test enforces this.
- **No green state or Done button while unlinked.** This is the invariant the whole change exists to establish; asserted by a component test.
- Conventional Commits (`type(scope): summary`). Branch `worktree-chat-onboarding-spec`; never commit to `main`.
- Go tests: `go test ./... -count=1`. Frontend: `cd web/ui && npx vitest run`.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/gateway/credspec.go` | `BotIdentity`, `LinkTargets`, `ValidateFunc`, settings marshalling | 1 |
| `internal/gateway/discord.go` | Parse bot `id`; `LinkURLs` (DM + invite); rewritten `SetupSteps` | 1, 5 |
| `internal/gateway/slack.go` | Return `UserID`/`TeamID`; `LinkURLs`; split `SetupSteps` | 1, 5 |
| `internal/gateway/telegram.go` | `LinkURLs` | 1 |
| `web/server.go` | Telegram `Validate` wrapper returning `BotIdentity` | 1 |
| `web/handlers_connectors.go` | Persist `BotIdentity`; `testConnectorIdentity` signature | 1 |
| `internal/db/repositories.go` | Deterministic identity ordering; `DeletePlatformIdentity` | 2 |
| `internal/gateway/gateway.go` | `SendToUser` primary-first resolution | 3 |
| `internal/gateway/router.go` | `handleStart` per-platform label | 4 |
| `web/api_connectors.go` | `linked`/`linked_identity`/`primary`/link URLs DTO; primary + unlink routes | 6 |
| `web/ui/src/lib/connections.ts` | Mirrored types; primary/unlink hooks | 7 |
| `web/ui/src/pages/connections/ChatAppWizard.tsx` | Step 4; Manage unlink | 7, 8 |
| `web/ui/src/pages/connections/ConnectionsPage.tsx` | Linked badges; primary radio | 8 |

---

### Task 1: `BotIdentity` — structured validation, deep links, persistence

`CredSpec.Validate` returns a bare display string today, so the bot's user id (which Discord's invite URL needs) is unavailable. Widen it to a struct, add a per-platform deep-link builder, and persist the result as a workspace setting.

**Files:**
- Modify: `internal/gateway/credspec.go`
- Modify: `internal/gateway/discord.go:25-63`
- Modify: `internal/gateway/slack.go:29-61`
- Modify: `internal/gateway/telegram.go:217-228`
- Modify: `web/server.go:145-147`
- Modify: `web/handlers_connectors.go:20-70`, `:74-103`
- Test: `internal/gateway/credspec_test.go`, `internal/gateway/discord_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `gateway.BotIdentity{Username, UserID, TeamID string}` with `MarshalSetting() (string, error)` and `gateway.BotIdentityFromSetting(s string) BotIdentity`
  - `gateway.LinkTargets{DMURL, InviteURL string}`
  - `CredSpec.Validate func(map[string]string) (BotIdentity, error)`
  - `CredSpec.LinkURLs func(BotIdentity) LinkTargets`
  - `gateway.BotIdentitySettingKey(platform string) string` → `"bot_identity.<platform>"`
  - `Server.testConnectorIdentity(workspaceID, platform string) (gateway.BotIdentity, error)`
  - `Server.saveConnector(...) (identity gateway.BotIdentity, botStartErr, err error)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/gateway/credspec_test.go`:

```go
func TestBotIdentitySettingRoundTrip(t *testing.T) {
	in := BotIdentity{Username: "rookery_bot", UserID: "123456789", TeamID: "T01"}
	s, err := in.MarshalSetting()
	if err != nil {
		t.Fatal(err)
	}
	if got := BotIdentityFromSetting(s); got != in {
		t.Fatalf("round trip: got %+v want %+v", got, in)
	}
}

func TestBotIdentityFromSettingToleratesGarbage(t *testing.T) {
	// A setting written by an older build (or hand-edited) must degrade to an
	// empty identity, never panic — the wizard falls back to prose links.
	for _, s := range []string{"", "not json", "{", `{"username":123}`} {
		if got := BotIdentityFromSetting(s); got != (BotIdentity{}) {
			t.Fatalf("BotIdentityFromSetting(%q) = %+v, want zero", s, got)
		}
	}
}

func TestBotIdentitySettingKeyIsPerPlatform(t *testing.T) {
	if got := BotIdentitySettingKey("discord"); got != "bot_identity.discord" {
		t.Fatalf("key = %q", got)
	}
}
```

Append to `internal/gateway/discord_test.go`:

```go
func TestDiscordLinkURLsBuildInviteWithNoPermissions(t *testing.T) {
	spec, ok := CredSpecFor("discord")
	if !ok {
		t.Fatal("discord spec not registered")
	}
	if spec.LinkURLs == nil {
		t.Fatal("discord spec has no LinkURLs builder")
	}
	got := spec.LinkURLs(BotIdentity{Username: "rookery_bot", UserID: "987654321"})

	// permissions=0 is deliberate: guild permissions do not govern 1:1 DMs,
	// and a no-permission invite creates no role and asks for nothing.
	want := "https://discord.com/api/oauth2/authorize?client_id=987654321&scope=bot&permissions=0"
	if got.InviteURL != want {
		t.Fatalf("InviteURL = %q, want %q", got.InviteURL, want)
	}
	if got.DMURL != "https://discord.com/users/987654321" {
		t.Fatalf("DMURL = %q", got.DMURL)
	}
}

func TestDiscordLinkURLsEmptyWithoutBotID(t *testing.T) {
	spec, _ := CredSpecFor("discord")
	got := spec.LinkURLs(BotIdentity{})
	if got.InviteURL != "" || got.DMURL != "" {
		t.Fatalf("expected empty targets without a bot id, got %+v", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gateway/ -run 'BotIdentity|DiscordLinkURLs' -v`
Expected: FAIL — `undefined: BotIdentity`, `undefined: BotIdentitySettingKey`.

- [ ] **Step 3: Add the types to `internal/gateway/credspec.go`**

Add `"strings"` to the import block, then replace the `CredSpec` declaration and add the new types:

```go
// BotIdentity is what a platform's Validate probe learns about the BOT account
// behind a set of credentials. Every field is a PUBLIC identifier — a bot's
// username and id are meant to be shared, and Discord's invite URL embeds the
// id — so this is persisted as a plain workspace setting rather than in the
// encrypted config blob. Keeping it out of ciphertext is what lets the
// connectors list endpoint stay DB-only and therefore cheap to poll.
type BotIdentity struct {
	Username string `json:"username,omitempty"` // display handle, e.g. "rookery_bot"
	UserID   string `json:"user_id,omitempty"`  // the bot's platform user id; on Discord this is ALSO the application id
	TeamID   string `json:"team_id,omitempty"`  // Slack only: the team id its DM deep link needs
}

// LinkTargets are the platform-specific URLs the wizard's linking step renders.
// They are built here rather than in the SPA so that adding a platform stays a
// gateway-package change alone.
type LinkTargets struct {
	DMURL     string // opens a DM with the bot
	InviteURL string // Discord only: adds the bot to a server, a PREREQUISITE for DMs
}

// MarshalSetting encodes the identity for db.SetSetting.
func (b BotIdentity) MarshalSetting() (string, error) {
	out, err := json.Marshal(b)
	return string(out), err
}

// BotIdentityFromSetting decodes a value written by MarshalSetting. It never
// errors: a missing, truncated or hand-edited setting degrades to an empty
// identity, and the wizard falls back to prose instructions.
func BotIdentityFromSetting(s string) BotIdentity {
	var b BotIdentity
	if json.Unmarshal([]byte(s), &b) != nil {
		return BotIdentity{}
	}
	return b
}

// BotIdentitySettingKey namespaces the identity per platform in the shared
// workspace settings table.
func BotIdentitySettingKey(platform string) string {
	return "bot_identity." + strings.ToLower(platform)
}

type CredSpec struct {
	Platform   string
	Label      string // human display name, e.g. "Discord"
	Blurb      string // one-line description for the connector card
	Fields     []CredField
	SetupURL   string
	SetupSteps []string
	// Validate probes the credentials against the platform and reports the bot
	// account behind them. Nil means "nothing to probe".
	Validate func(values map[string]string) (BotIdentity, error)
	// LinkURLs builds the deep links the wizard's linking step shows. Nil means
	// the step falls back to prose ("open a DM with the bot").
	LinkURLs func(b BotIdentity) LinkTargets
}
```

- [ ] **Step 4: Update Discord's validator and register `LinkURLs`**

In `internal/gateway/discord.go`, change `validateDiscordToken` to parse `id` as well as `username`, and return a `BotIdentity`:

```go
func validateDiscordToken(token string) (BotIdentity, error) {
	req, err := http.NewRequest(http.MethodGet, discordAPIBase+"/users/@me", nil)
	if err != nil {
		return BotIdentity{}, err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return BotIdentity{}, fmt.Errorf("discord api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return BotIdentity{}, fmt.Errorf("discord rejected token (status %d)", resp.StatusCode)
	}
	var out struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	// A bot's user id IS its application id, which is what the invite URL needs.
	if err := json.Unmarshal(body, &out); err != nil || out.Username == "" {
		return BotIdentity{}, fmt.Errorf("invalid response from discord")
	}
	return BotIdentity{Username: out.Username, UserID: out.ID}, nil
}
```

In the same file's `RegisterCredSpec` call, update `Validate` and add `LinkURLs`:

```go
		Validate: func(v map[string]string) (BotIdentity, error) { return validateDiscordToken(v["token"]) },
		LinkURLs: func(b BotIdentity) LinkTargets {
			if b.UserID == "" {
				return LinkTargets{}
			}
			return LinkTargets{
				DMURL: "https://discord.com/users/" + b.UserID,
				// permissions=0: guild permissions do not govern 1:1 DMs, so a
				// DM-only bot needs none. It also creates no role on join and
				// the consent screen asks for nothing.
				InviteURL: "https://discord.com/api/oauth2/authorize?client_id=" +
					b.UserID + "&scope=bot&permissions=0",
			}
		},
```

- [ ] **Step 5: Update Slack and Telegram**

In `internal/gateway/slack.go`:

```go
func validateSlackToken(botToken string) (BotIdentity, error) {
	resp, err := slackAPINew(botToken).AuthTest()
	if err != nil {
		return BotIdentity{}, fmt.Errorf("slack rejected bot token: %w", err)
	}
	return BotIdentity{Username: resp.User, UserID: resp.UserID, TeamID: resp.TeamID}, nil
}
```

and in its `RegisterCredSpec` call:

```go
		Validate: func(v map[string]string) (BotIdentity, error) {
			if v["app_token"] == "" {
				return BotIdentity{}, fmt.Errorf("app-level token (xapp-) is required for Socket Mode")
			}
			return validateSlackToken(v["token"])
		},
		LinkURLs: func(b BotIdentity) LinkTargets {
			if b.UserID == "" || b.TeamID == "" {
				return LinkTargets{}
			}
			// The slack:// scheme opens the desktop app; browser-only users get
			// the app.slack.com equivalent, which resolves the same DM.
			return LinkTargets{DMURL: "https://app.slack.com/client/" + b.TeamID + "/" + b.UserID}
		},
```

In `internal/gateway/telegram.go`'s `RegisterCredSpec` call, add:

```go
		LinkURLs: func(b BotIdentity) LinkTargets {
			if b.Username == "" {
				return LinkTargets{}
			}
			return LinkTargets{DMURL: "https://t.me/" + strings.TrimPrefix(b.Username, "@")}
		},
```

Ensure `strings` is imported in `telegram.go`.

- [ ] **Step 6: Update the web-layer consumers**

In `web/server.go:145-147`:

```go
	if spec, ok := gateway.CredSpecFor("telegram"); ok && spec.Validate == nil {
		spec.Validate = func(v map[string]string) (gateway.BotIdentity, error) {
			return testTelegramToken(v["token"])
		}
```

In `web/handlers_connectors.go`, change `testTelegramToken` to return a `BotIdentity` (Telegram's `getMe` returns `id` alongside `username` — parse both):

```go
func testTelegramToken(token string) (gateway.BotIdentity, error) {
	resp, err := http.Get("https://api.telegram.org/bot" + token + "/getMe")
	if err != nil {
		return gateway.BotIdentity{}, fmt.Errorf("telegram api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return gateway.BotIdentity{}, fmt.Errorf("invalid response from telegram")
	}
	if !result.OK {
		return gateway.BotIdentity{}, fmt.Errorf("telegram rejected token: %s", result.Description)
	}
	return gateway.BotIdentity{
		Username: result.Result.Username,
		UserID:   strconv.FormatInt(result.Result.ID, 10),
	}, nil
}
```

Add `"strconv"` to that file's imports. Then update the two call sites in the same file — `saveConnector`'s signature and probe:

```go
func (s *Server) saveConnector(workspaceID, platform string, values map[string]string) (identity gateway.BotIdentity, botStartErr error, err error) {
	spec, ok := gateway.CredSpecFor(platform)
	if !ok {
		return gateway.BotIdentity{}, nil, fmt.Errorf("unsupported platform: %s", platform)
	}
	if spec.Validate != nil {
		if identity, err = spec.Validate(values); err != nil {
			return gateway.BotIdentity{}, nil, fmt.Errorf("invalid credentials: %w", err)
		}
	}
```

Every other `return "", nil, …` in that function becomes `return gateway.BotIdentity{}, nil, …`, and the final success return keeps `identity`.

Replace the existing telegram-only setting write (`handlers_connectors.go:60`) with a generic persist, keeping the legacy key because `web/api_settings.go:445` still reads it:

```go
	// Persist the bot's public identifiers so the connectors list endpoint can
	// build deep links without a network call. Best-effort: a failure here must
	// not fail an otherwise-good connect.
	if encoded, mErr := identity.MarshalSetting(); mErr == nil {
		_ = s.db.SetSetting(workspaceID, gateway.BotIdentitySettingKey(platform), encoded)
	}
	if platform == "telegram" && identity.Username != "" {
		// Legacy key, still read by the settings page.
		_ = s.db.SetSetting(workspaceID, "telegram_bot_username", "@"+identity.Username)
	}
```

Finally, `testConnectorIdentity`:

```go
func (s *Server) testConnectorIdentity(workspaceID, platform string) (gateway.BotIdentity, error) {
```

with its `return "", nil` becoming `return gateway.BotIdentity{}, nil`, its `spec.Validate == nil` branch returning `gateway.BotIdentity{}, nil`, and every early error return returning `gateway.BotIdentity{}, err`.

- [ ] **Step 7: Fix the two API call sites so the package compiles**

In `web/api_connectors.go`, `apiSaveConnector` and `apiTestConnector` both assign a string. Take the username:

```go
	resp := apiSaveConnectorResponse{OK: true, Identity: identity.Username}
```

```go
	identity, err := s.testConnectorIdentity(u.ID, platform)
	if err != nil {
		return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: false, Error: err.Error()})
	}
	return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: true, Identity: identity.Username})
```

- [ ] **Step 8: Run the full Go suite**

Run: `go test ./... -count=1`
Expected: PASS. If `web/connectors_test.go` or `internal/gateway/*_test.go` reference the old string return, update those assertions to use `.Username`.

- [ ] **Step 9: Commit**

```bash
git add internal/gateway/ web/ && \
git commit -m "feat(gateway): structured BotIdentity from Validate, with deep links

A bot's user id was discarded by Validate's bare-string return, so nothing
could build Discord's invite URL. Widen the probe to a BotIdentity struct,
add a per-platform LinkURLs builder so deep links stay gateway-package
knowledge, and persist the identifiers as a plain workspace setting — they
are public values, and keeping them out of ciphertext is what lets the
connectors list endpoint stay DB-only."
```

---

### Task 2: Deterministic identity ordering and `DeletePlatformIdentity`

`ListPlatformIdentities` has no `ORDER BY`, so delivery fallback order depends on SQLite rowid. There is also no way to delete an identity, which the Unlink action needs.

**Files:**
- Modify: `internal/db/repositories.go:364-395` (add `ORDER BY`), and append a new method after it
- Test: `internal/db/platform_identity_test.go` (create)

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `(*DB).DeletePlatformIdentity(workspaceID, platform string) error`; `ListPlatformIdentities` ordered by `linked_at, platform, id`.

- [ ] **Step 1: Write the failing test**

Create `internal/db/platform_identity_test.go`:

```go
package db_test

import (
	"path/filepath"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

func newIdentityTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws1", Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return database
}

// Identities linked in the same second tie on linked_at, which is stored as a
// string. Without the id tiebreaker the "deterministic" fallback order is
// undefined in exactly the case it exists to pin down.
func TestListPlatformIdentitiesIsDeterministic(t *testing.T) {
	database := newIdentityTestDB(t)
	for _, id := range []struct{ rowID, platform string }{
		{"id-c", "telegram"},
		{"id-a", "slack"},
		{"id-b", "discord"},
	} {
		if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
			ID: id.rowID, WorkspaceID: "ws1", Platform: id.platform, PlatformUserID: "u-" + id.platform,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id.platform, err)
		}
	}

	var first []string
	for i := 0; i < 5; i++ {
		rows, err := database.ListPlatformIdentities("ws1", "")
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(rows))
		for j, r := range rows {
			got[j] = r.Platform
		}
		if i == 0 {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("row count changed: %v vs %v", got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("order not stable: run %d = %v, run 0 = %v", i, got, first)
			}
		}
	}

	// All three tie on linked_at, so the id tiebreaker decides: id-a, id-b, id-c.
	want := []string{"slack", "discord", "telegram"}
	for i := range want {
		if first[i] != want[i] {
			t.Fatalf("order = %v, want %v", first, want)
		}
	}
}

func TestDeletePlatformIdentityRemovesOnlyThatPlatform(t *testing.T) {
	database := newIdentityTestDB(t)
	for _, p := range []string{"telegram", "discord"} {
		if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
			ID: "id-" + p, WorkspaceID: "ws1", Platform: p, PlatformUserID: "u-" + p,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := database.DeletePlatformIdentity("ws1", "discord"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	rows, err := database.ListPlatformIdentities("ws1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Platform != "telegram" {
		t.Fatalf("after delete, rows = %+v", rows)
	}

	// Deleting an absent identity is a no-op, not an error — the Unlink button
	// may race a link that was already removed.
	if err := database.DeletePlatformIdentity("ws1", "discord"); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/db/ -run 'PlatformIdentit' -v`
Expected: FAIL — `database.DeletePlatformIdentity undefined`.

- [ ] **Step 3: Add the `ORDER BY` and the delete method**

In `internal/db/repositories.go`, both queries inside `ListPlatformIdentities` gain the same ordering clause:

```go
	if platform == "" {
		rows, err = d.Query(`SELECT id,workspace_id,platform,platform_user_id,linked_at
			FROM platform_identities WHERE workspace_id=?
			ORDER BY linked_at, platform, id`, workspaceID)
	} else {
		rows, err = d.Query(`SELECT id,workspace_id,platform,platform_user_id,linked_at
			FROM platform_identities WHERE workspace_id=? AND platform=?
			ORDER BY linked_at, platform, id`, workspaceID, platform)
	}
```

Append after the function:

```go
// DeletePlatformIdentity unlinks a workspace from one chat platform. Deleting an
// identity that is not there is a no-op: the Unlink action can race a link that
// was already removed, and reporting that as an error would be noise.
func (d *DB) DeletePlatformIdentity(workspaceID, platform string) error {
	_, err := d.Exec(`DELETE FROM platform_identities WHERE workspace_id=? AND platform=?`,
		workspaceID, platform)
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/db/ -run 'PlatformIdentit' -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/db/ && \
git commit -m "fix(db): order platform identities deterministically, add delete

linked_at is stored as a string, so identities created in the same second
tie and the fallback delivery order was left to SQLite rowid. Order by
linked_at, platform, id. Adds DeletePlatformIdentity for the Unlink action."
```

---

### Task 3: Primary app for unprompted delivery

`SendToUser` iterates identities first-success-wins over an unordered list. Make the target explicit and configurable.

**Files:**
- Modify: `internal/gateway/gateway.go:220-235`
- Test: `internal/gateway/sendtouser_test.go` (create)

**Interfaces:**
- Consumes: `ListPlatformIdentities` ordering from Task 2.
- Produces: `gateway.PrimaryPlatformSettingKey = "chat.primary_platform"` (exported const); `SendToUser` resolves primary-first.

- [ ] **Step 1: Write the failing test**

Create `internal/gateway/sendtouser_test.go`:

```go
package gateway_test

import (
	"path/filepath"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
)

func TestPrimaryPlatformSettingKeyIsStable(t *testing.T) {
	// The SPA and the settings row both hardcode this string; changing it
	// silently resets every workspace's choice.
	if gateway.PrimaryPlatformSettingKey != "chat.primary_platform" {
		t.Fatalf("key = %q", gateway.PrimaryPlatformSettingKey)
	}
}

func TestResolveDeliveryOrderPrefersPrimary(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"), "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws1", Name: "w"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"discord", "telegram"} {
		if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
			ID: "id-" + p, WorkspaceID: "ws1", Platform: p, PlatformUserID: "u-" + p,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Unset primary: defined order (first linked), not arbitrary.
	got, err := gateway.ResolveDeliveryOrder(database, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Platform != "discord" {
		t.Fatalf("unset primary: order = %+v, want discord first", got)
	}

	// Set primary: it moves to the front, the rest keep their relative order
	// so a primary that is down still falls back predictably.
	if err := database.SetSetting("ws1", gateway.PrimaryPlatformSettingKey, "telegram"); err != nil {
		t.Fatal(err)
	}
	got, err = gateway.ResolveDeliveryOrder(database, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Platform != "telegram" || got[1].Platform != "discord" {
		t.Fatalf("with primary: order = %+v", got)
	}

	// A primary naming a platform that is no longer linked must not drop the
	// remaining targets — otherwise unlinking the primary silences delivery.
	if err := database.SetSetting("ws1", gateway.PrimaryPlatformSettingKey, "slack"); err != nil {
		t.Fatal(err)
	}
	got, err = gateway.ResolveDeliveryOrder(database, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Platform != "discord" {
		t.Fatalf("stale primary: order = %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/gateway/ -run 'PrimaryPlatform|ResolveDeliveryOrder' -v`
Expected: FAIL — `undefined: gateway.PrimaryPlatformSettingKey`.

- [ ] **Step 3: Implement the resolver and rewrite `SendToUser`**

In `internal/gateway/gateway.go`, replace `SendToUser` and add the resolver above it:

```go
// PrimaryPlatformSettingKey names the workspace setting holding which linked
// chat app receives UNPROMPTED delivery (scheduled agent runs, reminders).
// Replies to a message the user typed always go back to the platform it arrived
// on — that is handled in dispatch, not here.
const PrimaryPlatformSettingKey = "chat.primary_platform"

// ResolveDeliveryOrder returns this workspace's linked identities with the
// configured primary first. An unset or stale primary is not an error: the
// order simply falls back to ListPlatformIdentities' deterministic ordering, so
// unlinking the primary can never silence delivery altogether.
func ResolveDeliveryOrder(database *db.DB, workspaceID string) ([]*db.PlatformIdentity, error) {
	identities, err := database.ListPlatformIdentities(workspaceID, "")
	if err != nil {
		return nil, err
	}
	primary, _ := database.GetSetting(workspaceID, PrimaryPlatformSettingKey)
	if primary == "" || len(identities) < 2 {
		return identities, nil
	}
	ordered := make([]*db.PlatformIdentity, 0, len(identities))
	for _, i := range identities {
		if i.Platform == primary {
			ordered = append(ordered, i)
		}
	}
	for _, i := range identities {
		if i.Platform != primary {
			ordered = append(ordered, i)
		}
	}
	return ordered, nil
}

// SendToUser delivers an unprompted message to the workspace's primary chat app,
// falling back through the remaining linked apps if it fails.
// Satisfies the reminder.Sender interface.
func (m *GatewayManager) SendToUser(workspaceID, text string) error {
	identities, err := ResolveDeliveryOrder(m.db, workspaceID)
	if err != nil || len(identities) == 0 {
		return fmt.Errorf("no platform identity for user %s", workspaceID)
	}
	for _, identity := range identities {
		if err := m.Send(identity.Platform, workspaceID, identity.PlatformUserID, text); err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to deliver message to user %s on any platform", workspaceID)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/gateway/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/sendtouser_test.go && \
git commit -m "feat(gateway): primary chat app for unprompted delivery

With two apps linked, which one received a scheduled run or reminder was
undefined. Resolve a configurable primary first and fall back through the
rest in deterministic order. A stale primary naming an unlinked platform
degrades to the fallback rather than silencing delivery."
```

---

### Task 4: `handleStart` reports the right platform

Linking via Discord or Slack replies "Your **Telegram** account is now linked."

**Files:**
- Modify: `internal/gateway/router.go:199-233`
- Test: `internal/gateway/router_test.go` (append)

**Interfaces:**
- Consumes: `CredSpecFor` (existing).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Append to `internal/gateway/router_test.go`:

```go
func TestHandleStartNamesTheActualPlatform(t *testing.T) {
	for _, tc := range []struct{ platform, want string }{
		{"telegram", "Telegram"},
		{"discord", "Discord"},
		{"slack", "Slack"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			r, _, _, _ := newTestRouter(t)
			msg := testMsg("/start")
			msg.Platform = tc.platform
			msg.PlatformUserID = "user-" + tc.platform

			var got string
			if err := r.Handle(t.Context(), msg, func(s string) { got = s }); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("reply %q does not name %q", got, tc.want)
			}
			for _, other := range []string{"Telegram", "Discord", "Slack"} {
				if other != tc.want && strings.Contains(got, other) {
					t.Fatalf("reply %q names the wrong platform %q", got, other)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/gateway/ -run TestHandleStartNamesTheActualPlatform -v`
Expected: FAIL on the `discord` and `slack` subtests — the reply names Telegram.

- [ ] **Step 3: Read the label from the CredSpec**

In `internal/gateway/router.go`, replace the final send in `handleStart`:

```go
	// The platform's own label, so linking via Discord does not claim Telegram.
	label := msg.Platform
	if spec, ok := CredSpecFor(msg.Platform); ok && spec.Label != "" {
		label = spec.Label
	}

	w, err := r.db.GetWorkspaceByID(msg.WorkspaceID)
	if err != nil {
		send("Linked successfully! Send /help to get started.")
		return nil
	}

	send(fmt.Sprintf("Hi **%s**! Your %s account is now linked. Send /help to see what you can do.",
		w.Name, label))
	return nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/gateway/ -count=1`
Expected: PASS. Add `"strings"` to the test file's imports if it is not already there.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/router.go internal/gateway/router_test.go && \
git commit -m "fix(gateway): name the actual platform when linking

handleStart hardcoded 'Telegram', so linking via Discord or Slack confirmed
the wrong app. Read the label from the platform's CredSpec."
```

---

### Task 5: Credentials-only setup steps, with a guard test

Discord's steps contain an instruction that cannot work; Slack's step 3 crams four actions into one line.

**Files:**
- Modify: `internal/gateway/discord.go` (`SetupSteps`)
- Modify: `internal/gateway/slack.go` (`SetupSteps`)
- Test: `internal/gateway/setupsteps_test.go` (create)

**Interfaces:**
- Consumes: `CredSpecs()` (existing).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Create `internal/gateway/setupsteps_test.go`:

```go
package gateway

import (
	"strings"
	"testing"
)

// Linking is wizard step 4's job, uniform across platforms. A setup step that
// tells the user to DM the bot is how the false "OR just DM it after
// connecting" branch got in — it read plausibly and nothing checked it.
func TestSetupStepsDoNotInstructLinking(t *testing.T) {
	banned := []string{"/start", "dm it", "dm your bot", "dm the bot", "message the bot"}
	for _, spec := range CredSpecs() {
		for i, step := range spec.SetupSteps {
			lower := strings.ToLower(step)
			for _, b := range banned {
				if strings.Contains(lower, b) {
					t.Errorf("%s step %d instructs linking (%q): %q", spec.Platform, i+1, b, step)
				}
			}
		}
	}
}

// Without MESSAGE CONTENT INTENT the bot connects, reports healthy and receives
// every DM with an empty body — a silent failure worth pinning.
func TestDiscordSetupStepsNameTheMessageContentIntent(t *testing.T) {
	spec, ok := CredSpecFor("discord")
	if !ok {
		t.Fatal("discord spec not registered")
	}
	joined := strings.ToUpper(strings.Join(spec.SetupSteps, "\n"))
	if !strings.Contains(joined, "MESSAGE CONTENT INTENT") {
		t.Fatalf("discord steps never name the intent:\n%s", strings.Join(spec.SetupSteps, "\n"))
	}
}

// Every step should be one action. A step carrying several semicolons is the
// dense-instruction smell that made Slack's step 3 unfollowable.
func TestSetupStepsAreSingleActions(t *testing.T) {
	for _, spec := range CredSpecs() {
		for i, step := range spec.SetupSteps {
			if strings.Count(step, ";") > 1 {
				t.Errorf("%s step %d packs several actions: %q", spec.Platform, i+1, step)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/gateway/ -run 'SetupSteps' -v`
Expected: FAIL — Discord and Slack steps mention `/start`; Slack's step 3 has multiple semicolons.

- [ ] **Step 3: Rewrite Discord's steps**

In `internal/gateway/discord.go`'s `RegisterCredSpec` call:

```go
		SetupSteps: []string{
			"Open the Discord Developer Portal and click New Application",
			"Open the Bot tab, click Reset Token, and copy the token",
			"Still on the Bot tab, enable the MESSAGE CONTENT INTENT under Privileged Gateway Intents",
		},
```

The invite instruction is deliberately gone: step 4 renders a generated invite button, which is a click rather than a portal walkthrough.

- [ ] **Step 4: Split Slack's steps**

In `internal/gateway/slack.go`'s `RegisterCredSpec` call:

```go
		SetupSteps: []string{
			"Create a Slack app at api.slack.com/apps, choosing From scratch",
			"Open Socket Mode and enable it",
			"Generate an App-Level Token with the connections:write scope, then copy the xapp- token",
			"Open OAuth & Permissions and add the bot scopes chat:write, im:history, im:write and files:read",
			"Click Install to Workspace, then copy the xoxb- Bot Token",
			"Open Event Subscriptions, enable it, and subscribe to the bot event message.im",
			"Open App Home, enable the Messages Tab, and allow users to send messages from it",
		},
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/gateway/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/ && \
git commit -m "fix(gateway): setup steps describe credentials only

Discord's steps told users to 'invite the bot to a server OR just DM it
after connecting'. The OR branch cannot work — Discord permits a bot DM only
between parties sharing a guild — and the invite branch had no instructions.
Linking moves to the wizard; these steps now cover credentials alone, and a
guard test fails the build if one instructs linking again."
```

---

### Task 6: Link status, primary and unlink on the API

**Files:**
- Modify: `web/api_connectors.go`
- Test: `web/api_connectors_test.go` (append)

**Interfaces:**
- Consumes: `BotIdentityFromSetting`, `BotIdentitySettingKey`, `CredSpec.LinkURLs` (Task 1); `DeletePlatformIdentity` (Task 2); `PrimaryPlatformSettingKey` (Task 3).
- Produces: `apiConnectorPlatform` gains `Linked bool`, `LinkedIdentity string`, `Primary bool`, `DMURL string`, `InviteURL string`; routes `PUT /connectors/:platform/primary` and `DELETE /connectors/:platform/identity`.

- [ ] **Step 1: Write the failing test**

Append to `web/api_connectors_test.go`, using the harness that file already relies on — `newAPITestServer(t) (*Server, *db.DB)`, `bootstrapAndLogin(t, s) []*http.Cookie`, `createAndEnterWorkspace(t, s, cookies) ([]*http.Cookie, string)` (the string is the workspace id), and `doJSON(t, s, method, path, body, cookies) *httptest.ResponseRecorder`:

```go
// decodeConnectorList unwraps GET /api/v1/connectors into the DTO.
func decodeConnectorList(t *testing.T, rec *httptest.ResponseRecorder) []apiConnectorPlatform {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body apiConnectorListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — body %s", err, rec.Body.String())
	}
	return body.Platforms
}

func findPlatform(t *testing.T, list []apiConnectorPlatform, name string) apiConnectorPlatform {
	t.Helper()
	for _, p := range list {
		if p.Platform == name {
			return p
		}
	}
	t.Fatalf("%s not present in the platform list", name)
	return apiConnectorPlatform{}
}

func TestAPIConnectors_GET_ReportsLinkState(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	list := decodeConnectorList(t, doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies))
	for _, p := range list {
		if p.Linked {
			t.Fatalf("%s reported linked with no identity row", p.Platform)
		}
	}

	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID: "id1", WorkspaceID: wsID, Platform: "telegram", PlatformUserID: "1843540314",
	}); err != nil {
		t.Fatal(err)
	}

	list = decodeConnectorList(t, doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies))
	tg := findPlatform(t, list, "telegram")
	if !tg.Linked || tg.LinkedIdentity != "1843540314" {
		t.Fatalf("telegram: linked=%v identity=%q", tg.Linked, tg.LinkedIdentity)
	}
	// The sole linked platform is the implicit primary.
	if !tg.Primary {
		t.Fatal("sole linked platform should be primary")
	}
	if dc := findPlatform(t, list, "discord"); dc.Linked {
		t.Fatal("discord should not be linked")
	}
}

func TestAPIConnectors_GET_BuildsDiscordInviteFromStoredIdentity(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	encoded, err := gateway.BotIdentity{Username: "rookery_bot", UserID: "42"}.MarshalSetting()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting(wsID, gateway.BotIdentitySettingKey("discord"), encoded); err != nil {
		t.Fatal(err)
	}

	list := decodeConnectorList(t, doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies))
	dc := findPlatform(t, list, "discord")
	if !strings.Contains(dc.InviteURL, "client_id=42") {
		t.Fatalf("InviteURL = %q", dc.InviteURL)
	}
	// permissions=0 is load-bearing: guild permissions do not govern 1:1 DMs.
	if !strings.Contains(dc.InviteURL, "permissions=0") {
		t.Fatalf("invite must request no permissions: %q", dc.InviteURL)
	}
	if dc.DMURL != "https://discord.com/users/42" {
		t.Fatalf("DMURL = %q", dc.DMURL)
	}
	if dc.Identity != "rookery_bot" {
		t.Fatalf("Identity = %q", dc.Identity)
	}
}

func TestAPIConnectors_Primary_RequiresALinkedPlatform(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Unlinked: refused, so the setting can never name an unreachable target.
	rec := doJSON(t, s, http.MethodPut, "/api/v1/connectors/discord/primary", nil, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unlinked platform, got %d: %s", rec.Code, rec.Body.String())
	}

	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID: "id1", WorkspaceID: wsID, Platform: "discord", PlatformUserID: "u1",
	}); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/connectors/discord/primary", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := database.GetSetting(wsID, gateway.PrimaryPlatformSettingKey); got != "discord" {
		t.Fatalf("primary setting = %q", got)
	}
}

func TestAPIConnectors_Unlink_KeepsCredentialsAndClearsPrimary(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID: "id1", WorkspaceID: wsID, Platform: "discord", PlatformUserID: "u1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting(wsID, gateway.PrimaryPlatformSettingKey, "discord"); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/connectors/discord/identity", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rows, err := database.ListPlatformIdentities(wsID, "discord")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("identity survived unlink: %+v", rows)
	}
	// A primary naming a now-unlinked platform must not persist.
	if got, _ := database.GetSetting(wsID, gateway.PrimaryPlatformSettingKey); got != "" {
		t.Fatalf("stale primary survived: %q", got)
	}
}
```

Ensure the file imports `encoding/json`, `net/http/httptest`, `strings`, `github.com/ilijad1/rookery/internal/db` and `github.com/ilijad1/rookery/internal/gateway`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./web/ -run 'TestAPIConnectors_(GET_ReportsLinkState|GET_BuildsDiscordInvite|Primary_|Unlink_)' -v`
Expected: FAIL to compile — `p.Linked undefined`, `gateway.BotIdentitySettingKey undefined` if Task 1 is not yet merged.

- [ ] **Step 3: Extend the DTO and the list builder**

In `web/api_connectors.go`, extend the struct:

```go
type apiConnectorPlatform struct {
	Platform   string              `json:"platform"`
	Label      string              `json:"label"`
	Blurb      string              `json:"blurb"`
	SetupSteps []string            `json:"setup_steps"`
	Fields     []apiConnectorField `json:"fields"`
	Connected  bool                `json:"connected"`
	Identity   string              `json:"identity"` // the BOT's username
	// Linked reports whether the OPERATOR has completed the /start handshake.
	// Connected means only that the token authenticates; Linked is what makes
	// the integration usable, and the two are routinely different.
	Linked         bool   `json:"linked"`
	LinkedIdentity string `json:"linked_identity"` // the operator's platform user id
	Primary        bool   `json:"primary"`         // receives unprompted delivery
	DMURL          string `json:"dm_url"`
	InviteURL      string `json:"invite_url"`
}
```

Rewrite `connectorPlatformList`'s body from the `if conn, err := …` block onward. This stays DB-only — every value is a settings read or an identity row:

```go
	// One read for the whole list rather than one per platform.
	identities, _ := s.db.ListPlatformIdentities(u.ID, "")
	linkedBy := make(map[string]*db.PlatformIdentity, len(identities))
	for _, i := range identities {
		linkedBy[i.Platform] = i
	}
	primary, _ := s.db.GetSetting(u.ID, gateway.PrimaryPlatformSettingKey)
	if primary == "" && len(identities) > 0 {
		// Unset primary means "first linked" — defined, not arbitrary.
		primary = identities[0].Platform
	}

	for _, spec := range specs {
		// … fields loop and entry construction unchanged …

		if conn, err := s.db.GetPlatformConnection(u.ID, spec.Platform); err == nil {
			entry.Connected = conn.Active
		}

		bot := gateway.BotIdentityFromSetting(mustSetting(s, u.ID, gateway.BotIdentitySettingKey(spec.Platform)))
		entry.Identity = bot.Username
		if spec.LinkURLs != nil {
			targets := spec.LinkURLs(bot)
			entry.DMURL, entry.InviteURL = targets.DMURL, targets.InviteURL
		}

		if id, ok := linkedBy[spec.Platform]; ok {
			entry.Linked = true
			entry.LinkedIdentity = id.PlatformUserID
			entry.Primary = spec.Platform == primary
		}

		out = append(out, entry)
	}
	return out
}

// mustSetting reads a setting, treating a miss as empty — every caller here
// degrades gracefully on absence.
func mustSetting(s *Server, workspaceID, key string) string {
	v, _ := s.db.GetSetting(workspaceID, key)
	return v
}
```

Delete the old telegram-only `entry.Identity` special case and its comment — the identity now comes from the generic setting for every platform.

- [ ] **Step 4: Add the primary and unlink routes**

In `registerConnectorsAPI`:

```go
	g.PUT("/connectors/:platform/primary", s.apiSetPrimaryConnector)
	g.DELETE("/connectors/:platform/identity", s.apiUnlinkConnector)
```

And the handlers:

```go
// apiSetPrimaryConnector chooses which linked chat app receives unprompted
// delivery. PUT /api/v1/connectors/:platform/primary → 200 {"ok":true}.
func (s *Server) apiSetPrimaryConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}
	// Only a LINKED platform can be primary; otherwise the setting names a
	// target that can never receive anything.
	rows, err := s.db.ListPlatformIdentities(u.ID, platform)
	if err != nil || len(rows) == 0 {
		return jsonErr(c, http.StatusBadRequest, "not_linked",
			"link "+platform+" before making it the primary app")
	}
	if err := s.db.SetSetting(u.ID, gateway.PrimaryPlatformSettingKey, platform); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save primary app")
	}
	s.audit.Log(u.ID, "set_primary_platform", "platform:"+platform, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiUnlinkConnector removes the operator's identity link while KEEPING the
// saved credentials, so a wrong link is self-serviceable. The router otherwise
// refuses a re-link with "contact your administrator", which is a dead end in a
// single-owner product.
// DELETE /api/v1/connectors/:platform/identity → 200 {"ok":true}.
func (s *Server) apiUnlinkConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}
	if err := s.db.DeletePlatformIdentity(u.ID, platform); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to unlink")
	}
	// Clear a primary that now names an unlinked platform.
	if cur, _ := s.db.GetSetting(u.ID, gateway.PrimaryPlatformSettingKey); cur == platform {
		_ = s.db.SetSetting(u.ID, gateway.PrimaryPlatformSettingKey, "")
	}
	s.audit.Log(u.ID, "unlink_platform", "platform:"+platform, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}
```

- [ ] **Step 5: Register the routes in the parity inventory**

`web/api_parity_test.go`'s `want` table is a merge gate. Add both entries:

```go
	{http.MethodPut, "/api/v1/connectors/:platform/primary"},
	{http.MethodDelete, "/api/v1/connectors/:platform/identity"},
```

- [ ] **Step 6: Run the web suite**

Run: `go test ./web/ -count=1`
Expected: PASS, including `TestAPIParityInventory`.

- [ ] **Step 7: Commit**

```bash
git add web/ && \
git commit -m "feat(web): expose link state, primary app and unlink on the API

Nothing reported whether the operator had actually completed the /start
handshake, so the UI could only ever show 'the token authenticates'. Adds
linked/linked_identity/primary plus server-built deep links to the connectors
DTO, and routes to choose the primary app and to unlink. The endpoint stays
DB-only so the wizard can poll it."
```

---

### Task 7: Wizard step 4 — "Link your account"

**Files:**
- Modify: `web/ui/src/lib/connections.ts`
- Modify: `web/ui/src/pages/connections/ChatAppWizard.tsx`
- Test: `web/ui/src/pages/connections/ChatAppWizard.test.tsx` (append)

**Interfaces:**
- Consumes: the Task 6 DTO fields.
- Produces: `ConnectorPlatform` gains `linked`, `linked_identity`, `primary`, `dm_url`, `invite_url`; hooks `useSetPrimaryConnector()`, `useUnlinkConnector()`; `STEPS` becomes `["setup","credentials","test","link"]`.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/connections/ChatAppWizard.test.tsx`:

```tsx
const LINK_STEP_PLATFORM: ConnectorPlatform = {
  platform: "discord",
  label: "Discord",
  blurb: "",
  setup_steps: ["Open the Discord Developer Portal and click New Application"],
  fields: [{ name: "token", label: "Bot Token", secret: true }],
  connected: true,
  identity: "rookery_bot",
  linked: false,
  linked_identity: "",
  primary: false,
  dm_url: "https://discord.com/users/42",
  invite_url:
    "https://discord.com/api/oauth2/authorize?client_id=42&scope=bot&permissions=0",
};

test("shows no Done button and no success state while unlinked", async () => {
  renderWizard({ ...LINK_STEP_PLATFORM, connected: false });

  await userEvent.click(screen.getByRole("button", { name: /next/i }));
  await userEvent.type(screen.getByLabelText(/bot token/i), "tok");
  await userEvent.click(screen.getByRole("button", { name: /save & continue/i }));

  // Step 4 is reached but unlinked: the wizard must not offer completion.
  expect(await screen.findByText(/waiting for you to send/i)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^done$/i })).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: /invite/i })).toHaveAttribute(
    "href",
    LINK_STEP_PLATFORM.invite_url,
  );
});

test("offers Done once the identity row appears", async () => {
  renderWizard({ ...LINK_STEP_PLATFORM, linked: true, linked_identity: "ilija#4821" });

  expect(await screen.findByText(/ilija#4821/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /^done$/i })).toBeInTheDocument();
  expect(screen.queryByText(/waiting for you to send/i)).not.toBeInTheDocument();
});

test("the escape hatch never reads as success", async () => {
  renderWizard({ ...LINK_STEP_PLATFORM, connected: false });

  await userEvent.click(screen.getByRole("button", { name: /next/i }));
  await userEvent.type(screen.getByLabelText(/bot token/i), "tok");
  await userEvent.click(screen.getByRole("button", { name: /save & continue/i }));

  const escape = await screen.findByRole("button", { name: /finish later/i });
  expect(escape).toHaveTextContent(/not linked/i);
});
```

Add a `renderWizard(platform)` helper alongside the file's existing render setup, mounting `ChatAppWizard` inside the same `QueryClientProvider`/`MemoryRouter`/`AppShell` wrapper the current tests use, and stubbing `fetch` so `/api/v1/connectors` returns `{platforms:[platform]}` and `POST /api/v1/connectors` returns `{ok:true,identity:"rookery_bot"}`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web/ui && npx vitest run src/pages/connections/ChatAppWizard.test.tsx`
Expected: FAIL — no "waiting" text; a Done button renders after save.

- [ ] **Step 3: Extend the SPA types and hooks**

In `web/ui/src/lib/connections.ts`, extend `ConnectorPlatform`:

```ts
// Mirrors apiConnectorPlatform.
export type ConnectorPlatform = {
  platform: string;
  label: string;
  blurb: string;
  setup_steps: string[];
  fields: ConnectorField[];
  connected: boolean;
  identity: string; // the BOT's username
  // `connected` means the token authenticates; `linked` means the operator
  // completed the /start handshake and the integration is actually usable.
  linked: boolean;
  linked_identity: string;
  primary: boolean;
  dm_url: string;
  invite_url: string;
};
```

Append the two hooks:

```ts
export function useSetPrimaryConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (platform: string) =>
      api.put<{ ok: boolean }>(`/api/v1/connectors/${platform}/primary`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connectors"] }),
  });
}

export function useUnlinkConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (platform: string) =>
      api.del<{ ok: boolean }>(`/api/v1/connectors/${platform}/identity`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connectors"] }),
  });
}
```

If `api.put` does not exist in `web/ui/src/lib/api.ts`, add it mirroring `api.post`.

- [ ] **Step 4: Add the link step to the wizard**

In `ChatAppWizard.tsx`, widen the step machinery:

```tsx
type Step = "setup" | "credentials" | "test" | "link";
const STEPS: Step[] = ["setup", "credentials", "test", "link"];
const STEP_LABELS: Record<Step, string> = {
  setup: "Setup",
  credentials: "Credentials",
  test: "Test",
  link: "Link",
};
```

Add the step component above `ConnectWizard`:

```tsx
// The identity row is created only when the operator's /start actually reaches
// the bot, so its presence proves the inbound path end to end — which a token
// check cannot. Until it lands there is deliberately no Done button and no
// green state: the product must never signal completion it has not verified.
function LinkStep({
  platform,
  onFinishLater,
  onDone,
}: {
  platform: ConnectorPlatform;
  onFinishLater: () => void;
  onDone: () => void;
}) {
  const { data } = useConnectors({ refetchInterval: platform.linked ? false : 2000 });
  const live =
    data?.platforms.find((p) => p.platform === platform.platform) ?? platform;

  if (live.linked) {
    return (
      <div className="space-y-3">
        <OkNote>Linked as {live.linked_identity}</OkNote>
        <div className="flex justify-end">
          <Button onClick={onDone}>Done</Button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {live.invite_url && (
        <div className="space-y-2 rounded-lg border border-border bg-background p-3 text-sm">
          <p className="font-medium">First, add the bot to a server</p>
          <p className="text-muted-2">
            Discord only allows a direct message between accounts that share a
            server. Afterwards, check the server's Privacy Settings and make sure
            Direct Messages are allowed.
          </p>
          <Button asChild variant="outline" size="sm">
            <a href={live.invite_url} target="_blank" rel="noreferrer">
              <Link2 />
              Invite to a server
            </a>
          </Button>
        </div>
      )}

      <div className="space-y-2 rounded-lg border border-border bg-background p-3 text-sm">
        <p className="font-medium">Then send the bot a message</p>
        <p className="text-muted-2">
          Open a direct message with{" "}
          <b className="text-foreground">{live.identity || live.label}</b> and
          send:
        </p>
        <code className="block rounded bg-muted-surface px-2 py-1 font-mono">/start</code>
        {live.dm_url && (
          <Button asChild variant="outline" size="sm">
            <a href={live.dm_url} target="_blank" rel="noreferrer">
              <ArrowRight />
              Open {live.label}
            </a>
          </Button>
        )}
      </div>

      <Spinner text="Waiting for you to send /start…" />

      <div className="flex justify-end">
        <Button variant="link" onClick={onFinishLater}>
          Finish later — I'm not linked yet
        </Button>
      </div>
    </div>
  );
}
```

Change `useConnectors` in `connections.ts` to accept the poll option:

```ts
export function useConnectors(opts?: { refetchInterval?: number | false }) {
  return useQuery({
    queryKey: ["connectors"],
    queryFn: () => api.get<{ platforms: ConnectorPlatform[] }>("/api/v1/connectors"),
    refetchInterval: opts?.refetchInterval,
  });
}
```

In `ConnectWizard`, advance to `"link"` instead of closing, and render it:

```tsx
      {step === "test" && (
        <div className="space-y-3">
          {warning && <WarningNote>{warning}</WarningNote>}
          <TestResult
            platform={platform.label}
            pending={testMutation.isPending}
            ok={testOk}
            identity={testMutation.data?.identity}
            error={testMutation.data?.error}
          />
          {testMutation.isError && (
            <ErrorNote>
              {testMutation.error instanceof ApiError
                ? testMutation.error.message
                : "Something went wrong"}
            </ErrorNote>
          )}
          <div className="flex justify-end">
            {testOk === true ? (
              <Button onClick={() => setStep("link")}>
                <ArrowRight />
                Next
              </Button>
            ) : (
              !testMutation.isPending && (
                <Button
                  variant="outline"
                  onClick={() => testMutation.mutate(platform.platform)}
                >
                  <RotateCcw />
                  Retry
                </Button>
              )
            )}
          </div>
        </div>
      )}

      {step === "link" && (
        <LinkStep
          platform={platform}
          onFinishLater={() => close()}
          onDone={() => close()}
        />
      )}
```

Note the save handler already advances to `"test"`; leave it. Import `useConnectors` in the wizard.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/connections/ChatAppWizard.test.tsx`
Expected: PASS (all three new tests plus the file's existing ones).

- [ ] **Step 6: Typecheck and lint**

Run: `cd web/ui && npx tsc -b && npx oxlint`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src/lib/connections.ts web/ui/src/pages/connections/ChatAppWizard.tsx web/ui/src/pages/connections/ChatAppWizard.test.tsx && \
git commit -m "feat(web/connections): wizard step 4 waits for a real link

The wizard reported success once the token authenticated, while the step that
makes the integration usable happened off-screen. Adds a linking step that
polls for the operator's identity row, renders a generated Discord invite and
a DM deep link, and offers no Done button until the link actually lands."
```

---

### Task 8: Connections page — link badges, primary radio, unlink

**Files:**
- Modify: `web/ui/src/pages/connections/ConnectionsPage.tsx` (chat apps section, from `:532`)
- Modify: `web/ui/src/pages/connections/ChatAppWizard.tsx` (`ManageWizard`)
- Test: `web/ui/src/pages/connections/connections.test.tsx` (append)

**Interfaces:**
- Consumes: Task 7's types and hooks.
- Produces: final UI.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/connections/connections.test.tsx`:

```tsx
test("a connected but unlinked app is not shown as ready", async () => {
  renderConnections([
    { ...CHAT_APP_FIXTURE, platform: "discord", label: "Discord", connected: true, linked: false },
  ]);

  expect(await screen.findByText(/not linked yet/i)).toBeInTheDocument();
  expect(screen.queryByText(/^connected$/i)).not.toBeInTheDocument();
});

test("primary radio is offered only to linked apps", async () => {
  renderConnections([
    {
      ...CHAT_APP_FIXTURE,
      platform: "telegram",
      label: "Telegram",
      connected: true,
      linked: true,
      linked_identity: "1843540314",
      primary: true,
    },
    { ...CHAT_APP_FIXTURE, platform: "discord", label: "Discord", connected: true, linked: false },
  ]);

  const radios = await screen.findAllByRole("radio");
  expect(radios).toHaveLength(1);
  expect(radios[0]).toBeChecked();
  expect(screen.getByText(/delivered to Telegram/i)).toBeInTheDocument();
});
```

Define `CHAT_APP_FIXTURE` with every `ConnectorPlatform` field, and `renderConnections(platforms)` stubbing `GET /api/v1/connectors`, following the file's existing patterns.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web/ui && npx vitest run src/pages/connections/connections.test.tsx`
Expected: FAIL — no "not linked yet" text, no radios.

- [ ] **Step 3: Render link state and the primary radio**

In `ConnectionsPage.tsx`'s chat apps section, replace the connected badge with a three-state one and add the radio. The status must distinguish *connected* from *usable*:

```tsx
function ChatAppStatus({ app }: { app: ConnectorPlatform }) {
  if (!app.connected) return <span className="text-xs text-muted-2">Not connected</span>;
  if (!app.linked)
    return (
      <span className="flex items-center gap-1.5 text-xs font-medium text-warn">
        <span className="size-1.5 rounded-full bg-warn" />
        Not linked yet
      </span>
    );
  return (
    <span className="flex items-center gap-1.5 text-xs font-medium text-ok">
      <span className="size-1.5 rounded-full bg-ok" />
      Linked as {app.linked_identity}
    </span>
  );
}
```

Below the card grid, render the delivery chooser over the linked apps only:

```tsx
{linkedApps.length > 0 && (
  <div className="mt-4 space-y-2 rounded-lg border border-border p-3">
    <p className="text-sm font-medium">Where should agent runs and reminders go?</p>
    {linkedApps.map((app) => (
      <label key={app.platform} className="flex items-center gap-2 text-sm">
        <input
          type="radio"
          name="primary-chat-app"
          checked={app.primary}
          onChange={() => setPrimary.mutate(app.platform)}
        />
        {app.label}
      </label>
    ))}
    <p className="text-xs text-muted-2">
      Delivered to {linkedApps.find((a) => a.primary)?.label ?? linkedApps[0].label}. You can
      chat with your agents from any linked app.
    </p>
  </div>
)}
```

with `const linkedApps = apps.filter((a) => a.linked)` and `const setPrimary = useSetPrimaryConnector()`.

- [ ] **Step 4: Add Unlink to the Manage panel**

In `ChatAppWizard.tsx`'s `ManageWizard`, replace the hardcoded "Connected" header with the live link state and add an unlink control above the disconnect block:

```tsx
  const unlinkMutation = useUnlinkConnector();

  // …in the returned JSX, replacing the static Connected header:
  {platform.linked ? (
    <div className="space-y-1">
      <div className="flex items-center gap-1.5 text-sm font-medium text-ok">
        <span className="size-1.5 rounded-full bg-ok" /> Linked
      </div>
      <div className="text-xs text-muted-2">{platform.linked_identity}</div>
      <Button
        variant="outline"
        size="sm"
        onClick={() => unlinkMutation.mutate(platform.platform)}
        disabled={unlinkMutation.isPending}
      >
        <Unlink />
        {unlinkMutation.isPending ? "Unlinking…" : "Unlink this account"}
      </Button>
    </div>
  ) : (
    <LinkStep platform={platform} onFinishLater={close} onDone={close} />
  )}
```

This is what makes a wrong link self-serviceable — the router otherwise answers a re-link attempt with "contact your administrator", and this product has no administrator.

- [ ] **Step 5: Run the frontend suite**

Run: `cd web/ui && npx vitest run`
Expected: PASS.

- [ ] **Step 6: Run the full gate**

Run: `make ci`
Expected: PASS — gofmt, vet, `go test -race`, cross-compile, and the frontend job.

- [ ] **Step 7: Commit**

```bash
git add web/ui/ && \
git commit -m "feat(web/connections): show link state, choose the primary app

A card said 'Connected' whether or not the operator could actually use the
app. Distinguish not connected / not linked yet / linked, offer the primary
radio to linked apps only, and add Unlink so a wrong link is self-serviceable."
```

---

## Manual verification

The CI container smoke test is the project's only end-to-end coverage, so verify the real flow by hand once Task 8 lands. This install already has the ideal fixture: Discord is connected and unlinked.

1. `make deploy`, then open `/connections`.
2. **Discord should read "Not linked yet"**, not "Connected" — this is the bug reproducing itself, now visible.
3. Open Discord → Manage. The link step should offer an **Invite to a server** button. Follow it, pick a server, then check that server's Privacy Settings → Direct Messages is on.
4. DM the bot `/start`. The panel should flip to **Linked as \<you\>** within ~2s without a reload, and the reply should say "Your **Discord** account is now linked" — not Telegram.
5. Both apps now linked: pick Telegram as primary, fire a reminder, confirm it arrives on Telegram only. Switch to Discord, repeat.
6. Unlink Discord and confirm the card returns to "Not linked yet" while the credentials survive.

## Self-Review

**Spec coverage.** Every section maps to a task: setup steps → 5; step 4 → 7; bot identity persistence → 1; link-status DTO → 6; primary app → 3 (backend) + 6 (API) + 8 (UI); `handleStart` label → 4; Unlink → 2 (DB) + 6 (API) + 8 (UI); all three test categories → distributed. Out-of-scope items are correctly absent.

**Two deliberate deviations from the spec, both recorded here:**

1. **Bot identifiers persist as a plain workspace setting, not in `encrypted_config`.** The spec chose `encrypted_config` to avoid a migration, but the settings table avoids one equally *and* is already used for exactly this (`telegram_bot_username`, `api_connectors.go:101`). It also avoids changing `SplitCreds`. A bot's username and id are public — Discord's invite URL is meant to be shared — so encrypting them buys nothing, and keeping them out of ciphertext is what lets the list endpoint stay cheap enough to poll.
2. **Deep links are built server-side via `CredSpec.LinkURLs`, not in the SPA.** This keeps platform knowledge in `internal/gateway`, matching the project's existing principle that a new platform's connect card is data rather than hand-written markup. Adding a platform now needs no SPA change.

**Placeholder scan.** No TBD/TODO. The Go tests were rewritten against the harness that actually exists (`newAPITestServer`, `bootstrapAndLogin`, `createAndEnterWorkspace`, `doJSON` — verified in `web/api_test_helpers_test.go`); an earlier draft invented a `newConnectorTestServer` that does not. Two frontend steps still say "following the file's existing patterns" for `renderWizard`/`renderConnections`, because those wrappers must mirror the current `QueryClientProvider`/`MemoryRouter`/`AppShell` setup in the file being edited; every assertion around them is fully specified.

**Verified before writing.** `api.put` exists (`web/ui/src/lib/api.ts:49`); `Button` supports `asChild` (`button.tsx:62`); Go is 1.26.5, so `t.Context()` is available; `apiOKResponse` and `jsonErr` are already used in this file.

**Type consistency.** `BotIdentity{Username,UserID,TeamID}` is used identically in Tasks 1, 6 and the tests. `LinkTargets{DMURL,InviteURL}` maps to DTO `dm_url`/`invite_url` and to TS `dm_url`/`invite_url`. `PrimaryPlatformSettingKey` is defined once in Task 3 and consumed in Task 6. `DeletePlatformIdentity(workspaceID, platform)` is defined in Task 2 and called in Task 6. `useConnectors` gains its options parameter in Task 7 before Task 8 relies on the query.

**Known ordering constraint.** Task 1 changes `CredSpec.Validate`'s signature, which breaks compilation until every call site in that task is updated. Tasks 1 and 2 are independent; 3 depends on 2; 6 depends on 1, 2 and 3; 7 depends on 6; 8 depends on 7. Run them in order.
