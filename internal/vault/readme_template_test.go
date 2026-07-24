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

	// The specific memory files EnsureScaffold writes, so a user can connect
	// the note to what they actually see on disk.
	for _, f := range []string{"USER.md", "SOUL.md"} {
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
