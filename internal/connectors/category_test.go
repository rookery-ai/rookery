package connectors

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// validCategories is the closed set the connections page knows how to order.
// "Advertising" is deliberately present but unused until the Meta/Google Ads
// providers land — the UI orders it now so those arrive in the right slot.
var validCategories = map[string]bool{
	"Google": true, "Publishing & Media": true, "Advertising": true,
	"Productivity": true, "Communication": true, "Commerce": true,
	"Developer": true, "Support": true, "Other": true,
	// Everyday-connector categories. "Self-hosted" groups by auth shape ("runs on
	// my box, needs a base URL") rather than by domain, unlike the other eight —
	// a deliberate inconsistency, because it matches how the page is browsed and
	// the alternative scatters Home Assistant, Immich and Paperless across three
	// unrelated headings.
	"Self-hosted": true, "Health & Fitness": true, "Finance": true,
	"Data & Reference": true,
	// "Cloud" is infrastructure the user rents rather than runs — the AWS
	// connector and, in time, the cloud-adjacent hosts. Distinct from
	// "Self-hosted", which is the box under their desk.
	"Cloud": true,
}

// Every bundled provider must declare a category, or it silently lands in
// "Other" on a page whose whole purpose is grouping.
func TestEveryProviderHasAValidCategory(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	names := reg.ProviderNames()
	if len(names) < 30 {
		t.Fatalf("expected the provider set to be substantial, got %d — an empty "+
			"registry would make this test vacuously pass", len(names))
	}
	for _, name := range names {
		p, _ := reg.ProviderByName(name)
		if p.Category == "" {
			t.Errorf("provider %q has no category", name)
			continue
		}
		if !validCategories[p.Category] {
			t.Errorf("provider %q has unknown category %q", name, p.Category)
		}
	}
}

// The four Google publisher providers belong with the rest of the Google family
// so a user who connected Gmail finds them next to it.
func TestPublisherProvidersAreCategorised(t *testing.T) {
	reg, _ := LoadBundled()
	want := map[string]string{
		"google_adsense":       "Google",
		"google_analytics":     "Google",
		"google_searchconsole": "Google",
		"youtube":              "Publishing & Media",
	}
	for slug, cat := range want {
		p, ok := reg.ProviderByName(slug)
		if !ok {
			t.Fatalf("provider %q not loaded", slug)
		}
		if p.Category != cat {
			t.Errorf("%s category = %q, want %q", slug, p.Category, cat)
		}
	}
}

// The SPA's CATEGORY_ORDER and this package's validCategories must agree. A category
// valid only in Go funnels its providers into "Other" on the page (groupByCategory
// treats an unknown category as Other); one present only in the SPA renders an empty
// heading. Neither failure is visible in either file alone.
func TestCategoriesMatchTheSPA(t *testing.T) {
	src, err := os.ReadFile("../../web/ui/src/lib/connections.ts")
	if err != nil {
		t.Fatalf("read connections.ts: %v", err)
	}
	const marker = "export const CATEGORY_ORDER = ["
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatal("CATEGORY_ORDER not found in connections.ts — did it move or get renamed?")
	}
	rest := string(src)[i+len(marker):]
	end := strings.Index(rest, "]")
	if end < 0 {
		t.Fatal("CATEGORY_ORDER array is not terminated")
	}
	spa := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(rest[:end], -1) {
		spa[m[1]] = true
	}
	if len(spa) < 5 {
		t.Fatalf("parsed only %d categories from the SPA — the parse is wrong, not the data", len(spa))
	}
	for c := range validCategories {
		if !spa[c] {
			t.Errorf("category %q is valid in Go but missing from the SPA's CATEGORY_ORDER", c)
		}
	}
	for c := range spa {
		if !validCategories[c] {
			t.Errorf("category %q is in the SPA's CATEGORY_ORDER but not in validCategories", c)
		}
	}
}
