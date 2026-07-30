package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fullIdentity() Identity {
	return Identity{
		WorkspaceName:  "Personal",
		WorkspaceAbout: "keeping my notes, research and daily journal in one place",
		DisplayName:    "Peer",
		Email:          "peer@example.com",
		Location:       "Skopje, North Macedonia",
		Notes:          "I run a small consultancy and travel often.",
		Tone:           "concise",
		Language:       "English",
	}
}

func TestRenderAboutFull(t *testing.T) {
	got := RenderAbout(fullIdentity())
	for _, want := range []string{
		"# About This Workspace",
		"## Workspace",
		"**Personal**",
		"keeping my notes, research and daily journal in one place",
		"## Who I am",
		"Name: Peer",
		"peer@example.com",
		"Skopje, North Macedonia",
		"## Background",
		"small consultancy",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderAbout missing %q in:\n%s", want, got)
		}
	}
}

// A rendered file must never be classified as empty, or the SeedIdentity guard
// would rewrite it on every boot.
func TestRenderedAboutIsNotEffectivelyEmpty(t *testing.T) {
	for name, id := range map[string]Identity{
		"full":       fullIdentity(),
		"about only": {WorkspaceAbout: "just this"},
		"name only":  {DisplayName: "Peer"},
	} {
		out := RenderAbout(id)
		if out == "" {
			t.Fatalf("[%s] expected a render, got empty", name)
		}
		if isEffectivelyEmpty(out) {
			t.Errorf("[%s] rendered ABOUT.md classified as empty:\n%s", name, out)
		}
	}
}

// An empty source renders nothing at all — never a heading-only file.
func TestRenderEmptyIdentity(t *testing.T) {
	if got := RenderAbout(Identity{}); got != "" {
		t.Errorf("RenderAbout(empty) = %q, want \"\"", got)
	}
	if got := RenderStyle(Identity{}); got != "" {
		t.Errorf("RenderStyle(empty) = %q, want \"\"", got)
	}
}

// A workspace NAME alone is not identity — it is already in the UI and would
// produce a file that says nothing.
func TestRenderAboutNameOnlyIsEmpty(t *testing.T) {
	if got := RenderAbout(Identity{WorkspaceName: "Personal"}); got != "" {
		t.Errorf("name-only identity should render nothing, got:\n%s", got)
	}
}

func TestRenderAboutOmitsEmptySections(t *testing.T) {
	got := RenderAbout(Identity{WorkspaceAbout: "research notes"})
	if strings.Contains(got, "## Who I am") {
		t.Errorf("empty person fields must omit the section:\n%s", got)
	}
	if strings.Contains(got, "## Background") {
		t.Errorf("empty notes must omit Background:\n%s", got)
	}
}

func TestRenderStyleExpandsTone(t *testing.T) {
	got := RenderStyle(Identity{Tone: "concise", Language: "English"})
	if !strings.Contains(got, "# Communication Style") {
		t.Errorf("missing heading:\n%s", got)
	}
	if !strings.Contains(got, "English") {
		t.Errorf("missing language:\n%s", got)
	}
	// The label alone is not an instruction — it must expand into guidance.
	if !strings.Contains(got, "brief") {
		t.Errorf("tone %q must expand into guidance, got:\n%s", "concise", got)
	}
}

// Every curated TONE_OPTIONS value has an expansion; an unknown value degrades
// to the raw label rather than vanishing.
func TestToneGuidanceCoversCuratedOptions(t *testing.T) {
	for _, tone := range []string{
		"direct", "friendly", "concise", "detailed",
		"formal", "casual", "encouraging", "neutral",
	} {
		if toneGuidance(tone) == "" {
			t.Errorf("no guidance for curated tone %q", tone)
		}
	}
	if g := toneGuidance("swashbuckling"); !strings.Contains(g, "swashbuckling") {
		t.Errorf("unknown tone must fall back to the raw value, got %q", g)
	}
}

// ── SeedIdentity ─────────────────────────────────────────────────────────

func TestSeedIdentityWritesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.SeedIdentity("ws1", fullIdentity()); err != nil {
		t.Fatalf("SeedIdentity: %v", err)
	}
	about := readMemFile(t, dir, "ws1", AboutFile)
	if !strings.Contains(about, "**Personal**") {
		t.Errorf("ABOUT.md not seeded:\n%s", about)
	}
	style := readMemFile(t, dir, "ws1", StyleFile)
	if !strings.Contains(style, "English") {
		t.Errorf("STYLE.md not seeded:\n%s", style)
	}
}

// The exact state every existing install is in: the old placeholder, or an H1
// whose comment was stripped by a pass through the KB editor.
func TestSeedIdentityOverwritesEffectivelyEmpty(t *testing.T) {
	for name, placeholder := range map[string]string{
		"scaffold placeholder": "# About Me\n\n<!-- Add your name, location, role, and background here -->\n",
		"comment stripped":     "# About Me",
		"blank":                "",
	} {
		dir := t.TempDir()
		s := New(dir)
		writeMemFile(t, dir, "ws1", AboutFile, placeholder)
		if err := s.SeedIdentity("ws1", fullIdentity()); err != nil {
			t.Fatalf("[%s] SeedIdentity: %v", name, err)
		}
		if got := readMemFile(t, dir, "ws1", AboutFile); !strings.Contains(got, "**Personal**") {
			t.Errorf("[%s] expected overwrite, got:\n%s", name, got)
		}
	}
}

func TestSeedIdentityNeverTouchesRealContent(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	mine := "# About Me\n\nI am a person with opinions about my own notes.\n"
	writeMemFile(t, dir, "ws1", AboutFile, mine)
	if err := s.SeedIdentity("ws1", fullIdentity()); err != nil {
		t.Fatalf("SeedIdentity: %v", err)
	}
	if got := readMemFile(t, dir, "ws1", AboutFile); got != mine {
		t.Errorf("user content was modified:\nwant %q\ngot  %q", mine, got)
	}
}

// Nothing to say → nothing written, not an empty file.
func TestSeedIdentityEmptySourceWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.SeedIdentity("ws1", Identity{}); err != nil {
		t.Fatalf("SeedIdentity: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ws1", "memory", AboutFile)); !os.IsNotExist(err) {
		t.Errorf("expected no ABOUT.md, stat err = %v", err)
	}
}

func TestSeedIdentityIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.SeedIdentity("ws1", fullIdentity()); err != nil {
		t.Fatalf("first: %v", err)
	}
	first := readMemFile(t, dir, "ws1", AboutFile)
	if err := s.SeedIdentity("ws1", Identity{WorkspaceAbout: "something else entirely"}); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := readMemFile(t, dir, "ws1", AboutFile); got != first {
		t.Errorf("second seed changed a non-empty file:\n%s", got)
	}
}

// The whole point: after seeding, the values reach the prompt.
func TestSeedIdentityReachesContextString(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.SeedIdentity("ws1", fullIdentity()); err != nil {
		t.Fatalf("SeedIdentity: %v", err)
	}
	ctx, err := s.ContextString("ws1")
	if err != nil {
		t.Fatalf("ContextString: %v", err)
	}
	for _, want := range []string{"daily journal", "Peer", "English", "brief"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("ContextString missing %q:\n%s", want, ctx)
		}
	}
}

// ── MigrateIdentityFiles ─────────────────────────────────────────────────

func TestMigrateRenamesBothFiles(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	writeMemFile(t, dir, "ws1", "USER.md", "# About Me\n\nMy own words.\n")
	writeMemFile(t, dir, "ws1", "SOUL.md", "# Communication Style\n\nBe blunt.\n")

	if err := s.MigrateIdentityFiles("ws1", fullIdentity()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := readMemFile(t, dir, "ws1", AboutFile); !strings.Contains(got, "My own words") {
		t.Errorf("ABOUT.md lost content:\n%s", got)
	}
	if got := readMemFile(t, dir, "ws1", StyleFile); !strings.Contains(got, "Be blunt") {
		t.Errorf("STYLE.md lost content:\n%s", got)
	}
	// A rename, not a copy: two identity files would both be injected.
	for _, gone := range []string{"USER.md", "SOUL.md"} {
		if _, err := os.Stat(filepath.Join(dir, "ws1", "memory", gone)); !os.IsNotExist(err) {
			t.Errorf("%s still present after migration (stat err = %v)", gone, err)
		}
	}
}

func TestMigrateBackfillsEmptyRenamedFile(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	writeMemFile(t, dir, "ws1", "USER.md", "# About Me")
	writeMemFile(t, dir, "ws1", "SOUL.md", "# Communication Style\n\n<!-- placeholder -->\n")

	if err := s.MigrateIdentityFiles("ws1", fullIdentity()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := readMemFile(t, dir, "ws1", AboutFile); !strings.Contains(got, "**Personal**") {
		t.Errorf("empty ABOUT.md not backfilled:\n%s", got)
	}
	if got := readMemFile(t, dir, "ws1", StyleFile); !strings.Contains(got, "English") {
		t.Errorf("empty STYLE.md not backfilled:\n%s", got)
	}
}

// Both names present means an ambiguous state we must not resolve by guessing.
func TestMigrateSkipsWhenTargetExists(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	writeMemFile(t, dir, "ws1", "USER.md", "# About Me\n\nlegacy words\n")
	writeMemFile(t, dir, "ws1", AboutFile, "# About This Workspace\n\nnew words\n")

	if err := s.MigrateIdentityFiles("ws1", fullIdentity()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := readMemFile(t, dir, "ws1", "USER.md"); !strings.Contains(got, "legacy words") {
		t.Errorf("USER.md was touched:\n%s", got)
	}
	if got := readMemFile(t, dir, "ws1", AboutFile); !strings.Contains(got, "new words") {
		t.Errorf("ABOUT.md was touched:\n%s", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	writeMemFile(t, dir, "ws1", "USER.md", "# About Me")

	if err := s.MigrateIdentityFiles("ws1", fullIdentity()); err != nil {
		t.Fatalf("first: %v", err)
	}
	first := readMemFile(t, dir, "ws1", AboutFile)
	if err := s.MigrateIdentityFiles("ws1", fullIdentity()); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := readMemFile(t, dir, "ws1", AboutFile); got != first {
		t.Errorf("second run changed the file:\nwant %q\ngot  %q", first, got)
	}
}

// A workspace with no memory dir at all (never opened the KB) must not error.
func TestMigrateNoMemoryDir(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.MigrateIdentityFiles("ws1", fullIdentity()); err != nil {
		t.Fatalf("Migrate on bare workspace: %v", err)
	}
	if got := readMemFile(t, dir, "ws1", AboutFile); !strings.Contains(got, "**Personal**") {
		t.Errorf("expected a seeded ABOUT.md:\n%s", got)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func writeMemFile(t *testing.T, base, ws, name, content string) {
	t.Helper()
	dir := filepath.Join(base, ws, "memory")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readMemFile(t *testing.T, base, ws, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(base, ws, "memory", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// The collision case with an EMPTY successor: the rename is skipped because
// ABOUT.md exists, and the backfill must be skipped too. Seeding it would put
// DB values in ABOUT.md while the owner's real content stayed in USER.md — and
// ContextString globs *.md, so BOTH would be injected.
func TestMigrateCollisionDoesNotSeedOverTheGap(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	writeMemFile(t, dir, "ws1", "USER.md", "# About Me\n\nmy real content\n")
	writeMemFile(t, dir, "ws1", AboutFile, "# About This Workspace") // effectively empty

	if err := s.MigrateIdentityFiles("ws1", fullIdentity()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if got := readMemFile(t, dir, "ws1", AboutFile); strings.Contains(got, "**Personal**") {
		t.Errorf("ABOUT.md was seeded while USER.md still holds the real content:\n%s", got)
	}
	if got := readMemFile(t, dir, "ws1", "USER.md"); !strings.Contains(got, "my real content") {
		t.Errorf("USER.md was touched:\n%s", got)
	}

	// The unblocked file is still backfilled — one collision must not stall the
	// other file.
	if got := readMemFile(t, dir, "ws1", StyleFile); !strings.Contains(got, "English") {
		t.Errorf("STYLE.md should still be seeded:\n%s", got)
	}
}
