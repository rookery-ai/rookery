package coder

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	// ── Wave 2 (2026-08): hosted tier ──
	// Base URLs live in llm.DefaultBaseURL and were each confirmed against the
	// provider's own current documentation. AI21 was scoped and DROPPED: its
	// /studio/v1/chat/completions endpoint exists, but nothing in AI21's own
	// docs states OpenAI schema compatibility, and an unverified provider that
	// authenticates and then fails on tool-calling is worse than its absence.
	{"cohere", "Cohere", "openai", "command-a-03-2025", "https://dashboard.cohere.com/api-keys", true, false, "hosted"},
	{"nvidia", "NVIDIA NIM", "openai", "deepseek-ai/deepseek-v3.1", "https://build.nvidia.com", true, false, "hosted"},
	{"vercel_ai", "Vercel AI Gateway", "openai", "anthropic/claude-opus-5", "https://vercel.com/docs/ai-gateway", true, false, "hosted"},
	{"minimax", "MiniMax", "openai", "MiniMax-M2", "https://platform.minimax.io", true, false, "hosted"},
	{"baseten", "Baseten", "openai", "deepseek-ai/DeepSeek-V3.1", "https://app.baseten.co/settings/api_keys", true, false, "hosted"},
	{"novita", "Novita AI", "openai", "deepseek/deepseek-v3", "https://novita.ai/settings/key-management", true, false, "hosted"},
	{"hyperbolic", "Hyperbolic", "openai", "deepseek-ai/DeepSeek-V3", "https://app.hyperbolic.xyz/settings", true, false, "hosted"},
	{"venice", "Venice AI", "openai", "qwen3-235b", "https://venice.ai/settings/api", true, false, "hosted"},

	// Chutes and a LiteLLM-proxy local entry were scoped for this wave and
	// DROPPED at the last step: neither has a mark in lobehub or simple-icons,
	// and the project's rule is to vendor the real published logo or show a
	// letter — never approximate someone else's brand. Adding them would have
	// meant growing allowNoLogo purely to make a count. Both are worth adding
	// once an UPSTREAM_SVG entry points at each publisher's own asset.

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

// detectHost is everything DetectInstalled needs to know about the machine it
// is probing. It is a parameter rather than a set of direct calls so that a host
// we are not running on can be described in a test — there is no macOS or
// Windows runner here, and the bugs this replaced were all platform-specific.
type detectHost struct {
	GOOS string
	Home string
	// LookPath resolves a name against PATH. On Windows the real
	// exec.LookPath consults PATHEXT, so `claude` finds `claude.cmd`.
	LookPath func(string) (string, error)
	// Stat reports a candidate file in one of the fallback directories.
	Stat func(string) (os.FileInfo, error)
	// Getenv reads an environment variable (APPDATA and LOCALAPPDATA on Windows).
	Getenv func(string) string
}

func currentHost() detectHost {
	home, _ := os.UserHomeDir()
	return detectHost{
		GOOS:     runtime.GOOS,
		Home:     home,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
		Getenv:   os.Getenv,
	}
}

// coderSearchDirs returns the directories probed after PATH, in order.
//
// PATH alone is not enough on any of the three platforms, for a different reason
// on each:
//
//   - Linux: a pip/npm --user install lands in ~/.local/bin, which plenty of
//     shells do not add to PATH.
//   - macOS: Homebrew installs to /opt/homebrew/bin on Apple silicon and
//     /usr/local/bin on Intel, and a process started by launchd inherits a
//     minimal PATH containing neither. Detection could therefore fail for
//     someone whose terminal finds the binary without any trouble — the report
//     would be "Rookery cannot see my coder" with a working `which` right there.
//   - Windows: npm's global shims live in %APPDATA%\npm, and installers
//     commonly drop binaries under %LOCALAPPDATA%\Programs.
func coderSearchDirs(h detectHost) []string {
	getenv := h.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	var dirs []string
	add := func(parts ...string) {
		for _, p := range parts {
			if p != "" {
				dirs = append(dirs, p)
			}
		}
	}

	switch h.GOOS {
	case "windows":
		if v := getenv("APPDATA"); v != "" {
			add(filepath.Join(v, "npm"))
		}
		if v := getenv("LOCALAPPDATA"); v != "" {
			add(filepath.Join(v, "Programs"))
		}
		if h.Home != "" {
			add(filepath.Join(h.Home, ".local", "bin"))
		}
	case "darwin":
		if h.Home != "" {
			add(
				filepath.Join(h.Home, ".local", "bin"),
				filepath.Join(h.Home, ".npm-global", "bin"),
				filepath.Join(h.Home, "bin"),
			)
		}
		add("/opt/homebrew/bin", "/usr/local/bin")
	default:
		if h.Home != "" {
			add(
				filepath.Join(h.Home, ".local", "bin"),
				filepath.Join(h.Home, ".npm-global", "bin"),
				filepath.Join(h.Home, "bin"),
			)
		}
		add("/usr/local/bin")
	}
	return dirs
}

// binCandidates expands a bare binary name into the file names to look for in a
// fallback directory.
//
// exec.LookPath already applies PATHEXT when searching PATH, but a direct Stat
// does not — and a coder installed by npm on Windows is a `claude.cmd` shim, not
// a `claude`. Statting the bare name would find nothing and report the coder as
// absent.
func binCandidates(goos, bin string) []string {
	if goos != "windows" {
		return []string{bin}
	}
	return []string{bin + ".exe", bin + ".cmd", bin + ".bat", bin + ".ps1", bin}
}

// DetectInstalled probes PATH and the platform's usual install directories for
// supported coder binaries, returning the ones found, de-duplicated by resolved
// path.
func DetectInstalled() []Installed { return detectInstalled(currentHost()) }

func detectInstalled(h detectHost) []Installed {
	dirs := coderSearchDirs(h)

	var out []Installed
	seen := map[string]bool{}
	for _, kc := range knownCoders {
		for _, bin := range kc.Bins {
			path := resolveCoderBin(h, bin, dirs)
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

// resolveCoderBin returns the path to bin, checking PATH first and then the
// given directories. Returns "" if not found.
func resolveCoderBin(h detectHost, bin string, dirs []string) string {
	if h.LookPath != nil {
		if p, err := h.LookPath(bin); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
	}
	if h.Stat == nil {
		return ""
	}
	for _, dir := range dirs {
		for _, name := range binCandidates(h.GOOS, bin) {
			cand := filepath.Join(dir, name)
			fi, err := h.Stat(cand)
			if err != nil || fi.IsDir() {
				continue
			}
			// The executable bit is a POSIX concept. Go synthesizes mode bits on
			// Windows from file attributes and never sets 0111, so requiring it
			// there rejects every candidate — which is why the fallback path
			// could not find anything on Windows at all. Existence plus a
			// PATHEXT-shaped name is the right test on that platform.
			if h.GOOS != "windows" && fi.Mode()&0o111 == 0 {
				continue
			}
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
