package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// withTestDiscordAttachment points downloadDiscordAttachment's CDN allowlist
// and HTTP client at a hermetic httptest server for the duration of a test.
// Both overrides are necessary and independent: the allowlist swap lets a
// non-cdn.discordapp.com host pass the pinning check, and the unguarded
// client is required because the real guarded client (nethttp.GuardedClient)
// refuses to dial loopback outright — httptest servers always bind
// 127.0.0.1, so the production client could never reach one at all.
func withTestDiscordAttachment(t *testing.T, srvURL string) {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}

	oldHosts := discordCDNHosts
	oldClient := discordAttachmentClient
	discordCDNHosts = map[string]bool{u.Hostname(): true}
	discordAttachmentClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() {
		discordCDNHosts = oldHosts
		discordAttachmentClient = oldClient
	})
}

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

	identity, err := validateDiscordToken("good-token")
	if err != nil || identity.Username != "MyBot" || identity.UserID != "1" {
		t.Fatalf("good token: identity=%+v err=%v", identity, err)
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

func TestDownloadDiscordAttachment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("item,cost\nrent,900\n"))
	}))
	defer srv.Close()
	withTestDiscordAttachment(t, srv.URL)

	data, err := downloadDiscordAttachment(srv.URL)
	if err != nil {
		t.Fatalf("downloadDiscordAttachment: %v", err)
	}
	if string(data) != "item,cost\nrent,900\n" {
		t.Errorf("unexpected data: %q", data)
	}
}

func TestDownloadDiscordAttachmentTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxAttachmentBytes+1))
	}))
	defer srv.Close()
	withTestDiscordAttachment(t, srv.URL)

	if _, err := downloadDiscordAttachment(srv.URL); err == nil {
		t.Error("an oversized attachment must be refused")
	}
}

func TestDownloadDiscordAttachmentNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	withTestDiscordAttachment(t, srv.URL)

	if _, err := downloadDiscordAttachment(srv.URL); err == nil {
		t.Error("a non-200 response must be refused")
	}
}

// TestDownloadDiscordAttachmentRejectsNonCDNHost proves the allowlist itself:
// even with a reachable, well-formed server, a host outside discordCDNHosts
// must be refused before any network I/O — this is what stops a
// malformed/tampered attachment URL from reaching an arbitrary host.
func TestDownloadDiscordAttachmentRejectsNonCDNHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should never be reached"))
	}))
	defer srv.Close()
	// Deliberately do NOT call withTestDiscordAttachment — the production
	// allowlist (cdn.discordapp.com / media.discordapp.net) stays in effect,
	// and srv.URL's host is neither.

	if _, err := downloadDiscordAttachment(srv.URL); err == nil {
		t.Error("a non-discord-cdn host must be refused")
	}
}

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
