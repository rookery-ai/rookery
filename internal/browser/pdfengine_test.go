package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// mkExe creates an executable file at rel under root, making parents as needed.
func mkExe(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestChromiumExecutableFindsAPlaywrightBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable-bit layout")
	}
	dir := t.TempDir()
	want := mkExe(t, dir, "chromium-1234/chrome-linux64/chrome")

	if got := chromiumExecutableIn(dir); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The headless shell is the build meant for printing — no window, no GPU stack —
// and produced a markedly smaller PDF from identical input, so it wins when both
// are installed. Playwright commonly installs both.
func TestChromiumExecutablePrefersTheHeadlessShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable-bit layout")
	}
	dir := t.TempDir()
	mkExe(t, dir, "chromium-1234/chrome-linux64/chrome")
	want := mkExe(t, dir, "chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell")

	if got := chromiumExecutableIn(dir); got != want {
		t.Errorf("got %q, want the headless shell %q", got, want)
	}
}

// Playwright leaves older builds in place after an upgrade. Without an explicit
// ordering the winner would be directory order — stable enough to look correct
// and arbitrary enough to be wrong.
func TestChromiumExecutablePrefersTheNewestRevision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable-bit layout")
	}
	dir := t.TempDir()
	mkExe(t, dir, "chromium_headless_shell-1223/chrome-headless-shell-linux64/chrome-headless-shell")
	want := mkExe(t, dir, "chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell")

	if got := chromiumExecutableIn(dir); got != want {
		t.Errorf("got %q, want the newest revision %q", got, want)
	}
}

func TestChromiumExecutableAbsentCases(t *testing.T) {
	t.Run("empty dir argument", func(t *testing.T) {
		if got := chromiumExecutableIn(""); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("directory does not exist", func(t *testing.T) {
		if got := chromiumExecutableIn(filepath.Join(t.TempDir(), "nope")); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("no chromium build installed", func(t *testing.T) {
		dir := t.TempDir()
		mkExe(t, dir, "ffmpeg-1011/ffmpeg-linux/ffmpeg")
		if got := chromiumExecutableIn(dir); got != "" {
			t.Errorf("got %q, want empty — ffmpeg is not a browser", got)
		}
	})
	t.Run("build dir present but no executable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "chromium-1234", "chrome-linux64"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if got := chromiumExecutableIn(dir); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// A non-executable file with the right name is not a browser. On Windows Go
// never sets the executable bit, so the name alone is the signal there — the
// same platform split coder detection documents.
func TestChromiumExecutableIgnoresANonExecutableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the executable bit is not meaningful on Windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "chromium-1234", "chrome-linux64", "chrome")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("not executable"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := chromiumExecutableIn(dir); got != "" {
		t.Errorf("got %q, want empty for a non-executable file", got)
	}
}
