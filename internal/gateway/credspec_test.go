package gateway

import "testing"

func TestSplitCredsSeparatesTokenFromConfig(t *testing.T) {
	spec := CredSpec{Platform: "slack", Fields: []CredField{
		{Key: "token", Label: "Bot Token", Secret: true},
		{Key: "app_token", Label: "App Token", Secret: true},
	}}
	token, cfg, err := SplitCreds(spec, map[string]string{"token": "xoxb", "app_token": "xapp"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "xoxb" {
		t.Fatalf("token = %q", token)
	}
	if cfg != `{"app_token":"xapp"}` {
		t.Fatalf("config = %q", cfg)
	}
}

func TestRegisterAndGetCredSpec(t *testing.T) {
	RegisterCredSpec(CredSpec{Platform: "cs-test", Fields: []CredField{{Key: "token"}}})
	if _, ok := CredSpecFor("cs-test"); !ok {
		t.Fatal("spec not registered")
	}
}
