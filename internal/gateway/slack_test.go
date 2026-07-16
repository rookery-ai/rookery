package gateway

import "testing"

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

func TestParseSlackConfig(t *testing.T) {
	tok, err := parseSlackConfig(`{"app_token":"xapp-1"}`)
	if err != nil || tok != "xapp-1" {
		t.Fatalf("parseSlackConfig = %q, %v", tok, err)
	}
	if _, err := parseSlackConfig(`{}`); err == nil {
		t.Fatal("missing app_token must error")
	}
}
