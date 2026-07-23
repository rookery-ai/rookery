package vault

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func seedDesignerVault(t *testing.T) (*Vault, string) {
	t.Helper()
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	v.WriteNote(ws, "notes/health.md", []byte("# Health\n\n## Appointments\n\nOrthodontist visit booked for Tuesday.\n"))
	// Deliberately NOT under files/ (FilesDir) — that directory is reserved for
	// ImportFile's preserved originals and is excluded from designer retrieval
	// (see TestBuildKBContextExcludesFilesDir); this fixture instead proves the
	// separate, unrelated claim that non-markdown files in general are reachable.
	v.WriteNote(ws, "documents/expenses.csv", []byte("item,cost\nrent,900\n"))
	for i := 0; i < 80; i++ {
		v.WriteNote(ws, "notes/bulk/n"+string(rune('a'+i%26))+string(rune('a'+i/26))+".md", []byte("# Filler\n\nnothing relevant\n"))
	}
	return v, ws
}

func TestBuildKBContextRetrievesRelevantPassages(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "remind me about my dentist appointments")

	if !strings.Contains(got, "notes/health.md") {
		t.Errorf("expected the relevant note, got:\n%s", got)
	}
	if !strings.Contains(got, "Orthodontist") {
		t.Errorf("expected passage TEXT, not just a path, got:\n%s", got)
	}
}

func TestBuildKBContextFindsNonMarkdownByName(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "summarize my expenses spreadsheet each month")
	if !strings.Contains(got, "expenses.csv") {
		t.Errorf("a non-markdown file must be reachable, got:\n%s", got)
	}
}

func TestBuildKBContextIsBounded(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "filler")
	if len(got) > maxKBContextBytes {
		t.Errorf("context is %d bytes, over the %d cap", len(got), maxKBContextBytes)
	}
	// 80+ notes must NOT appear as 80 individual paths.
	if strings.Count(got, "notes/bulk/") > 8 {
		t.Errorf("the folder summary should replace an exhaustive path list, got:\n%s", got)
	}
}

func TestBuildKBContextStatesWhenNothingMatched(t *testing.T) {
	v, ws := seedDesignerVault(t)
	got := BuildKBContext(v, ws, "quantum chromodynamics lattice simulation")
	if !strings.Contains(strings.ToLower(got), "no existing notes matched") {
		t.Errorf("an empty retrieval must be stated explicitly so the designer asks instead of inventing a path, got:\n%s", got)
	}
}

func TestBuildKBContextEmptyVault(t *testing.T) {
	v := New(t.TempDir())
	v.EnsureScaffold("empty")
	if got := BuildKBContext(v, "empty", "anything"); got == "" {
		t.Error("an empty vault should still describe itself rather than returning nothing")
	}
}

// TestBuildKBContextDoesNotContradictItself is Finding 1(a): many single-file
// folders (the folder summary alone used to run past 5 KiB, itself under the
// old unsplit cap) plus one genuinely-matching note. The old code wrote the
// "relevant notes" header unconditionally before the budget check, so the
// header AND the "no notes matched" sentence both landed in the same output
// while the real match was quoted nowhere. With the folder summary and the
// passages each budgeted separately, the note has real room and must be
// shown — not silently dropped into a false "nothing matched" claim.
func TestBuildKBContextDoesNotContradictItself(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	for i := 0; i < 130; i++ {
		rel := fmt.Sprintf("notes/f%03d/note.md", i)
		if err := v.WriteNote(ws, rel, []byte("# Filler\n\nnothing relevant here\n")); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// A genuine match sized deliberately: too big to fit the ~1.5 KiB left
	// over once a 130-folder summary (~4.5 KB, unsplit) eats most of the 6
	// KiB cap, but comfortably under the ~4 KiB the passage section gets once
	// the summary has its own separate, smaller budget. This is what makes
	// the test actually exercise the budget SPLIT, not just the "don't write
	// a contradictory header unconditionally" fix on its own. The size lives
	// in the HEADING (uncapped, see chunk.go) rather than the body, so this
	// stays a single passage rather than being split into several
	// independently-fitting pieces by the chunker's hard per-chunk bound.
	headingText := strings.Repeat("Mortgage refinancing details for this quarter ", 36)
	if err := v.WriteNote(ws, "notes/finances.md", []byte(
		"# Household finances\n\n## "+headingText+"\n\nRefinancing the mortgage this quarter; rate quote attached.\n",
	)); err != nil {
		t.Fatalf("write finances note: %v", err)
	}

	got := BuildKBContext(v, ws, "mortgage refinancing")

	hasRelevant := strings.Contains(got, "Existing notes relevant to this request:")
	hasNoMatch := strings.Contains(got, "No existing notes matched this request")
	if hasRelevant && hasNoMatch {
		t.Fatalf("both contradictory statements present at once:\n%s", got)
	}
	if hasNoMatch {
		t.Errorf("a real match exists (notes/finances.md) — must not claim nothing matched:\n%s", got)
	}
	if !hasRelevant {
		t.Errorf("expected the relevant-notes header now that the summary no longer starves the passage budget, got:\n%s", got)
	}
	if !strings.Contains(got, "finances.md") || !strings.Contains(got, "Refinancing") {
		t.Errorf("expected the real match reported with its text, got:\n%s", got)
	}
}

// TestBuildKBContextMatchTooLargeReportsNoRoomNotNoMatch covers the other
// half of Finding 1(a): a single matching passage too large to ever fit the
// passage budget. This must say plainly that a match existed but didn't fit —
// never the "no notes matched" sentence, which is a different, false claim.
//
// A chunk's BODY text is hard-bounded at targetChunkChars (1500 chars, see
// chunk.go) — deliberately, so it always fits a byte-capped tool result — but
// the HEADING TRAIL quoted alongside it is not: deeply nested real headings
// ("H1 > H2 > H3 …", each with a real title) accumulate with no cap. This
// note stands that in for a single very long heading, which is enough on its
// own to push one entry past the whole remaining passage budget.
func TestBuildKBContextMatchTooLargeReportsNoRoomNotNoMatch(t *testing.T) {
	v, ws := seedDesignerVault(t)
	longHeading := strings.Repeat("xylophone92 refrigerator58 filler words padding out this heading title ", 160)
	note := "# " + longHeading + "\n\nShort body mentioning xylophone92 refrigerator58 once.\n"
	if err := v.WriteNote(ws, "notes/oddity.md", []byte(note)); err != nil {
		t.Fatalf("write note: %v", err)
	}

	got := BuildKBContext(v, ws, "xylophone92 refrigerator58")

	if strings.Contains(got, "No existing notes matched this request") {
		t.Errorf("a match existed — must not claim nothing matched:\n%s", got)
	}
	if strings.Contains(got, "Existing notes relevant to this request:") {
		t.Errorf("the oversized match cannot fit — must not claim it was shown:\n%s", got)
	}
	if !strings.Contains(got, "matched this request but did not fit") {
		t.Errorf("expected an explicit matched-but-no-room notice, got:\n%s", got)
	}
	if len(got) > maxKBContextBytes {
		t.Errorf("context is %d bytes, over the %d cap", len(got), maxKBContextBytes)
	}
}

// TestBuildKBContextFolderCountBlowoutStaysBounded is Finding 1(b): hundreds
// of one-file folders blow the folder summary itself past the whole cap
// before anything else is appended. The old hard tail-slice then cut mid-line
// and dropped the passages/no-match sentence entirely. With the summary
// separately budgeted, the total stays capped and the omission is visible
// (an "…and N more folders" marker) instead of silent.
func TestBuildKBContextFolderCountBlowoutStaysBounded(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	for i := 0; i < 400; i++ {
		rel := fmt.Sprintf("notes/day%04d/note.md", i)
		if err := v.WriteNote(ws, rel, []byte("# Journal\n\nnothing special today\n")); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	got := BuildKBContext(v, ws, "quantum chromodynamics lattice simulation")

	if len(got) > maxKBContextBytes {
		t.Fatalf("context is %d bytes, over the %d cap", len(got), maxKBContextBytes)
	}
	if !strings.Contains(got, "more folders") {
		t.Errorf("expected a visible truncation marker on the folder summary, got:\n%s", got)
	}
	if !strings.Contains(got, "No existing notes matched this request") {
		t.Errorf("the no-match sentence must survive truncation, not be sliced away, got:\n%s", got)
	}
}

// TestBuildKBContextExcludesChatsAndAgentState is Finding 2: chat transcripts
// and agent internal state must never be quoted verbatim into the designer's
// prompt just because a query happens to match a word in them — that is an
// always-on auto-injection, a materially different exposure mode than a model
// deliberately reading the file via a tool. notes/ matches must still work.
func TestBuildKBContextExcludesChatsAndAgentState(t *testing.T) {
	v, ws := seedDesignerVault(t)
	if err := v.WriteNote(ws, "chats/c1.md", []byte(
		"# Chat\n\nMy bank routing number is 123456789 — remember it privately.\n",
	)); err != nil {
		t.Fatalf("write chat: %v", err)
	}
	if err := v.WriteNote(ws, "agents/a1/state.md", []byte(
		"# State\n\n```json\n{\"internal_note\": \"do not surface this externally, routing sensitive\"}\n```\n",
	)); err != nil {
		t.Fatalf("write agent state: %v", err)
	}
	if err := v.WriteNote(ws, "notes/banking.md", []byte(
		"# Banking\n\nMy routing number reminder should live here.\n",
	)); err != nil {
		t.Fatalf("write banking note: %v", err)
	}

	got := BuildKBContext(v, ws, "routing number")

	if strings.Contains(got, "123456789") {
		t.Errorf("chat transcript content must not be quoted into the design block, got:\n%s", got)
	}
	if strings.Contains(got, "do not surface this externally") {
		t.Errorf("agent state content must not be quoted into the design block, got:\n%s", got)
	}
	if !strings.Contains(got, "notes/banking.md") {
		t.Errorf("a real notes/ match should still be reported, got:\n%s", got)
	}
}

// TestBuildKBContextExcludesFilesDir pins Fix 4: files/ (FilesDir) holds the
// preserved ORIGINAL of every imported document, which the index re-converts
// and chunks independently of the already-converted notes/ copy — so without
// this exclusion, one imported document competes with itself for the
// designer's limited passage budget. The notes/ copy must still be found.
func TestBuildKBContextExcludesFilesDir(t *testing.T) {
	v, ws := seedDesignerVault(t)
	if err := v.WriteNote(ws, "files/report.csv", []byte(
		"item,cost\nzzqinvoice77,4500\n",
	)); err != nil {
		t.Fatalf("write preserved original: %v", err)
	}
	if err := v.WriteNote(ws, "notes/report.md", []byte(
		"# Report\n\nzzqinvoice77 total is 4500 this quarter.\n",
	)); err != nil {
		t.Fatalf("write converted note: %v", err)
	}

	got := BuildKBContext(v, ws, "zzqinvoice77")

	if strings.Contains(got, "files/report.csv") {
		t.Errorf("files/ (the preserved original) must be excluded from designer retrieval, got:\n%s", got)
	}
	if !strings.Contains(got, "notes/report.md") {
		t.Errorf("the converted notes/ copy must still be found, got:\n%s", got)
	}
}

// TestBuildKBContextFindsOversizedFileByName is the actual user-facing
// surface of Fix 1: a file over maxIndexFileBytes must not just survive in
// the raw index (see index_test.go) — it must stop BuildKBContext from
// asserting "no existing notes matched" when the user genuinely has a note by
// that name. A name-only hit (fieldBoost matched the filename, but there is
// no body to quote as a passage) is a different fact than "nothing matched"
// and must be reported as such.
func TestBuildKBContextFindsOversizedFileByName(t *testing.T) {
	v, ws := seedDesignerVault(t)

	body := "quarterly-expenses-report unique-marker-zzqx\n" +
		strings.Repeat("filler filler filler filler filler filler filler filler\n", 90000)
	if len(body) <= maxIndexFileBytes {
		t.Fatalf("test fixture body (%d bytes) must exceed maxIndexFileBytes (%d)", len(body), maxIndexFileBytes)
	}
	if err := v.WriteNote(ws, "notes/quarterly-expenses-report.md", []byte(body)); err != nil {
		t.Fatalf("write oversized note: %v", err)
	}

	got := BuildKBContext(v, ws, "quarterly expenses report")

	if strings.Contains(got, "No existing notes matched this request") {
		t.Fatalf("a note the user has must never be reported as unmatched, got:\n%s", got)
	}
	if !strings.Contains(got, "quarterly-expenses-report.md") {
		t.Errorf("the oversized file should be named so the designer can reference it, got:\n%s", got)
	}
}

// TestRuneSafeCutNeverSplitsAMultibyteRune is Finding 3: a hard byte-slice
// truncation must never land inside a multibyte UTF-8 rune.
func TestRuneSafeCutNeverSplitsAMultibyteRune(t *testing.T) {
	// "é" is 2 bytes (0xC3 0xA9); sweeping every cut point exercises landing
	// on both its first and second byte.
	s := strings.Repeat("a", 9) + "é" + strings.Repeat("b", 9)
	for cut := 0; cut <= len(s); cut++ {
		got := runeSafeCut(s, cut)
		if !utf8.ValidString(s[:got]) {
			t.Fatalf("cut at %d (safe=%d) produced invalid utf8: %q", cut, got, s[:got])
		}
	}
}
