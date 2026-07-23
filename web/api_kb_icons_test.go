package web

import (
	"reflect"
	"strings"
	"testing"
)

func TestMigrateIconKeys(t *testing.T) {
	t.Run("moves exact entry", func(t *testing.T) {
		icons := map[string]string{"notes/foo.md": "📌", "notes/bar.md": "⭐"}
		changed := migrateIconKeys(icons, "notes/foo.md", "notes/renamed.md")
		if !changed {
			t.Fatal("expected changed=true")
		}
		want := map[string]string{"notes/renamed.md": "📌", "notes/bar.md": "⭐"}
		if !reflect.DeepEqual(icons, want) {
			t.Fatalf("got %v want %v", icons, want)
		}
	})

	t.Run("carries folder descendants on move", func(t *testing.T) {
		icons := map[string]string{
			"projects":          "📁",
			"projects/a.md":     "🅰",
			"projects/sub/b.md": "🅱",
			"notes/keep.md":     "✅",
		}
		changed := migrateIconKeys(icons, "projects", "archive/projects")
		if !changed {
			t.Fatal("expected changed=true")
		}
		want := map[string]string{
			"archive/projects":          "📁",
			"archive/projects/a.md":     "🅰",
			"archive/projects/sub/b.md": "🅱",
			"notes/keep.md":             "✅",
		}
		if !reflect.DeepEqual(icons, want) {
			t.Fatalf("got %v want %v", icons, want)
		}
	})

	t.Run("no-op when nothing matches", func(t *testing.T) {
		icons := map[string]string{"notes/foo.md": "📌"}
		if migrateIconKeys(icons, "other/x.md", "other/y.md") {
			t.Fatal("expected changed=false")
		}
		// A prefix that is NOT a path boundary must not match (projectsX vs projects/).
		icons2 := map[string]string{"projectsX/a.md": "📌"}
		if migrateIconKeys(icons2, "projects", "archive") {
			t.Fatalf("false prefix match: %v", icons2)
		}
	})
}

func TestDropIconKeys(t *testing.T) {
	t.Run("drops exact and descendants", func(t *testing.T) {
		icons := map[string]string{
			"projects":       "📁",
			"projects/a.md":  "🅰",
			"projects2/b.md": "🅱", // sibling with shared prefix — must survive
			"notes/keep.md":  "✅",
		}
		changed := dropIconKeys(icons, "projects")
		if !changed {
			t.Fatal("expected changed=true")
		}
		want := map[string]string{"projects2/b.md": "🅱", "notes/keep.md": "✅"}
		if !reflect.DeepEqual(icons, want) {
			t.Fatalf("got %v want %v", icons, want)
		}
	})

	t.Run("no-op when absent", func(t *testing.T) {
		icons := map[string]string{"notes/foo.md": "📌"}
		if dropIconKeys(icons, "notes/bar.md") {
			t.Fatal("expected changed=false")
		}
	})
}

func TestStripFrontmatter(t *testing.T) {
	cases := []struct{ in, want string }{
		{"---\ntitle: x\ndate: y\n---\n\n# Body\n", "# Body\n"},
		{"# No frontmatter\n\ntext", "# No frontmatter\n\ntext"},
		{"---\nonly: open\nno close\n", "---\nonly: open\nno close\n"}, // unterminated → whole body
		{"---\n---\nbody", "body"},                                     // empty block
		{"not---\nfoo", "not---\nfoo"},                                 // not a fence at start
	}
	for i, c := range cases {
		if got := stripFrontmatter(c.in); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestSlugifyAssetAndImageDetection(t *testing.T) {
	if got := slugifyAsset("My Photo (2).PNG"); got != "my-photo-2-png" {
		t.Errorf("slugifyAsset = %q", got)
	}
	if got := slugifyAsset("   !!!   "); got != "" {
		t.Errorf("slugifyAsset(punct) = %q, want empty", got)
	}
	if !isImagePath("assets/x.png") || !isImagePath("A.JPEG") {
		t.Error("isImagePath should accept png/jpeg")
	}
	if isImagePath("notes/x.md") || isImagePath("a.txt") {
		t.Error("isImagePath should reject non-images")
	}
	// assetName preserves the stem + ext with a random middle.
	n := assetName("Holiday Snap.JPG")
	if !strings.HasPrefix(n, "holiday-snap-") || !strings.HasSuffix(n, ".jpg") {
		t.Errorf("assetName = %q", n)
	}
}
