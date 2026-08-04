package coder

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/llm"
)

func TestAPIProviders_CatalogIntegrity(t *testing.T) {
	provs := APIProviders()
	if len(provs) < 31 {
		t.Fatalf("expected >=31 providers, got %d", len(provs))
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
		if p.Group != GroupHosted && p.Group != GroupLocal {
			t.Errorf("provider %q has bad group %q", p.Name, p.Group)
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

func TestAPIProviders_KeylessIsLocalTier(t *testing.T) {
	// Keyless <=> local. A hosted provider that forgets RequiresKey would let a
	// user select it with no credential; a local one that demands a key blocks a
	// server that accepts any string. Both directions matter, which is why this
	// is an iff and not the old "only ollama_local" spot check.
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
	// value like "https://bedrock-mantle.{region}.api.aws/v1" would satisfy every
	// other test in this file and then fail at request time with a DNS error on a
	// literal "{region}". Region/port variation belongs in the per-workspace
	// base-URL override, not in the registry default.
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
