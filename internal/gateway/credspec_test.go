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

func TestSplitCredsNoExtraReturnsEmptyConfig(t *testing.T) {
	spec := CredSpec{Platform: "tg-noextra", Fields: []CredField{{Key: "token"}}}
	token, cfg, err := SplitCreds(spec, map[string]string{"token": "t"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "t" {
		t.Fatalf("token = %q", token)
	}
	if cfg != "" {
		t.Fatalf("expected empty config JSON, got %q", cfg)
	}
}

func TestCredSpecsSorted(t *testing.T) {
	RegisterCredSpec(CredSpec{Platform: "zzz-sort"})
	RegisterCredSpec(CredSpec{Platform: "aaa-sort"})
	specs := CredSpecs()
	ai, zi := -1, -1
	for i, s := range specs {
		switch s.Platform {
		case "aaa-sort":
			ai = i
		case "zzz-sort":
			zi = i
		}
	}
	if ai == -1 || zi == -1 || ai > zi {
		t.Fatalf("CredSpecs not sorted: aaa-sort at %d, zzz-sort at %d", ai, zi)
	}
}
