package coder

import "testing"

func TestCoderKeySecretName(t *testing.T) {
	if got := CoderKeySecretName("zai"); got != "CODER_KEY_ZAI" {
		t.Errorf("got %q", got)
	}
	if got := CoderKeySecretName("opencode_go"); got != "CODER_KEY_OPENCODE_GO" {
		t.Errorf("got %q", got)
	}
}

func TestPlanKeySecret_PastedKeyStored(t *testing.T) {
	p := PlanKeySecret("zai", "sk-abc", "")
	if !p.WriteSecret || p.SecretName != "CODER_KEY_ZAI" || p.WriteValue != "sk-abc" || p.Err != "" {
		t.Fatalf("unexpected plan: %+v", p)
	}
}

func TestPlanKeySecret_NoKeyProviderStoresDummy(t *testing.T) {
	p := PlanKeySecret("ollama_local", "", "")
	if !p.WriteSecret || p.SecretName != "CODER_KEY_OLLAMA_LOCAL" || p.WriteValue != "ollama" || p.Err != "" {
		t.Fatalf("unexpected plan: %+v", p)
	}
}

func TestPlanKeySecret_EditRetainsExistingSecret(t *testing.T) {
	p := PlanKeySecret("zai", "", "CODER_KEY_ZAI")
	if p.WriteSecret || p.SecretName != "CODER_KEY_ZAI" || p.Err != "" {
		t.Fatalf("edit with blank key should retain secret, got: %+v", p)
	}
}

func TestPlanKeySecret_MissingKeyErrors(t *testing.T) {
	p := PlanKeySecret("zai", "", "")
	if p.Err == "" || p.WriteSecret {
		t.Fatalf("missing key should error, got: %+v", p)
	}
}
