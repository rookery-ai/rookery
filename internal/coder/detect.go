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
// needed), so DetectInstalled surfaces them for the settings picker.
type APIProvider struct {
	Name  string // registry name, e.g. "openai"
	Label string // human label, e.g. "OpenAI"
}

// apiProviders is the catalog of direct-LLM-API coder providers (registry
// names match internal/llm). "generic" is any OpenAI-compatible endpoint; the
// base URL field is always shown as an optional override for every provider
// (e.g. Azure OpenAI, a private gateway) and is required only for "generic"
// (enforced by llm.New at call time, not by the form).
var apiProviders = []APIProvider{
	{"openai", "OpenAI"},
	{"openrouter", "OpenRouter"},
	{"anthropic", "Anthropic"},
	{"generic", "Generic OpenAI-compatible"},
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
