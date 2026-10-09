package connectors

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func mustParas(t *testing.T, f *fakeDocs, want ...string) {
	t.Helper()
	if got := f.paragraphs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("paragraphs:\n got %q\nwant %q", got, want)
	}
}

func mustApplied(t *testing.T, out string, err error) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if json.Unmarshal([]byte(out), &m) != nil || m["status"] != "applied" || m["verified"] != true {
		t.Fatalf("not an applied+verified result: %s", out)
	}
	return m
}

func sampleDoc() *fakeDocs {
	return newFakeDocs(
		"TITLE Project plan",
		"# Goals",
		"Ship the beta by June.",
		"Keep costs under budget.",
		"# Risks",
		"- Hiring is slow",
		"- Vendor lock-in",
		"# Notes",
		"Last reviewed in May.",
	)
}

// The reported failure: a small model asked to add a line under a heading. With text
// anchors the model names the heading; the handler finds the place.
func TestInsertAfterParagraphByAnchor(t *testing.T) {
	f := sampleDoc()
	out, err := runDocs(t, f, "docs_insert_text", map[string]any{
		"anchor_text": "Ship the beta by June.", "text": "Launch marketing in July."})
	mustApplied(t, out, err)
	mustParas(t, f, "Project plan", "Goals", "Ship the beta by June.", "Launch marketing in July.",
		"Keep costs under budget.", "Risks", "Hiring is slow", "Vendor lock-in", "Notes", "Last reviewed in May.")
	if !strings.Contains(out, "Launch marketing in July.") {
		t.Fatalf("result must show the landed text in context: %s", out)
	}
}

// Text added after a HEADING must not become a heading itself — Docs gives the new
// paragraph the style of the one it was split from.
func TestInsertAfterHeadingIsBodyText(t *testing.T) {
	f := sampleDoc()
	_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Notes", "text": "A plain note."})
	if err != nil {
		t.Fatal(err)
	}
	st := f.paraStyles()
	if f.paragraphs()[8] != "A plain note." || st[8] != "NORMAL_TEXT" {
		t.Fatalf("inserted paragraph style = %s (%q)", st[8], f.paragraphs()[8])
	}
	for _, format := range []string{"plain"} {
		f := sampleDoc()
		_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Notes", "text": "Plain too.", "format": format})
		if err != nil {
			t.Fatal(err)
		}
		if f.paraStyles()[8] != "NORMAL_TEXT" {
			t.Fatalf("%s: inserted paragraph style = %s", format, f.paraStyles()[8])
		}
	}
}

func TestAmbiguousAnchorChangesNothing(t *testing.T) {
	f := newFakeDocs("Total: 5", "Other line", "Total: 5")
	_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Total: 5", "text": "x"})
	if err == nil || !strings.Contains(err.Error(), "appears 2 times") || !strings.Contains(err.Error(), "occurrence=2") {
		t.Fatalf("want an ambiguity error listing occurrences, got %v", err)
	}
	if f.batches != 0 {
		t.Fatal("an ambiguous anchor must not write anything")
	}
	_, err = runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Total: 5", "text": "after second", "occurrence": 2})
	if err != nil {
		t.Fatal(err)
	}
	mustParas(t, f, "Total: 5", "Other line", "Total: 5", "after second")
}

func TestMissingAnchorSuggestsClosest(t *testing.T) {
	f := sampleDoc()
	_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "ship beta in june", "text": "x"})
	// normalised match does not cover reordered words, so this is "not found"
	if err == nil || !strings.Contains(err.Error(), "NOTHING was changed") || !strings.Contains(err.Error(), "Ship the beta by June.") {
		t.Fatalf("want not-found with a suggestion, got %v", err)
	}
	if f.batches != 0 {
		t.Fatal("nothing must be written")
	}
}

// Models re-type quotes and spacing; the anchor still resolves to the real text.
func TestNormalisedAnchor(t *testing.T) {
	f := newFakeDocs("It’s  the “final” draft — really.")
	out, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": `it's the "final" draft - really.`, "text": "Next."})
	mustApplied(t, out, err)
	mustParas(t, f, "It’s  the “final” draft — really.", "Next.")
}

func TestMarkdownBecomesStructure(t *testing.T) {
	f := sampleDoc()
	_, err := runDocs(t, f, "docs_append_text", map[string]any{
		"text": "## Decisions\n- Use **Go** for the API\n- See [spec](https://example.com/spec)\nDone."})
	if err != nil {
		t.Fatal(err)
	}
	p := f.paragraphs()
	st := f.paraStyles()
	n := len(p)
	if !reflect.DeepEqual(p[n-4:], []string{"Decisions", "Use Go for the API", "See spec", "Done."}) {
		t.Fatalf("markdown text: %q", p[n-4:])
	}
	if !reflect.DeepEqual(st[n-4:], []string{"HEADING_2", "NORMAL_TEXT+bullet", "NORMAL_TEXT+bullet", "NORMAL_TEXT"}) {
		t.Fatalf("markdown styles: %q", st[n-4:])
	}
	// **Go** is bold, nothing else on that line is
	var bold []string
	for _, c := range f.chars {
		if c.bold {
			bold = append(bold, string(c.r))
		}
	}
	if strings.Join(bold, "") != "Go" {
		t.Fatalf("bold runs = %q", strings.Join(bold, ""))
	}
}

func TestAppendToEmptyDocumentHasNoBlankFirstLine(t *testing.T) {
	f := newFakeDocs("")
	out, err := runDocs(t, f, "docs_append_text", map[string]any{"text": "First line", "end_index": 999})
	mustApplied(t, out, err)
	mustParas(t, f, "First line")
}

func TestAddToSectionEndAndStart(t *testing.T) {
	f := sampleDoc()
	_, err := runDocs(t, f, "docs_add_to_section", map[string]any{"heading": "Goals", "text": "Hire two engineers."})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runDocs(t, f, "docs_add_to_section", map[string]any{"heading": "## risks", "text": "- Budget cuts", "position": "start"})
	if err != nil {
		t.Fatal(err)
	}
	mustParas(t, f, "Project plan", "Goals", "Ship the beta by June.", "Keep costs under budget.", "Hire two engineers.",
		"Risks", "Budget cuts", "Hiring is slow", "Vendor lock-in", "Notes", "Last reviewed in May.")
	if st := f.paraStyles(); st[6] != "NORMAL_TEXT+bullet" || st[4] != "NORMAL_TEXT" {
		t.Fatalf("styles: %q", st)
	}
}

func TestReplaceSectionMiddleAndEnd(t *testing.T) {
	f := sampleDoc()
	_, err := runDocs(t, f, "docs_replace_section", map[string]any{"heading": "Risks", "text": "- Only one risk left"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runDocs(t, f, "docs_replace_section", map[string]any{"heading": "Notes", "text": "Reviewed in October.\nApproved."})
	if err != nil {
		t.Fatal(err)
	}
	mustParas(t, f, "Project plan", "Goals", "Ship the beta by June.", "Keep costs under budget.",
		"Risks", "Only one risk left", "Notes", "Reviewed in October.", "Approved.")
	if st := f.paraStyles(); st[7] != "NORMAL_TEXT" || st[6] != "HEADING_1" {
		t.Fatalf("styles: %q", st)
	}
}

func TestReplaceTextTargetedAndZeroMatch(t *testing.T) {
	f := sampleDoc()
	out, err := runDocs(t, f, "docs_replace_text", map[string]any{"find": "June", "replace": "July"})
	mustApplied(t, out, err)
	if f.paragraphs()[2] != "Ship the beta by July." {
		t.Fatalf("got %q", f.paragraphs()[2])
	}
	_, err = runDocs(t, f, "docs_replace_text", map[string]any{"find": "December", "replace": "x"})
	if err == nil || !strings.Contains(err.Error(), "NOTHING was changed") {
		t.Fatalf("a replace that matches nothing must fail, got %v", err)
	}
}

func TestReplaceAllShiftsCorrectly(t *testing.T) {
	f := newFakeDocs("a cat and a cat", "one cat")
	_, err := runDocs(t, f, "docs_replace_text", map[string]any{"find": "cat", "replace": "tiger"})
	if err == nil || !strings.Contains(err.Error(), "appears 3 times") {
		t.Fatalf("several matches without replace_all must refuse: %v", err)
	}
	out, err := runDocs(t, f, "docs_replace_text", map[string]any{"find": "cat", "replace": "tiger", "replace_all": true})
	mustApplied(t, out, err)
	mustParas(t, f, "a tiger and a tiger", "one tiger")
}

func TestDeleteTextAndWholeParagraph(t *testing.T) {
	f := sampleDoc()
	_, err := runDocs(t, f, "docs_delete_text", map[string]any{"text": " by June"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runDocs(t, f, "docs_delete_text", map[string]any{"text": "Vendor lock-in", "whole_paragraph": true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runDocs(t, f, "docs_delete_text", map[string]any{"text": "Last reviewed", "whole_paragraph": true})
	if err != nil {
		t.Fatal(err)
	}
	mustParas(t, f, "Project plan", "Goals", "Ship the beta.", "Keep costs under budget.", "Risks", "Hiring is slow", "Notes")
}

func TestFormatTextComputesFields(t *testing.T) {
	f := sampleDoc()
	out, err := runDocs(t, f, "docs_format_text", map[string]any{"text": "costs", "bold": true})
	mustApplied(t, out, err)
	var bold string
	for _, c := range f.chars {
		if c.bold {
			bold += string(c.r)
		}
	}
	if bold != "costs" {
		t.Fatalf("bold = %q", bold)
	}
	_, err = runDocs(t, f, "docs_format_text", map[string]any{"text": "Last reviewed in May.", "style": "heading 2"})
	if err != nil {
		t.Fatal(err)
	}
	if st := f.paraStyles(); st[8] != "HEADING_2" {
		t.Fatalf("style = %s", st[8])
	}
}

// Emoji are two UTF-16 units. An offset computed in runes lands one unit early per
// emoji — the wrong-location bug in its purest form.
func TestEmojiOffsets(t *testing.T) {
	f := newFakeDocs("🎉🎉 Party plan", "Bring snacks")
	_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Party plan", "text": " (Friday)", "position": "after_text"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runDocs(t, f, "docs_replace_text", map[string]any{"find": "snacks", "replace": "drinks 🥤"})
	if err != nil {
		t.Fatal(err)
	}
	mustParas(t, f, "🎉🎉 Party plan (Friday)", "Bring drinks 🥤")
}

// A concurrent edit between our read and our write: the revision check refuses the
// stale write, and the handler re-plans from a fresh read instead of landing on
// shifted text.
func TestRevisionConflictReplans(t *testing.T) {
	f := newFakeDocs("Alpha", "Beta")
	f.beforeWrite = func(f *fakeDocs) {
		f.chars = append([]fchar{{r: 'Z'}, {r: '\n'}}, f.chars...)
		f.rev++
	}
	out, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Alpha", "text": "after alpha"})
	mustApplied(t, out, err)
	mustParas(t, f, "Z", "Alpha", "after alpha", "Beta")
	if f.batches != 2 {
		t.Fatalf("want one refused + one applied batch, got %d", f.batches)
	}
}

func TestPinnedRevisionRefusesWithoutRetry(t *testing.T) {
	f := newFakeDocs("Alpha")
	_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Alpha", "text": "x", "revision_id": "rev0"})
	if err == nil || !strings.Contains(err.Error(), "NOTHING was changed") {
		t.Fatalf("got %v", err)
	}
}

// The other half of "it said it edited but it didn't": the API answers 200 and the
// text is not there. Verification must turn that into an error, never a success.
func TestAcceptedButNotAppliedIsAnError(t *testing.T) {
	f := newFakeDocs("Alpha")
	f.dropNext = true
	_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Alpha", "text": "ghost"})
	if err == nil || !strings.Contains(err.Error(), "Do NOT report this as done") {
		t.Fatalf("want a verification failure, got %v", err)
	}
}

func TestLegacyIndexStillWorksAndVerifies(t *testing.T) {
	f := newFakeDocs("Hello world")
	out, err := runDocs(t, f, "docs_insert_text", map[string]any{"text": "big ", "index": float64(7)})
	mustApplied(t, out, err)
	mustParas(t, f, "Hello big world")
	_, err = runDocs(t, f, "docs_insert_text", map[string]any{"text": "x", "index": float64(500)})
	if err == nil {
		t.Fatal("an out-of-range index must fail")
	}
}

func TestGetDocumentIsCompactAndPaged(t *testing.T) {
	var paras []string
	paras = append(paras, "# Big doc")
	for i := 0; i < 200; i++ {
		paras = append(paras, strings.Repeat("Lorem ipsum dolor sit amet. ", 4)+"row "+string(rune('A'+i%26)))
	}
	f := newFakeDocs(paras...)
	out, err := runDocs(t, f, "docs_get_document", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 8*1024 {
		t.Fatalf("listing is %d bytes — over the tool-result cap", len(out))
	}
	var m struct {
		Next  int `json:"next_from_paragraph"`
		Paras []struct {
			N     int    `json:"n"`
			Style string `json:"style"`
		} `json:"paragraphs"`
	}
	json.Unmarshal([]byte(out), &m)
	if m.Next == 0 || m.Paras[0].Style != "HEADING_1" {
		t.Fatalf("want a paged listing starting with the heading: %s", out[:300])
	}
	out2, _ := runDocs(t, f, "docs_get_document", map[string]any{"from_paragraph": m.Next})
	if !strings.Contains(out2, `"n":`+itoa(m.Next)) {
		t.Fatalf("second page must start at %d", m.Next)
	}
	out3, _ := runDocs(t, f, "docs_get_document", map[string]any{"headings_only": true})
	if strings.Contains(out3, "Lorem") {
		t.Fatal("headings_only listed body text")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// A realistic documents.get body: section break, title, heading, a list, a table with
// cells, an inline image and an emoji — the shapes parseDoc must survive.
func TestParseRealisticFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/gdocs/realistic.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseDoc(raw)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, p := range d.Paras {
		texts = append(texts, p.display())
	}
	want := []string{"Quarterly report", "Summary", "Revenue grew 🚀 fast [object] see chart.", "First point", "Region", "Total", "EU", "1,200", ""}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("paragraphs:\n got %q\nwant %q", texts, want)
	}
	ms := d.findMatches("see chart")
	if len(ms) != 1 || ms[0].Start != 49 || ms[0].End != 58 {
		t.Fatalf("UTF-16 offsets after an emoji and an inline object: %+v", ms)
	}
	if !d.Paras[6].InTable || d.Paras[3].style() != "NORMAL_TEXT" || !d.Paras[3].List {
		t.Fatalf("table/list flags wrong: %+v %+v", d.Paras[6], d.Paras[3])
	}
	if d.BodyEnd != 100 {
		t.Fatalf("body end = %d", d.BodyEnd)
	}
}

// A section holding a table must be deleted from the TABLE's start, not its first
// cell's — Docs rejects a range that cuts a table in half.
func TestSectionRangeCoversWholeTables(t *testing.T) {
	raw, _ := os.ReadFile("testdata/gdocs/realistic.json")
	d, _ := parseDoc(raw)
	h, err := d.findHeading("Summary", 0)
	if err != nil {
		t.Fatal(err)
	}
	body := d.section(h)
	s, e := sectionRange(body)
	if s != 26 || e != 100 {
		t.Fatalf("range = [%d,%d), want [26,100)", s, e)
	}
	var cells []*gpara
	for _, p := range body {
		if p.InTable {
			cells = append(cells, p)
		}
	}
	if s2, e2 := sectionRange(cells); s2 != 72 || e2 != 99 {
		t.Fatalf("table-only range = [%d,%d), want the table's own [72,99)", s2, e2)
	}
}

func TestNormStyle(t *testing.T) {
	for in, want := range map[string]string{"h2": "HEADING_2", "Heading 3": "HEADING_3", "heading2": "HEADING_2",
		"normal": "NORMAL_TEXT", "TITLE": "TITLE", "banana": ""} {
		if got := normStyle(in); got != want {
			t.Errorf("normStyle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInlineInsertKeepsLeadingMarkersLiteral(t *testing.T) {
	f := newFakeDocs("Score:")
	_, err := runDocs(t, f, "docs_insert_text", map[string]any{"anchor_text": "Score:", "text": " - 3 **points**", "position": "after_text"})
	if err != nil {
		t.Fatal(err)
	}
	mustParas(t, f, "Score: - 3 points")
}
