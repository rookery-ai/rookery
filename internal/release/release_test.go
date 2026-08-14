package release

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestArchiveNameStripsTheLeadingV(t *testing.T) {
	// goreleaser names archives by VERSION, not by tag. Keeping the "v" would
	// request a URL that 404s, and the 404 message points at the wrong cause.
	if got, want := ArchiveName("v0.1.4", "linux", "amd64"), "rookery_0.1.4_linux_amd64.tar.gz"; got != want {
		t.Errorf("ArchiveName = %q, want %q", got, want)
	}
	if got, want := ArchiveName("0.1.4", "linux", "amd64"), "rookery_0.1.4_linux_amd64.tar.gz"; got != want {
		t.Errorf("a tag without the v must give the same name: %q", got)
	}
}

func TestArchiveNameUsesZipOnWindows(t *testing.T) {
	if got, want := ArchiveName("v1.2.3", "windows", "arm64"), "rookery_1.2.3_windows_arm64.zip"; got != want {
		t.Errorf("ArchiveName = %q, want %q", got, want)
	}
	if got := ArchiveName("v1.2.3", "darwin", "arm64"); !strings.HasSuffix(got, ".tar.gz") {
		t.Errorf("non-Windows must be a tar.gz, got %q", got)
	}
}

// The Go implementation and the two shell installers must build the SAME name.
// They cannot share code — one is sh, one is PowerShell — so the only thing
// keeping three copies in agreement is a test that reads the other two.
// packaging/scripts_test.go pins the shell side against literal strings; this
// pins the Go side against the same literals, so a change to the naming scheme
// fails in both places rather than silently splitting them.
func TestArchiveNameMatchesTheShellInstallers(t *testing.T) {
	root := repoRoot(t)
	sh := readFile(t, filepath.Join(root, "install.sh"))
	ps := readFile(t, filepath.Join(root, "install.ps1"))

	// install.sh: rookery_${NUM}_${OS}_${ARCH}.tar.gz, where NUM is the tag
	// with its leading v removed.
	if !strings.Contains(sh, "rookery_${NUM}_${OS}_${ARCH}.tar.gz") {
		t.Error("install.sh no longer builds rookery_<version>_<os>_<arch>.tar.gz; " +
			"ArchiveName must change with it")
	}
	if !strings.Contains(ps, "rookery_${num}_windows_${Arch}.zip") {
		t.Error("install.ps1 no longer builds rookery_<version>_windows_<arch>.zip; " +
			"ArchiveName must change with it")
	}
}

func TestParseChecksums(t *testing.T) {
	in := strings.NewReader(
		"abc123  rookery_0.1.4_linux_amd64.tar.gz\n" +
			"def456 *rookery_0.1.4_windows_amd64.zip\n" +
			"\n" +
			"garbage line with three fields here\n")
	got, err := ParseChecksums(in)
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if got["rookery_0.1.4_linux_amd64.tar.gz"] != "abc123" {
		t.Errorf("linux digest = %q", got["rookery_0.1.4_linux_amd64.tar.gz"])
	}
	// goreleaser writes "*name" in binary mode; the star is not part of the
	// filename and a literal lookup would miss every entry.
	if got["rookery_0.1.4_windows_amd64.zip"] != "def456" {
		t.Errorf("the binary-mode star must be stripped, got %v", got)
	}
}

func TestParseChecksumsRejectsAnEmptyFile(t *testing.T) {
	// An empty or unparseable checksums.txt must not read as "nothing to
	// verify against" — that would turn verification into a no-op exactly when
	// something is wrong with the release.
	if _, err := ParseChecksums(strings.NewReader("\n\n")); err == nil {
		t.Fatal("an empty checksums.txt must be an error, not an empty map")
	}
}

func TestVerify(t *testing.T) {
	if err := Verify([]byte("rookery"), "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("a wrong digest must fail")
	}
	// Round-trip against a digest this test computes, so the assertion is
	// about Verify rather than about a constant someone pasted.
	data := []byte("some archive bytes")
	if err := Verify(data, sha256Hex(data)); err != nil {
		t.Errorf("a matching digest must pass: %v", err)
	}
	if err := Verify(data, strings.ToUpper(sha256Hex(data))); err != nil {
		t.Errorf("digest comparison must be case-insensitive: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the repository root")
	return ""
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
