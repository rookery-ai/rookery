# Identity Source of Truth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `memory/ABOUT.md` and `memory/STYLE.md` the single authoritative, KB-editable home for workspace and owner identity — seeded from the setup wizard, backfilled on existing installs, and injected into every prompt — and give chat a real product identity.

**Architecture:** `internal/memory` gains a dependency-free `Identity` struct plus renderers and two writers (`SeedIdentity`, `MigrateIdentityFiles`); callers assemble values from `db` + `profile`. `internal/vault` stops writing memory placeholders and its README is rewritten. `profile.ContextString()` is deleted in favour of `profile.RuntimeContextString()`, which carries only the date/time/timezone that markdown cannot hold without going stale. `internal/prompts` gains a shared `productIdentityBlock` consumed by both the chat prompt and the agent platform block.

**Tech Stack:** Go 1.x (stdlib only — no new dependencies), React 19 + TypeScript + Vite + vitest for the SPA.

**Spec:** `docs/superpowers/specs/2026-07-30-identity-source-of-truth-design.md`

## Global Constraints

- **No new Go dependencies.** Everything here is stdlib.
- **The words `Obsidian`, `vault`, and `self-hosted` must not appear in any prompt text or user-visible UI copy.** The Go package `internal/vault`, its types, and filesystem paths keep their names — that rename is explicitly out of scope.
- **Every migration step logs and continues on failure.** A failed migration must never abort `serve`.
- **Conventional Commits** for every commit: `type(scope): summary`.
- **Run `go test ./... -count=1` before each commit** in Go tasks; `cd web/ui && npx vitest run` for SPA tasks.
- Tests that assert on prompt text must assert on **absence** of the banned words as well as presence of the new ones.

---

### Task 1: `memory.Identity` and the two renderers

**Files:**
- Create: `internal/memory/identity.go`
- Create: `internal/memory/identity_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Identity struct { WorkspaceName, WorkspaceAbout, DisplayName, Email, Location, Notes, Tone, Language string }`
  - `func RenderAbout(id Identity) string`
  - `func RenderStyle(id Identity) string`
  - `const AboutFile = "ABOUT.md"`, `const StyleFile = "STYLE.md"`

- [ ] **Step 1: Write the failing test**

Create `internal/memory/identity_test.go`:

```go
package memory

import "strings"
import "testing"

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run 'TestRender|TestTone' -v`
Expected: FAIL — `undefined: Identity`, `undefined: RenderAbout`.

- [ ] **Step 3: Write the implementation**

Create `internal/memory/identity.go`:

```go
package memory

import (
	"fmt"
	"strings"
)

// Identity is the full set of values the identity memory files are rendered
// from. Every field is optional; an empty field omits its line or section.
//
// It is a plain struct rather than a reference to db.Workspace or
// profile.Profile so this package keeps importing neither: the caller reads
// from whichever store owns each value and hands the result over.
type Identity struct {
	WorkspaceName  string
	WorkspaceAbout string
	DisplayName    string
	Email          string
	Location       string
	Notes          string
	Tone           string
	Language       string
}

// The two identity files. Named constants because three packages reference
// them (renderer, migration, README template test).
const (
	AboutFile = "ABOUT.md"
	StyleFile = "STYLE.md"

	// The files these replaced. Kept here, next to their successors, so the
	// rename cannot drift apart from the names it maps to.
	legacyAboutFile = "USER.md"
	legacyStyleFile = "SOUL.md"
)

// toneGuidance expands a curated tone value into an instruction. The picker's
// label ("Concise — brief but complete") is a UI affordance; a model needs the
// behaviour spelled out. The keys match TONE_OPTIONS in
// web/ui/src/components/profile/options.ts.
//
// An unrecognised value (a tone typed by hand, or a future option) falls
// through to the raw string rather than being dropped: a user's own words are
// better guidance than silence.
func toneGuidance(tone string) string {
	switch strings.ToLower(strings.TrimSpace(tone)) {
	case "":
		return ""
	case "direct":
		return "**direct** — lead with the answer, no filler, no preamble"
	case "friendly":
		return "**friendly** — warm and conversational, but still get to the point"
	case "concise":
		return "**concise** — brief but complete; prefer bullets over paragraphs"
	case "detailed":
		return "**detailed** — explain the reasoning and cover the edge cases"
	case "formal":
		return "**formal** — professional register, no slang"
	case "casual":
		return "**casual** — relaxed and informal"
	case "encouraging":
		return "**encouraging** — supportive and positive in tone"
	case "neutral":
		return "**neutral** — plain and matter-of-fact, no editorialising"
	default:
		return "**" + tone + "**"
	}
}

// RenderAbout renders memory/ABOUT.md. It returns "" when there is nothing
// worth saying — a heading-only file would be classified as content by
// isEffectivelyEmpty and would then block the backfill forever.
//
// A workspace NAME alone does not count as content: it is already visible
// everywhere in the UI, and a file whose only claim is "this workspace is
// called X" costs prompt budget for nothing.
func RenderAbout(id Identity) string {
	hasPerson := id.DisplayName != "" || id.Email != "" || id.Location != ""
	if id.WorkspaceAbout == "" && !hasPerson && id.Notes == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# About This Workspace\n")

	if id.WorkspaceAbout != "" || id.WorkspaceName != "" {
		sb.WriteString("\n## Workspace\n")
		switch {
		case id.WorkspaceName != "" && id.WorkspaceAbout != "":
			fmt.Fprintf(&sb, "This workspace is called **%s**. It is for: %s\n",
				id.WorkspaceName, id.WorkspaceAbout)
		case id.WorkspaceName != "":
			fmt.Fprintf(&sb, "This workspace is called **%s**.\n", id.WorkspaceName)
		default:
			fmt.Fprintf(&sb, "This workspace is for: %s\n", id.WorkspaceAbout)
		}
	}

	if hasPerson {
		sb.WriteString("\n## Who I am\n")
		if id.DisplayName != "" {
			fmt.Fprintf(&sb, "- Name: %s\n", id.DisplayName)
		}
		if id.Email != "" {
			fmt.Fprintf(&sb, "- Email: %s\n", id.Email)
		}
		if id.Location != "" {
			fmt.Fprintf(&sb, "- Based in: %s\n", id.Location)
		}
	}

	if id.Notes != "" {
		sb.WriteString("\n## Background\n")
		sb.WriteString(strings.TrimSpace(id.Notes))
		sb.WriteString("\n")
	}

	return sb.String()
}

// RenderStyle renders memory/STYLE.md. Returns "" when neither tone nor
// language is set.
func RenderStyle(id Identity) string {
	guidance := toneGuidance(id.Tone)
	if guidance == "" && id.Language == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Communication Style\n\n")
	if id.Language != "" {
		fmt.Fprintf(&sb, "- Reply in **%s**.\n", id.Language)
	}
	if guidance != "" {
		fmt.Fprintf(&sb, "- Tone: %s.\n", guidance)
	}
	return sb.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ -run 'TestRender|TestTone' -v`
Expected: PASS (all seven tests).

- [ ] **Step 5: Commit**

```bash
git add internal/memory/identity.go internal/memory/identity_test.go
git commit -m "feat(memory): render ABOUT.md and STYLE.md from setup values"
```

---

### Task 2: `SeedIdentity` — write only when there is nothing to lose

**Files:**
- Modify: `internal/memory/identity.go`
- Modify: `internal/memory/identity_test.go`

**Interfaces:**
- Consumes: `Identity`, `RenderAbout`, `RenderStyle`, `AboutFile`, `StyleFile` (Task 1); `Store.memDir` and `writeFileAtomic` (existing, `internal/memory/memory.go`); `isEffectivelyEmpty` (existing, `internal/memory/memory.go:316`).
- Produces: `func (s *Store) SeedIdentity(workspaceID string, id Identity) error`

- [ ] **Step 1: Write the failing test**

Append to `internal/memory/identity_test.go`:

```go
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
```

Add `"os"` and `"path/filepath"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run TestSeedIdentity -v`
Expected: FAIL — `s.SeedIdentity undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/memory/identity.go` (add `"os"` and `"path/filepath"` to imports):

```go
// SeedIdentity writes ABOUT.md and STYLE.md from id.
//
// Each file is written only when it is absent or "effectively empty" — the
// predicate ContextString already uses to decide whether a file is worth
// injecting. Reusing that one function for both "is this worth injecting" and
// "is this safe to overwrite" is deliberate: two predicates could disagree, and
// the disagreement would either strand an install with an empty identity or
// overwrite a file someone had written in.
//
// An empty render writes nothing at all, so a workspace that skipped both the
// basics text and the profile step is left with no file rather than a
// heading-only one.
func (s *Store) SeedIdentity(workspaceID string, id Identity) error {
	dir := s.memDir(workspaceID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("memory dir: %w", err)
	}
	for _, f := range []struct {
		name    string
		content string
	}{
		{AboutFile, RenderAbout(id)},
		{StyleFile, RenderStyle(id)},
	} {
		if f.content == "" {
			continue
		}
		path := filepath.Join(dir, f.name)
		existing, err := os.ReadFile(path)
		if err == nil && !isEffectivelyEmpty(stripFrontmatter(string(existing))) {
			continue // the user has written here; never touch it
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", f.name, err)
		}
		if err := writeFileAtomic(path, []byte(f.content), 0o640); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ -count=1 -v`
Expected: PASS, including the pre-existing memory tests.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/identity.go internal/memory/identity_test.go
git commit -m "feat(memory): seed identity files only when absent or empty"
```

---

### Task 3: `MigrateIdentityFiles` — rename, then backfill

**Files:**
- Modify: `internal/memory/identity.go`
- Modify: `internal/memory/identity_test.go`

**Interfaces:**
- Consumes: `SeedIdentity` (Task 2), `legacyAboutFile`/`legacyStyleFile` (Task 1).
- Produces: `func (s *Store) MigrateIdentityFiles(workspaceID string, id Identity) error`

- [ ] **Step 1: Write the failing test**

Append to `internal/memory/identity_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/ -run TestMigrate -v`
Expected: FAIL — `s.MigrateIdentityFiles undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/memory/identity.go` (add `"log/slog"` to imports):

```go
// MigrateIdentityFiles brings a workspace's memory/ up to the current identity
// layout: USER.md → ABOUT.md, SOUL.md → STYLE.md, then a backfill of whatever
// is still empty.
//
// The rename is a MOVE, not a copy. ContextString globs memory/*.md and
// sections by filename, so a leftover USER.md would be injected alongside
// ABOUT.md and the install would carry two identity documents — the exact
// duplication this design exists to remove.
//
// When BOTH names already exist, neither is touched and the collision is
// logged. That state means someone (or an interrupted earlier run) produced
// two files, and there is no safe way to pick a winner automatically.
//
// Idempotent: a workspace already on the new layout with non-empty files is a
// no-op on every subsequent call.
func (s *Store) MigrateIdentityFiles(workspaceID string, id Identity) error {
	dir := s.memDir(workspaceID)

	for _, r := range []struct{ from, to string }{
		{legacyAboutFile, AboutFile},
		{legacyStyleFile, StyleFile},
	} {
		src := filepath.Join(dir, r.from)
		if _, err := os.Stat(src); err != nil {
			continue // nothing to rename
		}
		dst := filepath.Join(dir, r.to)
		if _, err := os.Stat(dst); err == nil {
			slog.Warn("memory: identity rename skipped, both names present",
				"workspace", workspaceID, "legacy", r.from, "current", r.to)
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			slog.Warn("memory: identity rename failed",
				"workspace", workspaceID, "from", r.from, "to", r.to, "err", err)
		}
	}

	return s.SeedIdentity(workspaceID, id)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/identity.go internal/memory/identity_test.go
git commit -m "feat(memory): migrate USER.md/SOUL.md to ABOUT.md/STYLE.md"
```

---

### Task 4: Vault stops writing placeholders; README rewritten

**Files:**
- Modify: `internal/vault/vault.go:203-220` (delete the two placeholder writes)
- Modify: `internal/vault/readme_template.go` (new template + append the outgoing one to `legacyREADMEs`)
- Modify: `internal/vault/readme_template_test.go`
- Modify: `internal/vault/vault_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: an `EnsureScaffold` that creates directories and `README.md` only.

- [ ] **Step 1: Write the failing tests**

Append to `internal/vault/readme_template_test.go`:

```go
// TestCurrentTemplateIsInLegacyList is the test that stops the NEXT README
// revision from stranding every existing install.
//
// EnsureScaffold upgrades a README only when it byte-matches an entry in
// legacyREADMEs. Shipping a new template without adding the OUTGOING one to
// that list means installs that already have the outgoing text keep it
// forever — which is precisely the failure the mechanism exists to prevent.
// Asserting the CURRENT template is present forces the author of the next
// revision to move it into the list, because this test fails the moment they
// change readmeTemplate without doing so.
func TestCurrentTemplateIsInLegacyList(t *testing.T) {
	if !isPristineREADME([]byte(readmeTemplate)) {
		t.Fatal("readmeTemplate is not in legacyREADMEs — add it, or every " +
			"existing install keeps the previous README forever")
	}
}

func TestReadmeDescribesFilesThatExist(t *testing.T) {
	for _, stale := range []string{"USER.md", "SOUL.md", "Obsidian", "vault"} {
		if strings.Contains(readmeTemplate, stale) {
			t.Errorf("readmeTemplate still mentions %q", stale)
		}
	}
	for _, want := range []string{"ABOUT.md", "STYLE.md", "knowledge base"} {
		if !strings.Contains(readmeTemplate, want) {
			t.Errorf("readmeTemplate missing %q", want)
		}
	}
	// GENERAL.md may be mentioned only as something that appears on demand.
	if strings.Contains(readmeTemplate, "GENERAL.md") &&
		!strings.Contains(readmeTemplate, "/memory") {
		t.Error("if GENERAL.md is named, the README must say it appears when you use /memory")
	}
}
```

Append to `internal/vault/vault_test.go`:

```go
// EnsureScaffold must not write memory files. The identity writer
// (memory.SeedIdentity) owns ABOUT.md and STYLE.md exclusively; a placeholder
// written here would be created by a lazy KB visit and then rewritten by the
// next boot's backfill, giving one file two writers.
func TestEnsureScaffoldWritesNoMemoryFiles(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	if err := v.EnsureScaffold("ws1"); err != nil {
		t.Fatalf("EnsureScaffold: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "ws1", "memory"))
	if err != nil {
		t.Fatalf("memory dir should exist: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("EnsureScaffold wrote memory files: %v", names)
	}
}
```

Note: `New(dir)` is the existing `vault.New` constructor — check its exact signature in `internal/vault/vault.go` and match the other tests in `vault_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/vault/ -run 'TestCurrentTemplateIsInLegacyList|TestReadmeDescribes|TestEnsureScaffoldWritesNoMemoryFiles' -v`
Expected: all three FAIL.

- [ ] **Step 3: Implement — delete the placeholder writes**

In `internal/vault/vault.go`, delete the whole block from the comment
`// Scaffold structured memory files if they don't exist yet.` through the
closing brace of the `soulMD` write (lines 203–219), leaving the function
ending at `return nil`. The `memory` subdirectory is still created by the loop
at line 181, which is what `SeedIdentity` needs.

- [ ] **Step 4: Implement — new README template**

In `internal/vault/readme_template.go`, move the existing `readmeTemplate`
string into `legacyREADMEs` as a new **first** entry (copy it verbatim as a Go
string literal — it currently uses backtick-quoted segments joined with
`+ "`...`" +`, so convert to a double-quoted string with `\n` escapes and
literal backticks, matching how the other entries are written). Then replace
`readmeTemplate` with:

```go
const readmeTemplate = `# Knowledge Base

This is your knowledge base — one folder of linked markdown notes, shared by
you, your agents, and chat. Anything worth remembering between sessions lives
here.

## Folders

- **memory/** — who you are and how you like to be talked to. Every ` + "`.md`" + `
  file here is injected into the context of every chat, agent run, and design
  session, so this is the fastest way to change how the assistant behaves.
  ` + "`ABOUT.md`" + ` holds what this workspace is for and who you are;
  ` + "`STYLE.md`" + ` holds tone and language. Both are filled in from what you
  entered during setup, and editing them here is how you change them — they are
  the source of truth, not a copy of a setting somewhere else. Add your own
  files here freely; they are all picked up. (A ` + "`GENERAL.md`" + ` appears
  once you use the ` + "`/memory`" + ` command from a chat app.)
- **notes/** — yours. Notes, journals, plans, todos, research, anything. The
  app does not write here unless you or an agent chooses to.
- **agents/** — one folder per agent, holding its instructions
  (` + "`AGENT.md`" + `), its memory between runs (` + "`state.md`" + `), any scripts it
  wrote, and its run logs. Managed by the app: read it freely, but let each
  agent own its own folder.
- **chats/** — a markdown transcript of each conversation, written
  automatically so past chats stay searchable. Managed by the app.
- **skills/** — the skills you have created or imported. Each is a folder with
  a ` + "`SKILL.md`" + ` and optionally scripts the skill runs.

Built-in skills are not shown here — they ship inside the app and are always
available to every agent. Reminders are not here either: they live in the app,
not as notes.

## What you can do here

- **Write and edit notes** — open any ` + "`.md`" + ` file to edit it, formatted or as
  raw markdown. Other file types open read-only.
- **Organise** — create folders, move, rename, and drag notes into the order
  you want. Your arrangement is remembered.
- **Link notes** — write ` + "`[[note name]]`" + ` to link one note to another. A
  note shows what links back to it.
- **Search** — full-text search across everything here.
- **Add files** — upload a PDF, Word, Excel, PowerPoint, CSV, or web page and
  it is converted to markdown so it becomes searchable and usable by agents.
  Conversion notes anything it could not extract cleanly.

## How agents use it

Agents read the whole knowledge base and can write to it — that is how what
they learn survives from one run to the next. If an agent should remember
something, telling it to write that down here is usually the answer.
`
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/vault/ -count=1`
Expected: PASS. If `isPristineREADME` tests for the historical templates fail,
the conversion of the outgoing template to a quoted literal is wrong — compare
byte-for-byte with `git show HEAD:internal/vault/readme_template.go`.

- [ ] **Step 6: Commit**

```bash
git add internal/vault/
git commit -m "feat(vault): rewrite the home note and stop scaffolding memory placeholders"
```

---

### Task 5: `profile.RuntimeContextString` replaces `ContextString`

**Files:**
- Modify: `internal/profile/profile.go:107-128`
- Modify: `internal/profile/profile_test.go`
- Modify: `internal/chat/chat.go:114-121`
- Modify: `internal/agentdesigner/flow.go:2613-2620`
- Modify: `internal/skilldesigner/flow.go:965-971`
- Modify: `internal/agentrunner/runner.go` (around line 296)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func RuntimeContextString(g Getter, workspaceID string, now time.Time) string`. `Profile.ContextString()` is **removed** — the compiler finds every caller.

- [ ] **Step 1: Write the failing test**

Add to `internal/profile/profile_test.go`:

```go
func TestRuntimeContextString(t *testing.T) {
	g := fakeGetter{"profile_timezone": "Europe/Skopje"}
	now := time.Date(2026, 7, 30, 12, 32, 0, 0, time.UTC)
	got := RuntimeContextString(g, "ws1", now)

	for _, want := range []string{"[Current context]", "Europe/Skopje", "2026", "14:32"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Identity must NOT be here — that lives in memory/ now.
	for _, banned := range []string{"[User profile]", "Preferred tone", "Email"} {
		if strings.Contains(got, banned) {
			t.Errorf("runtime context must not carry identity (%q):\n%s", banned, got)
		}
	}
}

// profile.Timezone is free text: "", "CEST" and "UTC+2" all fail
// time.LoadLocation. None may panic or blank the block.
func TestRuntimeContextStringBadTimezones(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	for _, tz := range []string{"", "CEST", "UTC+2", "Mars/Olympus"} {
		got := RuntimeContextString(fakeGetter{"profile_timezone": tz}, "ws1", now)
		if !strings.Contains(got, "[Current context]") {
			t.Errorf("tz %q produced no block:\n%s", tz, got)
		}
		if !strings.Contains(got, "UTC") {
			t.Errorf("tz %q should fall back to UTC:\n%s", tz, got)
		}
	}
}
```

`fakeGetter` — if `profile_test.go` has no such helper, add:

```go
type fakeGetter map[string]string

func (f fakeGetter) GetSetting(_, key string) (string, error) {
	v, ok := f[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/profile/ -run TestRuntimeContextString -v`
Expected: FAIL — `undefined: RuntimeContextString`.

- [ ] **Step 3: Implement in `internal/profile/profile.go`**

Delete the `ContextString` method entirely and add:

```go
// RuntimeContextString renders the "[Current context]" block injected into
// every LLM prompt.
//
// It deliberately carries ONLY what markdown cannot hold without going stale.
// Identity — who the user is, what the workspace is for, how they want to be
// spoken to — lives in memory/ABOUT.md and memory/STYLE.md, which the user
// edits directly and which memory.ContextString injects. The timezone stays
// here because it is editable in Settings and is read programmatically by
// LoadLocation for reminder parsing; a copy rendered into markdown at setup
// would silently diverge the moment the user changed it.
//
// The current date and time are here because nothing else supplied them: before
// this, the only time.Now() in prompt construction was the reminder parser, so
// chat and agent runs could not say what day it was.
//
// now is a parameter rather than a call to time.Now() so tests are not
// clock-dependent. Never returns an error: an unparseable timezone degrades to
// UTC exactly as LoadLocation does.
func RuntimeContextString(g Getter, workspaceID string, now time.Time) string {
	loc := LoadLocation(g, workspaceID)
	local := now.In(loc)
	tz := Load(g, workspaceID).Timezone
	if _, err := time.LoadLocation(tz); tz == "" || err != nil {
		tz = "UTC"
	}
	return "[Current context]\n" +
		"- Current date and time: " + local.Format("Monday, 2 January 2006, 15:04") + " (" + tz + ")\n" +
		"- Timezone: " + tz + "\n"
}
```

- [ ] **Step 4: Update the four call sites**

`internal/chat/chat.go` — replace the profile block at the top of
`BuildUserContext`:

```go
	sb.WriteString(profile.RuntimeContextString(database, workspaceID, time.Now()))
```

(Import `"time"`. The old `if p := …; p != ""` guard is gone —
`RuntimeContextString` always returns a block, which is correct: the date is
always worth stating.)

`internal/agentdesigner/flow.go` — rewrite `loadUserProfile`:

```go
// loadRuntimeContext returns the "[Current context]" block (date, time,
// timezone) for workspaceID, or "" if no db is attached. Identity now comes
// from memory/ (loadUserMemory).
func (f *Flow) loadRuntimeContext(workspaceID string) string {
	if f.db == nil {
		return ""
	}
	return profile.RuntimeContextString(f.db, workspaceID, time.Now())
}
```

Rename every `loadUserProfile` call in that file to `loadRuntimeContext`
(`grep -n loadUserProfile internal/agentdesigner/flow.go`).

`internal/skilldesigner/flow.go` — the same rename and rewrite.

`internal/agentrunner/runner.go` — around line 296, after the `userMemory`
block, add the runtime context and pass it into the prompt alongside memory.
Find where `userMemory` is consumed (`grep -n userMemory internal/agentrunner/runner.go`)
and prepend the runtime block to the same prompt field:

```go
	runtimeCtx := profile.RuntimeContextString(r.db, input.WorkspaceID, time.Now())
```

Then set the prompt param to `runtimeCtx + "\n" + userMemory` at the point
`userMemory` is currently passed. Import `internal/profile`.

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go test ./... -count=1 -timeout 300s`
Expected: PASS. Any remaining compile error is a `ContextString` caller the
grep missed — fix it the same way.

- [ ] **Step 6: Commit**

```bash
git add internal/profile/ internal/chat/ internal/agentdesigner/ internal/skilldesigner/ internal/agentrunner/
git commit -m "feat(profile): replace the identity block with runtime date/time context"
```

---

### Task 6: `productIdentityBlock` and the chat prompt rewrite

**Files:**
- Modify: `internal/prompts/prompts.go` (new block; `platformContextBlock` at 259-340; `BuildChatSystemPrompt` at 2027-2126)
- Modify: `internal/prompts/prompts_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Surface string` with `SurfaceChat Surface = "chat"` and `SurfaceAgent Surface = "agent"`
  - `func productIdentityBlock(surface Surface) string` (unexported; both consumers are in-package)

- [ ] **Step 1: Write the failing test**

Add to `internal/prompts/prompts_test.go`:

```go
// TestNoStaleOrForeignTermsInPrompts locks the wording contract. Each banned
// string is a real defect that shipped:
//   - "Obsidian"    — describes the product as a copy of an unrelated one, and
//                     is the term the model echoed back to the user.
//   - "vault"       — an internal Go package name; the user only ever sees
//                     "knowledge base".
//   - "self-hosted" — irrelevant to the user and to the model's behaviour.
//   - USER.md/SOUL.md — renamed; naming them points the model at files that
//                     no longer exist.
//   - "reminders/"  — a folder the CLI chat prompt advertised that has never
//                     existed; reminders are DB-only.
func TestNoStaleOrForeignTermsInPrompts(t *testing.T) {
	banned := []string{"Obsidian", "obsidian", "self-hosted", "USER.md", "SOUL.md", "reminders/"}
	subjects := map[string]string{
		"chat/tool-calling": BuildChatSystemPrompt("/kb", BackendToolCalling, nil, nil, ""),
		"chat/cli":          BuildChatSystemPrompt("/kb", BackendFullCoder, nil, nil, ""),
		"platform_context":  platformContextBlock(nil, "/kb"),
	}
	for name, text := range subjects {
		for _, b := range banned {
			if strings.Contains(text, b) {
				t.Errorf("[%s] contains banned term %q", name, b)
			}
		}
	}
	// "vault" is banned as a standalone word; the paths themselves are fine.
	for name, text := range subjects {
		if strings.Contains(strings.ToLower(text), "vault root") ||
			strings.Contains(strings.ToLower(text), "your vault") ||
			strings.Contains(strings.ToLower(text), "the vault") {
			t.Errorf("[%s] still calls the knowledge base a vault", name)
		}
	}
}

func TestChatPromptStatesIdentityAndLimits(t *testing.T) {
	p := BuildChatSystemPrompt("/home/u/.rookery/vaults/abc", BackendToolCalling, nil, nil, "")
	for _, want := range []string{
		"Rookery",         // it must know what it is
		"knowledge base",  // the term the user sees
		"cannot",          // it must state a limit
		"agents",          // it must know the platform has agents…
		"skills",          // …and skills…
		"reminders",       // …and reminders, even though chat cannot make them
	} {
		if !strings.Contains(p, want) {
			t.Errorf("chat prompt missing %q", want)
		}
	}
	// It must be told not to recite the absolute path at the user — the
	// observed failure was the model quoting /home/u/.rookery/vaults/... back.
	if !strings.Contains(p, "absolute") {
		t.Errorf("chat prompt must forbid quoting the absolute path:\n%s", p)
	}
}

// One product description, two consumers. A future edit to one must not be
// able to drift from the other.
func TestProductIdentitySharedByBothConsumers(t *testing.T) {
	chat := BuildChatSystemPrompt("/kb", BackendToolCalling, nil, nil, "")
	agent := platformContextBlock(nil, "/kb")
	marker := "Rookery"
	if !strings.Contains(chat, marker) || !strings.Contains(agent, marker) {
		t.Fatalf("both prompts must carry the shared product identity")
	}
}

func TestPlatformContextNamesCurrentMemoryFiles(t *testing.T) {
	out := platformContextBlock(nil, "/kb")
	for _, want := range []string{"ABOUT.md", "STYLE.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("platform context missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompts/ -run 'TestNoStaleOrForeign|TestChatPromptStates|TestProductIdentity|TestPlatformContextNames' -v`
Expected: FAIL on every one.

- [ ] **Step 3: Implement `productIdentityBlock`**

Add near `platformContextBlock` in `internal/prompts/prompts.go`:

```go
// Surface names which part of the platform a prompt is being built for. The
// product is the same; what the surface can DO is not.
type Surface string

const (
	SurfaceChat  Surface = "chat"
	SurfaceAgent Surface = "agent"
)

// productIdentityBlock describes the platform and the current surface's real
// capabilities.
//
// It exists because chat had no product identity at all: asked what platform it
// was, the model inferred the name from the knowledge base's filesystem path
// and then recited that absolute path to the user. Both the chat prompt and the
// agent platform block consume this one block, so the description cannot drift
// between the two surfaces.
//
// Deliberately absent: whether the install is self-hosted (irrelevant to
// behaviour), and any comparison to another notes product (the user's own term
// is "knowledge base").
func productIdentityBlock(surface Surface) string {
	var sb strings.Builder
	sb.WriteString("<identity>\n")
	sb.WriteString("You are part of Rookery, a personal AI platform. Rookery gives its owner:\n")
	sb.WriteString("  - a knowledge base of linked markdown notes, which you can read and write\n")
	sb.WriteString("  - agents: instructions that run on a schedule and report back\n")
	sb.WriteString("  - skills: reusable know-how agents load when a task needs it\n")
	sb.WriteString("  - reminders: one-off nudges at a time the owner picks\n")
	sb.WriteString("  - connected accounts (Gmail, GitHub, Slack and others) it can act on\n\n")

	switch surface {
	case SurfaceChat:
		sb.WriteString("Right now you are the CHAT assistant. You can:\n")
		sb.WriteString("  - search, read, create and edit notes in the knowledge base\n")
		sb.WriteString("  - look things up on the public web\n")
		sb.WriteString("  - act on the owner's connected accounts, when any are listed below\n")
		sb.WriteString("You cannot: run scripts or shell commands, delete or rename notes,\n")
		sb.WriteString("create agents or skills, or set reminders. The owner does those in the\n")
		sb.WriteString("app. If asked for one, say so plainly and point at the app — do not\n")
		sb.WriteString("improvise a workaround and do not claim you did it.\n\n")
	case SurfaceAgent:
		sb.WriteString("Right now you are an AGENT run: you execute your own instructions on a\n")
		sb.WriteString("schedule or on demand, and report back through the output protocol\n")
		sb.WriteString("described below.\n\n")
	}

	sb.WriteString("When you refer to a note, use its path inside the knowledge base (for\n")
	sb.WriteString("example notes/trip-planning.md). Never quote the knowledge base's absolute\n")
	sb.WriteString("filesystem path back to the owner — they do not think about their notes as\n")
	sb.WriteString("a directory on a disk, and it tells them nothing.\n")
	sb.WriteString("</identity>\n")
	return sb.String()
}
```

- [ ] **Step 4: Wire it into both consumers and fix the stale text**

In `platformContextBlock` (line ~261): replace the two opening
`sb.WriteString("You are an AI agent running inside Rookery …")` lines with
`sb.WriteString(productIdentityBlock(SurfaceAgent))` followed by a newline.

Then in the same function:
- Line ~267-269: `"Every user has a personal vault — an Obsidian-style, ever-growing knowledge base that the user owns and organizes themselves (like Obsidian or Notion, but local and markdown-based)."` → `"Every workspace has a knowledge base: an ever-growing folder of linked markdown notes the owner owns and organizes themselves."`
- Line ~271: `"The vault root is:\n  "` → `"Its root is:\n  "`
- Line ~275: `"At runtime you are told the vault root path.\n"` → `"At runtime you are told its root path.\n"`
- Lines ~289-291: `USER.md` → `ABOUT.md` with description `"what this workspace is for, and who the owner is"`; `SOUL.md` → `STYLE.md` with `"tone and language preferences"`; keep `GENERAL.md` but describe it as `"quick notes appended via the /memory command (appears once used)"`.
- Line ~313: `"memory/USER.md / SOUL.md — the user's core profile/context; update in place, do not move."` → `"memory/ABOUT.md / STYLE.md — the owner's identity and style; update in place, do not move."`
- Any remaining `vault` in this function → `knowledge base`. Verify with
  `grep -in vault internal/prompts/prompts.go`, ignoring `vaultRoot` parameter
  names and Go identifiers.

In `BuildChatSystemPrompt`, both branches:
- Prepend `sb.WriteString(productIdentityBlock(SurfaceChat))` plus a blank line before the existing `fmt.Sprintf` call.
- Change the opening sentence of both branches from
  `"You are a helpful assistant chatting with the user. Your working directory is the user's personal knowledge base, an Obsidian-style vault of markdown notes rooted at:"`
  to
  `"Your working directory is the owner's knowledge base — a folder of markdown notes rooted at:"`.
- In the CLI branch, change
  `"The vault contains folders like notes/, memory/, chats/, agents/, reminders/, and any folders/files the user has created themselves."`
  to
  `"It contains folders like notes/, memory/, chats/, agents/, and any folders the owner created themselves."`
  — dropping `reminders/`, which has never existed there.
- Replace every other occurrence of `vault` in both branches with
  `knowledge base` (`"%[1]s/notes/**/*.md"` Glob examples keep the path
  substitution; only the prose changes).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/prompts/ -count=1 -v`
Expected: PASS, including the pre-existing chat-prompt tests
(`TestChatSystemPromptConnectorsNativeTools`, the tool-set test at line 209).

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/
git commit -m "feat(prompts): give chat a real product identity and drop stale KB wording"
```

---

### Task 7: Seed identity at workspace create and setup completion

**Files:**
- Modify: `web/api_workspaces.go:47-60` (`apiCreateWorkspace`)
- Modify: `web/api_settings.go:475-485` (setup step 7 branch of `apiPostSetup`)
- Modify: `web/api_settings.go:213-232` (`apiPutSettingsWorkspace`)
- Modify: `web/api_settings.go:186-212` (`apiPutSettingsProfile`)
- Create: `web/api_identity_test.go`

**Interfaces:**
- Consumes: `memory.Identity`, `(*memory.Store).SeedIdentity` (Tasks 1-2).
- Produces: `func (s *Server) identityFor(w *db.Workspace) memory.Identity` — the one place that assembles an `Identity` from the DB, reused by the seed call sites and (in Task 8) by the startup migration's equivalent.

- [ ] **Step 1: Write the failing test**

Create `web/api_identity_test.go`:

```go
package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Setup step 7 is the single point every wizard path passes through — steps 1
// and 4 are both skippable — so it is where identity must be seeded.
func TestSetupCompletionSeedsIdentityFiles(t *testing.T) {
	s, env := newAPITestServer(t)
	// Arrange: a workspace mid-setup with basics + profile recorded.
	ws := env.workspace
	if err := s.db.UpdateWorkspaceMeta(ws.ID, "Personal", "keeping my research in one place"); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"display_name":     "Peer",
		"profile_language": "English",
		"profile_tone":     "concise",
	} {
		if err := s.db.SetSetting(ws.ID, k, v); err != nil {
			t.Fatal(err)
		}
	}

	rec := env.postJSON(t, "/api/v1/setup", map[string]any{"step": 7})
	if rec.Code != http.StatusOK {
		t.Fatalf("step 7 = %d, body %s", rec.Code, rec.Body.String())
	}

	about := readVaultFile(t, env.dataDir, ws.ID, "ABOUT.md")
	if !strings.Contains(about, "keeping my research in one place") {
		t.Errorf("ABOUT.md missing the workspace about text:\n%s", about)
	}
	if !strings.Contains(about, "Peer") {
		t.Errorf("ABOUT.md missing the display name:\n%s", about)
	}
	style := readVaultFile(t, env.dataDir, ws.ID, "STYLE.md")
	if !strings.Contains(style, "English") {
		t.Errorf("STYLE.md missing the language:\n%s", style)
	}
}

// Renaming a workspace must not blank workspaces.about — it is the seed source
// the startup backfill reads for installs whose files are still empty.
func TestSaveWorkspaceMetaPreservesAbout(t *testing.T) {
	s, env := newAPITestServer(t)
	ws := env.workspace
	if err := s.db.UpdateWorkspaceMeta(ws.ID, "Personal", "the original about text"); err != nil {
		t.Fatal(err)
	}

	rec := env.putJSON(t, "/api/v1/settings/workspace", map[string]any{
		"name": "Renamed", "about": "a client trying to overwrite it",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body %s", rec.Code, rec.Body.String())
	}

	got, err := s.db.GetWorkspaceByID(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" {
		t.Errorf("name not saved, got %q", got.Name)
	}
	if got.About != "the original about text" {
		t.Errorf("About was overwritten: %q", got.About)
	}
}

// The five prose keys are no longer read; the endpoint must stop writing them
// so Settings cannot look like the place to change them.
func TestSaveProfileIgnoresProseFields(t *testing.T) {
	s, env := newAPITestServer(t)
	ws := env.workspace

	rec := env.putJSON(t, "/api/v1/settings/profile", map[string]any{
		"display_name": "Peer", "timezone": "Europe/Skopje",
		"tone": "formal", "language": "German", "notes": "ignored",
		"email": "x@y.z", "location": "Nowhere",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body %s", rec.Code, rec.Body.String())
	}
	for _, key := range []string{"profile_tone", "profile_language", "profile_notes", "profile_email", "profile_location"} {
		if v, err := s.db.GetSetting(ws.ID, key); err == nil && v != "" {
			t.Errorf("%s should not be written any more, got %q", key, v)
		}
	}
	if v, _ := s.db.GetSetting(ws.ID, "display_name"); v != "Peer" {
		t.Errorf("display_name = %q, want Peer", v)
	}
	if v, _ := s.db.GetSetting(ws.ID, "profile_timezone"); v != "Europe/Skopje" {
		t.Errorf("profile_timezone = %q, want Europe/Skopje", v)
	}
}

func readVaultFile(t *testing.T, dataDir, wsID, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "vaults", wsID, "memory", name))
	if err != nil {
		t.Fatalf("read memory/%s: %v", name, err)
	}
	return string(b)
}
```

**Before writing this test**, read `web/api_auth_test.go` and
`web/api_settings_test.go` to find the real names of the test-server helper
(`newAPITestServer`), its returned env struct, and the JSON request helpers.
Match them exactly; the names above are placeholders for whatever that file
actually provides, and the env must expose the data dir (or derive it from the
server's vault). If the existing harness has no `putJSON`/`postJSON`, use the
same `httptest.NewRequest` + `s.echo.ServeHTTP` pattern the neighbouring tests
use.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run 'TestSetupCompletionSeeds|TestSaveWorkspaceMetaPreserves|TestSaveProfileIgnores' -v`
Expected: FAIL — no memory files written; About overwritten; prose keys written.

- [ ] **Step 3: Implement `identityFor` and the seed calls**

Add to `web/api_settings.go` (or a new `web/identity.go` if that file is already large):

```go
// identityFor assembles the memory.Identity for a workspace from the two stores
// that still own structured values: the workspaces row (name, about) and the
// per-workspace settings table (the profile keys).
//
// This is the ONLY place the mapping lives, so the seed-at-setup path and the
// startup backfill cannot render different files from the same data.
func (s *Server) identityFor(w *db.Workspace) memory.Identity {
	p := profile.Load(s.db, w.ID)
	return memory.Identity{
		WorkspaceName:  w.Name,
		WorkspaceAbout: w.About,
		DisplayName:    p.DisplayName,
		Email:          p.Email,
		Location:       p.Location,
		Notes:          p.Notes,
		Tone:           p.Tone,
		Language:       p.Language,
	}
}
```

In `apiPostSetup`'s `case 7:` branch, after `MarkWorkspaceSetupComplete`
succeeds and before `apiSetupOK`:

```go
		// Seed identity here rather than at step 1 or 4: both of those are
		// skippable, and step 7 is the one point every path reaches. Best
		// effort — a memory-file write must not fail setup and strand the
		// owner with a half-configured workspace; the startup backfill picks
		// it up on the next boot.
		if w2, err := s.db.GetWorkspaceByID(w.ID); err == nil {
			if err := s.memory.SeedIdentity(w2.ID, s.identityFor(w2)); err != nil {
				slog.Warn("setup: seed identity files", "workspace", w2.ID, "err", err)
			}
		}
```

In `apiCreateWorkspace`, after `auth.CreateWorkspace` succeeds:

```go
	if err := s.memory.SeedIdentity(w.ID, s.identityFor(w)); err != nil {
		slog.Warn("create workspace: seed identity files", "workspace", w.ID, "err", err)
	}
```

In `apiPutSettingsWorkspace`, change the update call to preserve `About`:

```go
	// req.About is deliberately ignored: memory/ABOUT.md is the source of truth
	// for what this workspace is about, and workspaces.about survives only as
	// the seed value the startup backfill reads. Passing req.About through would
	// let a rename blank it. Ignored rather than rejected so an older SPA build
	// still sending the field does not start failing.
	if err := s.db.UpdateWorkspaceMeta(w.ID, req.Name, w.About); err != nil {
```

In `apiPutSettingsProfile`, drop the five prose fields:

```go
	// Only the two fields code actually reads are persisted. Tone, language,
	// location, email and background live in memory/ABOUT.md and
	// memory/STYLE.md, which the owner edits in the knowledge base — Settings
	// must not offer a second place to change them.
	prof := profile.Profile{
		DisplayName: req.DisplayName,
		Timezone:    req.Timezone,
	}
```

Leave `apiSetupProfile` (step 4) writing all seven — it is the collection point
that feeds the seed.

Add `"log/slog"` and `"github.com/ilijad1/rookery/internal/memory"` imports
where needed. Confirm the server field holding the memory store is named
`s.memory` (see `web/handlers_misc.go:155`, which uses `s.memory`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./web/ -count=1 -timeout 300s`
Expected: PASS. Existing settings tests asserting the prose keys round-trip
will fail — update them to assert the new behaviour, since that behaviour is
now the spec.

- [ ] **Step 5: Commit**

```bash
git add web/
git commit -m "feat(web): seed identity files at setup and stop writing prose profile keys"
```

---

### Task 8: Wire the startup migration

**Files:**
- Modify: `cmd/rookery/main.go:217-228` (the existing per-workspace memory loop)

**Interfaces:**
- Consumes: `(*memory.Store).MigrateIdentityFiles` (Task 3), `profile.Load`, `db.GetWorkspaceByID`.
- Produces: nothing.

- [ ] **Step 1: Extend the existing loop**

The loop that calls `MigrateToStructuredFiles` already iterates every workspace
directory. Extend it — `MigrateIdentityFiles` must run **after**
`MigrateToStructuredFiles`, which may have just created the `memory/`
directory:

```go
			// Consolidate any legacy UUID-keyed memory notes into GENERAL.md,
			// then bring memory/ up to the current identity layout: USER.md →
			// ABOUT.md, SOUL.md → STYLE.md, and a backfill from the DB for any
			// file still empty. The backfill is what repairs an EXISTING
			// install: before this, setup values never reached memory/ at all,
			// so every workspace's identity files are the untouched scaffold.
			if userDirs, err := os.ReadDir(vaultsDir); err == nil {
				for _, d := range userDirs {
					if !d.IsDir() {
						continue
					}
					if err := memStore.MigrateToStructuredFiles(d.Name()); err != nil {
						slog.Warn("memory: migrate to structured files", "user", d.Name(), "err", err)
					}
					w, err := database.GetWorkspaceByID(d.Name())
					if err != nil {
						// A vault dir with no workspace row: a deleted tenant's
						// leftovers. Skip it rather than inventing an identity.
						continue
					}
					p := profile.Load(database, w.ID)
					id := memory.Identity{
						WorkspaceName:  w.Name,
						WorkspaceAbout: w.About,
						DisplayName:    p.DisplayName,
						Email:          p.Email,
						Location:       p.Location,
						Notes:          p.Notes,
						Tone:           p.Tone,
						Language:       p.Language,
					}
					if err := memStore.MigrateIdentityFiles(w.ID, id); err != nil {
						slog.Warn("memory: migrate identity files", "workspace", w.ID, "err", err)
					}
				}
			}
```

Add the `internal/profile` import if `main.go` lacks it
(`grep -n internal/profile cmd/rookery/main.go`).

- [ ] **Step 2: Build and verify against the live install**

```bash
go build -o bin/rookery ./cmd/rookery
ls ~/.rookery/vaults/*/memory/
```

Expected before: `SOUL.md  USER.md`.

```bash
./bin/rookery serve 2>&1 | head -40   # Ctrl-C once it reports listening
ls ~/.rookery/vaults/*/memory/
head -20 ~/.rookery/vaults/*/memory/ABOUT.md
```

Expected after: `ABOUT.md  STYLE.md`, with `ABOUT.md` carrying the real
workspace name and about text. This is the acceptance check for the whole
plan — the reported bug was that these files were empty.

- [ ] **Step 3: Verify idempotency**

Run `./bin/rookery serve` a second time, Ctrl-C, then confirm the files are
byte-identical:

```bash
md5sum ~/.rookery/vaults/*/memory/ABOUT.md   # note the sum
./bin/rookery serve 2>&1 | head -5           # Ctrl-C
md5sum ~/.rookery/vaults/*/memory/ABOUT.md   # must match
```

- [ ] **Step 4: Commit**

```bash
git add cmd/rookery/main.go
git commit -m "feat(cmd): migrate and backfill identity files on startup"
```

---

### Task 9: SPA — Settings stops offering a second place to edit identity

**Files:**
- Modify: `web/ui/src/pages/settings/SettingsPage.tsx` (`ProfileSection` ~line 81-190, `WorkspaceSection` ~line 191-262)
- Modify: `web/ui/src/pages/settings/settings.test.tsx`

**Interfaces:**
- Consumes: the API from Task 7 (profile PUT ignores prose fields; workspace PUT ignores `about`).
- Produces: nothing.

- [ ] **Step 1: Write the failing test**

Add to `web/ui/src/pages/settings/settings.test.tsx` (match the file's existing
render/harness pattern):

```tsx
it("profile section offers only the fields the app actually reads", async () => {
  renderSettings("profile");
  expect(await screen.findByLabelText(/display name/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/timezone/i)).toBeInTheDocument();
  // Tone, language, notes, email and location now live in the knowledge base.
  expect(screen.queryByLabelText(/^tone$/i)).not.toBeInTheDocument();
  expect(screen.queryByLabelText(/^notes$/i)).not.toBeInTheDocument();
  expect(screen.queryByLabelText(/^language$/i)).not.toBeInTheDocument();
  // …and the user is told where they went.
  expect(screen.getByText(/knowledge base/i)).toBeInTheDocument();
});

it("workspace About is read-only and labelled About Workspace", async () => {
  renderSettings("workspace");
  expect(await screen.findByText(/about workspace/i)).toBeInTheDocument();
  expect(screen.queryByLabelText(/about/i)).not.toBeInTheDocument();
  expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/settings/settings.test.tsx`
Expected: FAIL — the Tone/Notes/Language controls still exist; About is still an editable textarea.

- [ ] **Step 3: Implement `ProfileSection`**

Delete the Email, Location, Tone, Language, and Notes form controls (and the
now-unused `countryOptions`/`TONE_OPTIONS`/`LANGUAGE_OPTIONS` imports and the
`countries` memo if nothing else uses them). Keep Display name and Timezone.
Trim `EMPTY_PROFILE` to `{ display_name: "", timezone: "" }` and narrow the
`Profile` type in `web/ui/src/lib/settings.ts` to match, or keep the wider type
and only send the two fields. Add below the Save button:

```tsx
<p className="text-sm text-muted-2">
  Your background, tone, and language live in your knowledge base, in{" "}
  <Link to="/kb?path=memory/ABOUT.md" className="underline">memory/ABOUT.md</Link>{" "}
  and{" "}
  <Link to="/kb?path=memory/STYLE.md" className="underline">memory/STYLE.md</Link>.
  Editing them there is what changes how the assistant talks to you.
</p>
```

Check the KB route's real query-parameter shape in `web/ui/src/pages/kb/` and
use whatever `KBPage` actually reads; if deep-linking to a note is not
supported, link to `/kb` alone and name the files in the text.

- [ ] **Step 4: Implement `WorkspaceSection`**

Replace the About textarea with read-only display, and stop sending `about`:

```tsx
<div className="space-y-1.5">
  <Label>About Workspace</Label>
  {workspace?.about
    ? <p className="rounded-md border border-border bg-chrome/50 p-3 text-sm">{workspace.about}</p>
    : <p className="text-sm text-muted-2">Not set.</p>}
  <p className="text-xs text-muted-2">
    Read-only here. This is what your agents and chat are told the workspace is
    for — edit it in{" "}
    <Link to="/kb" className="underline">memory/ABOUT.md</Link> in your
    knowledge base.
  </p>
</div>
```

Change `save.mutateAsync(form)` to `save.mutateAsync({ name: form.name })` and
initialise `form` from the name alone.

- [ ] **Step 5: Run tests and the type check**

```bash
cd web/ui && npx tsc -b && npx oxlint && npx vitest run
```
Expected: PASS. Fix any unused-import errors the deletions caused.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src
git commit -m "feat(web/ui): make About Workspace read-only and move style fields to the KB"
```

---

### Task 10: Full gate and end-to-end check

**Files:** none modified.

- [ ] **Step 1: Run the full local CI gate**

Run: `make ci`
Expected: PASS — fmt, vet, `-race` tests, the six-target cross-compile, and the
UI build.

- [ ] **Step 2: Deploy and smoke-test the real behaviour**

```bash
make deploy
sleep 3
curl -sS http://127.0.0.1:8080/healthz | head -5
head -30 ~/.rookery/vaults/*/memory/ABOUT.md
```

- [ ] **Step 3: Verify the reported symptom is gone**

Open the SPA, start a chat, and ask *"who are you?"* then *"what is the name of
this platform?"*. The reply must:
- name Rookery without being asked to guess,
- call it a knowledge base (not an Obsidian-style vault),
- not contain the absolute path `/home/…/.rookery/vaults/…`,
- state at least one thing it cannot do,
- greet by the display name from `ABOUT.md`.

- [ ] **Step 4: Commit anything the gate fixed and open the PR**

```bash
git push -u origin feat/identity-source-of-truth
gh pr create --title "feat(memory): make memory/ the source of truth for workspace identity" --body "$(cat <<'EOF'
Implements docs/superpowers/specs/2026-07-30-identity-source-of-truth-design.md.

`workspaces.about` reached zero prompts despite the comment claiming otherwise,
and `EnsureScaffold` wrote memory placeholders that `isEffectivelyEmpty` then
discarded — so a freshly set-up workspace told the LLM nothing about itself.

- memory/ABOUT.md + memory/STYLE.md are seeded from setup and are now the
  authoritative, KB-editable home for workspace and owner identity.
- Idempotent startup migration renames USER.md/SOUL.md and backfills any file
  still empty, repairing existing installs.
- profile.ContextString() is replaced by RuntimeContextString(), which carries
  the current date/time/timezone — no prompt had those before.
- Chat gets a shared productIdentityBlock: it names the platform, states what it
  can and cannot do, and stops calling the knowledge base an Obsidian vault or
  reciting absolute host paths.
- Settings no longer offers a second place to edit identity.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
|---|---|
| Data model table (DB vs markdown split) | 5, 7 |
| `memory/` layout, ABOUT.md/STYLE.md shape | 1 |
| Tone expansion in Go, not the frontend | 1 (`toneGuidance`) |
| Sections omitted when the source is empty | 1 |
| `internal/memory` Identity + renderers + writers | 1, 2, 3 |
| `EnsureScaffold` stops writing placeholders | 4 |
| README rewrite + append current to `legacyREADMEs` | 4 |
| Call sites: workspace create, setup step 7, startup | 7, 8 |
| `profile.RuntimeContextString` replaces `ContextString` | 5 |
| All four injection sites switched | 5 |
| `productIdentityBlock` shared by both consumers | 6 |
| "Obsidian"/"vault"/"self-hosted" removed | 6 (test enforces) |
| `platformContextBlock` stale file list | 6 |
| `reminders/` removed from the CLI chat branch | 6 |
| Settings About read-only + renamed | 9 |
| Settings Profile trimmed | 9 |
| `UpdateWorkspaceMeta` preserves existing About | 7 |
| Migration: rename, skip on collision, backfill only when empty | 3 |
| Idempotency | 2, 3, 8 |
| Error handling: log and continue, best-effort seed | 3, 7, 8 |
| Every listed test | 1-9 |

**Placeholder scan:** one deliberate instruction to read the existing test
harness before writing `web/api_identity_test.go` (Task 7 Step 1) and one to
check the KB deep-link route shape (Task 9 Step 3). Both are "verify the local
convention", not undefined work — the surrounding code and assertions are fully
specified.

**Type consistency:** `Identity` field names are identical in Tasks 1, 2, 3, 7,
and 8. `AboutFile`/`StyleFile` constants are used in Tasks 1-4 and the tests.
`RuntimeContextString(g Getter, workspaceID string, now time.Time)` has the same
signature at its definition (Task 5 Step 3) and all four call sites (Step 4).
`productIdentityBlock(surface Surface)` matches both consumers.
`identityFor(w *db.Workspace) memory.Identity` (Task 7) and the inline
equivalent in Task 8 build the same struct — noted in Task 7's doc comment as
the single mapping, with Task 8 duplicating it because `cmd/` cannot reach a
`web.Server` method.
