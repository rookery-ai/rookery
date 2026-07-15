package coder

import "strings"

// CoderKeySecretName is the reserved auto-secret name that holds a provider's
// API key when the user pastes it on the coder form.
func CoderKeySecretName(provider string) string {
	return "CODER_KEY_" + strings.ToUpper(provider)
}

// KeySecretPlan tells the coder-save handler how to persist the API key.
type KeySecretPlan struct {
	SecretName  string // value to store in coder_api_key_secret
	WriteValue  string // value to svc.Set when WriteSecret is true
	WriteSecret bool   // whether the handler must write a secret
	Err         string // non-empty → user-facing validation error (no write, no save)
}

// PlanKeySecret decides the API-key secret for an "api" coder save.
//
//	provider      — chosen registry name
//	pastedKey     — the inline key field (may be empty)
//	currentSecret — coder_api_key_secret already stored (edit case; may be empty)
//
// Rules: a pasted key is stored under CODER_KEY_<PROVIDER>. A provider that
// needs no key (ollama_local) gets a dummy "ollama" value so llm.New's
// key-required check passes. On edit with a blank key and an already-referenced
// secret, the existing secret is retained (no re-paste required). Otherwise the
// key is required.
func PlanKeySecret(provider, pastedKey, currentSecret string) KeySecretPlan {
	name := CoderKeySecretName(provider)
	if pastedKey != "" {
		return KeySecretPlan{SecretName: name, WriteValue: pastedKey, WriteSecret: true}
	}
	if !providerRequiresKey(provider) {
		return KeySecretPlan{SecretName: name, WriteValue: "ollama", WriteSecret: true}
	}
	if currentSecret != "" {
		return KeySecretPlan{SecretName: currentSecret}
	}
	return KeySecretPlan{Err: "API key is required for this provider"}
}

// providerRequiresKey reports whether the catalog marks provider as needing a
// key. Unknown providers default to true (safe).
func providerRequiresKey(provider string) bool {
	for _, p := range apiProviders {
		if p.Name == provider {
			return p.RequiresKey
		}
	}
	return true
}
