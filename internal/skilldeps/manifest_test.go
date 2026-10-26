package skilldeps

import (
	"path/filepath"
	"testing"
	"time"
)

func TestManifestAppendAndSatisfies(t *testing.T) {
	dir := t.TempDir()
	if err := AppendInstall(dir, InstallRecord{At: time.Now(), Requested: []string{"requests==2.32.4"}, Resolved: []Package{{"requests", "2.32.4"}}, MalwareCheck: "clean"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendInstall(dir, InstallRecord{At: time.Now(), Requested: []string{"rich"}}); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Installs) != 2 {
		t.Fatalf("installs = %d, want 2", len(m.Installs))
	}
	if !m.Satisfies([]string{"requests==2.32.4", "rich"}) {
		t.Error("both requested specs were installed")
	}
	if m.Satisfies([]string{"pandas"}) {
		t.Error("pandas was never requested")
	}
}

func TestReadManifestMissingIsEmpty(t *testing.T) {
	m, err := ReadManifest(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Installs) != 0 || m.Satisfies([]string{"x"}) {
		t.Errorf("missing manifest should be empty, got %+v", m)
	}
}
