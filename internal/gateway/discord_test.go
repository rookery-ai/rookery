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

func TestDownloadDiscordAttachment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("item,cost\nrent,900\n"))
	}))
	defer srv.Close()

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

	if _, err := downloadDiscordAttachment(srv.URL); err == nil {
		t.Error("an oversized attachment must be refused")
	}
}

func TestDownloadDiscordAttachmentNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := downloadDiscordAttachment(srv.URL); err == nil {
		t.Error("a non-200 response must be refused")
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
