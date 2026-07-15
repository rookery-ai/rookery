package coder

import (
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/llm"
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
