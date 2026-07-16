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
