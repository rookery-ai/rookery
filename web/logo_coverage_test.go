package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/websearch"
)

// logoAssetDir is where scripts/vendor-brand-logos.sh writes the committed
// brand SVGs that the SPA's ProviderLogo renders.
const logoAssetDir = "ui/src/assets/logos"

// TestBrandLogoCoverage is the merge gate for "every provider shows a real
// logo, not a coloured letter tile".
//
// ProviderLogo falls back to an initial when a slug has no vendored asset, and
// that fallback is silent by design — which means a provider added on the Go
// side would quietly regress the connections page back to placeholders. This
// test enumerates the slugs the UI can actually render, from the same lists the
// handlers use, and fails when one has no logo.
//
// A slug listed in allowNoLogo is exempt on purpose (see below); everything
// else must resolve to <slug>.svg.
func TestBrandLogoCoverage(t *testing.T) {
	// "generic" is the coder settings' "Custom (OpenAI-compatible)" escape
	// hatch — a user-supplied endpoint, so there is no brand to show and the
	// neutral fallback tile is the correct rendering.
	allowNoLogo := map[string]bool{"generic": true}

	var slugs []string

	// Service connectors — the exact list the connections page renders. Loaded
	// from the registry directly rather than through a Server: this test needs
	// no other server state, and the handler derives its list from the same call.
	reg, err := connectors.LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	slugs = append(slugs, reg.ProviderNames()...)

	// Chat platforms — the platforms with a real adapter. Deliberately NOT
	// gateway.CredSpecs(): that registry is global and mutable, and other
	// tests in this package register throwaway specs into it, which would
	// make this test demand a logo for a fixture like "cs-multi".
	slugs = append(slugs, gateway.RegisteredAdapterPlatforms()...)

	// Coder API providers — the settings/setup provider picker.
	for _, p := range coder.APIProviders() {
		slugs = append(slugs, p.Name)
	}

	// Web-search key providers — the third gallery on the connections page.
	// Derived from the secret names so adding a provider there is caught here
	// rather than shipping one more letter tile.
	for _, name := range websearch.KeySecretNames() {
		slugs = append(slugs, strings.ToLower(strings.TrimPrefix(name, "SEARCH_KEY_")))
	}

	if len(slugs) < 40 {
		t.Fatalf("expected the combined provider set to be substantial, got %d — "+
			"an empty registry would make this test vacuously pass", len(slugs))
	}

	seen := map[string]bool{}
	for _, slug := range slugs {
		if seen[slug] || allowNoLogo[slug] {
			continue
		}
		seen[slug] = true

		path := filepath.Join(logoAssetDir, slug+".svg")
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("provider %q has no brand logo at %s — it will render as a "+
				"coloured initial. Add it via scripts/vendor-brand-logos.sh.", slug, path)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("provider %q has an empty logo file at %s", slug, path)
		}
	}
}

// TestBrandLogoAssetsAreWellFormed guards the vendoring step itself: a failed
// download that wrote an HTML error page, or an asset carrying a <script>,
// would otherwise reach the DOM — ProviderLogo inlines these files with
// dangerouslySetInnerHTML precisely because monochrome marks need currentColor.
func TestBrandLogoAssetsAreWellFormed(t *testing.T) {
	entries, err := os.ReadDir(logoAssetDir)
	if err != nil {
		t.Fatalf("read logo dir: %v", err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".svg" {
			continue
		}
		count++
		b, err := os.ReadFile(filepath.Join(logoAssetDir, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		s := string(b)
		if !strings.HasPrefix(s, "<svg") {
			t.Errorf("%s: does not start with <svg (a failed download writes HTML here)", e.Name())
		}
		for _, bad := range []string{"<script", "javascript:"} {
			if strings.Contains(strings.ToLower(s), bad) {
				t.Errorf("%s: contains %q — must be stripped before vendoring", e.Name(), bad)
			}
		}
		if !strings.Contains(s, "viewBox") {
			t.Errorf("%s: no viewBox, so it cannot scale to the tile", e.Name())
		}
		// The tile that inlines this already carries role="img" + aria-label.
		// A <title> inside the mark adds a SECOND accessible name, so the brand
		// shows up twice in the accessibility tree — which broke the
		// connections-page tests by making getByText("Notion") ambiguous.
		if strings.Contains(strings.ToLower(s), "<title") {
			t.Errorf("%s: contains a <title> — it duplicates the tile's aria-label", e.Name())
		}
	}
	if count == 0 {
		t.Fatal("no logo assets found — did the vendoring script run?")
	}
}
