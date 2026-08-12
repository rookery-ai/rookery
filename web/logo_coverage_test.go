package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/gateway"
	"github.com/rookery-ai/rookery/internal/websearch"
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
	// neutral fallback tile is the correct rendering, not a degraded one.
	//
	// Everything else must resolve to <slug>.svg. llamacpp, localai, jan and
	// readwise were exempt here until 2026-08-05, when no vendoring source
	// carried their marks; each now vendors its own publisher's asset (see
	// UPSTREAM_* in scripts/vendor-brand-logos.sh). The policy that put them
	// here is unchanged and still applies to the next brand without a mark:
	// vendor the real published logo or show a letter — never approximate
	// someone else's brand.
	//
	// microsoft_todo joins them for the same reason and under the same policy:
	// Microsoft's product marks were REMOVED from simple-icons, and
	// worldvectorlogo carries OneDrive, Excel and OneNote but not To Do. The
	// alternatives were both worse than a letter — reusing the Outlook mark
	// would label it as a different product, and drawing something
	// To-Do-shaped would be approximating a brand we do not own.
	allowNoLogo := map[string]bool{
		"generic":        true,
		"microsoft_todo": true,
	}

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

// TestBrandLogoMarksAreVisibleOnTheTile catches the failure mode coverage
// cannot: a file that exists, is well-formed, passes every other check — and
// renders invisibly.
//
// ProviderLogo draws every mark on a WHITE tile. Moonshot shipped as lobehub's
// "-color" variant, which for that brand is a white mark on a transparent
// field, drawn for Moonshot's own blue container. On the tile it showed
// nothing but a stray blue dot, and no test objected.
//
// A monochrome mark is fine — it paints with currentColor and the tile supplies
// a near-black colour. A raster wrapped in <image> is fine — it carries its own
// pixels. What is not fine is a vector mark whose every explicit fill is white.
// The condition is deliberately "no non-white fill ANYWHERE", not "contains a
// white fill": Telegram, Reddit, Facebook, Trello and a dozen others are
// legitimately white over their own coloured background shape.
//
// LIMITS, stated so nobody mistakes this for comprehensive: it catches only the
// TOTAL case. It does NOT catch a mark that is mostly white with a small
// coloured accent — which is exactly what Moonshot was. Deciding that requires
// the rendered AREA of each shape, and area cannot be approximated from the
// source: Hacker News draws its full-canvas orange background as the 18-byte
// path "m4 4h188v188h-188z", so path-data length ranks it as 90% white ink,
// above Moonshot's 83%. Both a length heuristic and a shape-count heuristic
// therefore rank a correct mark as worse than a broken one. Answering it
// properly means rasterising, which does not belong in a unit test — so the
// partial case is pinned per-brand below instead.
func TestBrandLogoMarksAreVisibleOnTheTile(t *testing.T) {
	entries, err := os.ReadDir(logoAssetDir)
	if err != nil {
		t.Fatalf("read logo dir: %v", err)
	}
	fillRe := regexp.MustCompile(`(?i)fill="([^"]+)"`)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".svg" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(logoAssetDir, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		s := string(b)
		if strings.Contains(s, "currentColor") || strings.Contains(s, "<image") {
			continue
		}
		matches := fillRe.FindAllStringSubmatch(s, -1)
		if len(matches) == 0 {
			continue // styled by other means; not this test's business
		}
		checked++
		var hasVisible bool
		for _, m := range matches {
			v := strings.ToLower(strings.TrimSpace(m[1]))
			if v == "none" {
				continue
			}
			if v != "#fff" && v != "#ffffff" && v != "white" {
				hasVisible = true
				break
			}
		}
		if !hasVisible {
			t.Errorf("%s: every fill is white — invisible on ProviderLogo's white tile. "+
				"Vendor the monochrome variant rather than the -color one.", e.Name())
		}
	}
	if checked == 0 {
		t.Fatal("no coloured marks examined — the filters above have gone wrong")
	}
}

// TestMoonshotMarkIsMonochrome pins the one brand where the general guard
// above provably cannot help.
//
// lobehub publishes Kimi as both "kimi" (monochrome, currentColor) and
// "kimi-color". For this brand the "-color" variant is a WHITE mark on a
// transparent field, drawn for Moonshot's own blue container — its only
// coloured element is a small dot. Vendored as-is onto ProviderLogo's white
// tile it rendered as an empty square with a speck in one corner, and every
// existing test passed.
//
// The monochrome variant is the right one here: ProviderLogo pins
// color:#18181b for currentColor marks, so it always has contrast on the tile.
// If a future re-vendor switches this back to a -color variant, this fails.
func TestMoonshotMarkIsMonochrome(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(logoAssetDir, "moonshot.svg"))
	if err != nil {
		t.Fatalf("read moonshot.svg: %v", err)
	}
	if !strings.Contains(string(b), "currentColor") {
		t.Error(`moonshot.svg is not the monochrome mark — the -color variant is ` +
			`white-on-transparent and renders invisibly on the white tile. ` +
			`Vendor lobehub's "kimi", not "kimi-color".`)
	}
}

// TestBrandLogoAssetsCarryNoDanglingClasses guards the interaction between two
// things that are each individually correct.
//
// strip_svg MUST remove <style> blocks — these files are inlined into the DOM
// with dangerouslySetInnerHTML. But Illustrator and Inkscape export marks as
// `<rect class="st2"/>` plus `<style>.st2{fill:#1b1f20}</style>`, so stripping
// the style left every classed element at the SVG default fill:black:
//
//   - llama.cpp rendered as a SOLID BLACK SQUARE (its background rect is .st2)
//   - Google Ads and Google Analytics rendered as black silhouettes
//   - Gotify rendered as a black blob
//   - Open Library silently lost three stroke paths
//
// Every one of those passed the coverage, well-formedness and white-fill tests.
// scripts/vendor-brand-logos.sh now inlines class-based paint rules BEFORE the
// strip; a class attribute surviving into a committed asset means that step did
// not resolve it, and the mark is very likely painting itself black.
func TestBrandLogoAssetsCarryNoDanglingClasses(t *testing.T) {
	entries, err := os.ReadDir(logoAssetDir)
	if err != nil {
		t.Fatalf("read logo dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".svg" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(logoAssetDir, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		s := string(b)
		if strings.Contains(s, "<style") {
			t.Errorf("%s: retains a <style> block — it must be stripped, since these "+
				"files are inlined into the DOM", e.Name())
		}
		if strings.Contains(s, `class="`) {
			t.Errorf("%s: carries a class attribute with no stylesheet to resolve it — "+
				"the mark falls back to fill:black. Re-run "+
				"scripts/vendor-brand-logos.sh, whose inline_class_styles step "+
				"resolves these into presentation attributes.", e.Name())
		}
	}
}
