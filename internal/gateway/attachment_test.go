package gateway

import (
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/vault"
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
	if err == nil && !strings.Contains(strings.ToLower(reply), "couldn") {
		t.Errorf("an unconvertible attachment must say so plainly, got reply=%q err=%v", reply, err)
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
