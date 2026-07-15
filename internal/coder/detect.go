package coder

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Installed describes a coder CLI binary detected on the host. The workspace
// coder-settings UI lists these so the operator can pick one per workspace.
type Installed struct {
	Name        string // human label, e.g. "Claude Code"
	Bin         string // resolved path (or bare name if only found on PATH)
	BackendType string // "claude" or "generic" (drives output parsing)
}

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

// apiProviders is the catalog of direct-LLM-API coder providers (registry
// names match internal/llm). "generic" is any OpenAI-compatible endpoint; the
// base URL field is always shown as an optional override for every provider
// (e.g. Azure OpenAI, a private gateway) and is required only for "generic"
// (enforced by llm.New at call time, not by the form).
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

// APIProviders returns the available direct-LLM-API coder providers.
func APIProviders() []APIProvider { return apiProviders }

// knownCoders is the catalog of supported local coder CLIs, probed in order.
var knownCoders = []struct {
	Name    string
	Bins    []string // candidate binary names (first match wins)
	Backend string
}{
	{"Claude Code", []string{"claude", "claude-code"}, "claude"},
	{"OpenCode", []string{"opencode"}, "generic"},
	{"Codex", []string{"codex"}, "generic"},
	{"Cursor", []string{"cursor-agent", "cursor"}, "generic"},
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
