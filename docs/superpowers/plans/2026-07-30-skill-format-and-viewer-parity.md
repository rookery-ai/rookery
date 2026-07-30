# Skill Format and Viewer Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make built-in and user-created skills the same kind of object everywhere — the same metadata parsed by the same parser, the same viewer, the same card — and guarantee a generated skill's frontmatter matches a core skill's.

**Architecture:** `skilllibrary.ParseMeta` (which already parses everything) is run over user-skill content too, and a shared `apiSkillMeta` DTO carries category/version/requires on all three skill endpoints. In the SPA, `SkillDetailPage` and `CoreSkillViewPage` collapse into one `SkillView` with a Rendered/Raw toggle, editable only for user skills. On the generation side the prompt requires full frontmatter and `SkillSaver.SaveSkill` fills defaults rather than rejecting.

**Tech Stack:** Go 1.x (no new dependencies — `ParseMeta` and `gopkg.in/yaml.v3` are already in use), React 19 + TypeScript + `react-markdown` + `remark-gfm`, vitest.

**Spec:** `docs/superpowers/specs/2026-07-30-skill-format-and-viewer-parity-design.md`

## Global Constraints

- **No new dependencies**, Go or npm. `react-markdown` and `remark-gfm` are already imported by `SkillDetailPage.tsx`.
- **Markdown rendering keeps the existing safe config**: `remarkGfm` only, **no `rehype-raw`**, links forced to `target="_blank" rel="noreferrer noopener"`. This currently applies to core skills; extending it to user skills matters *more*, not less — user skill content is model-generated.
- **Never reject a save over a cosmetic frontmatter field.** Missing or unknown values are defaulted (`version: 1.0.0`, `license: MIT-0`, `category: Other`). A blocked save at the end of a design conversation is strictly worse than a defaulted field.
- **Valid categories** (exact strings, matching what core skills ship): `File Processing`, `Agent Behaviour`, `Web & Research`, `Development`, `Productivity`, `Integrations`, `Meta`, `Other`.
- **Conventional Commits.** Run `go test ./... -count=1` before Go commits and `cd web/ui && npx tsc -b && npx oxlint && npx vitest run` before SPA commits.

---

### Task 1: `apiSkillMeta` — one parser feeds both kinds

**Files:**
- Modify: `web/api_skills.go:40-115` (both list DTOs, `apiListSkills`, `apiGetCoreSkill`, `apiGetSkill`)
- Create: `web/api_skills_meta_test.go`

**Interfaces:**
- Consumes: `skilllibrary.ParseMeta(content string) (SkillMeta, string)` and `skilllibrary.SkillMeta{Name, Description, Category, Version, License, Topics, RequiresBins, AnyBins, RequiresEnv, Install}` (existing, `internal/skilllibrary/library.go:55-140`).
- Produces:
  - `type apiSkillMeta struct { Category string; Version string; Requires []string }` with JSON keys `category`, `version`, `requires`
  - `func skillMetaDTO(m skilllibrary.SkillMeta) apiSkillMeta`
  - `func flattenRequires(m skilllibrary.SkillMeta) []string`

- [ ] **Step 1: Write the failing test**

Create `web/api_skills_meta_test.go`:

```go
package web

import (
	"reflect"
	"testing"

	"github.com/ilijad1/rookery/internal/skilllibrary"
)

const metaFixture = `---
name: demo
description: Does a demo thing when the user asks for a demo.
version: 2.1.0
license: MIT-0
category: File Processing
metadata:
  requires:
    bins: [pandoc]
    anyBins: [pdftotext, mutool]
    env: [DEMO_KEY]
---

# Demo

Body text.
`

// The whole point of the change: given ONE SKILL.md, a user skill and a core
// skill must be described identically. Two parsers would drift; this asserts
// there is one.
func TestSkillMetaDTOIdenticalForBothKinds(t *testing.T) {
	m, _ := skilllibrary.ParseMeta(metaFixture)
	got := skillMetaDTO(m)

	want := apiSkillMeta{
		Category: "File Processing",
		Version:  "2.1.0",
		Requires: []string{"pandoc", "pdftotext or mutool", "$DEMO_KEY"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skillMetaDTO() = %+v, want %+v", got, want)
	}
}

// A skill with no category must render as "Other", not as an empty chip.
func TestSkillMetaDTODefaultsCategory(t *testing.T) {
	m, _ := skilllibrary.ParseMeta("---\nname: bare\ndescription: x\n---\n\nbody\n")
	got := skillMetaDTO(m)
	if got.Category != "Other" {
		t.Errorf("Category = %q, want Other", got.Category)
	}
	if got.Version != "" {
		t.Errorf("Version = %q, want empty (the UI omits the chip)", got.Version)
	}
	if len(got.Requires) != 0 {
		t.Errorf("Requires = %v, want empty", got.Requires)
	}
}

// Malformed frontmatter must not break the view — that is exactly when the
// user needs to open the file.
func TestSkillMetaDTOMalformedFrontmatter(t *testing.T) {
	m, body := skilllibrary.ParseMeta("---\nname: [unclosed\n---\n\nthe body\n")
	got := skillMetaDTO(m)
	if got.Category != "Other" {
		t.Errorf("Category = %q, want Other", got.Category)
	}
	if body == "" {
		t.Error("body must survive a frontmatter parse failure")
	}
}

func TestFlattenRequiresShapes(t *testing.T) {
	cases := map[string]struct {
		meta skilllibrary.SkillMeta
		want []string
	}{
		"bins only":    {skilllibrary.SkillMeta{RequiresBins: []string{"rg"}}, []string{"rg"}},
		"anyBins pair": {skilllibrary.SkillMeta{AnyBins: []string{"a", "b"}}, []string{"a or b"}},
		"anyBins trio": {skilllibrary.SkillMeta{AnyBins: []string{"a", "b", "c"}}, []string{"a or b or c"}},
		"env":          {skilllibrary.SkillMeta{RequiresEnv: []string{"KEY"}}, []string{"$KEY"}},
		"none":         {skilllibrary.SkillMeta{}, nil},
	}
	for name, tc := range cases {
		if got := flattenRequires(tc.meta); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("[%s] flattenRequires = %v, want %v", name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run 'TestSkillMetaDTO|TestFlattenRequires' -v`
Expected: FAIL — `undefined: skillMetaDTO`, `undefined: apiSkillMeta`, `undefined: flattenRequires`.

- [ ] **Step 3: Write the implementation**

Add to `web/api_skills.go`, above the DTO block:

```go
// apiSkillMeta is the frontmatter metadata surfaced for BOTH core and user
// skills. It exists so the two kinds are described by the same fields parsed by
// the same parser — before this, only the core path parsed metadata at all and
// then threw it away at the DTO boundary, so a built-in skill and a created one
// could not be shown side by side.
type apiSkillMeta struct {
	Category string   `json:"category"`
	Version  string   `json:"version"`
	Requires []string `json:"requires"`
}

// defaultSkillCategory is what an unset or unrecognised category renders as.
// The UI needs a chip either way; an empty one reads as a bug.
const defaultSkillCategory = "Other"

// flattenRequires renders a skill's tool requirements as display strings, with
// the KIND encoded in the string rather than the shape.
//
// The nesting ParseMeta returns (bins / anyBins / env) is a Go detail; making
// the SPA branch on three lists to render one line would put that detail in two
// languages. "a or b" and "$KEY" carry the same information in a form the UI can
// print directly.
func flattenRequires(m skilllibrary.SkillMeta) []string {
	var out []string
	out = append(out, m.RequiresBins...)
	if len(m.AnyBins) > 0 {
		out = append(out, strings.Join(m.AnyBins, " or "))
	}
	for _, e := range m.RequiresEnv {
		out = append(out, "$"+e)
	}
	return out
}

// skillMetaDTO maps parsed frontmatter into the wire shape. Version is left
// empty when unset (the UI omits the chip) but Category always resolves, since
// the UI shows one unconditionally.
func skillMetaDTO(m skilllibrary.SkillMeta) apiSkillMeta {
	cat := m.Category
	if cat == "" {
		cat = defaultSkillCategory
	}
	return apiSkillMeta{Category: cat, Version: m.Version, Requires: flattenRequires(m)}
}
```

Add `"strings"` to the imports if absent.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./web/ -run 'TestSkillMetaDTO|TestFlattenRequires' -v`
Expected: PASS.

- [ ] **Step 5: Wire the DTO into all three endpoints**

Embed the metadata in both list items and the two detail responses:

```go
type apiSkillListItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	apiSkillMeta
}

type apiCoreSkillListItem struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	apiSkillMeta
}
```

`toAPISkillListItem` needs the content to parse, so give it the store lookup.
In `apiListSkills`, replace the user-skill loop with:

```go
	out := make([]apiSkillListItem, 0, len(skills))
	for _, sk := range skills {
		item := apiSkillListItem{
			ID: sk.ID, Name: sk.Name, Description: sk.Description, CreatedAt: sk.InstalledAt,
			// Degrades cleanly when the skill store is not configured: the
			// list must never fail over missing metadata.
			apiSkillMeta: apiSkillMeta{Category: defaultSkillCategory},
		}
		if s.skills != nil {
			if content, err := s.skills.Load(u.ID, sk.Name); err == nil {
				m, _ := skilllibrary.ParseMeta(content)
				item.apiSkillMeta = skillMetaDTO(m)
			}
		}
		out = append(out, item)
	}
```

Check the real signature of the skill store's read method
(`grep -n "func (s \*SkillStore)" internal/skillstore/*.go`) and use it —
`Load(workspaceID, name)` above is the expected shape; match whatever exists,
and see how `apiGetSkill` already reads content for the detail response.

For core skills, `LoadBundled()` already returns `SkillMeta`:

```go
	for _, m := range skilllibrary.LoadBundled() {
		core = append(core, apiCoreSkillListItem{
			Slug: m.Name, Name: m.Name, Description: m.Description,
			apiSkillMeta: skillMetaDTO(m),
		})
	}
```

In `apiGetCoreSkill`, parse the content it already loads and merge the fields
into the response map:

```go
	m, _ := skilllibrary.ParseMeta(content)
	meta := skillMetaDTO(m)
	return c.JSON(http.StatusOK, map[string]any{
		"slug": slug, "content": content,
		"description": m.Description,
		"category":    meta.Category,
		"version":     meta.Version,
		"requires":    meta.Requires,
	})
```

In `apiGetSkill`, do the same over the content it already returns.

- [ ] **Step 6: Verify the wiring with a round-trip test**

Append to `web/api_skills_meta_test.go`:

```go
// The list endpoint must carry metadata for core skills — this is what the
// card and the header render from.
func TestListSkillsCarriesCoreMetadata(t *testing.T) {
	_, env := newAPITestServer(t)
	rec := env.get(t, "/api/v1/skills")
	if rec.Code != 200 {
		t.Fatalf("GET /skills = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		CoreSkills []struct {
			Slug     string   `json:"slug"`
			Category string   `json:"category"`
			Version  string   `json:"version"`
			Requires []string `json:"requires"`
		} `json:"core_skills"`
	}
	decodeJSON(t, rec, &body)
	if len(body.CoreSkills) == 0 {
		t.Fatal("no core skills returned")
	}
	var pdf *struct {
		Slug     string   `json:"slug"`
		Category string   `json:"category"`
		Version  string   `json:"version"`
		Requires []string `json:"requires"`
	}
	for i := range body.CoreSkills {
		if body.CoreSkills[i].Slug == "pdf" {
			pdf = &body.CoreSkills[i]
		}
	}
	if pdf == nil {
		t.Fatal("core skill \"pdf\" missing")
	}
	if pdf.Category != "File Processing" {
		t.Errorf("pdf category = %q, want File Processing", pdf.Category)
	}
	if pdf.Version == "" {
		t.Error("pdf version missing")
	}
	if len(pdf.Requires) == 0 {
		t.Error("pdf requires missing (it declares anyBins)")
	}
	// Every core skill must resolve a category — an empty chip reads as a bug.
	for _, cs := range body.CoreSkills {
		if cs.Category == "" {
			t.Errorf("core skill %q has an empty category", cs.Slug)
		}
	}
}
```

Match `env.get` / `decodeJSON` to the helpers the neighbouring `web` tests
already use (read `web/api_skills_test.go` first).

Run: `go test ./web/ -run TestSkill -v -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/api_skills.go web/api_skills_meta_test.go
git commit -m "feat(web): expose the same skill metadata for core and user skills"
```

---

### Task 2: Generated skills carry full frontmatter

**Files:**
- Modify: `internal/prompts/prompts.go:2184-2192` (the frontmatter section of `BuildSkillImplementationPrompt`)
- Modify: `internal/skilldesigner/` — the `SkillSaver.SaveSkill` implementation (find it with `grep -rn "func.*SaveSkill" internal/skilldesigner/`)
- Modify: `internal/prompts/prompts_test.go`
- Create or modify: `internal/skilldesigner/frontmatter_test.go`

**Interfaces:**
- Consumes: `skilllibrary.ParseMeta` (existing), `apiSkillMeta` conventions from Task 1 (the same category list).
- Produces: `func normalizeFrontmatter(content, name string) string` in `internal/skilldesigner` — returns the content with `name`, `description`, `version`, `license`, and `category` guaranteed present and the category coerced to a known value.

- [ ] **Step 1: Write the failing test**

Create `internal/skilldesigner/frontmatter_test.go`:

```go
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
	out := normalizeFrontmatter(in, "my-skill")

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
	m, _ := skilllibrary.ParseMeta(normalizeFrontmatter(in, "pdf-ish"))
	if m.Version != "3.2.1" || m.License != "Apache-2.0" || m.Category != "File Processing" {
		t.Errorf("explicit values overwritten: %+v", m)
	}
}

// A hallucinated category must not pollute the UI's grouping.
func TestNormalizeFrontmatterCoercesUnknownCategory(t *testing.T) {
	in := "---\nname: x\ndescription: d\ncategory: Miscellaneous Wizardry\n---\n\nbody\n"
	m, _ := skilllibrary.ParseMeta(normalizeFrontmatter(in, "x"))
	if m.Category != "Other" {
		t.Errorf("Category = %q, want Other", m.Category)
	}
}

// Categories are matched case-insensitively — a model writing "meta" means Meta.
func TestNormalizeFrontmatterCategoryCaseInsensitive(t *testing.T) {
	in := "---\nname: x\ndescription: d\ncategory: file processing\n---\n\nbody\n"
	m, _ := skilllibrary.ParseMeta(normalizeFrontmatter(in, "x"))
	if m.Category != "File Processing" {
		t.Errorf("Category = %q, want the canonical File Processing", m.Category)
	}
}

// The name in the file must be the validated slug, not whatever the model wrote.
func TestNormalizeFrontmatterForcesValidatedName(t *testing.T) {
	in := "---\nname: Some Pretty Name\ndescription: d\n---\n\nbody\n"
	m, _ := skilllibrary.ParseMeta(normalizeFrontmatter(in, "some-pretty-name"))
	if m.Name != "some-pretty-name" {
		t.Errorf("Name = %q, want some-pretty-name", m.Name)
	}
}

// No frontmatter at all: synthesize a complete block rather than failing.
func TestNormalizeFrontmatterNoFrontmatter(t *testing.T) {
	out := normalizeFrontmatter("# Just A Body\n\nno frontmatter here\n", "just-a-body")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skilldesigner/ -run TestNormalizeFrontmatter -v`
Expected: FAIL — `undefined: normalizeFrontmatter`.

- [ ] **Step 3: Write the implementation**

Create `internal/skilldesigner/frontmatter.go`:

```go
package skilldesigner

import (
	"fmt"
	"strings"

	"github.com/ilijad1/rookery/internal/skilllibrary"
)

// validCategories is the closed set a skill may be filed under, matching what
// the 22 core skills ship. A generated value outside it is coerced rather than
// passed through: the UI groups on this field, and one hallucinated category
// creates a group of one that never goes away.
var validCategories = []string{
	"File Processing",
	"Agent Behaviour",
	"Web & Research",
	"Development",
	"Productivity",
	"Integrations",
	"Meta",
	"Other",
}

const (
	defaultVersion  = "1.0.0"
	defaultLicense  = "MIT-0"
	defaultCategory = "Other"
)

// canonicalCategory maps a model-written category onto the closed set,
// case-insensitively. Anything unrecognised becomes "Other".
func canonicalCategory(v string) string {
	v = strings.TrimSpace(v)
	for _, c := range validCategories {
		if strings.EqualFold(v, c) {
			return c
		}
	}
	return defaultCategory
}

// normalizeFrontmatter guarantees a saved SKILL.md carries the same frontmatter
// a built-in skill does: name, description, version, license, category.
//
// It DEFAULTS rather than rejects. The generation prompt asks for every field,
// but a weak model omitting one must not fail a save at the end of a full
// design conversation — losing the conversation is far worse than a skill filed
// under "Other". Explicit values always win; only absent or unrecognised ones
// are replaced.
//
// name is the already-validated slug (SaveSkill re-slugifies before calling
// this), so the file's name field cannot disagree with its directory.
func normalizeFrontmatter(content, name string) string {
	meta, body := skilllibrary.ParseMeta(content)

	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		desc = "A user-created skill."
	}
	version := strings.TrimSpace(meta.Version)
	if version == "" {
		version = defaultVersion
	}
	license := strings.TrimSpace(meta.License)
	if license == "" {
		license = defaultLicense
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", name)
	fmt.Fprintf(&sb, "description: %s\n", yamlScalar(desc))
	fmt.Fprintf(&sb, "version: %s\n", version)
	fmt.Fprintf(&sb, "license: %s\n", license)
	fmt.Fprintf(&sb, "category: %s\n", canonicalCategory(meta.Category))
	sb.WriteString(metadataBlock(meta))
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(body))
	sb.WriteString("\n")
	return sb.String()
}

// yamlScalar quotes a description that would otherwise break the block — a
// leading indicator character or an embedded colon-space. Descriptions are
// model-written prose and routinely contain both.
func yamlScalar(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	needsQuote := strings.ContainsAny(s[:1], "&*?|-<>=!%@`{}[]#,\"'") ||
		strings.Contains(s, ": ") || strings.HasSuffix(s, ":")
	if !needsQuote {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// metadataBlock re-emits the requires/install metadata the model declared.
// Rewriting the frontmatter must not silently drop a skill's tool
// requirements — resolveSkillBins reads them at run time, and a skill whose
// tools never resolve fails with a misleading "tool not installed".
func metadataBlock(m skilllibrary.SkillMeta) string {
	if len(m.RequiresBins) == 0 && len(m.AnyBins) == 0 &&
		len(m.RequiresEnv) == 0 && len(m.Install) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("metadata:\n")
	if len(m.RequiresBins) > 0 || len(m.AnyBins) > 0 || len(m.RequiresEnv) > 0 {
		sb.WriteString("  requires:\n")
		if len(m.RequiresBins) > 0 {
			fmt.Fprintf(&sb, "    bins: [%s]\n", strings.Join(m.RequiresBins, ", "))
		}
		if len(m.AnyBins) > 0 {
			fmt.Fprintf(&sb, "    anyBins: [%s]\n", strings.Join(m.AnyBins, ", "))
		}
		if len(m.RequiresEnv) > 0 {
			fmt.Fprintf(&sb, "    env: [%s]\n", strings.Join(m.RequiresEnv, ", "))
		}
	}
	if len(m.Install) > 0 {
		sb.WriteString("  install:\n")
		for _, sp := range m.Install {
			fmt.Fprintf(&sb, "    - kind: %s\n", sp.Kind)
			if sp.Bin != "" {
				fmt.Fprintf(&sb, "      bin: %s\n", sp.Bin)
			}
			if sp.Package != "" {
				fmt.Fprintf(&sb, "      package: %s\n", sp.Package)
			}
			if sp.URL != "" {
				fmt.Fprintf(&sb, "      url: %s\n", sp.URL)
			}
			if sp.Strip != 0 {
				fmt.Fprintf(&sb, "      strip: %d\n", sp.Strip)
			}
		}
	}
	return sb.String()
}
```

Check `skilllibrary.InstallSpec`'s real field names
(`grep -n "type InstallSpec" -A 10 internal/skilllibrary/library.go`) and match
them exactly in `metadataBlock`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/skilldesigner/ -run TestNormalizeFrontmatter -v`
Expected: PASS (six tests).

- [ ] **Step 5: Call it from `SaveSkill` and the paste-import path**

Find `SaveSkill` (`grep -rn "func.*SaveSkill" internal/skilldesigner/`) and pass
the SKILL.md content through `normalizeFrontmatter(content, slug)` immediately
before the write, using the already-re-slugified name. Do the same in the
paste-import handler (`POST /api/v1/skills`, `apiCreateSkill` in
`web/api_skills.go`) — a pasted SKILL.md from elsewhere has the same gap.
For the web path, expose it as an exported
`skilldesigner.NormalizeFrontmatter(content, name string) string` wrapper if the
handler cannot reach the unexported one, and update the tests to call the
exported name.

- [ ] **Step 6: Tighten the generation prompt**

In `internal/prompts/prompts.go` around line 2184, replace

```
The SKILL.md frontmatter (only name + description are strictly required):
```

with

```
The SKILL.md frontmatter — every field below is REQUIRED:
```

and after the `category:` line add:

```
  category must be exactly one of: File Processing, Agent Behaviour,
  Web & Research, Development, Productivity, Integrations, Meta, Other
```

Add to `internal/prompts/prompts_test.go`:

```go
// Generated skills must carry the same frontmatter a built-in skill does, or
// the two look like different kinds of object in the UI.
func TestSkillImplementationPromptRequiresFullFrontmatter(t *testing.T) {
	out := BuildSkillImplementationPrompt(SkillImplementationParams{SkillName: "demo"})
	if strings.Contains(out, "only name + description are strictly required") {
		t.Error("prompt still says version/license/category are optional")
	}
	for _, want := range []string{"version:", "license:", "category:", "File Processing"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
```

Match `BuildSkillImplementationPrompt`'s real signature — check
`grep -n "func BuildSkillImplementationPrompt" internal/prompts/prompts.go`
and how `skilldesigner` calls it.

- [ ] **Step 7: Run the full suite**

Run: `go test ./... -count=1 -timeout 300s`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/skilldesigner/ internal/prompts/ web/api_skills.go
git commit -m "feat(skilldesigner): guarantee full frontmatter on saved skills"
```

---

### Task 3: One `SkillView` for both kinds

**Files:**
- Create: `web/ui/src/pages/skills/SkillView.tsx`
- Modify: `web/ui/src/pages/skills/SkillDetailPage.tsx` (becomes two thin wrappers)
- Modify: `web/ui/src/lib/skills.ts` (types gain the metadata fields)
- Modify: `web/ui/src/pages/skills/skills.test.tsx`

**Interfaces:**
- Consumes: the API fields from Task 1 — `category: string`, `version: string`, `requires: string[]` on skill list items, `GET /api/v1/skills/:id`, and `GET /api/v1/skills/core/:slug`.
- Produces:
  - `export function SkillView(props: { kind: "core" | "user"; name: string; description?: string; category: string; version?: string; requires?: string[]; content: string; onSave?: (content: string) => Promise<void>; onDelete?: () => Promise<void> })`
  - `export default function SkillDetailPage()` and `export function CoreSkillViewPage()` — unchanged names and routes, now wrappers.

- [ ] **Step 1: Write the failing test**

Add to `web/ui/src/pages/skills/skills.test.tsx`:

```tsx
const CONTENT = "---\nname: demo\ndescription: d\n---\n\n# Demo\n\nSome **bold** body.\n";

function renderView(kind: "core" | "user", extra = {}) {
  return render(
    <SkillView
      kind={kind}
      name="demo"
      description="Does a demo thing."
      category="File Processing"
      version="1.0.0"
      requires={["pandoc", "pdftotext or mutool"]}
      content={CONTENT}
      {...extra}
    />,
  );
}

it("renders the same metadata header for both kinds", () => {
  for (const kind of ["core", "user"] as const) {
    const { unmount } = renderView(kind);
    expect(screen.getByText("demo")).toBeInTheDocument();
    expect(screen.getByText(/File Processing/)).toBeInTheDocument();
    expect(screen.getByText(/1\.0\.0/)).toBeInTheDocument();
    expect(screen.getByText(/pandoc/)).toBeInTheDocument();
    expect(screen.getByText(/pdftotext or mutool/)).toBeInTheDocument();
    unmount();
  }
});

it("defaults to the rendered view for both kinds", () => {
  renderView("user");
  // Rendered markdown produces a heading element; the raw source does not.
  expect(screen.getByRole("heading", { name: "Demo" })).toBeInTheDocument();
  expect(screen.queryByLabelText("SKILL.md")).not.toBeInTheDocument();
});

it("core skills expose a read-only raw view and no write controls", async () => {
  renderView("core");
  await userEvent.click(screen.getByRole("button", { name: /raw/i }));
  const ta = screen.getByLabelText("SKILL.md") as HTMLTextAreaElement;
  expect(ta.readOnly).toBe(true);
  expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /delete/i })).not.toBeInTheDocument();
});

it("user skills have an editable raw view with Save disabled until dirty", async () => {
  renderView("user", { onSave: vi.fn(), onDelete: vi.fn() });
  await userEvent.click(screen.getByRole("button", { name: /raw/i }));
  const ta = screen.getByLabelText("SKILL.md") as HTMLTextAreaElement;
  expect(ta.readOnly).toBe(false);
  expect(screen.getByRole("button", { name: /save skill/i })).toBeDisabled();
  await userEvent.type(ta, "x");
  expect(screen.getByRole("button", { name: /save skill/i })).toBeEnabled();
});

// The toggle must not be a way to silently lose an edit.
it("keeps an unsaved edit across a Raw → Rendered → Raw round trip", async () => {
  renderView("user", { onSave: vi.fn() });
  await userEvent.click(screen.getByRole("button", { name: /raw/i }));
  await userEvent.type(screen.getByLabelText("SKILL.md"), "\nEDITED");
  await userEvent.click(screen.getByRole("button", { name: /rendered/i }));
  await userEvent.click(screen.getByRole("button", { name: /raw/i }));
  expect((screen.getByLabelText("SKILL.md") as HTMLTextAreaElement).value).toContain("EDITED");
});

it("renders Other when a skill has no category", () => {
  renderView("user", { category: "Other", version: "" });
  expect(screen.getByText(/Other/)).toBeInTheDocument();
  // No version chip when the version is unset — an empty chip reads as a bug.
  expect(screen.queryByText(/^v$/)).not.toBeInTheDocument();
});
```

Import `SkillView` and match the file's existing render harness (it may need a
`MemoryRouter` and a `QueryClientProvider` wrapper — copy what the neighbouring
tests use).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/skills/skills.test.tsx`
Expected: FAIL — `SkillView` is not exported.

- [ ] **Step 3: Write `SkillView`**

Create `web/ui/src/pages/skills/SkillView.tsx`:

```tsx
import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// Shared markdown styling. Kept in one place because the whole point of this
// component is that a built-in skill and a created one look identical.
const PROSE = [
  "max-w-none rounded-lg border border-border bg-background p-6 text-sm leading-relaxed",
  "[&_p]:my-2 [&_pre]:my-3 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-chrome [&_pre]:p-3",
  "[&_code]:break-words [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5",
  "[&_strong]:font-semibold [&_a]:underline [&_a]:text-accent",
  "[&_h1]:mt-4 [&_h1]:text-lg [&_h1]:font-bold [&_h2]:mt-4 [&_h2]:text-base [&_h2]:font-bold",
].join(" ");

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full bg-chrome px-2 py-0.5 text-xs font-medium text-muted-2">
      {children}
    </span>
  );
}

export type SkillViewProps = {
  kind: "core" | "user";
  name: string;
  description?: string;
  category: string;
  version?: string;
  requires?: string[];
  content: string;
  /** Present only for kind="user" — core skills are embedded in the binary. */
  onSave?: (content: string) => Promise<void>;
  onDelete?: () => void;
};

export function SkillView({
  kind, name, description, category, version, requires = [], content,
  onSave, onDelete,
}: SkillViewProps) {
  // Rendered is the default for BOTH kinds: a skill is a document meant to be
  // read, and the source is the secondary view even where it is editable.
  const [tab, setTab] = useState<"rendered" | "raw">("rendered");
  const [draft, setDraft] = useState(content);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Only resets when the LOADED content changes, never on a tab switch, so
  // toggling views cannot silently discard an unsaved edit.
  useEffect(() => setDraft(content), [content]);

  const editable = kind === "user" && !!onSave;
  const dirty = draft !== content;

  async function handleSave() {
    if (!onSave) return;
    setSaving(true);
    setError(null);
    try {
      await onSave(draft);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-1 flex items-start justify-between gap-3">
        <h1 className="text-xl font-bold">{name}</h1>
        <div className="flex shrink-0 items-center gap-2">
          <div className="flex rounded-md border border-border p-0.5">
            {(["rendered", "raw"] as const).map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => setTab(t)}
                className={cn(
                  "rounded px-2 py-1 text-xs font-medium capitalize",
                  tab === t ? "bg-chrome text-foreground" : "text-muted-2",
                )}
              >
                {t}
              </button>
            ))}
          </div>
          {editable && (
            <>
              <Button size="sm" aria-label="Save skill" onClick={() => void handleSave()} disabled={!dirty || saving}>
                Save
              </Button>
              <Button size="sm" variant="outline" className="text-danger" onClick={onDelete}>
                Delete
              </Button>
            </>
          )}
        </div>
      </div>

      <div className="mb-2 flex flex-wrap items-center gap-1.5">
        <Chip>{category}</Chip>
        {version && <Chip>v{version}</Chip>}
        <Chip>{kind === "core" ? "Built-in" : "Yours"}</Chip>
      </div>

      {requires.length > 0 && (
        <p className="mb-2 text-xs text-muted-2">Needs: {requires.join(", ")}</p>
      )}
      {description && <p className="mb-4 max-w-2xl text-sm text-muted-2">{description}</p>}

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      {tab === "rendered" ? (
        <div className={PROSE}>
          {/* No rehype-raw: raw HTML in a SKILL.md renders as inert text. This
              matters MORE for user skills than core ones — their content is
              model-generated. */}
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              a: ({ node: _node, ...props }) => (
                <a {...props} target="_blank" rel="noreferrer noopener" />
              ),
            }}
          >
            {tab === "rendered" && editable ? draft : content}
          </ReactMarkdown>
        </div>
      ) : (
        <textarea
          aria-label="SKILL.md"
          value={editable ? draft : content}
          readOnly={!editable}
          onChange={(e) => setDraft(e.target.value)}
          className="min-h-[60vh] w-full resize-y rounded-md border border-border bg-background p-3 font-mono text-xs outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
        />
      )}
    </div>
  );
}
```

- [ ] **Step 4: Rewrite the two pages as wrappers**

In `SkillDetailPage.tsx`, keep both exported component names and routes; each
now loads its data and renders `SkillView`. `SkillDetailPage` keeps its delete
confirmation `Dialog` and passes `onDelete={() => setDeleteOpen(true)}`.
`CoreSkillViewPage` renders `<SkillView kind="core" … />` plus the existing
"Back to skills" link. Delete the two bespoke layouts.

Extend the types in `web/ui/src/lib/skills.ts`:

```ts
export type SkillMeta = {
  category: string;
  version: string;
  requires: string[];
};
export type SkillListItem = { id: string; name: string; description: string; created_at: string } & SkillMeta;
export type CoreSkillListItem = { slug: string; name: string; description: string } & SkillMeta;
```

and add the same fields to whatever `useSkillDetail` / `useCoreSkill` return.

- [ ] **Step 5: Unify the card**

In `SkillsPage.tsx`, replace `SkillCard` and `CoreSkillCard` with one component
taking `kind`. Same border (`border-border`), same background (`bg-background`),
same text contrast — no `border-dashed`, no `bg-chrome/50`, no `text-muted-2` on
the whole card. Render the category chip and a `Built-in`/`Yours` chip. Keep the
two list sections and the existing `showEmpty` / `noMatches` logic untouched.

- [ ] **Step 6: Run the checks**

```bash
cd web/ui && npx tsc -b && npx oxlint && npx vitest run
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src
git commit -m "feat(web/ui): one viewer and one card for core and user skills"
```

---

### Task 4: Gate and visual check

**Files:** none modified.

- [ ] **Step 1: Run the full gate**

Run: `make ci`
Expected: PASS.

- [ ] **Step 2: Deploy and look at it**

```bash
make deploy && sleep 3
curl -sS http://127.0.0.1:8080/api/v1/healthz >/dev/null || curl -sS http://127.0.0.1:8080/healthz
```

Open `/skills`. Confirm: built-in and user cards are visually equal apart from
their chip; opening a built-in skill shows the metadata header and a rendered
body with a working Raw tab that cannot be typed into; opening a user skill
shows the same header, defaults to Rendered, and Save stays disabled until the
Raw text actually changes.

- [ ] **Step 3: Push and open the PR**

```bash
git push -u origin feat/identity-source-of-truth
gh pr create --title "feat(skills): one format and one viewer for core and user skills" --body "$(cat <<'EOF'
Implements docs/superpowers/specs/2026-07-30-skill-format-and-viewer-parity-design.md.

Built-in and user skills are the same kind of object but were described and
displayed as two different things: the DTOs dropped the metadata ParseMeta
already produced, user skills opened in a raw textarea while core skills
rendered as markdown, and core cards were dashed and greyed.

- One apiSkillMeta (category, version, requires) on all three skill endpoints,
  parsed by the same ParseMeta for both kinds.
- One SkillView with a shared metadata header and a Rendered/Raw toggle,
  editable only for user skills. One card with a Built-in/Yours chip.
- The generation prompt now requires version/license/category, and
  normalizeFrontmatter fills defaults on save rather than rejecting — losing a
  design conversation over a cosmetic field is worse than a defaulted one.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
|---|---|
| `apiSkillMeta` on both list DTOs and the detail DTO | 1 |
| One parser for both kinds | 1 (test asserts identical output) |
| `requires` flattened server-side with kind in the string | 1 |
| `apiListSkills` degrades when `s.skills == nil` | 1 |
| Generation prompt requires full frontmatter | 2 |
| `SaveSkill` defaults rather than rejects | 2 |
| Unknown category coerced to `Other` | 2 |
| Paste-import gets the same defaulting | 2 (Step 5) |
| One `SkillView`, metadata header, Rendered/Raw toggle | 3 |
| Rendered default for both | 3 |
| Raw read-only for core, editable for user | 3 |
| Safe markdown config carried to user skills | 3 |
| Unsaved edit survives the toggle | 3 (test) |
| One card with Built-in/Yours chip, sections preserved | 3 (Step 5) |
| Malformed frontmatter still viewable | 1, 2 (tests) |
| Every listed test | 1-3 |

**Placeholder scan:** three "check the real signature" instructions (skill store
read method, `InstallSpec` fields, `BuildSkillImplementationPrompt` signature).
Each names the exact grep and the exact use — local-convention verification, not
undefined work.

**Type consistency:** `apiSkillMeta{Category, Version, Requires}` has the same
JSON keys in Task 1 and the same names in the TS types in Task 3.
`skillMetaDTO`/`flattenRequires` signatures match their definitions and call
sites. `normalizeFrontmatter(content, name string) string` is identical in its
definition, its tests, and both call sites. `SkillView`'s prop names in the test
(Task 3 Step 1) match the `SkillViewProps` type (Step 3). The category list is
byte-identical in the prompt (Task 2 Step 6), `validCategories` (Step 3), and the
spec.
