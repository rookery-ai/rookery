package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
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
	data, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/foo.txt", "text/plain")
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
	if _, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/big.bin", "application/octet-stream"); err == nil {
		t.Fatal("an oversized file must be refused")
	}
}

func TestDownloadSlackFileDownloaderError(t *testing.T) {
	dl := &fakeSlackDownloader{err: errors.New("boom")}
	if _, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/foo.txt", "text/plain"); err == nil {
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
	if _, err := downloadSlackFile(dl, "https://evil.example.com/f", "text/plain"); err == nil {
		t.Fatal("a non-files.slack.com host must be rejected")
	}
	if dl.called {
		t.Fatal("downloader must not be invoked when the host is rejected")
	}
}

// slackSignInPage is a representative Slack web sign-in page: the HTML that
// url_private_download actually returns (HTTP 200, not an error status) when
// the requesting bot token lacks the files:read scope or has expired. Real
// pages have more markup; the leading doctype/html tag is what the sniff
// checks, so a trimmed stand-in is sufficient and keeps the fixture readable.
const slackSignInPage = `<!DOCTYPE html>
<html>
<head><title>Sign in | Slack</title></head>
<body>Sign in to your workspace to continue.</body>
</html>`

// TestDownloadSlackFileRejectsMisScopedTokenHTML is Finding 1's core case: a
// mis-scoped/expired bot token makes url_private_download return an HTML
// sign-in page with HTTP 200 — GetFile's non-200 check alone would let this
// through, and internal/convert genuinely handles HTML, so without the sniff
// the sign-in page would silently become the "imported" note in place of the
// user's actual file. The declared mimetype (from Slack's own file metadata)
// is NOT text/html, so the mismatch must be caught.
func TestDownloadSlackFileRejectsMisScopedTokenHTML(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte(slackSignInPage)}
	_, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/report.pdf", "application/pdf")
	if err == nil {
		t.Fatal("an HTML sign-in page masquerading as a declared-non-HTML file must be rejected")
	}
	if !strings.Contains(err.Error(), "files:read") {
		t.Fatalf("error should name the likely cause (files:read scope), got: %v", err)
	}
}

// TestDownloadSlackFileAllowsGenuineHTML proves the check is targeted: a file
// Slack itself declares as text/html (e.g. a saved web page) must still
// import normally even though its bytes also look like HTML.
func TestDownloadSlackFileAllowsGenuineHTML(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte(slackSignInPage)}
	data, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/page.html", "text/html")
	if err != nil {
		t.Fatalf("a genuinely-declared HTML file must import normally: %v", err)
	}
	if string(data) != slackSignInPage {
		t.Fatalf("data mismatch for genuine HTML file")
	}
}

// TestDownloadSlackFileNormalFileUnaffected proves the sniff doesn't affect
// ordinary non-HTML downloads (binary or plain text) regardless of declared
// mimetype.
func TestDownloadSlackFileNormalFileUnaffected(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte("%PDF-1.4 fake pdf bytes")}
	data, err := downloadSlackFile(dl, "https://files.slack.com/files-pri/T1-F1/report.pdf", "application/pdf")
	if err != nil {
		t.Fatalf("a normal file must be unaffected: %v", err)
	}
	if string(data) != "%PDF-1.4 fake pdf bytes" {
		t.Fatalf("data mismatch")
	}
}

// TestSlackAttachmentFromEventNoFiles proves the nil case: an event with no
// files (or a nil Message, matching a message_changed-shaped event) yields no
// Attachment at all — the point of extracting this helper out of readLoop is
// that this "which field / is it present" logic is now unit-testable
// directly, not only reachable through the live socketmode transport.
func TestSlackAttachmentFromEventNoFiles(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte("should never be reached")}
	if att := slackAttachmentFromEvent(&slackevents.MessageEvent{Message: &slack.Msg{}}, dl); att != nil {
		t.Fatalf("no files must yield a nil attachment, got %+v", att)
	}
	if att := slackAttachmentFromEvent(&slackevents.MessageEvent{}, dl); att != nil {
		t.Fatalf("nil Message must yield a nil attachment, got %+v", att)
	}
	if dl.called {
		t.Fatal("downloader must not be invoked when there are no files")
	}
}

// TestSlackAttachmentFromEventDownloadsFirstFile proves the happy path: the
// first file's name/mimetype are read from me.Message.Files (not a top-level
// me.Files, which slackevents.MessageEvent doesn't have) and downloaded.
func TestSlackAttachmentFromEventDownloadsFirstFile(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte("file bytes")}
	me := &slackevents.MessageEvent{
		Message: &slack.Msg{
			Files: []slack.File{
				{Name: "report.pdf", Mimetype: "application/pdf", URLPrivateDownload: "https://files.slack.com/files-pri/T1-F1/report.pdf"},
			},
		},
	}
	att := slackAttachmentFromEvent(me, dl)
	if att == nil {
		t.Fatal("expected a non-nil attachment")
	}
	if att.Err != nil {
		t.Fatalf("unexpected error: %v", att.Err)
	}
	if att.Filename != "report.pdf" || string(att.Data) != "file bytes" {
		t.Fatalf("attachment = %+v", att)
	}
	if !dl.called {
		t.Fatal("downloader must have been invoked")
	}
}

// TestSlackAttachmentFromEventDefaultsMissingName proves an unnamed file gets
// a sensible default filename rather than an empty one.
func TestSlackAttachmentFromEventDefaultsMissingName(t *testing.T) {
	dl := &fakeSlackDownloader{data: []byte("x")}
	me := &slackevents.MessageEvent{
		Message: &slack.Msg{
			Files: []slack.File{{URLPrivateDownload: "https://files.slack.com/files-pri/T1-F1/f", Mimetype: "text/plain"}},
		},
	}
	att := slackAttachmentFromEvent(me, dl)
	if att == nil || att.Filename != "attachment" {
		t.Fatalf("expected default filename 'attachment', got %+v", att)
	}
}

// TestSlackAttachmentFromEventDownloadError proves a failed download (e.g.
// the Finding-1 HTML sign-in case, or a plain transport error) surfaces as an
// explicit Attachment.Err rather than a nil attachment or dropped message.
func TestSlackAttachmentFromEventDownloadError(t *testing.T) {
	dl := &fakeSlackDownloader{err: errors.New("boom")}
	me := &slackevents.MessageEvent{
		Message: &slack.Msg{
			Files: []slack.File{{Name: "f.txt", Mimetype: "text/plain", URLPrivateDownload: "https://files.slack.com/files-pri/T1-F1/f.txt"}},
		},
	}
	att := slackAttachmentFromEvent(me, dl)
	if att == nil || att.Err == nil {
		t.Fatalf("expected an attachment carrying an error, got %+v", att)
	}
	if att.Filename != "f.txt" {
		t.Fatalf("filename must survive a download error, got %q", att.Filename)
	}

	// The Finding-1 case specifically: a mis-scoped token's HTML sign-in page
	// must flow through here as an Err too, not get dispatched as Data.
	htmlDL := &fakeSlackDownloader{data: []byte(slackSignInPage)}
	htmlMe := &slackevents.MessageEvent{
		Message: &slack.Msg{
			Files: []slack.File{{Name: "report.pdf", Mimetype: "application/pdf", URLPrivateDownload: "https://files.slack.com/files-pri/T1-F1/report.pdf"}},
		},
	}
	htmlAtt := slackAttachmentFromEvent(htmlMe, htmlDL)
	if htmlAtt == nil || htmlAtt.Err == nil {
		t.Fatalf("HTML sign-in page must surface as Attachment.Err, got %+v", htmlAtt)
	}
	if htmlAtt.Data != nil {
		t.Fatalf("HTML sign-in page must not be delivered as Data, got %q", htmlAtt.Data)
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

// TestSlackHandleEventRecoversFromPanic pins the whole-branch-review fix: the
// read loop runs panic-capable per-event code (the file download) in a
// long-lived background goroutine with no supervisor, so one malformed event
// must be dropped, not crash the loop and the whole server. A dispatch that
// panics must not escape handleEvent.
func TestSlackHandleEventRecoversFromPanic(t *testing.T) {
	g := &SlackGateway{
		ownerWorkspaceID: "ws1",
		dispatch: func(context.Context, Message) {
			panic("boom in dispatch")
		},
		dmChannels: map[string]string{},
	}
	// A minimal plain-DM callback event that reaches the dispatch call.
	inner := slackevents.EventsAPIInnerEvent{Type: "message"}
	inner.Data = &slackevents.MessageEvent{
		User: "U1", ChannelType: "im", Text: "hi", TimeStamp: "1.2",
	}
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: inner},
	}
	// Must not panic out of handleEvent.
	g.handleEvent(evt)
	// Still usable afterwards — a second event is handled without issue.
	g.handleEvent(evt)
}
