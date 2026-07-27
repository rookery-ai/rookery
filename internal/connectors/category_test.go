package connectors

import "testing"

// validCategories is the closed set the connections page knows how to order.
// "Advertising" is deliberately present but unused until the Meta/Google Ads
// providers land — the UI orders it now so those arrive in the right slot.
var validCategories = map[string]bool{
	"Google": true, "Publishing & Media": true, "Advertising": true,
	"Productivity": true, "Communication": true, "Commerce": true,
	"Developer": true, "Support": true, "Other": true,
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
