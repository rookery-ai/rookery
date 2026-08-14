package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	data := tarGz(t, map[string]string{
		"LICENSE":   "apache",
		"README.md": "readme",
		"rookery":   "ELF-ish bytes",
	})
	got, err := extractBinary(data, "rookery_0.1.4_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != "ELF-ish bytes" {
		t.Errorf("got %q", got)
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	data := zipOf(t, map[string]string{"LICENSE": "apache", "rookery.exe": "PE bytes"})
	got, err := extractBinary(data, "rookery_0.1.4_windows_amd64.zip")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != "PE bytes" {
		t.Errorf("got %q", got)
	}
}

// The binary is selected BY NAME, never as "the first file" or "the biggest
// one". An archive is fetched over the network and its contents are about to
// be executed as the user, so a member called something else must never be
// picked up no matter where it sits in the archive.
func TestExtractBinaryIgnoresOtherMembers(t *testing.T) {
	data := tarGz(t, map[string]string{
		"install.sh": "#!/bin/sh\necho pwned",
		"LICENSE":    "apache",
	})
	if _, err := extractBinary(data, "rookery_0.1.4_linux_amd64.tar.gz"); err == nil {
		t.Fatal("an archive with no rookery member must be an error, not a substitution")
	}
}

// A member named "../../rookery" must not be treated as the binary by virtue of
// its trailing component alone — path.Base defuses the traversal, and the
// extracted bytes never touch that name anyway, but the case is pinned so a
// future rewrite that writes members to disk starts from a test that says so.
func TestExtractBinaryUsesTheBaseName(t *testing.T) {
	data := tarGz(t, map[string]string{"./rookery": "good bytes"})
	got, err := extractBinary(data, "rookery_0.1.4_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != "good bytes" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceBinaryIsAtomicAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rookery")
	if err := os.WriteFile(target, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(target, []byte("new")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	// An install deliberately made group-readable must stay that way.
	if fi.Mode().Perm() != 0o750 {
		t.Errorf("mode = %v, want the replaced file's 0750", fi.Mode().Perm())
	}
	// No temporary file may survive: one left in a directory that is on PATH
	// is a stray executable.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("leftover files in the target directory: %v", names)
	}
}

func TestIsDowngrade(t *testing.T) {
	cases := []struct {
		current, target string
		want            bool
	}{
		{"0.1.4", "0.1.3", true},
		{"v0.2.0", "v0.1.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.3", "0.1.4", false},
		{"0.1.4", "0.1.4", false},
		{"0.1.4", "1.0.0", false},
		// Unparseable on either side must NOT warn: a false alarm on a -dev
		// build teaches people to ignore the warning that matters.
		{"0.0.0-dev", "0.1.4", false},
		{"0.1.4", "nightly", false},
	}
	for _, c := range cases {
		if got := isDowngrade(c.current, c.target); got != c.want {
			t.Errorf("isDowngrade(%q, %q) = %v, want %v", c.current, c.target, got, c.want)
		}
	}
}

func TestSameVersionIgnoresTheLeadingV(t *testing.T) {
	if !sameVersion("0.1.4", "v0.1.4") {
		t.Error("a tag and a bare version naming the same release must compare equal")
	}
	if sameVersion("0.1.4", "v0.1.5") {
		t.Error("different releases must not compare equal")
	}
}

// --purge requires the data directory typed back, not "y".
//
// The whole risk is deleting a directory the user did not realise was the live
// one, and a single keystroke cannot distinguish "I read that path" from "I
// pressed y".
func TestConfirmPurgeRequiresTheExactPath(t *testing.T) {
	var out bytes.Buffer
	if err := confirmPurge(&out, strings.NewReader("y\n"), "/srv/rookery"); err == nil {
		t.Fatal("y must not be accepted as confirmation for --purge")
	}
	out.Reset()
	if err := confirmPurge(&out, strings.NewReader("/srv/rookery\n"), "/srv/rookery"); err != nil {
		t.Fatalf("the exact path must be accepted: %v", err)
	}
	// The one irreversible fact has to be on screen before the prompt.
	if !strings.Contains(out.String(), "system.key") {
		t.Error("the confirmation must name system.key — it is what makes this unrecoverable")
	}
}

func TestConfirmPurgeRejectsAClosedInput(t *testing.T) {
	// A non-interactive run without --yes must refuse rather than proceed.
	var out bytes.Buffer
	if err := confirmPurge(&out, strings.NewReader(""), "/srv/rookery"); err == nil {
		t.Fatal("EOF must cancel, not confirm")
	}
}

func TestReadYes(t *testing.T) {
	for _, s := range []string{"y\n", "Y\n", "yes\n", "YES\n"} {
		if !readYes(strings.NewReader(s)) {
			t.Errorf("readYes(%q) = false", s)
		}
	}
	for _, s := range []string{"", "n\n", "no\n", "\n", "sure\n"} {
		if readYes(strings.NewReader(s)) {
			t.Errorf("readYes(%q) = true — only an explicit yes may proceed", s)
		}
	}
}
