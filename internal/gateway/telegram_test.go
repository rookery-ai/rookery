package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	telebot "gopkg.in/telebot.v4"
)

// newFakeTelegramBot builds a *telebot.Bot pointed at a fake Bot API server
// that answers getFile with filePath, then serves body at the resulting
// download URL. Offline:true skips the real getMe call NewBot otherwise makes.
func newFakeTelegramBot(t *testing.T, filePath string, body []byte) *telebot.Bot {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/getFile"):
			resp := struct {
				OK     bool         `json:"ok"`
				Result telebot.File `json:"result"`
			}{OK: true, Result: telebot.File{FileID: "f1", FilePath: filePath}}
			json.NewEncoder(w).Encode(resp)
		case strings.Contains(r.URL.Path, "/file/bot"):
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	bot, err := telebot.NewBot(telebot.Settings{Token: "test-token", URL: srv.URL, Offline: true})
	if err != nil {
		t.Fatalf("new fake bot: %v", err)
	}
	return bot
}

func TestDownloadTelegramFile(t *testing.T) {
	want := []byte("item,cost\nrent,900\n")
	bot := newFakeTelegramBot(t, "documents/budget.csv", want)
	g := &TelegramGateway{bot: bot}

	data, name, err := g.downloadTelegramFile(telebot.File{FileID: "f1"}, "budget.csv")
	if err != nil {
		t.Fatalf("downloadTelegramFile: %v", err)
	}
	if string(data) != string(want) {
		t.Errorf("unexpected data: %q", data)
	}
	if name != "budget.csv" {
		t.Errorf("unexpected name: %q", name)
	}
}

func TestDownloadTelegramFileEmptyNameDefaults(t *testing.T) {
	bot := newFakeTelegramBot(t, "photos/p.jpg", []byte{0xFF, 0xD8, 0xFF})
	g := &TelegramGateway{bot: bot}

	_, name, err := g.downloadTelegramFile(telebot.File{FileID: "f1"}, "  ")
	if err != nil {
		t.Fatalf("downloadTelegramFile: %v", err)
	}
	if name != "attachment" {
		t.Errorf("expected default name 'attachment', got %q", name)
	}
}

func TestDownloadTelegramFileTooLarge(t *testing.T) {
	bot := newFakeTelegramBot(t, "documents/big.csv", make([]byte, maxAttachmentBytes+1))
	g := &TelegramGateway{bot: bot}

	if _, _, err := g.downloadTelegramFile(telebot.File{FileID: "f1"}, "big.csv"); err == nil {
		t.Error("an oversized attachment must be refused")
	}
}
