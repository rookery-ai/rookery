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
	// AuthTest with an obviously invalid token must error (no network dependency
	// on the error path — slack lib returns "invalid_auth" style error offline too;
	// if this proves flaky offline, skip via testing.Short()).
	if _, err := validateSlackToken("not-a-real-token"); err == nil {
		t.Fatal("invalid token must error")
	}
}
