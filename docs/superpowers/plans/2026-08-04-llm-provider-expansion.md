# LLM Provider Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add fifteen drop-in OpenAI-compatible LLM providers (taking the coder catalog from 16 to 31) and make a provider's base URL discoverable and editable, so a non-default Ollama port can actually be configured.

**Architecture:** Every new provider speaks the OpenAI chat-completions schema with `Authorization: Bearer <key>`, so `internal/llm/openaiProvider` handles all fifteen unchanged — each is three data edits (a default base URL, a factory registration, a catalog row). A new `Group` field (`"hosted"` / `"local"`) on `coder.APIProvider` classifies the catalog, which the SPA renders as two sections and which makes the "keyless ⇔ local" invariant expressible as a test. The base-URL fix is pure UI: the server already ships each provider's default to the SPA; the form simply never used it.

**Tech Stack:** Go 1.x (`internal/llm`, `internal/coder`, `web`), React + TypeScript + Tailwind v4 (`web/ui`), Vitest + Testing Library, `go test`.

## Global Constraints

- **`defaultBases` values must be resolved, dialable URLs — never templates.** `llm.New` assigns the value straight into the HTTP client with no validation, so a literal `{region}` would pass every test and fail at request time with an opaque DNS error. Region/port variation goes through the base-URL override.
- **`generic` must remain the LAST entry** in `coder.apiProviders` (`TestAPIProviders_CustomIsGenericAndLast`). All new providers append **before** it.
- **`Schema` is the wire schema, not the model vendor.** Every new provider is `"openai"`, including Bedrock (which serves Anthropic models over an OpenAI-shaped API).
- **Conventional Commits** on every commit: `type(scope): summary`.
- **Never commit to `main`.** All work is on the current feature branch.
- `apiProviders` uses **positional** struct literals. Adding a field to `APIProvider` requires updating all 16 existing rows.
- Brand logos are **committed SVGs**; nothing is fetched at runtime. `web/logo_coverage_test.go` fails the build for a provider slug with no asset.

---

### Task 1: The fifteen providers, their tests, and their brand logos

The Go catalog, the `Group` field, the test generalizations, and the logos ship as one deliverable because `web/logo_coverage_test.go` gates the catalog: adding a provider without its logo leaves `go test ./...` red.

**Files:**
- Modify: `internal/llm/provider.go` (the `defaultBases` map, ~line 123)
- Modify: `internal/llm/openai.go` (the `init()` registration list, ~line 222)
- Modify: `internal/coder/detect.go` (the `APIProvider` struct ~line 22, the `apiProviders` slice ~line 37)
- Modify: `scripts/vendor-brand-logos.sh` (the `LOBEHUB` manifest)
- Modify: `web/logo_coverage_test.go` (the `allowNoLogo` map)
- Test: `internal/llm/provider_test.go`, `internal/coder/apiproviders_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `coder.GroupHosted = "hosted"` and `coder.GroupLocal = "local"` string constants; `coder.APIProvider.Group string` as the 8th positional field. Tasks 3–5 rely on both.

- [ ] **Step 1: Write the failing tests**

Replace `TestAPIProviders_RequiresKeyOnlyOllamaLocal` in `internal/coder/apiproviders_test.go` with the generalized version, raise the catalog floor, and add the dialable-URL guard. Add `"net/url"` to that file's imports.

```go
func TestAPIProviders_KeylessIsLocalTier(t *testing.T) {
	// Keyless ⇔ local. A hosted provider that forgets RequiresKey would let a
	// user select it with no credential; a local one that demands a key blocks
	// a server that accepts any string. Both directions matter, which is why
	// this is an iff and not the old "only ollama_local" spot check.
	for _, p := range APIProviders() {
		wantKeyless := p.Group == GroupLocal
		if !p.RequiresKey != wantKeyless {
			t.Errorf("provider %q: RequiresKey=%v with Group=%q — keyless must mean local and vice versa",
				p.Name, p.RequiresKey, p.Group)
		}
	}
}

func TestAPIProviders_BaseURLsAreDialable(t *testing.T) {
	// defaultBases must hold a URL that can actually be dialled. A templated
	// value like "https://bedrock-mantle.{region}.api.aws/v1" would satisfy
	// every other test in this file and then fail at request time with a DNS
	// error on a literal "{region}". Region/port variation belongs in the
	// per-workspace base-URL override, not in the registry default.
	for _, p := range APIProviders() {
		if p.Custom {
			continue // generic has no default by design
		}
		base := llm.DefaultBaseURL(p.Name)
		if strings.ContainsAny(base, "{}") {
			t.Errorf("provider %q base URL %q contains a template placeholder", p.Name, base)
			continue
		}
		u, err := url.Parse(base)
		if err != nil {
			t.Errorf("provider %q base URL %q does not parse: %v", p.Name, base, err)
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Errorf("provider %q base URL %q has scheme %q, want http/https", p.Name, base, u.Scheme)
		}
		if u.Host == "" {
			t.Errorf("provider %q base URL %q has no host", p.Name, base)
		}
	}
}
```

In the same file, raise the floor and assert the group in `TestAPIProviders_CatalogIntegrity`:

```go
	if len(provs) < 31 {
		t.Fatalf("expected >=31 providers, got %d", len(provs))
	}
```

and inside that function's loop, before the `p.Custom` check:

```go
		if p.Group != GroupHosted && p.Group != GroupLocal {
			t.Errorf("provider %q has bad group %q", p.Name, p.Group)
		}
```

In `internal/llm/provider_test.go`, add all fifteen to the `TestDefaultBaseURL_KnownProviders` map:

```go
		"bedrock":       "https://bedrock-mantle.us-east-1.api.aws/v1",
		"alibaba":       "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		"together":      "https://api.together.xyz/v1",
		"fireworks":     "https://api.fireworks.ai/inference/v1",
		"cerebras":      "https://api.cerebras.ai/v1",
		"sambanova":     "https://api.sambanova.ai/v1",
		"nebius":        "https://api.studio.nebius.com/v1",
		"deepinfra":     "https://api.deepinfra.com/v1/openai",
		"huggingface":   "https://router.huggingface.co/v1",
		"github_models": "https://models.github.ai/inference",
		"lmstudio":      "http://localhost:1234/v1",
		"llamacpp":      "http://localhost:8080/v1",
		"vllm":          "http://localhost:8000/v1",
		"localai":       "http://localhost:8080/v1",
		"jan":           "http://localhost:1337/v1",
```

and the same fifteen names to `TestNew_CatalogProvidersBuild`'s string slice:

```go
		"bedrock", "alibaba", "together", "fireworks", "cerebras", "sambanova",
		"nebius", "deepinfra", "huggingface", "github_models",
		"lmstudio", "llamacpp", "vllm", "localai", "jan",
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/llm/... ./internal/coder/... -run 'TestAPIProviders|TestDefaultBaseURL|TestNew_Catalog' -count=1`

Expected: FAIL — `undefined: GroupLocal`, `undefined: GroupHosted`, and `p.Group undefined (type APIProvider has no field or method Group)`.

- [ ] **Step 3: Add the base URLs**

In `internal/llm/provider.go`, inside the `defaultBases` map literal, after the `"moonshot"` line:

```go
		// ── Wave 1 (2026-08) hosted tier ──
		// Bedrock: AWS documents three path shapes; this is the `bedrock-mantle`
		// endpoint AWS explicitly recommends, and the only one that accepts a
		// Bedrock API key as a plain bearer token with no SigV4 signing — which
		// is the whole reason Bedrock is a drop-in here. The region is baked to
		// us-east-1; another region goes in the per-workspace base-URL override.
		"bedrock":       "https://bedrock-mantle.us-east-1.api.aws/v1",
		"alibaba":       "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		"together":      "https://api.together.xyz/v1",
		"fireworks":     "https://api.fireworks.ai/inference/v1",
		"cerebras":      "https://api.cerebras.ai/v1",
		"sambanova":     "https://api.sambanova.ai/v1",
		"nebius":        "https://api.studio.nebius.com/v1",
		"deepinfra":     "https://api.deepinfra.com/v1/openai",
		"huggingface":   "https://router.huggingface.co/v1",
		"github_models": "https://models.github.ai/inference",
		// ── Wave 1 (2026-08) local tier ──
		// Each is the default its own docs publish. llamacpp and localai share
		// 8080: they are alternative servers, not concurrent ones, and a user
		// running both overrides one — the workflow the base-URL prefill makes
		// discoverable.
		"lmstudio": "http://localhost:1234/v1",
		"llamacpp": "http://localhost:8080/v1",
		"vllm":     "http://localhost:8000/v1",
		"localai":  "http://localhost:8080/v1",
		"jan":      "http://localhost:1337/v1",
```

- [ ] **Step 4: Register the factories**

In `internal/llm/openai.go`, extend the `init()` name slice (append after `"moonshot",`):

```go
		"bedrock", "alibaba", "together", "fireworks", "cerebras", "sambanova",
		"nebius", "deepinfra", "huggingface", "github_models",
		"lmstudio", "llamacpp", "vllm", "localai", "jan",
```

- [ ] **Step 5: Add the Group field and the catalog rows**

In `internal/coder/detect.go`, add the group constants above the `APIProvider` struct:

```go
// Catalog groups. GroupLocal is a self-hosted OpenAI-compatible server reached
// over localhost; GroupHosted is everything else. The distinction is load-
// bearing rather than cosmetic: a local server needs no API key, and the SPA
// renders the two tiers as separate sections.
const (
	GroupHosted = "hosted"
	GroupLocal  = "local"
)
```

Add the field to the struct (last, so existing positional literals extend rather than shuffle):

```go
	Custom           bool   // true only for the Custom (generic) entry
	Group            string // GroupHosted | GroupLocal
```

Update the doc comment on `RequiresKey` — it currently reads `// false only for ollama_local`:

```go
	RequiresKey      bool   // false for the local tier (Group == GroupLocal)
```

Append `, "hosted"` to each of the 16 existing rows, except `ollama_local` which takes `, "local"`. Then insert the fifteen new rows immediately **before** the `generic` row:

```go
	// ── Wave 1 (2026-08): hosted tier ──
	{"bedrock", "AWS Bedrock", "openai", "us.anthropic.claude-sonnet-4-6", "https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html", true, false, "hosted"},
	{"alibaba", "Alibaba Cloud (Qwen)", "openai", "qwen-max", "https://www.alibabacloud.com/help/en/model-studio/get-api-key", true, false, "hosted"},
	{"together", "Together AI", "openai", "deepseek-ai/DeepSeek-V3", "https://api.together.xyz/settings/api-keys", true, false, "hosted"},
	{"fireworks", "Fireworks AI", "openai", "accounts/fireworks/models/deepseek-v3", "https://fireworks.ai/account/api-keys", true, false, "hosted"},
	{"cerebras", "Cerebras", "openai", "llama-3.3-70b", "https://cloud.cerebras.ai/platform/apikeys", true, false, "hosted"},
	{"sambanova", "SambaNova", "openai", "Meta-Llama-3.3-70B-Instruct", "https://cloud.sambanova.ai/apis", true, false, "hosted"},
	{"nebius", "Nebius AI Studio", "openai", "deepseek-ai/DeepSeek-V3", "https://studio.nebius.com/settings/api-keys", true, false, "hosted"},
	{"deepinfra", "DeepInfra", "openai", "deepseek-ai/DeepSeek-V3", "https://deepinfra.com/dash/api_keys", true, false, "hosted"},
	{"huggingface", "Hugging Face", "openai", "deepseek-ai/DeepSeek-V3", "https://huggingface.co/settings/tokens", true, false, "hosted"},
	{"github_models", "GitHub Models", "openai", "openai/gpt-4o", "https://github.com/settings/personal-access-tokens", true, false, "hosted"},
	// ── Wave 1 (2026-08): local tier — self-hosted OpenAI-compatible servers.
	// RequiresKey is false: these accept any string as a bearer token, and
	// PlanKeySecret stores a placeholder so llm.New's non-empty check passes.
	{"lmstudio", "LM Studio (Local)", "openai", "qwen2.5-coder-7b-instruct", "https://lmstudio.ai/docs/app/api/endpoints/openai", false, false, "local"},
	{"llamacpp", "llama.cpp (Local)", "openai", "gpt-oss-20b", "https://github.com/ggml-org/llama.cpp/tree/master/tools/server", false, false, "local"},
	{"vllm", "vLLM (Local)", "openai", "Qwen/Qwen2.5-Coder-7B-Instruct", "https://docs.vllm.ai/en/latest/serving/openai_compatible_server.html", false, false, "local"},
	{"localai", "LocalAI (Local)", "openai", "qwen2.5-coder-7b", "https://localai.io/features/openai-functions/", false, false, "local"},
	{"jan", "Jan (Local)", "openai", "qwen2.5-coder-7b", "https://jan.ai/docs/desktop/api-server", false, false, "local"},
```

- [ ] **Step 6: Run the Go tests**

Run: `go test ./internal/llm/... ./internal/coder/... -count=1`

Expected: PASS.

- [ ] **Step 7: Confirm the logo gate is now red**

Run: `go test ./web/... -run TestBrandLogoCoverage -count=1`

Expected: FAIL, listing the fifteen new slugs with "has no brand logo".

- [ ] **Step 8: Add the twelve available marks to the vendoring manifest**

In `scripts/vendor-brand-logos.sh`, append to the `LOBEHUB` list (verified present in `@lobehub/icons-static-svg@1.94.0`):

```
bedrock:bedrock-color
alibaba:alibabacloud-color
together:together-color
fireworks:fireworks-color
cerebras:cerebras-color
sambanova:sambanova-color
nebius:nebius
deepinfra:deepinfra-color
huggingface:huggingface-color
github_models:github
lmstudio:lmstudio
vllm:vllm-color
```

`nebius` has only a monochrome mark (no `-color` variant); that is the published form, and `ProviderLogo` themes it via `currentColor`. `github_models:github` deliberately reuses the GitHub mark — GitHub Models is a GitHub product.

- [ ] **Step 9: Exempt the three brands with no mark anywhere**

In `web/logo_coverage_test.go`, add to `allowNoLogo` with the reason:

```go
		// Wave 1 coder providers. llama.cpp, LocalAI and Jan have no mark in
		// lobehub, worldvectorlogo or simple-icons (verified against
		// @lobehub/icons-static-svg@1.94.0 and the installed simple-icons).
		// They render as coloured initials until a source appears. Drawing an
		// approximation is not the alternative: an invented logo misrepresents
		// someone else's brand, which is worse than a letter.
		"llamacpp": true,
		"localai":  true,
		"jan":      true,
```

- [ ] **Step 10: Install SPA dependencies and vendor the logos**

The vendoring script's simple-icons section resolves out of `web/ui/node_modules`, so dependencies must exist first. This install is needed for the UI tests in Tasks 4–6 regardless.

Run:
```bash
(cd web/ui && npm ci)
./scripts/vendor-brand-logos.sh
```

Expected: the script prints each slug it wrote and exits 0. Twelve new files appear in `web/ui/src/assets/logos/`.

- [ ] **Step 11: Run the full Go suite**

Run: `go test ./... -count=1 -timeout 300s`

Expected: PASS, including `TestBrandLogoCoverage` and `TestBrandLogoAssetsAreWellFormed`.

- [ ] **Step 12: Commit**

```bash
git add internal/llm/provider.go internal/llm/openai.go internal/llm/provider_test.go \
        internal/coder/detect.go internal/coder/apiproviders_test.go \
        scripts/vendor-brand-logos.sh web/logo_coverage_test.go \
        web/ui/src/assets/logos
git commit -m "feat(coder): fifteen drop-in LLM providers across a hosted and local tier

Adds AWS Bedrock, Alibaba Cloud (Qwen), Together, Fireworks, Cerebras,
SambaNova, Nebius, DeepInfra, Hugging Face and GitHub Models, plus the
self-hosted tier: LM Studio, llama.cpp, vLLM, LocalAI and Jan. All speak
the OpenAI chat-completions schema with bearer auth, so openaiProvider
handles them unchanged.

A new Group field classifies the catalog and turns the old
'ollama_local is the only keyless provider' spot check into the real
invariant: keyless iff local. A new test pins default base URLs as
resolved and dialable, so a templated {region} cannot ship."
```

---

### Task 2: Generalize the keyless-provider placeholder secret

**Files:**
- Modify: `internal/coder/keysecret.go:26-40`
- Test: `internal/coder/keysecret_test.go:22-27`

**Interfaces:**
- Consumes: `coder.GroupLocal` (Task 1) — used only in the doc comment.
- Produces: no exported surface change. `PlanKeySecret`'s signature and semantics are unchanged; only the stored placeholder string changes from `"ollama"` to `"no-key"`.

- [ ] **Step 1: Update the test to expect the new placeholder**

In `internal/coder/keysecret_test.go`, replace `TestPlanKeySecret_NoKeyProviderStoresDummy`:

```go
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
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/coder/... -run TestPlanKeySecret -count=1`

Expected: FAIL — `undefined: placeholderLocalKey`.

- [ ] **Step 3: Add the constant and use it**

In `internal/coder/keysecret.go`, add below the imports:

```go
// placeholderLocalKey is stored as the API key for a provider that needs none
// (the local tier). llm.New rejects an empty key, so the field cannot simply be
// blank; the value is never sent as a meaningful credential — a local server
// accepts any bearer string.
const placeholderLocalKey = "no-key"
```

In `PlanKeySecret`, replace the dummy write:

```go
	if !providerRequiresKey(provider) {
		return KeySecretPlan{SecretName: name, WriteValue: placeholderLocalKey, WriteSecret: true}
	}
```

And update the function's doc comment line that reads `// needs no key (ollama_local) gets a dummy "ollama" value so llm.New's`:

```go
// needs no key (the local tier) gets placeholderLocalKey so llm.New's
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/coder/... -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/keysecret.go internal/coder/keysecret_test.go
git commit -m "refactor(coder): name the keyless-provider placeholder secret

PlanKeySecret stored the literal string \"ollama\" for any provider that
needs no API key. With five more local servers in the tier that reads as
a bug rather than a placeholder; it is now placeholderLocalKey = \"no-key\"
and the test covers the whole tier instead of one provider."
```

---

### Task 3: Expose Label and Group on the coder catalog DTO

The SPA's provider `<select>` renders `entry.name` because the catalog DTO carries no label. The human label already exists on `coder.APIProvider.Label` but is only sent on a different DTO (`api_providers`), which `CoderSection` does not receive.

**Files:**
- Modify: `web/api_settings.go:68-96` (`apiCoderCatalogEntry`, `coderCatalogSlice`)
- Modify: `web/ui/src/lib/settings.ts:48-58` (`CoderCatalogEntry`)
- Test: `web/api_settings_catalog_test.go` (create)

**Interfaces:**
- Consumes: `coder.APIProvider.Label`, `coder.APIProvider.Group` (Task 1).
- Produces: `CoderCatalogEntry` gains `label: string` and `group: string` (JSON keys `label`, `group`). Tasks 4 and 5 both read these.

- [ ] **Step 1: Write the failing test**

Create `web/api_settings_catalog_test.go`:

```go
package web

import (
	"testing"

	"github.com/ilijad1/rookery/internal/coder"
)

// The SPA's provider picker renders entry.label; without it the dropdown shows
// raw registry slugs like "ollama_local" and "github_models". Group drives the
// two-section rendering. Both come from the same catalog the Go side already
// has, so a missing field is a wiring bug, not a data gap.
func TestCoderCatalogSliceCarriesLabelAndGroup(t *testing.T) {
	s := &Server{}
	out := s.coderCatalogSlice(nil)

	want := make(map[string]coder.APIProvider, len(coder.APIProviders()))
	for _, p := range coder.APIProviders() {
		want[p.Name] = p
	}
	if len(out) != len(want) {
		t.Fatalf("catalog has %d entries, provider list has %d", len(out), len(want))
	}
	for _, e := range out {
		p, ok := want[e.Name]
		if !ok {
			t.Errorf("catalog entry %q is not a known provider", e.Name)
			continue
		}
		if e.Label != p.Label {
			t.Errorf("entry %q label = %q, want %q", e.Name, e.Label, p.Label)
		}
		if e.Group != p.Group {
			t.Errorf("entry %q group = %q, want %q", e.Name, e.Group, p.Group)
		}
		if e.Label == "" || e.Group == "" {
			t.Errorf("entry %q has an empty label or group", e.Name)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./web/... -run TestCoderCatalogSliceCarriesLabelAndGroup -count=1`

Expected: FAIL — `e.Label undefined` / `e.Group undefined`.

- [ ] **Step 3: Add the fields**

In `web/api_settings.go`, extend the struct:

```go
type apiCoderCatalogEntry struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Base        string `json:"base"`
	Model       string `json:"model"`
	Docs        string `json:"docs"`
	RequiresKey bool   `json:"requiresKey"`
	Custom      bool   `json:"custom"`
	HasKey      bool   `json:"hasKey"`
	Group       string `json:"group"`
}
```

and populate them in `coderCatalogSlice`:

```go
		out = append(out, apiCoderCatalogEntry{
			Name: p.Name, Label: p.Label, Base: llm.DefaultBaseURL(p.Name),
			Model: p.ModelPlaceholder, Docs: p.DocsURL,
			RequiresKey: p.RequiresKey, Custom: p.Custom,
			HasKey: have[coder.CoderKeySecretName(p.Name)],
			Group:  p.Group,
		})
```

- [ ] **Step 4: Mirror the type in TypeScript**

In `web/ui/src/lib/settings.ts`, extend `CoderCatalogEntry`:

```ts
export type CoderCatalogEntry = {
  name: string;
  label: string;
  base: string;
  model: string;
  docs: string;
  requiresKey: boolean;
  custom: boolean;
  hasKey: boolean;
  group: string; // "hosted" | "local" — mirrors coder.GroupHosted/GroupLocal
};
```

- [ ] **Step 5: Run the Go test and the type check**

Run: `go test ./web/... -run TestCoderCatalogSlice -count=1 && (cd web/ui && npx tsc -b)`

Expected: the Go test PASSes. `tsc` FAILS in `CoderSection.test.tsx` and `ProviderCards.test.tsx`, whose `CATALOG` fixtures now lack `label`/`group` — fixed in the next step.

- [ ] **Step 6: Update the test fixtures**

In `web/ui/src/pages/settings/CoderSection.test.tsx`, replace the `CATALOG` constant:

```tsx
const CATALOG: CoderCatalogEntry[] = [
  { name: "openrouter", label: "OpenRouter", base: "https://openrouter.ai/api/v1", model: "glm-5.2", docs: "https://openrouter.ai/keys", requiresKey: true, custom: false, hasKey: true, group: "hosted" },
  { name: "zai", label: "Z.AI (GLM)", base: "https://api.z.ai/v1", model: "glm-4.7", docs: "https://z.ai", requiresKey: true, custom: false, hasKey: false, group: "hosted" },
  { name: "ollama_local", label: "Ollama (Local)", base: "http://localhost:11434/v1", model: "qwen2.5-coder", docs: "https://docs.ollama.com", requiresKey: false, custom: false, hasKey: false, group: "local" },
  { name: "generic", label: "Custom (OpenAI-compatible)", base: "", model: "", docs: "", requiresKey: true, custom: true, hasKey: false, group: "hosted" },
];
```

In `web/ui/src/pages/settings/ProviderCards.test.tsx`, add `label` and `group` to each entry in its catalog fixture (line ~77), matching the shape above.

- [ ] **Step 7: Verify types and the existing suite still pass**

Run: `(cd web/ui && npx tsc -b && npx vitest run)`

Expected: PASS. (`CoderSection.test.tsx` gained a fourth catalog entry; if an existing assertion counts options, update it to expect four.)

- [ ] **Step 8: Commit**

```bash
git add web/api_settings.go web/api_settings_catalog_test.go web/ui/src/lib/settings.ts \
        web/ui/src/pages/settings/CoderSection.test.tsx web/ui/src/pages/settings/ProviderCards.test.tsx
git commit -m "feat(web/settings): carry provider label and group on the coder catalog

The provider picker rendered the raw registry slug because the catalog DTO
had no label — readable at sixteen providers, not at thirty-one with
github_models and llamacpp in the list. Label and Group both already exist
on coder.APIProvider; this only wires them through."
```

---

### Task 4: Discoverable base URL and labelled provider picker

The base-URL override already exists and already persists. What is missing is any way to discover it: the field sits behind "Advanced", starts empty, and shows a generic `https://api.example.com/v1` placeholder.

One deviation from the spec, recorded here: rather than hardcoding `bedrock` as a second auto-expand trigger (a magic string in the frontend for a Go-side concern), the collapsed "Advanced" toggle now shows the effective base URL for every API provider. That makes the URL discoverable for Bedrock's region without a per-provider special case, and auto-expansion stays a clean `group === "local"` rule.

**Files:**
- Modify: `web/ui/src/pages/settings/CoderSection.tsx` (lines ~75-128 for state/save, ~206-235 for the picker, ~282-313 for Advanced)
- Test: `web/ui/src/pages/settings/CoderSection.test.tsx`

**Interfaces:**
- Consumes: `CoderCatalogEntry.label`, `CoderCatalogEntry.base`, `CoderCatalogEntry.group` (Task 3).
- Produces: no exported surface change. `SaveCoderInput.base_url` semantics are preserved — an unmodified prefill posts `""`, meaning "follow the registry default".

- [ ] **Step 1: Write the failing tests**

Append to `web/ui/src/pages/settings/CoderSection.test.tsx`:

```tsx
test("API engine: the provider dropdown shows human labels, not registry slugs", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  expect(
    screen.getByRole("option", { name: /ollama \(local\)/i }),
  ).toBeInTheDocument();
});

test("API engine: selecting a provider prefills its default base URL", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  await user.selectOptions(
    screen.getByLabelText(/provider/i),
    "ollama_local",
  );
  // Local providers auto-expand Advanced, so the field is visible without a click.
  expect(screen.getByLabelText(/base url/i)).toHaveValue(
    "http://localhost:11434/v1",
  );
});

test("API engine: an unmodified prefill posts an empty base_url", async () => {
  const calls = mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  await user.selectOptions(screen.getByLabelText(/provider/i), "ollama_local");
  await user.type(screen.getByLabelText(/^model$/i), "qwen2.5-coder");
  await user.click(screen.getByRole("button", { name: /save coder/i }));
  await waitFor(() => {
    const put = calls.find((c) => c.method === "PUT");
    expect(put).toBeTruthy();
    // Storing the default explicitly would freeze this workspace on today's
    // URL if the registry default ever changed. Empty means "follow the default".
    expect((put!.body as { base_url: string }).base_url).toBe("");
  });
});

test("API engine: an edited base URL is posted verbatim", async () => {
  const calls = mockFetch();
  const user = userEvent.setup();
  wrap(undefined);
  await user.click(screen.getByRole("radio", { name: /api/i }));
  await user.selectOptions(screen.getByLabelText(/provider/i), "ollama_local");
  const field = screen.getByLabelText(/base url/i);
  await user.clear(field);
  await user.type(field, "http://192.168.1.50:12345/v1");
  await user.type(screen.getByLabelText(/^model$/i), "qwen2.5-coder");
  await user.click(screen.getByRole("button", { name: /save coder/i }));
  await waitFor(() => {
    const put = calls.find((c) => c.method === "PUT");
    expect((put!.body as { base_url: string }).base_url).toBe(
      "http://192.168.1.50:12345/v1",
    );
  });
});

test("API engine: a saved override survives a provider switch back", async () => {
  mockFetch();
  const user = userEvent.setup();
  wrap({
    kind: "api",
    bin: "",
    timeout_s: 90,
    provider: "ollama_local",
    model: "qwen2.5-coder",
    base_url: "http://nas.lan:11434/v1",
    api_key_secret: "CODER_KEY_OLLAMA_LOCAL",
  });
  // A stored override must not be clobbered by the prefill on mount.
  expect(screen.getByLabelText(/base url/i)).toHaveValue(
    "http://nas.lan:11434/v1",
  );
  await user.selectOptions(screen.getByLabelText(/provider/i), "zai");
  expect(screen.getByLabelText(/base url/i)).toHaveValue(
    "http://nas.lan:11434/v1",
  );
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `(cd web/ui && npx vitest run CoderSection)`

Expected: FAIL — the label test cannot find an option named "Ollama (Local)"; the prefill tests find an empty Base URL field (and cannot find it at all until Advanced is expanded).

- [ ] **Step 3: Track whether the base URL was deliberately set**

In `CoderSection.tsx`, add a ref beside the existing state (after the `baseURL` state, ~line 75):

```tsx
  // True once the base URL holds a value the user chose — either typed here or
  // loaded from a saved config. Prefill must never overwrite one of those; a
  // plain equality check against the default cannot tell them apart, because a
  // user may legitimately type the default back in.
  const baseURLTouched = useRef(false);
```

and add `useRef` to the React import on line 1.

Update the load effect (~line 85) so a saved override marks the field as touched, and an empty one is seeded from the catalog:

```tsx
  useEffect(() => {
    if (!coder) return;
    setEngine(coder.kind === "api" || coderMode === "slim" ? "api" : "local");
    setBin(coder.bin);
    setTimeoutS(coder.timeout_s || 120);
    setProvider(coder.provider);
    setModel(coder.model);
    if (coder.base_url) {
      setBaseURL(coder.base_url);
      baseURLTouched.current = true;
    } else {
      // Empty means "follow the registry default" — show that default rather
      // than a blank box, so the value is editable instead of merely absent.
      setBaseURL(catalog.find((c) => c.name === coder.provider)?.base ?? "");
    }
    if (catalog.find((c) => c.name === coder.provider)?.group === "local") {
      setAdvancedOpen(true);
    }
  }, [coder, coderMode, catalog]);
```

- [ ] **Step 4: Prefill on provider change and auto-expand for the local tier**

Add above `handleSave` (~line 103):

```tsx
  function handleProviderChange(name: string) {
    setProvider(name);
    const entry = catalog.find((c) => c.name === name);
    if (!baseURLTouched.current) setBaseURL(entry?.base ?? "");
    // A self-hosted server is the case where the URL routinely needs editing —
    // a different port, a different host. Open the section rather than making
    // the user discover it.
    if (entry?.group === "local") setAdvancedOpen(true);
  }
```

Wire the `<select>` (~line 213) to it and render the label (~line 225):

```tsx
                onChange={(e) => handleProviderChange(e.target.value)}
```

```tsx
                    <option key={c.name} value={c.name} disabled={!usable}>
                      {c.label || c.name}
                      {!usable
                        ? showApiKeyInput
                          ? " (enter your API key below)"
                          : " (add key above)"
                        : ""}
                    </option>
```

- [ ] **Step 5: Mark the field touched on edit, and normalize on save**

Replace the Base URL `<Input>`'s handler (~line 305):

```tsx
                    onChange={(e) => {
                      baseURLTouched.current = true;
                      setBaseURL(e.target.value);
                    }}
                    placeholder={selectedEntry?.base || "https://api.example.com/v1"}
```

In `handleSave`, send `""` when the value is still the provider's default:

```tsx
      // An unmodified prefill is not an override. Posting it verbatim would
      // pin this workspace to today's URL forever; empty keeps it following
      // the registry default, exactly as before this field was prefilled.
      const trimmedBase = baseURL.trim();
      const effectiveBase =
        trimmedBase === (selectedEntry?.base ?? "") ? "" : trimmedBase;
```

and use it in the mutation payload, replacing the existing `base_url` line:

```tsx
        base_url: engine === "api" ? effectiveBase : "",
```

- [ ] **Step 6: Add the helper text and the collapsed URL summary**

Replace the Advanced toggle's label text (~line 294) so the effective URL is visible while collapsed:

```tsx
                Advanced
                {engine === "api" && !advancedOpen && baseURL && (
                  <span className="truncate font-normal text-muted-2">
                    · {baseURL}
                  </span>
                )}
```

and add the helper line under the Base URL input, after the `baseURLError` block:

```tsx
                  <p className="text-xs text-muted-2">
                    Change the port here if your server listens somewhere else.
                  </p>
```

- [ ] **Step 7: Run the tests**

Run: `(cd web/ui && npx tsc -b && npx vitest run CoderSection)`

Expected: PASS, all five new tests plus the existing suite.

- [ ] **Step 8: Commit**

```bash
git add web/ui/src/pages/settings/CoderSection.tsx web/ui/src/pages/settings/CoderSection.test.tsx
git commit -m "fix(web/settings): make a provider's base URL discoverable and editable

Overriding Ollama's port was already possible and already persisted, but
undiscoverable: the field sat behind Advanced, started empty, and showed a
generic example placeholder. It now prefills with the selected provider's
real default, auto-expands for the self-hosted tier, shows the effective
URL on the collapsed toggle, and says what it is for.

An unmodified prefill still posts an empty base_url, so storage semantics
are unchanged — a workspace keeps following the registry default rather
than freezing on today's URL. The picker also shows human labels now
instead of raw registry slugs."
```

---

### Task 5: Group the provider-key gallery into hosted and local sections

Thirty-one flat cards in a two-column grid is a wall. The local tier also shows "No key needed", so mixing the two tiers makes that read as an inconsistency rather than a property of the group.

**Files:**
- Modify: `web/ui/src/pages/settings/ProviderCards.tsx:146-171`
- Test: `web/ui/src/pages/settings/ProviderCards.test.tsx`

**Interfaces:**
- Consumes: `CoderCatalogEntry.group`, `CoderCatalogEntry.label` (Task 3).
- Produces: no interface change. `ProviderCards`' props are unchanged.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/settings/ProviderCards.test.tsx`:

```tsx
test("cards are split into hosted and local sections", () => {
  renderCards();
  expect(screen.getByRole("heading", { name: /hosted/i })).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: /local & self-hosted/i }),
  ).toBeInTheDocument();
});
```

Use whatever the file's existing render helper is named; if the tests render `<ProviderCards …/>` inline, mirror that call and pass a catalog containing at least one `group: "hosted"` and one `group: "local"` entry.

- [ ] **Step 2: Run it to verify it fails**

Run: `(cd web/ui && npx vitest run ProviderCards)`

Expected: FAIL — no such headings in the document.

- [ ] **Step 3: Render the two sections**

Replace the body of `ProviderCards` in `ProviderCards.tsx`:

```tsx
export function ProviderCards({
  catalog,
  providers,
}: {
  catalog: CoderCatalogEntry[];
  providers: APIProvider[];
}) {
  if (catalog.length === 0) {
    return <p className="text-sm text-muted-2">No providers available.</p>;
  }

  const labelFor = (entry: CoderCatalogEntry) =>
    entry.label || providers.find((p) => p.name === entry.name)?.label || entry.name;

  // Two tiers rather than one flat wall: at thirty-one providers the list is
  // long, and "No key needed" reads as a property of the local group rather
  // than an inconsistency once the groups are visible.
  const sections = [
    { key: "hosted", title: "Hosted", entries: catalog.filter((c) => c.group !== "local") },
    { key: "local", title: "Local & self-hosted", entries: catalog.filter((c) => c.group === "local") },
  ].filter((s) => s.entries.length > 0);

  return (
    <div className="space-y-6">
      {sections.map((section) => (
        <div key={section.key}>
          <h3 className="mb-2 text-sm font-semibold text-muted-2">
            {section.title}
          </h3>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {section.entries.map((entry) => (
              <ProviderCard
                key={entry.name}
                entry={entry}
                label={labelFor(entry)}
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
```

An entry with an unrecognized group falls into "Hosted" rather than vanishing — a card that disappears is a worse failure than one filed under the wrong heading.

- [ ] **Step 4: Run the tests**

Run: `(cd web/ui && npx tsc -b && npx vitest run ProviderCards)`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/pages/settings/ProviderCards.tsx web/ui/src/pages/settings/ProviderCards.test.tsx
git commit -m "feat(web/settings): group the provider gallery by hosted and local tier"
```

---

### Task 6: Documentation and the full gate

**Files:**
- Modify: `CLAUDE.md` (the "Per-workspace coder" section and the `internal/llm` row of the package table)

**Interfaces:**
- Consumes: everything from Tasks 1–5.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Update the package table row**

In `CLAUDE.md`, the `internal/llm` row lists the provider registry. Replace the parenthesized list with:

```
`Provider` interface + registry (`openai`, `openrouter`, `anthropic`, `generic` OpenAI-compatible, plus ~27 further providers registered against the OpenAI schema — see `coder.APIProviders()`)
```

- [ ] **Step 2: Update the per-workspace coder section**

In the "Per-workspace coder" section, replace the sentence beginning `coder.APIProviders() returns a curated catalog of ~16 named providers`:

```
`coder.APIProviders()` returns a curated catalog of ~31 named providers in two
tiers. **Hosted** covers the frontier labs (OpenAI, Anthropic, Gemini, xAI,
Mistral, DeepSeek, Moonshot, Z.AI), the routers (OpenRouter, OpenCode Zen/Go,
Perplexity), the enterprise clouds (**AWS Bedrock**, **Alibaba Cloud/Qwen**) and
the open-weight inference clouds (Groq, Together, Fireworks, Cerebras,
SambaNova, Nebius, DeepInfra, plus the Hugging Face and GitHub Models
aggregators). **Local** covers self-hosted OpenAI-compatible servers — Ollama,
LM Studio, llama.cpp, vLLM, LocalAI and Jan — which need no API key
(`RequiresKey: false`, enforced as an iff against `Group == GroupLocal` by
`TestAPIProviders_KeylessIsLocalTier`) and whose base URL is prefilled and
auto-expanded in the settings form so a non-default port is editable in place.
A "Custom (OpenAI-compatible)" escape hatch remains last in the list.

Base URLs are single-sourced in `internal/llm.DefaultBaseURL(name)` and are
**always resolved, never templated**: `llm.New` assigns the value straight into
the HTTP client with no validation, so a `{region}` placeholder would pass every
test and fail at request time with an opaque DNS error. Bedrock therefore ships
`us-east-1` and region variation goes through the per-workspace base-URL
override (`TestAPIProviders_BaseURLsAreDialable` pins this).

**Azure OpenAI and Google Vertex AI are deliberately absent** — see
`docs/superpowers/specs/2026-08-04-llm-provider-expansion-design.md`. Azure uses
an `api-key` header, a deployment name in the path and a mandatory
`api-version` query parameter; Vertex mints short-lived OAuth tokens from a
service account, which `llm.Config.APIKey` (a plain string `llm.New` rejects
when empty) cannot express. Each needs its own provider implementation rather
than a catalog row.
```

- [ ] **Step 3: Run the full local gate**

Run: `make ci`

Expected: PASS on all of gofmt, `go vet`, `go test -race`, the six-target cross-compile, and the UI job (`tsc -b`, `oxlint`, `vitest`, `vite build`).

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: record the two-tier provider catalog and the resolved-base-URL rule"
```

- [ ] **Step 5: Push and open a pull request**

```bash
git push -u origin HEAD
gh pr create --title "feat(coder): fifteen drop-in LLM providers and a discoverable base URL" --body "..."
```

The PR title must itself be a valid Conventional Commit — merges are squashes, and release-please reads the title to compute the next version.

---

## Self-Review

**Spec coverage.** §1 fifteen providers → Task 1. §2 base-URL discoverability → Task 4. §3 dropdown labels and grouping → Tasks 3 (data), 4 (dropdown), 5 (gallery). §4 test generalization → Task 1 Step 1, plus the new dialable guard. §5 `PlanKeySecret` cleanup → Task 2. §6 logos → Task 1 Steps 8–10. Deferred providers and the risk note → Task 6 Step 2, written into `CLAUDE.md` so the reason survives outside the spec file. No gaps.

**Type consistency.** `Group` is the Go field, `group` the JSON key and the TS property, valued by the `coder.GroupHosted` / `coder.GroupLocal` constants and their literal `"hosted"` / `"local"` equivalents in TSX. `Label`/`label` likewise. `placeholderLocalKey` is used in both `keysecret.go` and its test. `baseURLTouched` is a `useRef` in every reference.

**One deviation from the spec, deliberate and recorded in Task 4:** the spec said auto-expand Advanced for the local tier "plus Bedrock". Hardcoding `bedrock` in the frontend is a magic string encoding a Go-side fact. Showing the effective base URL on the collapsed toggle serves the same goal — discoverability of the region — for every provider, with no special case.
