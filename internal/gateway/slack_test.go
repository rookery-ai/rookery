package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/slack-go/slack/slackevents"
)

func TestSlackSpecRegistered(t *testing.T) {
	spec, ok := CredSpecFor("slack")
	if !ok {
		t.Fatal("slack spec not registered")
	}
	if spec.Label != "Slack" || len(spec.Fields) != 2 {
		t.Fatalf("unexpected slack spec: %+v", spec)
	}
	keys := map[string]bool{}
	for _, f := range spec.Fields {
		keys[f.Key] = true
	}
	if !keys["token"] || !keys["app_token"] {
		t.Fatalf("slack fields missing token/app_token: %+v", spec.Fields)
	}
}

func TestValidateSlackTokenBadPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("makes a live network call to slack auth.test")
	}
	// AuthTest with an obviously invalid token must error (no network dependency
	// on the error path — slack lib returns "invalid_auth" style error offline too;
	// if this proves flaky offline, skip via testing.Short()).
	if _, err := validateSlackToken("not-a-real-token"); err == nil {
		t.Fatal("invalid token must error")
	}
}

func TestMapSlackDM(t *testing.T) {
	msg, ok := mapSlackDM("U1", "im", "hi", "1.2", "", "", "UBOT")
	if !ok || msg.Platform != "slack" || msg.PlatformUserID != "U1" || msg.Text != "hi" || msg.MessageID != "1.2" {
		t.Fatalf("human DM mapping wrong: %+v ok=%v", msg, ok)
	}
	if _, ok := mapSlackDM("UBOT", "im", "x", "1", "", "", "UBOT"); ok {
		t.Fatal("own message must be skipped")
	}
	if _, ok := mapSlackDM("U1", "im", "x", "1", "B123", "", "UBOT"); ok {
		t.Fatal("bot messages (BotID set) must be skipped")
	}
	if _, ok := mapSlackDM("U1", "im", "x", "1", "", "message_changed", "UBOT"); ok {
		t.Fatal("subtyped messages (edits/joins) must be skipped")
	}
	if _, ok := mapSlackDM("U1", "channel", "x", "1", "", "", "UBOT"); ok {
		t.Fatal("non-im (channel) messages must be skipped")
	}
}

// TestMapSlackDMFileShare proves file_share is the one subtype allowed
// through — a file upload's text is often empty, so the file IS the message.
func TestMapSlackDMFileShare(t *testing.T) {
	msg, ok := mapSlackDM("U1", "im", "", "1.2", "", "file_share", "UBOT")
	if !ok {
		t.Fatal("file_share message must pass through")
	}
	if msg.Platform != "slack" || msg.PlatformUserID != "U1" || msg.MessageID != "1.2" {
		t.Fatalf("file_share mapping wrong: %+v", msg)
	}
}

// TestMapSlackDMOtherSubtypesStillDropped proves file_share is not a blanket
// unblock of the subtype filter — every other subtype is still rejected.
func TestMapSlackDMOtherSubtypesStillDropped(t *testing.T) {
	for _, st := range []string{"message_changed", "message_deleted", "channel_join", "bot_message", "thread_broadcast"} {
		if _, ok := mapSlackDM("U1", "im", "x", "1", "", st, "UBOT"); ok {
			t.Fatalf("subtype %q must still be dropped", st)
		}
	}
}

// TestSlackFileShareEventUnmarshalPopulatesMessageFiles proves the load-bearing
// assumption the readLoop handler relies on: slackevents.MessageEvent has no
// Files field of its own (unlike AppMentionEvent), so a file_share event's
// files must be reached via me.Message.Files. MessageEvent's custom
// UnmarshalJSON populates Message from the SAME top-level payload for any
// non-message_changed event — this test unmarshals a representative
// file_share DM payload exactly as the socketmode transport would deliver it
// and asserts the file lands where the handler reads it from. If this ever
// breaks (e.g. a library upgrade changes the unmarshal shape), the handler
// would silently dispatch no attachment despite dispatching ok=true, and this
// is the one test that would catch it.
func TestSlackFileShareEventUnmarshalPopulatesMessageFiles(t *testing.T) {
	raw := []byte(`{
		"type": "message",
		"subtype": "file_share",
		"channel_type": "im",
		"user": "U1",
		"ts": "1.2",
		"text": "",
		"files": [
			{
				"id": "F1",
				"name": "report.pdf",
				"url_private_download": "https://files.slack.com/files-pri/T1-F1/download/report.pdf"
			}
		]
	}`)

	var me slackevents.MessageEvent
	if err := json.Unmarshal(raw, &me); err != nil {
		t.Fatalf("unmarshal file_share payload: %v", err)
	}
	if me.Message == nil {
		t.Fatal("Message must be populated for a non-message_changed event")
	}
	if len(me.Message.Files) != 1 {
		t.Fatalf("Message.Files = %d entries, want 1: %+v", len(me.Message.Files), me.Message)
	}
	f := me.Message.Files[0]
	if f.Name != "report.pdf" {
		t.Errorf("Files[0].Name = %q, want %q", f.Name, "report.pdf")
	}
	if f.URLPrivateDownload != "https://files.slack.com/files-pri/T1-F1/download/report.pdf" {
		t.Errorf("Files[0].URLPrivateDownload = %q", f.URLPrivateDownload)
	}
}

// fakeSlackDownloader is a hermetic stand-in for *slack.Client satisfying
// slackFileDownloader, used to test downloadSlackFile without a live Slack
// workspace or bot token.
type fakeSlackDownloader struct {
	data   []byte
	err    error
	called bool
}

func (f *fakeSlackDownloader) GetFile(downloadURL string, w io.Writer) error {
	f.called = true
	if f.err != nil {
		return f.err
	}
	_, err := w.Write(f.data)
	return err
}

func TestDownloadSlackFile(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte("hello world")}
	data, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/foo.txt")
	if err != nil {
		t.Fatalf("downloadSlackFile: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("data = %q, want %q", data, "hello world")
	}
	if !dl.called {
		t.Fatal("downloader must have been invoked for an allowed host")
	}
}

// TestDownloadSlackFileTooLarge proves the CappingWriter is actually wired
// in: a file whose bytes exceed maxAttachmentBytes must be refused.
func TestDownloadSlackFileTooLarge(t *testing.T) {
	dl := &fakeSlackDownloader{data: make([]byte, maxAttachmentBytes+1)}
	if _, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/big.bin"); err == nil {
		t.Fatal("an oversized file must be refused")
	}
}

func TestDownloadSlackFileDownloaderError(t *testing.T) {
	dl := &fakeSlackDownloader{err: errors.New("boom")}
	if _, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/foo.txt"); err == nil {
		t.Fatal("a downloader error must propagate")
	}
}

// TestDownloadSlackFileRejectsNonSlackHost proves the allowlist itself: even
// with a downloader that would happily serve data, a URL whose host isn't
// files.slack.com must be refused BEFORE any download is attempted — this is
// what stops a malformed/tampered file payload from reaching an arbitrary
// address.
func TestDownloadSlackFileRejectsNonSlackHost(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte("should never be reached")}
	if _, err := downloadSlackFile(dl, "https://evil.example.com/f"); err == nil {
		t.Fatal("a non-files.slack.com host must be rejected")
	}
	if dl.called {
		t.Fatal("downloader must not be invoked when the host is rejected")
	}
}

func TestParseSlackConfig(t *testing.T) {
	tok, err := parseSlackConfig(`{"app_token":"xapp-1"}`)
	if err != nil || tok != "xapp-1" {
		t.Fatalf("parseSlackConfig = %q, %v", tok, err)
	}
	if _, err := parseSlackConfig(`{}`); err == nil {
		t.Fatal("missing app_token must error")
	}
}
