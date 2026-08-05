package web

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
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
	// metaForContent is the path the user-skill DTO takes; same input, same out.
	if fromContent := metaForContent(metaFixture); !reflect.DeepEqual(fromContent, want) {
		t.Errorf("metaForContent() = %+v, want %+v", fromContent, want)
	}
}

// A skill with no category must render as "Other", not as an empty chip.
func TestSkillMetaDTODefaultsCategory(t *testing.T) {
	got := metaForContent("---\nname: bare\ndescription: x\n---\n\nbody\n")
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
// owner needs to open the file.
func TestSkillMetaDTOMalformedFrontmatter(t *testing.T) {
	if got := metaForContent("---\nname: [unclosed\n---\n\nthe body\n"); got.Category != "Other" {
		t.Errorf("Category = %q, want Other", got.Category)
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
		"none":         {skilllibrary.SkillMeta{}, []string{}},
	}
	for name, tc := range cases {
		if got := flattenRequires(tc.meta); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("[%s] flattenRequires = %v, want %v", name, got, tc.want)
		}
	}
}

// A core skill that declares no tooling must serialise requires as [], never
// null. The SPA's SkillView took `requires = []` as a default parameter, and a
// default parameter does not fire on null — so `null` here crashed the page
// with "Cannot read properties of null (reading 'length')".
//
// Asserted on the raw response bytes deliberately: decoding into []string
// erases the null-vs-[] distinction that IS the bug.
func TestCoreSkillRequiresIsNeverNull(t *testing.T) {
	s, _ := newAPITestServerWithSkills(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/skills/core/agent-collaboration", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET core skill = %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, `"requires":null`) {
		t.Errorf("requires serialised as null, want []: %s", body)
	}
}

// The list endpoint must carry metadata for core skills — the card and the
// detail header render from it.
func TestListSkillsCarriesCoreMetadata(t *testing.T) {
	s, _ := newAPITestServerWithSkills(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/skills", nil, cookies)
	if rec.Code != http.StatusOK {
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
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.CoreSkills) == 0 {
		t.Fatal("no core skills returned")
	}

	var pdfFound bool
	for _, cs := range body.CoreSkills {
		// Every core skill must resolve a category — an empty chip reads as a bug.
		if cs.Category == "" {
			t.Errorf("core skill %q has an empty category", cs.Slug)
		}
		if cs.Slug != "pdf" {
			continue
		}
		pdfFound = true
		if cs.Category != "File Processing" {
			t.Errorf("pdf category = %q, want File Processing", cs.Category)
		}
		if cs.Version == "" {
			t.Error("pdf version missing")
		}
		if len(cs.Requires) == 0 {
			t.Error("pdf requires missing (it declares anyBins)")
		}
	}
	if !pdfFound {
		t.Error(`core skill "pdf" missing from the list`)
	}
}

// A user skill goes through a different code path (content read from the store,
// not the embed) and must come out described the same way.
func TestUserSkillCarriesSameMetadata(t *testing.T) {
	s, _ := newAPITestServerWithSkills(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if _, err := s.skills.Create(wsID, "demo", "Does a demo thing.", metaFixture); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/skills", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /skills = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Skills []struct {
			ID       string   `json:"id"`
			Category string   `json:"category"`
			Version  string   `json:"version"`
			Requires []string `json:"requires"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Skills) != 1 {
		t.Fatalf("want 1 user skill, got %d", len(body.Skills))
	}
	got := body.Skills[0]
	if got.Category != "File Processing" || got.Version != "2.1.0" {
		t.Errorf("user skill metadata = %+v, want the same as the core path", got)
	}
	if !reflect.DeepEqual(got.Requires, []string{"pandoc", "pdftotext or mutool", "$DEMO_KEY"}) {
		t.Errorf("Requires = %v", got.Requires)
	}

	// …and the detail endpoint carries it too, so the viewer needs one request.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/skills/"+got.ID, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /skills/:id = %d: %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Category string   `json:"category"`
		Version  string   `json:"version"`
		Requires []string `json:"requires"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Category != "File Processing" || detail.Version != "2.1.0" || len(detail.Requires) != 3 {
		t.Errorf("detail metadata = %+v", detail)
	}
}
