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

		"lmstudio": "http://localhost:1234/v1",
		"llamacpp": "http://localhost:8080/v1",
		"vllm":     "http://localhost:8000/v1",
		"localai":  "http://localhost:8080/v1",
		"jan":      "http://localhost:1337/v1",
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
		"bedrock", "alibaba", "together", "fireworks", "cerebras", "sambanova",
		"nebius", "deepinfra", "huggingface", "github_models",
		"lmstudio", "llamacpp", "vllm", "localai", "jan",
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
