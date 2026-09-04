package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldREADMEDescribesTheVault guards the home note against drifting
// back to a bare folder list. It is the first thing a new user opens and the
// orientation an agent gets when it reads the KB, so it has to actually
// explain what each default folder is for.
func TestScaffoldREADMEDescribesTheVault(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws-readme"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(v.Root(ws), "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	got := string(b)

	// Every folder EnsureScaffold creates must be described, or the note is
	// lying by omission about the layout it claims to document.
	for _, folder := range []string{"memory/", "notes/", "agents/", "chats/", "skills/"} {
		if !strings.Contains(got, folder) {
			t.Errorf("README does not mention the %s folder", folder)
		}
	}

	// The specific memory files setup seeds, so a user can connect the note to
	// what they actually see on disk.
	for _, f := range []string{"ABOUT.md", "STYLE.md"} {
		if !strings.Contains(got, f) {
			t.Errorf("README does not mention %s", f)
		}
	}

	// The part users cannot discover by looking: memory/ is injected into
	// every LLM context, which is why editing it changes behaviour.
	if !strings.Contains(got, "injected") {
		t.Error("README does not explain that memory/ is injected into context")
	}

	// And what the KB can DO, not just what it holds.
	for _, capability := range []string{"[[note name]]", "Search", "converted to markdown"} {
		if !strings.Contains(got, capability) {
			t.Errorf("README does not cover %q", capability)
		}
	}

	if len(got) < 800 {
		t.Errorf("README is only %d bytes — too thin to orient anyone", len(got))
	}
}

// TestScaffoldREADMEUpgradesAnUntouchedLegacyNote is the reason legacyREADMEs
// exists: without it the richer home note would only ever reach vaults created
// from now on, and every existing install — the ones that actually asked for
// this — would keep the old four-line folder list.
func TestScaffoldREADMEUpgradesAnUntouchedLegacyNote(t *testing.T) {
	for i, legacy := range legacyREADMEs {
		v := New(t.TempDir())
		ws := "ws-legacy"
		if err := v.EnsureScaffold(ws); err != nil {
			t.Fatalf("scaffold: %v", err)
		}
		readme := filepath.Join(v.Root(ws), "README.md")
		if err := os.WriteFile(readme, []byte(legacy), 0o640); err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
		if err := v.EnsureScaffold(ws); err != nil {
			t.Fatalf("re-scaffold: %v", err)
		}
		b, err := os.ReadFile(readme)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(b) != readmeTemplate {
			t.Errorf("legacy template %d was not upgraded; still:\n%s", i, b)
		}
	}
}

// A note saved through the KB editor comes back without its trailing newline.
// Every README in the operator's live install was in that state, so a strictly
// byte-exact check would have skipped exactly the vaults this upgrade is for.
func TestScaffoldREADMEUpgradeToleratesAStrippedTrailingNewline(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws-no-trailing-newline"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	readme := filepath.Join(v.Root(ws), "README.md")
	stripped := strings.TrimRight(legacyREADMEs[0], "\n")
	if err := os.WriteFile(readme, []byte(stripped), 0o640); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("re-scaffold: %v", err)
	}
	b, _ := os.ReadFile(readme)
	if string(b) != readmeTemplate {
		t.Errorf("a legacy README missing its trailing newline was not upgraded:\n%s", b)
	}
}

// TestScaffoldREADMEIsNotRewritten pins the safety half of that upgrade: a home
// note the user has touched at all is theirs, and a later boot must not clobber
// it — including a legacy note with a single line appended.
func TestScaffoldREADMEIsNotRewritten(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws-readme-keep"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	readme := filepath.Join(v.Root(ws), "README.md")
	const mine = "# My own home note\n\nI rewrote this.\n"
	if err := os.WriteFile(readme, []byte(mine), 0o640); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("re-scaffold: %v", err)
	}
	b, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(b) != mine {
		t.Errorf("EnsureScaffold overwrote a user-edited README:\n%s", b)
	}

	// A legacy note with ONE extra line is an edited note, not a pristine one.
	edited := legacyREADMEs[0] + "\nMy own notes below.\n"
	if err := os.WriteFile(readme, []byte(edited), 0o640); err != nil {
		t.Fatalf("write edited legacy: %v", err)
	}
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("re-scaffold: %v", err)
	}
	if b, _ := os.ReadFile(readme); string(b) != edited {
		t.Errorf("an edited legacy README must be left alone, got:\n%s", b)
	}
}

// TestCurrentTemplateIsInLegacyList is the test that stops the NEXT README
// revision from stranding every existing install.
//
// EnsureScaffold upgrades a README only when it byte-matches an entry in
// legacyREADMEs. Shipping a new template without adding the OUTGOING one to
// that list means installs that already have the outgoing text keep it
// forever — precisely the failure the mechanism exists to prevent. Asserting
// the CURRENT template is present forces the author of the next revision to
// move it into the list, because this test fails the moment they change
// readmeTemplate without doing so.
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
	// GENERAL.md may be named only as something that appears on demand.
	if strings.Contains(readmeTemplate, "GENERAL.md") &&
		!strings.Contains(readmeTemplate, "/memory") {
		t.Error("if GENERAL.md is named, the README must say it appears when you use /memory")
	}
}

// TestScaffoldDoesNotResurrectADeletedREADME is the reason EnsureScaffold's
// create branch is gated on the vault having been absent. The home note is a
// starting point, not a system-managed file: the KB API deletes it happily
// (vault.IsUserMutationProtected does not cover it, and neither does the SPA's
// PROTECTED_TOP_DIRS), but EnsureScaffold runs on every KB tree and folder
// load, so an unconditional create wrote it straight back. The file reappeared
// before the user had finished looking at the tree, which reads as a delete
// that silently failed.
func TestScaffoldDoesNotResurrectADeletedREADME(t *testing.T) {
	v := New(t.TempDir())
	ws := "ws-readme-deleted"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	readme := filepath.Join(v.Root(ws), "README.md")
	if err := os.Remove(readme); err != nil {
		t.Fatalf("remove README: %v", err)
	}
	// Twice: the KB tree and folder endpoints each scaffold once per request,
	// so a returning user hits this on the very next page load.
	for i := range 2 {
		if err := v.EnsureScaffold(ws); err != nil {
			t.Fatalf("rescaffold %d: %v", i, err)
		}
	}
	if _, err := os.Stat(readme); !os.IsNotExist(err) {
		t.Fatalf("deleted README came back (stat err = %v)", err)
	}
}

// TestScaffoldStillWritesTheREADMEWhenSetupTouchedTheVaultFirst is the reason
// "has this vault been scaffolded?" is a sentinel and NOT os.Stat on the root.
// memory.seedIdentity runs during setup and MkdirAlls <root>/memory to write
// ABOUT.md and STYLE.md, and nothing calls EnsureScaffold at workspace
// creation — the two KB endpoints are its only callers outside migration. So
// the root routinely exists before the first KB visit, and gating the create
// on the root's absence would mean a brand-new workspace never gets a home
// note at all: the deleted-README fix, silently swallowing the first one.
func TestScaffoldStillWritesTheREADMEWhenSetupTouchedTheVaultFirst(t *testing.T) {
	v := New(t.TempDir())
	ws := "ws-seeded-first"
	// Exactly what memory.seedIdentity does before any KB page is opened.
	if err := os.MkdirAll(filepath.Join(v.Root(ws), "memory"), 0o750); err != nil {
		t.Fatalf("pre-create memory dir: %v", err)
	}
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.Root(ws), "README.md")); err != nil {
		t.Fatalf("first scaffold of a seeded vault wrote no README: %v", err)
	}
}
