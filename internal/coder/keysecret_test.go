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

func TestPlanKeySecret_LocalProviderStoresPlaceholder(t *testing.T) {
	// A local server accepts any bearer string, but llm.New rejects an empty
	// key — so a placeholder is stored rather than nothing. The value is
	// deliberately self-describing: "ollama" read like a bug once four more
	// local servers joined the tier.
	for _, name := range []string{"ollama_local", "lmstudio", "vllm", "localai", "jan", "llamacpp"} {
		p := PlanKeySecret(name, "", "")
		if !p.WriteSecret || p.WriteValue != placeholderLocalKey || p.Err != "" {
			t.Errorf("PlanKeySecret(%q): unexpected plan %+v", name, p)
		}
		if want := CoderKeySecretName(name); p.SecretName != want {
			t.Errorf("PlanKeySecret(%q).SecretName = %q, want %q", name, p.SecretName, want)
		}
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
