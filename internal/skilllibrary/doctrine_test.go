package skilllibrary

import (
	"strings"
	"testing"
)

// supersededLibraries are libraries a core skill must not send the model to
// install, because a native tool now does the same job better.
//
// This is the mechanised form of the rule removed_test.go states in prose: a
// skill and a tool competing for the same job is worse than either alone,
// because the small models this platform runs pick badly between them — and the
// skill, being the more specific instruction, usually wins with the weaker
// implementation.
//
// That rule was applied once, when playwright-browser was deleted for the native
// browser tools, and then not applied again: csv went on teaching pandas after
// kb_table_query shipped, and docx/pptx/xlsx/pdf went on teaching python-docx,
// markitdown and pdfplumber after `rookery kb convert` shipped. Five instances
// of a defect the project had already diagnosed once.
//
// Keyed on the INSTALL SPEC rather than on prose, deliberately. A skill may
// legitimately mention a library to say "do not use it" — the rewritten skills
// do exactly that, so the reader knows why the old advice went away. What it may
// not do is declare it as a dependency, because that is the instruction the
// agent's runtime acts on.
var supersededLibraries = map[string]string{
	"pandas":      "kb_table_query aggregates a table host-side; pandas is a large dependency whose wrong answer is a plausible number rather than an error",
	"python-docx": "`rookery kb convert` reads docx with image extraction and a lossy-conversion warning",
	"markitdown":  "`rookery kb convert` reads pptx with image extraction",
	"pdfplumber":  "`rookery kb convert` reads pdf with an OCR fallback and a thin-extraction warning",
	"pptxgenjs":   "pandoc builds a deck from markdown; positioning boxes by coordinate is harder to get right and harder for the user to edit",
}

// A core skill must not declare a dependency that a native tool supersedes.
func TestCoreSkillsDoNotInstallWhatAToolAlreadyDoes(t *testing.T) {
	for _, m := range LoadBundled() {
		// skill-creator is exempt for one entry only: it NAMES these libraries
		// in its guidance so a generated skill knows what not to reach for.
		// That is prose, not an install spec, so it is caught by the spec check
		// below like anything else — no carve-out is needed, and this comment
		// exists so nobody adds one.
		for _, spec := range m.Install {
			pkg := strings.ToLower(strings.TrimSpace(spec.Package))
			if pkg == "" {
				continue
			}
			if why, superseded := supersededLibraries[pkg]; superseded {
				t.Errorf("skill %q declares %q as a dependency, but %s",
					m.Name, pkg, why)
			}
		}
	}
}

// The document skills must point at the platform's converter, which is the
// whole substance of the rewrite. Without this the frontmatter could be cleaned
// up while the body still taught the library.
func TestDocumentSkillsPointAtTheConverter(t *testing.T) {
	for _, name := range []string{"csv", "docx", "pptx", "xlsx", "pdf"} {
		body, ok := CoreSkillContent(name)
		if !ok {
			t.Fatalf("core skill %q is not bundled", name)
		}
		if !strings.Contains(body, "rookery kb convert") {
			t.Errorf("skill %q does not mention `rookery kb convert`; reading a document "+
				"by hand is the defect this rewrite removed", name)
		}
	}
}

// The two skills whose job is arithmetic over a table must name the tool that
// does it, or the model falls back to writing the sum itself — which is the
// failure that produces a wrong number instead of an error.
func TestTableSkillsNameTheQueryTool(t *testing.T) {
	for _, name := range []string{"csv", "xlsx"} {
		body, ok := CoreSkillContent(name)
		if !ok {
			t.Fatalf("core skill %q is not bundled", name)
		}
		if !strings.Contains(body, "kb_table_query") {
			t.Errorf("skill %q does not name kb_table_query", name)
		}
		if !strings.Contains(body, "kb_file_map") {
			t.Errorf("skill %q does not tell the model to map a file before reading it", name)
		}
	}
}
