package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
)

func TestAPIConnectors_GET_Unauthenticated(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_GET_Authed_ListsTelegram(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"telegram"`) {
		t.Fatalf("expected response to contain telegram platform, got: %s", body)
	}
	if contains(body, "encrypted") || contains(body, "token\":\"") {
		t.Fatalf("response must never leak credential values: %s", body)
	}
}

// The registered telegram CredSpec validator (wired in NewServer, see
// web/server.go:123-126) hits the real Telegram API, so it can't fail
// deterministically offline. Slack's validator, by contrast, rejects a
// missing app_token synchronously with no network call — use that for a
// deterministic 400 on invalid credentials.
func TestAPIConnectors_POST_InvalidCredentials(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors", map[string]any{
		"platform": "slack",
		"values":   map[string]string{"token": "xoxb-garbage"},
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "invalid_credentials") {
		t.Fatalf("expected invalid_credentials code, got: %s", rec.Body.String())
	}
}

func TestAPIConnectors_POST_UnknownPlatform(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors", map[string]any{
		"platform": "not-a-real-platform",
		"values":   map[string]string{"token": "x"},
	}, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_DELETE_UnknownPlatform(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/connectors/not-a-real-platform", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_TEST_UnknownPlatform(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors/not-a-real-platform/test", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_DELETE_Success(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := database.UpsertPlatformConnection(&db.PlatformConnection{
		ID:          "conn-1",
		WorkspaceID: wsID,
		Platform:    "slack",
		Active:      true,
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/connectors/slack", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected ok:true, got: %s", rec.Body.String())
	}
	if _, err := database.GetPlatformConnection(wsID, "slack"); err == nil {
		t.Fatalf("expected connection to be deleted")
	}
}

func TestAPIConnectors_TEST_NotConnected(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// slack is a registered platform but nothing has been connected yet —
	// testConnectorIdentity should fail with "connector not found", surfaced
	// as ok:false, not an HTTP error.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors/slack/test", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("expected ok:false, got: %s", rec.Body.String())
	}
}

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

	// Identity/link fields are only surfaced while the platform is actually
	// connected (see TestAPIConnectors_GET_DisconnectedPlatformReportsNoIdentity),
	// so this needs a real connection row alongside the stored identity setting.
	if err := database.UpsertPlatformConnection(&db.PlatformConnection{
		ID:          "conn-discord",
		WorkspaceID: wsID,
		Platform:    "discord",
		Active:      true,
	}); err != nil {
		t.Fatal(err)
	}
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

	// Seed a real credentials row so "keeps credentials" is actually exercised —
	// without this, an implementation that also deleted the connection would
	// pass this test just as well.
	if err := database.UpsertPlatformConnection(&db.PlatformConnection{
		ID:          "conn-1",
		WorkspaceID: wsID,
		Platform:    "discord",
		Active:      true,
	}); err != nil {
		t.Fatal(err)
	}
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
	// The credentials themselves must survive — unlink only removes the
	// operator's identity link, so a wrong link stays self-serviceable.
	conn, err := database.GetPlatformConnection(wsID, "discord")
	if err != nil {
		t.Fatalf("credentials did not survive unlink: %v", err)
	}
	if !conn.Active {
		t.Fatalf("credentials survived unlink but were deactivated: %+v", conn)
	}
	// A primary naming a now-unlinked platform must not persist.
	if got, _ := database.GetSetting(wsID, gateway.PrimaryPlatformSettingKey); got != "" {
		t.Fatalf("stale primary survived: %q", got)
	}
}

// TestAPIConnectors_GET_PrimaryFallback_PicksFirstLinked links two platforms
// with the primary setting left unset and asserts the FIRST-linked one is
// reported primary. Discord is inserted first and telegram second, and
// "discord" < "telegram" alphabetically — so this is deterministic under
// ListPlatformIdentities' `ORDER BY linked_at, platform, id` regardless of
// whether the two inserts land in the same one-second linked_at bucket or not:
// if linked_at differs, chronological order (discord first) wins; if it ties,
// alphabetical order (discord < telegram) wins. Either column names the same
// row, so there is no flake window.
func TestAPIConnectors_GET_PrimaryFallback_PicksFirstLinked(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID: "id-discord", WorkspaceID: wsID, Platform: "discord", PlatformUserID: "u-discord",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID: "id-telegram", WorkspaceID: wsID, Platform: "telegram", PlatformUserID: "u-telegram",
	}); err != nil {
		t.Fatal(err)
	}

	// No primary setting written — this is the fallback path.
	list := decodeConnectorList(t, doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies))
	dc := findPlatform(t, list, "discord")
	tg := findPlatform(t, list, "telegram")
	if !dc.Primary {
		t.Fatalf("expected discord (first-linked) to be primary; discord=%+v telegram=%+v", dc, tg)
	}
	if tg.Primary {
		t.Fatalf("telegram should not be primary when discord was linked first: %+v", tg)
	}
}

// TestAPIConnectors_GET_DisconnectedPlatformReportsNoIdentity pins the fix for
// a regression: identity/link fields must come from an ACTIVE connection, not
// merely a leftover bot_identity setting from a previous connect. A platform
// with no platform_connections row must report empty identity, dm_url and
// invite_url even when the setting is still present.
func TestAPIConnectors_GET_DisconnectedPlatformReportsNoIdentity(t *testing.T) {
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
	// Deliberately no platform_connections row for discord.

	list := decodeConnectorList(t, doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies))
	dc := findPlatform(t, list, "discord")
	if dc.Connected {
		t.Fatalf("discord should not report connected: %+v", dc)
	}
	if dc.Identity != "" || dc.DMURL != "" || dc.InviteURL != "" {
		t.Fatalf("disconnected platform must not surface a stale identity/links: %+v", dc)
	}
}
