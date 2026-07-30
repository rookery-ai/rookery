package gateway

import (
	"errors"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/vault"
)

func TestRouterImportsAttachment(t *testing.T) {
	v := vault.New(t.TempDir())
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	r := &Router{vault: v}

	reply, err := r.handleAttachment("ws1", Attachment{
		Filename: "budget.csv",
		Data:     []byte("item,cost\nrent,900\n"),
	})
	if err != nil {
		t.Fatalf("handleAttachment: %v", err)
	}
	if !strings.Contains(reply, "notes/") {
		t.Errorf("reply should name the created note, got %q", reply)
	}
}

func TestRouterAttachmentUnsupportedRepliesClearly(t *testing.T) {
	v := vault.New(t.TempDir())
	v.EnsureScaffold("ws1")
	r := &Router{vault: v}

	reply, err := r.handleAttachment("ws1", Attachment{Filename: "x.bin", Data: []byte{0, 1, 2}})
	if err != nil {
		t.Fatalf("an unconvertible attachment is a refusal about the FILE, not a Go error — got err=%v", err)
	}
	lower := strings.ToLower(reply)
	if !strings.Contains(reply, "x.bin") {
		t.Errorf("reply should name the file, got %q", reply)
	}
	if !strings.Contains(lower, "couldn") {
		t.Errorf("reply should say it could not be read, got %q", reply)
	}
}

func TestRouterAttachmentTooLarge(t *testing.T) {
	v := vault.New(t.TempDir())
	v.EnsureScaffold("ws1")
	r := &Router{vault: v}

	big := make([]byte, maxAttachmentBytes+1)
	if _, err := r.handleAttachment("ws1", Attachment{Filename: "big.csv", Data: big}); err == nil {
		t.Error("an oversized attachment must be refused")
	}
}

func TestRouterAttachmentEmptyRefused(t *testing.T) {
	v := vault.New(t.TempDir())
	v.EnsureScaffold("ws1")
	r := &Router{vault: v}

	if _, err := r.handleAttachment("ws1", Attachment{Filename: "empty.csv"}); err == nil {
		t.Error("an empty attachment must be refused")
	}
}

func TestRouterAttachmentNoVault(t *testing.T) {
	r := &Router{}

	if _, err := r.handleAttachment("ws1", Attachment{Filename: "a.csv", Data: []byte("a,b\n1,2\n")}); err == nil {
		t.Error("handleAttachment must refuse when no vault is wired")
	}
}

func TestRouterAttachmentSurfacesWarnings(t *testing.T) {
	v := vault.New(t.TempDir())
	v.EnsureScaffold("ws1")
	r := &Router{vault: v}

	// A JPEG magic-byte stub with no OCR: convert.go returns a Warnings entry
	// for image content, which the chat reply must surface, not silently drop.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	reply, err := r.handleAttachment("ws1", Attachment{Filename: "photo.jpg", Data: jpeg})
	if err != nil {
		t.Fatalf("handleAttachment: %v", err)
	}
	if !strings.Contains(reply, "Note:") {
		t.Errorf("a lossy conversion must surface its warning in the reply, got %q", reply)
	}
}

func TestHandleRoutesAttachmentToKB(t *testing.T) {
	v := vault.New(t.TempDir())
	v.EnsureScaffold("ws1")
	r := &Router{vault: v}

	var sent string
	send := func(s string) { sent = s }

	msg := Message{
		WorkspaceID: "ws1",
		Platform:    "telegram",
		Attachment:  &Attachment{Filename: "notes.csv", Data: []byte("a,b\n1,2\n")},
	}
	if err := r.Handle(nil, msg, send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(sent, "notes/") {
		t.Errorf("Handle should reply naming the created note, got %q", sent)
	}
}

// TestHandleAttachmentErrorRepliesClearly is Finding 1, half one: a failed
// download must produce a user-visible reply naming the file — never an
// empty-text turn dispatched silently.
func TestHandleAttachmentErrorRepliesClearly(t *testing.T) {
	r := &Router{}

	var sent string
	send := func(s string) { sent = s }

	msg := Message{
		WorkspaceID: "ws1",
		Platform:    "telegram",
		Attachment:  &Attachment{Filename: "report.pdf", Err: errors.New("network blip")},
	}
	if err := r.Handle(nil, msg, send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(sent, "report.pdf") {
		t.Errorf("reply should name the file, got %q", sent)
	}
	if !strings.Contains(strings.ToLower(sent), "couldn't fetch") {
		t.Errorf("reply should say the fetch failed, got %q", sent)
	}
}

// TestHandleAttachmentErrorLeavesChallengeIntact is Finding 1, half two: a
// download failure while a master-password challenge is pending must leave
// that challenge INTACT, not cancel it. Before the fix, a failed download
// dispatched with empty Text, which handleText's pending-challenge branch
// read as an empty password reply and cancelled the challenge outright.
func TestHandleAttachmentErrorLeavesChallengeIntact(t *testing.T) {
	r := &Router{
		challenges: map[string]*secretChallenge{
			"ws1": {action: "show", name: "api-key"},
		},
	}

	var sent string
	send := func(s string) { sent = s }

	msg := Message{
		WorkspaceID: "ws1",
		Platform:    "telegram",
		Attachment:  &Attachment{Filename: "report.pdf", Err: errors.New("network blip")},
	}
	if err := r.Handle(nil, msg, send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ch, ok := r.challenges["ws1"]
	if !ok || ch == nil {
		t.Fatal("a failed attachment download cancelled the pending master-password challenge")
	}
	if ch.action != "show" || ch.name != "api-key" {
		t.Errorf("challenge was mutated: %+v", ch)
	}
	// And the user must have been told about the failed download, not left
	// wondering why the master-password prompt disappeared.
	if !strings.Contains(sent, "report.pdf") {
		t.Errorf("reply should name the file, got %q", sent)
	}
}

// TestHandleTextEmptyDoesNotCancelChallenge is the independent, defensive half
// of Finding 1: even with NO attachment at all, an empty-text message must not
// be read as an answer to a pending master-password challenge. This covers
// any future path that could dispatch empty text (not only a failed download).
func TestHandleTextEmptyDoesNotCancelChallenge(t *testing.T) {
	r := &Router{
		challenges: map[string]*secretChallenge{
			"ws1": {action: "delete", name: "db-password"},
		},
	}

	var sent string
	send := func(s string) { sent = s }

	msg := Message{WorkspaceID: "ws1", Platform: "telegram", Text: ""}
	if err := r.Handle(nil, msg, send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ch, ok := r.challenges["ws1"]
	if !ok || ch == nil {
		t.Fatal("an empty-text message cancelled the pending master-password challenge")
	}
	if ch.action != "delete" || ch.name != "db-password" {
		t.Errorf("challenge was mutated: %+v", ch)
	}
	if strings.Contains(strings.ToLower(sent), "cancelled") {
		t.Errorf("reply must not claim the challenge was cancelled, got %q", sent)
	}
}
