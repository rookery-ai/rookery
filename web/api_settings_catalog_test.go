package web

import (
	"testing"

	"github.com/ilijad1/rookery/internal/coder"
)

// TestCoderCatalogSliceCarriesLabelAndGroup guards the wiring between the coder
// catalog and the SPA's provider picker.
//
// The picker renders entry.label; without it the dropdown shows raw registry
// slugs like "ollama_local" and "github_models" — readable at sixteen
// providers, not at thirty-one. Group drives the two-section rendering of both
// the picker and the key gallery. Both values already exist on
// coder.APIProvider, so a missing field here is a wiring bug, not a data gap,
// and it is invisible from either file alone.
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
