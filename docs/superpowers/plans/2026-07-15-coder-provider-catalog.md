# Coder Provider Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the URL-first "generic" API coder with a curated named-provider catalog (16 providers + Custom), inline API-key paste stored transparently as a secret, and an Advanced base-URL override for every provider.

**Architecture:** Base URLs are single-sourced in `internal/llm` (`defaultBases` + a new `DefaultBaseURL` accessor); every new provider name registers against the existing shared `openaiProvider` factory. `internal/coder` carries a display-only catalog (`APIProvider` struct, enriched in place). The web settings/setup handlers turn a pasted key into a reserved `CODER_KEY_<PROVIDER>` secret using the existing headless master-password decrypt. No DB migration.

**Tech Stack:** Go, `modernc.org/sqlite`, Echo v4 templates, standard `testing`.

## Global Constraints

- Base URLs live ONLY in `internal/llm.defaultBases` — never duplicated in the coder catalog.
- Every catalog provider `Name` (except the Custom entry) MUST be registered in `internal/llm`.
- Reuse existing `workspaces` columns via `db.UpdateWorkspaceCoder(id, kind, bin, timeoutS, backendType, provider, model, apiKeySecret, baseURL)` — NO migration.
- Auto-secret name is exactly `"CODER_KEY_" + strings.ToUpper(provider)`.
- "Custom (OpenAI-compatible)" persists as provider `generic` (backward compat).
- `RequiresKey == false` for `ollama_local` only; its dummy key value is the literal `"ollama"`.
- Run the full suite with `go test ./... -count=1 -timeout 120s`; build with `go build -o bin/simple-agents ./cmd/simple-agents`.
- Anthropic keeps its own factory; do not route it through `openaiProvider`.

---

### Task 1: llm — register providers, defaults, and `DefaultBaseURL` accessor

**Files:**
- Modify: `internal/llm/openai.go` (the `init()` at lines ~217-222)
- Modify: `internal/llm/provider.go` (`defaultBases` at lines ~116-121; add `DefaultBaseURL`)
- Test: `internal/llm/provider_test.go` (create)

**Interfaces:**
- Produces: `func DefaultBaseURL(name string) string` — returns the registered default base URL for a provider name, or `""` if none (including `generic`).
- Produces: registered provider names `zai, ollama, ollama_local, deepseek, groq, xai, mistral, gemini, opencode_zen, opencode_go, perplexity, moonshot` (all on the shared OpenAI factory) plus existing `openai, openrouter, anthropic, generic`.

- [ ] **Step 1: Write the failing test**

Create `internal/llm/provider_test.go`:

```go
package llm

import "testing"

func TestDefaultBaseURL_KnownProviders(t *testing.T) {
	cases := map[string]string{
		"openai":       "https://api.openai.com/v1",
		"anthropic":    "https://api.anthropic.com",
		"openrouter":   "https://openrouter.ai/api/v1",
		"zai":          "https://api.z.ai/api/openai/v1",
		"ollama":       "https://ollama.com/v1",
		"ollama_local": "http://localhost:11434/v1",
		"deepseek":     "https://api.deepseek.com",
		"groq":         "https://api.groq.com/openai/v1",
		"xai":          "https://api.x.ai/v1",
		"mistral":      "https://api.mistral.ai/v1",
		"gemini":       "https://generativelanguage.googleapis.com/v1beta/openai/",
		"opencode_zen": "https://opencode.ai/zen/v1",
		"opencode_go":  "https://opencode.ai/zen/go/v1",
		"perplexity":   "https://api.perplexity.ai",
		"moonshot":     "https://api.moonshot.ai/v1",
	}
	for name, want := range cases {
		if got := DefaultBaseURL(name); got != want {
			t.Errorf("DefaultBaseURL(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDefaultBaseURL_GenericAndUnknownEmpty(t *testing.T) {
	if got := DefaultBaseURL("generic"); got != "" {
		t.Errorf("generic should have no default, got %q", got)
	}
	if got := DefaultBaseURL("nope"); got != "" {
		t.Errorf("unknown should be empty, got %q", got)
	}
}

func TestNew_CatalogProvidersBuild(t *testing.T) {
	for _, name := range []string{
		"zai", "ollama", "ollama_local", "deepseek", "groq", "xai",
		"mistral", "gemini", "opencode_zen", "opencode_go", "perplexity", "moonshot",
	} {
		p, err := New(Config{Provider: name, APIKey: "dummy"})
		if err != nil {
			t.Errorf("New(%q) error: %v", name, err)
			continue
		}
		if p.Name() != name {
			t.Errorf("New(%q).Name() = %q", name, p.Name())
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run 'DefaultBaseURL|CatalogProvidersBuild' -v`
Expected: FAIL — `DefaultBaseURL` undefined; `New("zai")` returns "unknown provider".

- [ ] **Step 3: Add default base URLs**

In `internal/llm/provider.go`, replace the `defaultBases` map literal (currently only openai/openrouter/anthropic) with:

```go
	defaultBases = map[string]string{
		"openai":       "https://api.openai.com/v1",
		"openrouter":   "https://openrouter.ai/api/v1",
		"anthropic":    "https://api.anthropic.com",
		"zai":          "https://api.z.ai/api/openai/v1",
		"ollama":       "https://ollama.com/v1",
		"ollama_local": "http://localhost:11434/v1",
		"deepseek":     "https://api.deepseek.com",
		"groq":         "https://api.groq.com/openai/v1",
		"xai":          "https://api.x.ai/v1",
		"mistral":      "https://api.mistral.ai/v1",
		"gemini":       "https://generativelanguage.googleapis.com/v1beta/openai/",
		"opencode_zen": "https://opencode.ai/zen/v1",
		"opencode_go":  "https://opencode.ai/zen/go/v1",
		"perplexity":   "https://api.perplexity.ai",
		"moonshot":     "https://api.moonshot.ai/v1",
	}
```

- [ ] **Step 4: Add the `DefaultBaseURL` accessor**

In `internal/llm/provider.go`, immediately after `func RegisterProvider(...)`, add:

```go
// DefaultBaseURL returns the registered default endpoint for a provider name,
// or "" if the provider has no default (e.g. "generic", which requires an
// explicit base URL). The web layer uses this to prefill the base-URL field.
func DefaultBaseURL(name string) string {
	return defaultBases[name]
}
```

- [ ] **Step 5: Register the new provider names on the shared OpenAI factory**

In `internal/llm/openai.go`, replace the body of `init()` with:

```go
func init() {
	factory := newOpenAIProvider()
	for _, name := range []string{
		"openai", "openrouter", "generic", // no registry default for generic — New() requires an explicit base_url
		"zai", "ollama", "ollama_local", "deepseek", "groq", "xai",
		"mistral", "gemini", "opencode_zen", "opencode_go", "perplexity", "moonshot",
	} {
		RegisterProvider(name, factory)
	}
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/llm/ -count=1`
Expected: PASS (all, including pre-existing tests).

- [ ] **Step 7: Commit**

```bash
git add internal/llm/openai.go internal/llm/provider.go internal/llm/provider_test.go
git commit -m "feat(llm): register popular providers + DefaultBaseURL accessor"
```

---

### Task 2: coder — enrich the `APIProvider` catalog

**Files:**
- Modify: `internal/coder/detect.go` (`APIProvider` struct ~20-23; `apiProviders` slice ~30-35)
- Test: `internal/coder/apiproviders_test.go` (create)

**Interfaces:**
- Consumes: `llm.DefaultBaseURL(name)` (Task 1).
- Produces: enriched `coder.APIProvider{ Name, Label, Schema, ModelPlaceholder, DocsURL, RequiresKey, Custom bool }`; `coder.APIProviders() []APIProvider` returns the 16-entry catalog (Custom last). Field names `Name`/`Label` are unchanged so existing callers (`web/handlers_misc.go`, `settings.html`) keep compiling.

- [ ] **Step 1: Write the failing test**

Create `internal/coder/apiproviders_test.go`:

```go
package coder

import (
	"strings"
	"testing"

	"simple-agents/internal/llm"
)

func TestAPIProviders_CatalogIntegrity(t *testing.T) {
	provs := APIProviders()
	if len(provs) < 16 {
		t.Fatalf("expected >=16 providers, got %d", len(provs))
	}
	customCount := 0
	seen := map[string]bool{}
	for _, p := range provs {
		if p.Name == "" || p.Label == "" {
			t.Errorf("provider %+v missing Name/Label", p)
		}
		if seen[p.Name] {
			t.Errorf("duplicate provider name %q", p.Name)
		}
		seen[p.Name] = true
		if p.Schema != "openai" && p.Schema != "anthropic" {
			t.Errorf("provider %q bad schema %q", p.Name, p.Schema)
		}
		if p.Custom {
			customCount++
			continue // generic has no registered default
		}
		if llm.DefaultBaseURL(p.Name) == "" {
			t.Errorf("provider %q has no llm default base URL", p.Name)
		}
	}
	if customCount != 1 {
		t.Errorf("expected exactly one Custom provider, got %d", customCount)
	}
}

func TestAPIProviders_RequiresKeyOnlyOllamaLocal(t *testing.T) {
	for _, p := range APIProviders() {
		if !p.RequiresKey && p.Name != "ollama_local" {
			t.Errorf("provider %q unexpectedly RequiresKey=false", p.Name)
		}
		if p.Name == "ollama_local" && p.RequiresKey {
			t.Errorf("ollama_local should not require a key")
		}
	}
}

func TestAPIProviders_CustomIsGenericAndLast(t *testing.T) {
	provs := APIProviders()
	last := provs[len(provs)-1]
	if !last.Custom || last.Name != "generic" {
		t.Errorf("last entry should be Custom generic, got %+v", last)
	}
	if !strings.Contains(strings.ToLower(last.Label), "custom") {
		t.Errorf("Custom label should say custom, got %q", last.Label)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coder/ -run APIProviders -v`
Expected: FAIL — unknown fields `Schema`/`RequiresKey`/`Custom`; only 4 providers.

- [ ] **Step 3: Enrich the struct**

In `internal/coder/detect.go`, replace the `APIProvider` struct with:

```go
// APIProvider describes a direct LLM API provider usable as a coder (the "api"
// kind). These are not probed — they're always available (no host binary
// needed). Base URLs are NOT stored here; they live in internal/llm.defaultBases
// (fetch via llm.DefaultBaseURL) so there is one source of truth.
type APIProvider struct {
	Name             string // registry name, e.g. "zai" — must be registered in internal/llm
	Label            string // human label, e.g. "Z.AI (GLM)"
	Schema           string // "openai" | "anthropic" (display/grouping only)
	ModelPlaceholder string // example model for the free-text hint, e.g. "glm-4.7"
	DocsURL          string // provider API-key/docs page
	RequiresKey      bool   // false only for ollama_local
	Custom           bool   // true only for the Custom (generic) entry
}
```

- [ ] **Step 4: Expand the catalog**

In `internal/coder/detect.go`, replace the `apiProviders` slice with:

```go
var apiProviders = []APIProvider{
	{"openai", "OpenAI", "openai", "gpt-4o", "https://platform.openai.com/api-keys", true, false},
	{"anthropic", "Anthropic", "anthropic", "claude-sonnet-5", "https://console.anthropic.com/settings/keys", true, false},
	{"openrouter", "OpenRouter", "openai", "anthropic/claude-3.5-sonnet", "https://openrouter.ai/keys", true, false},
	{"zai", "Z.AI (GLM)", "openai", "glm-4.7", "https://z.ai/model-api", true, false},
	{"ollama", "Ollama Cloud", "openai", "qwen3-coder:480b", "https://ollama.com/settings/keys", true, false},
	{"ollama_local", "Ollama (Local)", "openai", "qwen2.5-coder", "https://docs.ollama.com/api/openai-compatibility", false, false},
	{"deepseek", "DeepSeek", "openai", "deepseek-chat", "https://platform.deepseek.com/api_keys", true, false},
	{"groq", "Groq", "openai", "llama-3.3-70b-versatile", "https://console.groq.com/keys", true, false},
	{"xai", "xAI (Grok)", "openai", "grok-4", "https://console.x.ai", true, false},
	{"mistral", "Mistral", "openai", "mistral-large-latest", "https://console.mistral.ai/api-keys", true, false},
	{"gemini", "Google Gemini", "openai", "gemini-2.5-pro", "https://aistudio.google.com/apikey", true, false},
	{"opencode_zen", "OpenCode Zen", "openai", "opencode/gpt-5.5", "https://opencode.ai/docs/zen/", true, false},
	{"opencode_go", "OpenCode Go", "openai", "opencode/grok-code", "https://opencode.ai/docs/go/", true, false},
	{"perplexity", "Perplexity", "openai", "sonar-pro", "https://www.perplexity.ai/settings/api", true, false},
	{"moonshot", "Moonshot (Kimi)", "openai", "kimi-k2", "https://platform.moonshot.ai/console/api-keys", true, false},
	{"generic", "Custom (OpenAI-compatible)", "openai", "", "", true, true},
}
```

Leave `APIProviders()` (returns `apiProviders`) unchanged.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/coder/ -run APIProviders -count=1 && go build ./...`
Expected: PASS and clean build (existing callers still compile — only fields were added).

- [ ] **Step 6: Commit**

```bash
git add internal/coder/detect.go internal/coder/apiproviders_test.go
git commit -m "feat(coder): enrich API provider catalog (16 providers + Custom)"
```

---

### Task 3: coder — `PlanKeySecret` helper for the save handler

**Files:**
- Create: `internal/coder/keysecret.go`
- Test: `internal/coder/keysecret_test.go`

**Interfaces:**
- Produces: `func CoderKeySecretName(provider string) string` → `"CODER_KEY_" + strings.ToUpper(provider)`.
- Produces:
  ```go
  type KeySecretPlan struct {
      SecretName  string // value for coder_api_key_secret
      WriteValue  string // value to store when WriteSecret
      WriteSecret bool   // whether the handler must svc.Set(SecretName, WriteValue)
      Err         string // non-empty → user-facing validation error
  }
  func PlanKeySecret(provider, pastedKey, currentSecret string) KeySecretPlan
  ```
- Consumes: `APIProviders()` (Task 2) to look up `RequiresKey`.

- [ ] **Step 1: Write the failing test**

Create `internal/coder/keysecret_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coder/ -run 'KeySecret|PlanKeySecret' -v`
Expected: FAIL — `CoderKeySecretName`/`PlanKeySecret` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/coder/keysecret.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/coder/ -run 'KeySecret|PlanKeySecret' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/keysecret.go internal/coder/keysecret_test.go
git commit -m "feat(coder): PlanKeySecret helper for inline-key auto-secret"
```

---

### Task 4: web — settings handler wiring (auto-secret + catalog JSON)

**Files:**
- Modify: `web/handlers_misc.go` (`settingsPageData` struct ~356-368; `buildSettingsData` ~372-393; `handleSaveWorkspaceCoder` ~447-503)
- Test: manual (no HTTP harness exists; logic lives in the Task-3 helper which is unit-tested)

**Interfaces:**
- Consumes: `coder.APIProviders()`, `coder.PlanKeySecret`, `coder.CoderKeySecretName`, `llm.DefaultBaseURL` (Tasks 1-3); `secrets.DecryptMasterPassword`, `secrets.New(...).Set` (existing, per `handlers_secrets.go:55-61`).
- Produces: `settingsPageData.CoderCatalogJSON template.JS` consumed by the template (Task 5).

- [ ] **Step 1: Add the catalog-JSON field to the view model**

In `web/handlers_misc.go`, add to the `settingsPageData` struct (after `SecretNames`):

```go
	CoderCatalogJSON template.JS // JSON array of the provider catalog for the coder-form JS
```

Ensure `html/template` is imported (add `"html/template"` and `"encoding/json"` to the import block if absent).

- [ ] **Step 2: Populate the catalog JSON in `buildSettingsData`**

In `web/handlers_misc.go`, inside `buildSettingsData`, before the `return`, add:

```go
	type provJS struct {
		Name        string `json:"name"`
		Base        string `json:"base"`
		Model       string `json:"model"`
		Docs        string `json:"docs"`
		RequiresKey bool   `json:"requiresKey"`
		Custom      bool   `json:"custom"`
	}
	cat := coder.APIProviders()
	pjs := make([]provJS, 0, len(cat))
	for _, p := range cat {
		pjs = append(pjs, provJS{
			Name: p.Name, Base: llm.DefaultBaseURL(p.Name), Model: p.ModelPlaceholder,
			Docs: p.DocsURL, RequiresKey: p.RequiresKey, Custom: p.Custom,
		})
	}
	catJSON, _ := json.Marshal(pjs)
```

Then add `CoderCatalogJSON: template.JS(catJSON),` to the returned `&settingsPageData{...}`. Add the `simple-agents/internal/llm` import if not already present.

- [ ] **Step 3: Rewrite the api-branch of `handleSaveWorkspaceCoder`**

In `web/handlers_misc.go`, replace the `if kind == "api" { ... }` block (the one reading `coder_provider`/`coder_model`/`coder_api_key_secret`/`coder_base_url` and validating) with:

```go
	if kind == "api" {
		provider = c.FormValue("coder_provider")
		model = strings.TrimSpace(c.FormValue("coder_model"))
		baseURL = strings.TrimSpace(c.FormValue("coder_base_url"))
		backendType = "api"
		if provider == "" {
			p.Error = "Provider is required for an API coder"
			return c.Render(http.StatusBadRequest, "dashboard/settings.html", s.buildSettingsData(p, w))
		}
		if model == "" {
			p.Error = "Model is required for an API coder"
			return c.Render(http.StatusBadRequest, "dashboard/settings.html", s.buildSettingsData(p, w))
		}
		// Custom (generic) requires an explicit base URL; catalog providers resolve theirs from llm.
		isCustom := provider == "generic"
		if isCustom && baseURL == "" {
			p.Error = "A base URL is required for a Custom (OpenAI-compatible) provider"
			return c.Render(http.StatusBadRequest, "dashboard/settings.html", s.buildSettingsData(p, w))
		}
		// Decide the API-key secret from the pasted key + existing reference.
		plan := coder.PlanKeySecret(provider, strings.TrimSpace(c.FormValue("coder_api_key")), w.CoderAPIKeySecret)
		if plan.Err != "" {
			p.Error = plan.Err
			return c.Render(http.StatusBadRequest, "dashboard/settings.html", s.buildSettingsData(p, w))
		}
		if plan.WriteSecret {
			if w.SecretsSalt == "" || w.EncryptedMasterPassword == "" {
				p.Error = "Complete workspace setup before configuring an API coder"
				return c.Render(http.StatusBadRequest, "dashboard/settings.html", s.buildSettingsData(p, w))
			}
			masterPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
			if err != nil {
				p.Error = "Could not decrypt master password — re-run workspace setup"
				return c.Render(http.StatusInternalServerError, "dashboard/settings.html", s.buildSettingsData(p, w))
			}
			svc := secrets.New(s.db, w.ID, masterPw, w.SecretsSalt)
			if err := svc.Set(context.Background(), plan.SecretName, plan.WriteValue); err != nil {
				p.Error = "Failed to store API key: " + err.Error()
				return c.Render(http.StatusInternalServerError, "dashboard/settings.html", s.buildSettingsData(p, w))
			}
		}
		apiKeySecret = plan.SecretName
	} else {
```

Note: `apiKeySecret`, `provider`, `model`, `baseURL`, `backendType`, `bin` are the already-declared vars in the surrounding function. Confirm `context`, `secrets`, and `coder` are imported in `web/handlers_misc.go` (add any missing).

- [ ] **Step 4: Build and smoke-test**

```bash
go build -o bin/simple-agents ./cmd/simple-agents && go vet ./web/...
```
Expected: clean build.

Manual check (server already bootstrapped): `make deploy`, log in, enter a workspace, open `/dashboard/settings`, set Kind=api, Provider=Z.AI, paste a dummy key, Model=`glm-4.7`, Save. Then open `/dashboard/secrets` and confirm `CODER_KEY_ZAI` exists. Re-save the coder form with the key field blank and confirm it still references `CODER_KEY_ZAI` (edit retention). Expected: both hold.

- [ ] **Step 5: Commit**

```bash
git add web/handlers_misc.go
git commit -m "feat(web): inline API-key paste stored as CODER_KEY_<provider> secret"
```

---

### Task 5: web — settings template (catalog picker + Advanced base URL)

**Files:**
- Modify: `web/templates/dashboard/settings.html` (the `#coder_api` block, lines ~63-101, and the `<script>` at ~113-120)
- Test: `go test ./web/ -run Template` (existing template smoke test must still pass) + manual

**Interfaces:**
- Consumes: `.APIProviders` (existing range), `.CoderCatalogJSON` (Task 4), `.Workspace.CoderProvider/CoderModel/CoderBaseURL/CoderAPIKeySecret`.

- [ ] **Step 1: Replace the model, base-URL, and API-key fields**

In `web/templates/dashboard/settings.html`, replace the three form-controls inside `#coder_api` that currently render Model, Base URL, and API-key-secret (lines ~73-100) with:

```html
          <div class="form-control">
            <label class="label pb-1"><span class="label-text font-medium">Model</span></label>
            <input type="text" name="coder_model" id="coder_model" class="input input-bordered w-full"
              value="{{if .Workspace}}{{.Workspace.CoderModel}}{{end}}" placeholder="e.g. gpt-4o">
          </div>
          <div class="form-control" id="coder_key_wrap">
            <label class="label pb-1"><span class="label-text font-medium">API key</span></label>
            <input type="password" name="coder_api_key" id="coder_api_key" class="input input-bordered w-full"
              autocomplete="off" placeholder="{{if .Workspace.CoderAPIKeySecret}}leave blank to keep current key{{else}}paste your provider API key{{end}}">
            <label class="label pt-1"><span class="label-text-alt text-base-content/30">Stored securely as a secret automatically. <a id="coder_docs" class="link" href="#" target="_blank" rel="noopener">Get a key ↗</a></span></label>
          </div>
          <details class="mt-1" id="coder_advanced">
            <summary class="text-sm cursor-pointer text-base-content/60">Advanced</summary>
            <div class="form-control mt-2">
              <label class="label pb-1"><span class="label-text font-medium">Base URL</span></label>
              <input type="text" name="coder_base_url" id="coder_base_url" class="input input-bordered w-full"
                value="{{if .Workspace}}{{.Workspace.CoderBaseURL}}{{end}}" placeholder="https://api.example.com/v1">
              <label class="label pt-1"><span class="label-text-alt text-base-content/30">Auto-filled from the provider. Override for a self-hosted / regional / coding-plan endpoint. Required for Custom.</span></label>
            </div>
          </details>
```

(Keep the existing Provider `<select>` block above these unchanged — it already ranges `.APIProviders` and emits `<option value="{{.Name}}">{{.Label}}</option>`.)

- [ ] **Step 2: Replace the `<script>` block with catalog-driven behavior**

In `web/templates/dashboard/settings.html`, replace the existing `<script>` (the `saToggleCoderKind` block) with:

```html
  <script>
    const SA_CODER_CATALOG = {{.CoderCatalogJSON}};
    const SA_CODER_HAS_KEY = {{if .Workspace.CoderAPIKeySecret}}true{{else}}false{{end}};
    function saCoderProv() {
      const sel = document.querySelector('#coder_api select[name="coder_provider"]');
      return (SA_CODER_CATALOG || []).find(p => p.name === (sel && sel.value)) || null;
    }
    function saApplyProvider() {
      const p = saCoderProv();
      if (!p) return;
      const model = document.getElementById('coder_model');
      const keyWrap = document.getElementById('coder_key_wrap');
      const base = document.getElementById('coder_base_url');
      const adv = document.getElementById('coder_advanced');
      const docs = document.getElementById('coder_docs');
      if (model && p.model) model.placeholder = 'e.g. ' + p.model;
      if (keyWrap) keyWrap.style.display = p.requiresKey ? '' : 'none';
      if (docs) { docs.href = p.docs || '#'; docs.style.display = p.docs ? '' : 'none'; }
      // Prefill base URL from the provider default only when the field is empty
      // (don't clobber a saved override).
      if (base && !base.value && p.base) base.placeholder = p.base;
      if (adv) adv.open = !!p.custom; // Custom expands the base-URL field
    }
    function saToggleCoderKind() {
      const kind = document.getElementById('coder_kind').value;
      document.getElementById('coder_local').style.display = kind === 'api' ? 'none' : '';
      document.getElementById('coder_api').style.display = kind === 'api' ? '' : 'none';
      if (kind === 'api') saApplyProvider();
    }
    (function () {
      const sel = document.querySelector('#coder_api select[name="coder_provider"]');
      if (sel) sel.addEventListener('change', saApplyProvider);
      saToggleCoderKind();
    })();
  </script>
```

- [ ] **Step 3: Run the template smoke test + build**

Run: `go test ./web/ -run Template -count=1 && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: PASS + clean build (the template must parse and execute with the new `.CoderCatalogJSON` field).

- [ ] **Step 4: Manual visual check**

`make deploy`; open `/dashboard/settings`; switch Kind→api; change Provider and confirm: model placeholder changes, the API-key field hides for "Ollama (Local)", the docs link points at the provider, and selecting "Custom (OpenAI-compatible)" auto-opens Advanced with an editable base URL.

- [ ] **Step 5: Commit**

```bash
git add web/templates/dashboard/settings.html
git commit -m "feat(web): catalog-driven coder provider picker + Advanced base URL"
```

---

### Task 6: web — setup wizard API-coder branch

**Files:**
- Modify: `web/handlers_setup.go` (`handleSetupCoder` ~/func; add `CoderCatalogJSON` to `setupData` if needed)
- Modify: `web/templates/auth/setup.html` (step 3, lines ~70-101)
- Test: `go test ./web/ -run Template` + manual

**Interfaces:**
- Consumes: same helpers as Task 4 (`coder.PlanKeySecret`, `secrets.*`, `db.UpdateWorkspaceCoder`).
- Produces: a working "Direct LLM API" path in onboarding, mirroring settings.

- [ ] **Step 1: Add a Kind switch + API fields to the setup form**

In `web/templates/auth/setup.html` step 3 (`{{else if eq .Step 3}}`), add — above the existing `coder_bin` control, inside the same `<form>` — a Kind selector and an API sub-form. Reuse the settings markup pattern:

```html
          <div class="form-control">
            <label class="label pb-1"><span class="label-text font-medium">Kind</span></label>
            <select name="coder_kind" id="setup_coder_kind" class="select select-bordered w-full" onchange="setupToggleKind()">
              <option value="local" selected>Local CLI (claude-code, opencode, codex, cursor)</option>
              <option value="api">Direct LLM API (OpenAI, Anthropic, Z.AI, Ollama, …)</option>
            </select>
          </div>
          <div id="setup_coder_api" style="display:none" class="space-y-3">
            <div class="form-control">
              <label class="label pb-1"><span class="label-text font-medium">Provider</span></label>
              <select name="coder_provider" id="setup_coder_provider" class="select select-bordered w-full">
                {{range .APIProviders}}<option value="{{.Name}}">{{.Label}}</option>{{end}}
              </select>
            </div>
            <div class="form-control">
              <label class="label pb-1"><span class="label-text font-medium">Model</span></label>
              <input type="text" name="coder_model" id="setup_coder_model" class="input input-bordered w-full" placeholder="e.g. gpt-4o">
            </div>
            <div class="form-control" id="setup_coder_key_wrap">
              <label class="label pb-1"><span class="label-text font-medium">API key</span></label>
              <input type="password" name="coder_api_key" id="setup_coder_key" autocomplete="off" class="input input-bordered w-full" placeholder="paste your provider API key">
              <label class="label pt-1"><span class="label-text-alt text-base-content/30">Stored securely as a secret. <a id="setup_coder_docs" class="link" href="#" target="_blank" rel="noopener">Get a key ↗</a></span></label>
            </div>
            <details id="setup_coder_advanced"><summary class="text-sm cursor-pointer text-base-content/60">Advanced</summary>
              <div class="form-control mt-2">
                <label class="label pb-1"><span class="label-text font-medium">Base URL</span></label>
                <input type="text" name="coder_base_url" id="setup_coder_base" class="input input-bordered w-full" placeholder="https://api.example.com/v1">
              </div>
            </details>
          </div>
          <div id="setup_coder_local">
```

Close the extra `setup_coder_local` wrapper `</div>` after the existing `coder_bin`/timeout controls (before the submit button). Then add, near the bottom of the step, the driver script:

```html
          <script>
            const SETUP_CATALOG = {{.CoderCatalogJSON}};
            function setupApplyProvider(){
              const sel=document.getElementById('setup_coder_provider');
              const p=(SETUP_CATALOG||[]).find(x=>x.name===(sel&&sel.value)); if(!p) return;
              const m=document.getElementById('setup_coder_model'); if(m&&p.model)m.placeholder='e.g. '+p.model;
              const kw=document.getElementById('setup_coder_key_wrap'); if(kw)kw.style.display=p.requiresKey?'':'none';
              const d=document.getElementById('setup_coder_docs'); if(d){d.href=p.docs||'#';d.style.display=p.docs?'':'none';}
              const b=document.getElementById('setup_coder_base'); if(b&&!b.value&&p.base)b.placeholder=p.base;
              const a=document.getElementById('setup_coder_advanced'); if(a)a.open=!!p.custom;
            }
            function setupToggleKind(){
              const k=document.getElementById('setup_coder_kind').value;
              document.getElementById('setup_coder_api').style.display=k==='api'?'':'none';
              document.getElementById('setup_coder_local').style.display=k==='api'?'none':'';
              if(k==='api')setupApplyProvider();
            }
            (function(){const s=document.getElementById('setup_coder_provider');if(s)s.addEventListener('change',setupApplyProvider);setupToggleKind();})();
          </script>
```

- [ ] **Step 2: Provide the catalog JSON + providers to the setup view**

In `web/handlers_setup.go`, find where `setupData` is built for the coder step (Step 3 render) and add the provider catalog. Add fields to the `setupData` struct:

```go
	APIProviders     []coder.APIProvider
	CoderCatalogJSON template.JS
```

and populate them wherever `setupData` for step 3 is constructed (build `CoderCatalogJSON` with the same `provJS` marshal used in Task 4 — extract that into a shared helper `s.coderCatalogJSON()` in `web/handlers_misc.go` returning `template.JS`, and call it from both places to stay DRY). Add `APIProviders: coder.APIProviders()`.

- [ ] **Step 3: Handle the API branch in `handleSetupCoder`**

In `web/handlers_setup.go`, replace `handleSetupCoder` with a version that branches on kind, reusing the Task-4 logic:

```go
func (s *Server) handleSetupCoder(c echo.Context, w *db.Workspace) error {
	kind := c.FormValue("coder_kind")
	timeoutS := 0
	if v, err := strconv.Atoi(c.FormValue("coder_timeout_s")); err == nil && v > 0 {
		timeoutS = v
	}
	if kind == "api" {
		provider := c.FormValue("coder_provider")
		model := strings.TrimSpace(c.FormValue("coder_model"))
		baseURL := strings.TrimSpace(c.FormValue("coder_base_url"))
		if provider == "" || model == "" {
			return s.renderSetupCoderErr(c, w, "Provider and model are required for an API coder")
		}
		if provider == "generic" && baseURL == "" {
			return s.renderSetupCoderErr(c, w, "A base URL is required for a Custom provider")
		}
		plan := coder.PlanKeySecret(provider, strings.TrimSpace(c.FormValue("coder_api_key")), w.CoderAPIKeySecret)
		if plan.Err != "" {
			return s.renderSetupCoderErr(c, w, plan.Err)
		}
		if plan.WriteSecret {
			if w.SecretsSalt == "" || w.EncryptedMasterPassword == "" {
				return s.renderSetupCoderErr(c, w, "Set a master password (step 2) before configuring an API coder")
			}
			masterPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
			if err != nil {
				return s.renderSetupCoderErr(c, w, "Could not decrypt master password — re-run setup")
			}
			if err := secrets.New(s.db, w.ID, masterPw, w.SecretsSalt).Set(context.Background(), plan.SecretName, plan.WriteValue); err != nil {
				return s.renderSetupCoderErr(c, w, "Failed to store API key: "+err.Error())
			}
		}
		if err := s.db.UpdateWorkspaceCoder(w.ID, "api", "", timeoutS, "api", provider, model, plan.SecretName, baseURL); err != nil {
			return err
		}
	} else {
		bin := c.FormValue("coder_bin")
		backend := c.FormValue("coder_backend_type")
		if err := s.db.UpdateWorkspaceCoder(w.ID, "local", bin, timeoutS, backend, "", "", "", ""); err != nil {
			return err
		}
	}
	_ = s.db.SetSetting(w.ID, "wizard_coder_done", "1")
	s.audit.Log(w.ID, "configure_coder", "workspace:"+w.ID, kind, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/setup")
}
```

Add a small helper `renderSetupCoderErr(c, w, msg)` that re-renders `auth/setup.html` at step 3 with `sd.Error = msg` and the provider catalog populated (mirror the existing step-3 render path). Ensure `strings`, `context`, `secrets`, `coder` are imported.

- [ ] **Step 4: Build, template test, manual**

Run: `go build -o bin/simple-agents ./cmd/simple-agents && go test ./web/ -run Template -count=1`
Expected: clean build + PASS.

Manual: create a fresh workspace, run the wizard to step 3, pick "Direct LLM API" → Z.AI, paste a dummy key, model `glm-4.7`, submit; confirm the wizard advances and `/dashboard/settings` shows the api coder with `CODER_KEY_ZAI` in Secrets.

- [ ] **Step 5: Commit**

```bash
git add web/handlers_setup.go web/templates/auth/setup.html web/handlers_misc.go
git commit -m "feat(web): API-coder provider path in the setup wizard"
```

---

### Task 7: Full suite + docs

**Files:**
- Modify: `CLAUDE.md` (the coder-config sentence under "Per-workspace coder" / "API coder engine")

- [ ] **Step 1: Run the whole suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 2: Update CLAUDE.md**

In `/home/rookie/simple-agents-v2/CLAUDE.md`, update the `coder.APIProviders()` mention (under "Per-workspace coder") to note the catalog is now 16 named providers + Custom, base URLs single-sourced in `internal/llm.DefaultBaseURL`, and that the coder form accepts an inline API key stored as `CODER_KEY_<PROVIDER>`. One or two sentences.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(CLAUDE.md): document the coder provider catalog + inline-key auto-secret"
```

---

## Self-Review

**Spec coverage:**
- §1 single-source base URLs + `DefaultBaseURL` → Task 1 ✓
- §2 display-only catalog (enriched struct) → Task 2 ✓
- §3 Advanced base-URL override for every provider → Task 5 (settings) + Task 6 (setup) ✓
- §4 inline key → auto-secret, edit retention, no-key dummy, Custom base-URL required → Task 3 (logic) + Task 4/6 (wiring) ✓
- §5 UI settings + setup → Task 5 + Task 6 ✓
- Backward compat (generic/openai/anthropic untouched; Custom=generic) → Tasks 1, 2, 4 ✓
- Edge cases (switching leaves stale secrets; ollama_local localhost caveat; secret-name validity; blank master pw guard) → Task 3 tests + Task 4/6 guards; localhost caveat surfaced in template help text ✓
- Testing (llm resolve/build, catalog integrity, plan logic) → Tasks 1-3 automated; handler wiring manual (no HTTP harness — consistent with repo's known-gaps posture) ✓

**Placeholder scan:** No TBD/TODO; every code step shows full code. Manual-verification steps give exact click paths and expected results. ✓

**Type consistency:** `APIProvider` fields (`Name/Label/Schema/ModelPlaceholder/DocsURL/RequiresKey/Custom`) used identically in Tasks 2-6. `KeySecretPlan{SecretName,WriteValue,WriteSecret,Err}` and `PlanKeySecret(provider, pastedKey, currentSecret)` consistent Tasks 3-6. `DefaultBaseURL(name)` consistent Tasks 1-4. `CoderCatalogJSON template.JS` consistent Tasks 4-6. ✓

**Deviation from spec (noted):** the spec named the enriched type `APIProviderInfo`; the plan enriches the existing `APIProvider` in place instead, to avoid renaming callers — behavior-identical.
