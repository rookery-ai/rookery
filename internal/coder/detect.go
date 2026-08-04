package coder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Installed describes a coder CLI binary detected on the host. The workspace
// coder-settings UI lists these so the operator can pick one per workspace.
type Installed struct {
	Name        string // human label, e.g. "Claude Code"
	Bin         string // resolved path (or bare name if only found on PATH)
	BackendType string // "claude" or "generic" (drives output parsing)
}

// Catalog groups. GroupLocal is a self-hosted OpenAI-compatible server reached
// over localhost; GroupHosted is everything else. The distinction is load-
// bearing rather than cosmetic: a local server needs no API key, and the SPA
// renders the two tiers as separate sections.
const (
	GroupHosted = "hosted"
	GroupLocal  = "local"
)

// APIProvider describes a direct LLM API provider usable as a coder (the "api"
// kind). These are not probed — they're always available (no host binary
// needed). Base URLs are NOT stored here; they live in internal/llm.defaultBases
// (fetch via llm.DefaultBaseURL) so there is one source of truth.
type APIProvider struct {
	Name             string // registry name, e.g. "zai" — must be registered in internal/llm
	Label            string // human label, e.g. "Z.AI (GLM)"
	Schema           string // "openai" | "anthropic" — the WIRE schema, not the model vendor
	ModelPlaceholder string // example model for the free-text hint, e.g. "glm-4.7"
	DocsURL          string // provider API-key/docs page
	RequiresKey      bool   // false for the local tier (Group == GroupLocal)
	Custom           bool   // true only for the Custom (generic) entry
	Group            string // GroupHosted | GroupLocal
}

// apiProviders is the catalog of direct-LLM-API coder providers (registry
// names match internal/llm). "generic" is any OpenAI-compatible endpoint; the
// base URL field is always shown as an optional override for every provider
// (e.g. Azure OpenAI, a private gateway) and is required only for "generic"
// (enforced by llm.New at call time, not by the form).
var apiProviders = []APIProvider{
	{"openai", "OpenAI", "openai", "gpt-4o", "https://platform.openai.com/api-keys", true, false, "hosted"},
	{"anthropic", "Anthropic", "anthropic", "claude-sonnet-5", "https://console.anthropic.com/settings/keys", true, false, "hosted"},
	{"openrouter", "OpenRouter", "openai", "anthropic/claude-3.5-sonnet", "https://openrouter.ai/keys", true, false, "hosted"},
	{"zai", "Z.AI (GLM)", "openai", "glm-4.7", "https://z.ai/model-api", true, false, "hosted"},
	{"ollama", "Ollama Cloud", "openai", "qwen3-coder:480b", "https://ollama.com/settings/keys", true, false, "hosted"},
	{"ollama_local", "Ollama (Local)", "openai", "qwen2.5-coder", "https://docs.ollama.com/api/openai-compatibility", false, false, "local"},
	{"deepseek", "DeepSeek", "openai", "deepseek-chat", "https://platform.deepseek.com/api_keys", true, false, "hosted"},
	{"groq", "Groq", "openai", "llama-3.3-70b-versatile", "https://console.groq.com/keys", true, false, "hosted"},
	{"xai", "xAI (Grok)", "openai", "grok-4", "https://console.x.ai", true, false, "hosted"},
	{"mistral", "Mistral", "openai", "mistral-large-latest", "https://console.mistral.ai/api-keys", true, false, "hosted"},
	{"gemini", "Google Gemini", "openai", "gemini-2.5-pro", "https://aistudio.google.com/apikey", true, false, "hosted"},
	{"opencode_zen", "OpenCode Zen", "openai", "opencode/gpt-5.5", "https://opencode.ai/docs/zen/", true, false, "hosted"},
	{"opencode_go", "OpenCode Go", "openai", "opencode/grok-code", "https://opencode.ai/docs/go/", true, false, "hosted"},
	{"perplexity", "Perplexity", "openai", "sonar-pro", "https://www.perplexity.ai/settings/api", true, false, "hosted"},
	{"moonshot", "Moonshot (Kimi)", "openai", "kimi-k2", "https://platform.moonshot.ai/console/api-keys", true, false, "hosted"},

	// ── Wave 1 (2026-08): hosted tier ──
	// Bedrock's Schema is "openai", not "anthropic": that field is the WIRE
	// schema, and Bedrock serves Anthropic models over an OpenAI-shaped API.
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

	{"generic", "Custom (OpenAI-compatible)", "openai", "", "", true, true, "hosted"},
}

// APIProviders returns the available direct-LLM-API coder providers.
func APIProviders() []APIProvider { return apiProviders }

// knownCoders is the catalog of supported local coder CLIs, probed in order.
var knownCoders = []struct {
	Name    string
	Bins    []string // candidate binary names (first match wins)
	Backend string
}{
	{"Claude Code", []string{"claude", "claude-code"}, "claude"},
	{"OpenCode", []string{"opencode"}, "opencode"},
	{"Codex", []string{"codex"}, "codex"},
	{"Gemini CLI", []string{"gemini"}, "gemini"},
	{"Cursor", []string{"cursor-agent", "cursor"}, "cursor"},
}

// DetectInstalled probes PATH and ~/.local/bin for supported coder binaries and
// returns the ones found, de-duplicated by resolved path.
func DetectInstalled() []Installed {
	home, _ := os.UserHomeDir()
	extraDirs := []string{}
	if home != "" {
		extraDirs = append(extraDirs, filepath.Join(home, ".local", "bin"))
	}

	var out []Installed
	seen := map[string]bool{}
	for _, kc := range knownCoders {
		for _, bin := range kc.Bins {
			path := resolveCoderBin(bin, extraDirs)
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, Installed{Name: kc.Name, Bin: path, BackendType: kc.Backend})
			break // first matching candidate for this coder
		}
	}
	return out
}

// resolveCoderBin returns the absolute path to bin, checking PATH first and then
// the given extra directories. Returns "" if not found or not executable.
func resolveCoderBin(bin string, extraDirs []string) string {
	if p, err := exec.LookPath(bin); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	for _, dir := range extraDirs {
		cand := filepath.Join(dir, bin)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return cand
		}
	}
	return ""
}

// BackendForBin returns the coder backend type for a binary name or path by
// matching its base name against the known-coders catalog. Returns "" if the
// binary is not a recognized coder (caller falls back to name auto-detection).
func BackendForBin(bin string) string {
	if bin == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(bin))
	for _, kc := range knownCoders {
		for _, cand := range kc.Bins {
			if base == cand || base == cand+".exe" {
				return kc.Backend
			}
		}
	}
	return ""
}
