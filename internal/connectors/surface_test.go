package connectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The catalog is a PUBLISHED interface. An agent that was built against
// calendar_list_events(max_results:…) keeps calling it with that name forever — its
// AGENT.md is prose on disk, not something a refactor can update. So renaming a
// parameter, or dropping an action, breaks working agents silently: the call still
// goes out, the argument is quietly ignored (TestOptionalParamsAreActuallyUsed only
// sees the YAML, where the new name IS referenced), and the agent gets an unbounded
// or unfiltered result it has no way to recognise as wrong.
//
// This golden snapshot makes every such change VISIBLE IN THE DIFF instead. Adding an
// action or an optional parameter shows up as pure addition and is unremarkable; a
// deletion or a rename shows up as a removed line, which is the thing a reviewer has
// to consciously approve.
//
// Regenerate deliberately, never reflexively:
//
//	UPDATE_SURFACE=1 go test ./internal/connectors/ -run TestCatalogSurfaceIsStable
//
// then READ the diff. A removed line is a broken agent unless you meant it.
const surfaceGolden = "testdata/catalog_surface.json"

type actionSurface struct {
	Params   []string `json:"params"`
	Required []string `json:"required"`
	Mutating bool     `json:"mutating,omitempty"`
}

func buildSurface(t *testing.T, r *Registry) map[string]map[string]actionSurface {
	t.Helper()
	out := map[string]map[string]actionSurface{}
	for _, prov := range r.ProviderNames() {
		acts := map[string]actionSurface{}
		for _, a := range r.Actions(prov) {
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(a.Params, &schema); err != nil {
				t.Fatalf("%s/%s: params do not parse: %v", prov, a.Name, err)
			}
			names := make([]string, 0, len(schema.Properties))
			for p := range schema.Properties {
				names = append(names, p)
			}
			sort.Strings(names)
			req := append([]string(nil), schema.Required...)
			sort.Strings(req)
			if names == nil {
				names = []string{}
			}
			if req == nil {
				req = []string{}
			}
			acts[a.Name] = actionSurface{Params: names, Required: req, Mutating: a.Mutating}
		}
		out[prov] = acts
	}
	return out
}

func TestCatalogSurfaceIsStable(t *testing.T) {
	r := loadRegistry(t)
	got := buildSurface(t, r)

	if os.Getenv("UPDATE_SURFACE") != "" {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(surfaceGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(surfaceGolden, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("wrote", surfaceGolden, "— read the diff before committing it")
		return
	}

	raw, err := os.ReadFile(surfaceGolden)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with UPDATE_SURFACE=1)", surfaceGolden, err)
	}
	var want map[string]map[string]actionSurface
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("golden is not valid JSON: %v", err)
	}

	// Report only REMOVALS and renames. A pure addition is the normal case and
	// flagging it would train everyone to regenerate without reading, which is
	// exactly how a golden file stops protecting anything.
	for prov, wantActs := range want {
		gotActs, ok := got[prov]
		if !ok {
			t.Errorf("provider %q vanished from the catalog — every agent bound to it loses its tools", prov)
			continue
		}
		for name, w := range wantActs {
			g, ok := gotActs[name]
			if !ok {
				t.Errorf("%s/%s was REMOVED — an agent already calling it now gets 'unknown action'", prov, name)
				continue
			}
			for _, p := range w.Params {
				if !hasParam(g.Params, p) {
					t.Errorf("%s/%s lost parameter %q — an agent still passing it has the argument silently ignored",
						prov, name, p)
				}
			}
			for _, p := range w.Required {
				if !hasParam(g.Required, p) && hasParam(g.Params, p) {
					continue // relaxing required→optional is backward compatible
				}
				if !hasParam(g.Params, p) {
					t.Errorf("%s/%s lost required parameter %q", prov, name, p)
				}
			}
			// Tightening is the other direction: a parameter that becomes required
			// breaks every existing caller that omitted it.
			for _, p := range g.Required {
				if !hasParam(w.Required, p) && hasParam(w.Params, p) {
					t.Errorf("%s/%s made %q required — existing callers that omit it now fail validation",
						prov, name, p)
				}
			}
		}
	}
}

func hasParam(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
