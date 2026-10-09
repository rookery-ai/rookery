package connectors

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestGraphSplitsRecipients(t *testing.T) {
	b, _, _ := msgraphDraft(map[string]any{"to": "a@x.com, b@y.com; c@z.com", "cc": "d@x.com", "subject": "s", "body": "b"})
	var m struct {
		To  []struct{ EmailAddress struct{ Address string } } `json:"toRecipients"`
		Cc  []struct{ EmailAddress struct{ Address string } } `json:"ccRecipients"`
		Bcc []any                                             `json:"bccRecipients"`
	}
	json.Unmarshal(b, &m)
	if len(m.To) != 3 || m.To[1].EmailAddress.Address != "b@y.com" || len(m.Cc) != 1 || m.Bcc != nil {
		t.Fatalf("recipients: %s", b)
	}
}

func TestGmailHeaders(t *testing.T) {
	b, _, _ := gmailRFC822(map[string]any{"to": "a@x.com;b@y.com", "bcc": "c@z.com", "subject": "Состанок утре", "body": "здраво"})
	var m struct{ Raw string }
	json.Unmarshal(b, &m)
	raw, _ := base64.URLEncoding.DecodeString(m.Raw)
	msg := string(raw)
	for _, want := range []string{"To: a@x.com, b@y.com\r\n", "Bcc: c@z.com\r\n", "Subject: =?utf-8?q?", "\r\n\r\nздраво"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Cc:") {
		t.Fatal("empty cc must not produce a header")
	}
	// An argument carrying a newline must not smuggle in a header.
	b, _, _ = gmailRFC822(map[string]any{"to": "a@x.com", "subject": "hi\r\nBcc: evil@x.com", "body": "b"})
	json.Unmarshal(b, &m)
	raw, _ = base64.URLEncoding.DecodeString(m.Raw)
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("header injection: %q", raw)
	}
}

func TestSheetsFindReplaceScopesAllSheets(t *testing.T) {
	b, _, _ := sheetsFindReplace(map[string]any{"find": "a", "replacement": "b"})
	if !strings.Contains(string(b), `"allSheets":true`) {
		t.Fatalf("no sheet_id must search every sheet: %s", b)
	}
	b, _, _ = sheetsFindReplace(map[string]any{"find": "a", "replacement": "b", "sheet_id": float64(7)})
	if strings.Contains(string(b), "allSheets") || !strings.Contains(string(b), `"sheetId":7`) {
		t.Fatalf("sheet_id must scope to that sheet: %s", b)
	}
}

func TestOnenoteAppendBody(t *testing.T) {
	b, ct, _ := onenoteAppend(map[string]any{"content": "a & b"})
	var cmds []map[string]string
	if err := json.Unmarshal(b, &cmds); err != nil || ct != "application/json" || len(cmds) != 1 {
		t.Fatalf("got %s %s %v", ct, b, err)
	}
	if c := cmds[0]; c["target"] != "body" || c["action"] != "append" || c["content"] != "<p>a &amp; b</p>" {
		t.Fatalf("command = %v", c)
	}
}
