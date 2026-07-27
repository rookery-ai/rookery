package web

import (
	"testing"

	"github.com/ilijad1/simple-agents/internal/connectors"
)

// The connections page must show every provider the registry can actually execute.
// The hardcoded list this replaced silently omitted newly added data files, which is
// how a provider ships without ever appearing in the UI.
func TestServiceProvidersCoversRegistry(t *testing.T) {
	reg, err := connectors.LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	s := &Server{connectors: reg}

	got := s.serviceProviders()
	if len(got) != len(reg.ProviderNames()) {
		t.Fatalf("serviceProviders() returned %d providers, registry has %d",
			len(got), len(reg.ProviderNames()))
	}

	seen := map[string]bool{}
	for _, p := range got {
		seen[p] = true
	}
	for _, want := range []string{"google", "github", "notion", "stripe"} {
		if !seen[want] {
			t.Errorf("provider %q missing from serviceProviders()", want)
		}
	}
}

// Sorted order keeps the connections page stable across restarts; map iteration
// order would reshuffle the cards on every boot.
func TestServiceProvidersIsSorted(t *testing.T) {
	reg, err := connectors.LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	got := (&Server{connectors: reg}).serviceProviders()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("providers are not sorted: %q precedes %q", got[i-1], got[i])
		}
	}
}
