package skilldesigner

import (
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/skilllibrary"
)

// A weak model omitting a cosmetic field must not destroy a completed design
// conversation. The prompt is the ask; this is the guarantee.
func TestNormalizeFrontmatterFillsDefaults(t *testing.T) {
	in := "---\nname: my-skill\ndescription: Does a thing when asked.\n---\n\n# My Skill\n\nBody.\n"
	out := NormalizeFrontmatter(in, "my-skill")

	m, body := skilllibrary.ParseMeta(out)
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0", m.Version)
	}
	if m.License != "MIT-0" {
		t.Errorf("License = %q, want MIT-0", m.License)
	}
	if m.Category != "Other" {
		t.Errorf("Category = %q, want Other", m.Category)
	}
	if m.Description != "Does a thing when asked." {
		t.Errorf("Description was altered: %q", m.Description)
	}
	if !strings.Contains(body, "Body.") {
		t.Errorf("body lost:\n%s", out)
	}
}

func TestNormalizeFrontmatterPreservesExplicitValues(t *testing.T) {
	in := "---\nname: pdf-ish\ndescription: d\nversion: 3.2.1\nlicense: Apache-2.0\ncategory: File Processing\n---\n\nbody\n"
	m, _ := skilllibrary.ParseMeta(NormalizeFrontmatter(in, "pdf-ish"))
	if m.Version != "3.2.1" || m.License != "Apache-2.0" || m.Category != "File Processing" {
		t.Errorf("explicit values overwritten: %+v", m)
	}
}

// A hallucinated category must not pollute the UI's grouping with a category
// of one that never goes away.
func TestNormalizeFrontmatterCoercesUnknownCategory(t *testing.T) {
	in := "---\nname: x\ndescription: d\ncategory: Miscellaneous Wizardry\n---\n\nbody\n"
	m, _ := skilllibrary.ParseMeta(NormalizeFrontmatter(in, "x"))
	if m.Category != "Other" {
		t.Errorf("Category = %q, want Other", m.Category)
	}
}

// Categories match case-insensitively — a model writing "meta" means Meta.
func TestNormalizeFrontmatterCategoryCaseInsensitive(t *testing.T) {
	in := "---\nname: x\ndescription: d\ncategory: file processing\n---\n\nbody\n"
	m, _ := skilllibrary.ParseMeta(NormalizeFrontmatter(in, "x"))
	if m.Category != "File Processing" {
		t.Errorf("Category = %q, want the canonical File Processing", m.Category)
	}
}

// The name in the file must be the validated slug, not whatever the model wrote,
// or the frontmatter disagrees with the directory it lives in.
func TestNormalizeFrontmatterForcesValidatedName(t *testing.T) {
	in := "---\nname: Some Pretty Name\ndescription: d\n---\n\nbody\n"
	m, _ := skilllibrary.ParseMeta(NormalizeFrontmatter(in, "some-pretty-name"))
	if m.Name != "some-pretty-name" {
		t.Errorf("Name = %q, want some-pretty-name", m.Name)
	}
}

// No frontmatter at all: synthesize a complete block rather than failing.
func TestNormalizeFrontmatterNoFrontmatter(t *testing.T) {
	out := NormalizeFrontmatter("# Just A Body\n\nno frontmatter here\n", "just-a-body")
	m, body := skilllibrary.ParseMeta(out)
	if m.Name != "just-a-body" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Category != "Other" || m.Version != "1.0.0" || m.License != "MIT-0" {
		t.Errorf("defaults not applied: %+v", m)
	}
	if !strings.Contains(body, "no frontmatter here") {
		t.Errorf("body lost:\n%s", out)
	}
}

// Rewriting the frontmatter must not drop tool requirements: resolveSkillBins
// reads them at run time, and a skill whose tools never resolve fails with a
// misleading "tool not installed" instead of at parse time.
func TestNormalizeFrontmatterPreservesRequiresAndInstall(t *testing.T) {
	in := `---
name: x
description: d
category: Development
metadata:
  requires:
    bins: [pandoc]
    anyBins: [pdftotext, mutool]
    env: [API_KEY]
  install:
    - kind: pip
      package: pdfplumber
    - kind: binary
      bin: pandoc
      url: https://example.com/pandoc.tar.gz
      strip: 1
---

body
`
	m, _ := skilllibrary.ParseMeta(NormalizeFrontmatter(in, "x"))
	if len(m.RequiresBins) != 1 || m.RequiresBins[0] != "pandoc" {
		t.Errorf("RequiresBins = %v", m.RequiresBins)
	}
	if len(m.AnyBins) != 2 {
		t.Errorf("AnyBins = %v", m.AnyBins)
	}
	if len(m.RequiresEnv) != 1 || m.RequiresEnv[0] != "API_KEY" {
		t.Errorf("RequiresEnv = %v", m.RequiresEnv)
	}
	if len(m.Install) != 2 {
		t.Fatalf("Install = %+v", m.Install)
	}
	if m.Install[0].Kind != "pip" || m.Install[0].Package != "pdfplumber" {
		t.Errorf("Install[0] = %+v", m.Install[0])
	}
	if m.Install[1].URL == "" || m.Install[1].Strip != 1 {
		t.Errorf("Install[1] = %+v", m.Install[1])
	}
}

// Descriptions are model-written prose and routinely contain a ": ", which YAML
// would otherwise read as a mapping and lose.
func TestNormalizeFrontmatterQuotesAwkwardDescriptions(t *testing.T) {
	for _, desc := range []string{
		"Use this: it converts CSVs",
		"- leading dash",
		`has "quotes" inside`,
		"@mentions at the start",
	} {
		in := "---\nname: x\ndescription: " + desc + "\n---\n\nbody\n"
		m, _ := skilllibrary.ParseMeta(NormalizeFrontmatter(in, "x"))
		// The round trip must at minimum not lose the description entirely.
		if m.Description == "" {
			t.Errorf("description %q was lost", desc)
		}
	}
}

// The normalizer is applied on every save, so it must be a fixed point:
// normalizing its own output changes nothing.
func TestNormalizeFrontmatterIsIdempotent(t *testing.T) {
	in := "---\nname: x\ndescription: Does a thing.\nmetadata:\n  requires:\n    bins: [rg]\n---\n\nbody\n"
	once := NormalizeFrontmatter(in, "x")
	if twice := NormalizeFrontmatter(once, "x"); twice != once {
		t.Errorf("not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

// Documents the accepted truncation: modelled fields survive, unknown ones do
// not. A test rather than a comment alone, so the day someone adds a field to
// SkillMeta they find out this is where it has to be re-emitted.
func TestNormalizeFrontmatterDropsUnmodelledKeys(t *testing.T) {
	in := "---\nname: x\ndescription: d\ntopics: [alpha, beta]\nallowed-tools: [Bash]\n---\n\nbody\n"
	out := NormalizeFrontmatter(in, "x")

	m, _ := skilllibrary.ParseMeta(out)
	if len(m.Topics) != 2 {
		t.Errorf("topics must survive, got %v", m.Topics)
	}
	if strings.Contains(out, "allowed-tools") {
		t.Errorf("unexpectedly preserved an unmodelled key — update the doc comment:\n%s", out)
	}
}
