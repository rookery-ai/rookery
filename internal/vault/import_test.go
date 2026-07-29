package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ilijad1/rookery/internal/convert"
)

func TestImportFileWritesNoteWithFrontmatter(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	res, err := v.ImportFile(ws, ImportInput{
		Data:      []byte("Region,Sales\nEMEA,120\n"),
		Filename:  "q3 sales.csv",
		SourceURL: "https://example.com/q3.csv",
	})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.HasPrefix(res.NotePath, "notes/") || !strings.HasSuffix(res.NotePath, ".md") {
		t.Errorf("NotePath = %q, want a markdown note under notes/", res.NotePath)
	}

	data, err := v.ReadNote(ws, res.NotePath)
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"---\n",
		`source: "https://example.com/q3.csv"`,
		"kind: csv",
		"extractor: pure-go",
		"converted_at:",
		"| Region | Sales |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("note missing %q, got:\n%s", want, body)
		}
	}
}

func TestImportFileKeepsOriginal(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "data.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if res.OriginalPath == "" {
		t.Fatal("the original bytes must be preserved: conversion is lossy")
	}
	orig, err := v.ReadNote(ws, res.OriginalPath)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(orig) != "a,b\n1,2\n" {
		t.Errorf("original bytes altered: %q", orig)
	}
	note, _ := v.ReadNote(ws, res.NotePath)
	if !strings.Contains(string(note), res.OriginalPath) {
		t.Error("the note must link to the preserved original")
	}
}

// TestImportFileRefusesDuringBuild proves the build-phase guard sits at
// ImportFile itself — the one choke point every caller (save_to_kb, the KB
// bridge) funnels through — rather than in any one caller's local check, and
// that a refused import leaves nothing behind: neither the note nor the
// preserved original.
func TestImportFileRefusesDuringBuild(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	_, err := v.ImportFile(ws, ImportInput{
		Data: []byte("a,b\n1,2\n"), Filename: "data.csv", BuildPhase: true,
	})
	if err == nil {
		t.Fatal("ImportFile must refuse when BuildPhase is set")
	}

	entries, _ := os.ReadDir(filepath.Join(v.Root(ws), "notes"))
	if len(entries) != 0 {
		t.Errorf("no note should have been written, found %d entries", len(entries))
	}
	files, _ := os.ReadDir(filepath.Join(v.Root(ws), FilesDir))
	if len(files) != 0 {
		t.Errorf("no original should have been preserved, found %d entries", len(files))
	}
}

func TestImportFileSanitizesName(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("x,y\n1,2\n"), Filename: "../../etc/passwd.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if strings.Contains(res.NotePath, "..") {
		t.Errorf("path traversal survived sanitization: %q", res.NotePath)
	}
}

func TestImportFileUniqueOnCollision(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	first, _ := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "dup.csv"})
	second, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n3,4\n"), Filename: "dup.csv"})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if first.NotePath == second.NotePath {
		t.Error("a second import of the same name must not overwrite the first")
	}
	data, _ := v.ReadNote(ws, first.NotePath)
	if !strings.Contains(string(data), "| 1 | 2 |") {
		t.Error("the first note was overwritten")
	}
}

func TestImportFileRespectsDestDir(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "x.csv", DestDir: "notes/finance"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.HasPrefix(res.NotePath, "notes/finance/") {
		t.Errorf("NotePath = %q, want it under the requested folder", res.NotePath)
	}
}

func TestImportFileUnsupportedIsError(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	v.EnsureScaffold(ws)

	_, err := v.ImportFile(ws, ImportInput{Data: []byte{0x00, 0x01, 0x02}, Filename: "x.bin"})
	if err == nil {
		t.Fatal("an unconvertible file must error rather than create a blank note")
	}
	if !errors.Is(err, convert.ErrUnsupportedFormat) {
		t.Errorf("error = %v, want errors.Is(err, convert.ErrUnsupportedFormat)", err)
	}
}

// TestImportFileConcurrentSameFilenameNoCollision is the Finding-1 regression
// test: 8 goroutines import the same filename simultaneously. Every import
// must land on a distinct note path and a distinct original path, none may
// error, and each note must link back to its OWN original — not another
// goroutine's. Run with -race; the collision itself surfaces as a filesystem
// clobber (two imports resolving the same "free" path), which -race alone
// won't flag, so the assertions below are the actual proof.
func TestImportFileConcurrentSameFilenameNoCollision(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	const n = 8
	results := make([]ImportResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = v.ImportFile(ws, ImportInput{
				Data:     []byte(fmt.Sprintf("a,b\n%d,%d\n", i, i)),
				Filename: "dup.csv",
			})
		}(i)
	}
	wg.Wait()

	notePaths := make(map[string]int)
	origPaths := make(map[string]int)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("import %d returned an error: %v", i, err)
		}
		if j, seen := notePaths[results[i].NotePath]; seen {
			t.Fatalf("import %d and %d collided on NotePath %q", i, j, results[i].NotePath)
		}
		notePaths[results[i].NotePath] = i
		if j, seen := origPaths[results[i].OriginalPath]; seen {
			t.Fatalf("import %d and %d collided on OriginalPath %q", i, j, results[i].OriginalPath)
		}
		origPaths[results[i].OriginalPath] = i
	}

	for i, res := range results {
		note, err := v.ReadNote(ws, res.NotePath)
		if err != nil {
			t.Fatalf("read note %d (%s): %v", i, res.NotePath, err)
		}
		if !strings.Contains(string(note), res.OriginalPath) {
			t.Errorf("import %d: note %q does not link to its own original %q (cross-linked?)", i, res.NotePath, res.OriginalPath)
		}
		if _, err := v.ReadNote(ws, res.OriginalPath); err != nil {
			t.Errorf("import %d: original %q missing: %v", i, res.OriginalPath, err)
		}
	}
}

// TestImportFileCleansUpOrphanOnNoteWriteFailure is the Finding-2 regression
// test: the NOTE write itself (not just the path reservation before it) must
// fail, and the already-written original in files/ must not survive as an
// unreferenced orphan. The destination directory exists (so the free-path
// probe cleanly reports ENOENT and uniquePath succeeds) but is read-only, so
// the failure happens one step later, inside WriteNote's own CreateTemp —
// exactly the branch Finding 2 is about.
func TestImportFileCleansUpOrphanOnNoteWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory write permission is not enforced")
	}
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	roDir, err := v.Resolve(ws, "notes/ro")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := os.MkdirAll(roDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(roDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Restore write permission before t.TempDir's cleanup tries to remove it.
	defer os.Chmod(roDir, 0o750)

	_, err = v.ImportFile(ws, ImportInput{
		Data:     []byte("a,b\n1,2\n"),
		Filename: "orphan.csv",
		DestDir:  "notes/ro",
	})
	if err == nil {
		t.Fatal("expected the note write to fail")
	}

	filesDir := filepath.Join(v.Root(ws), FilesDir)
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		t.Fatalf("read files dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "orphan") {
			t.Errorf("orphaned original left behind after failed note write: %s", e.Name())
		}
	}
}

func TestImportFileRejectsSystemManagedDestDir(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	for _, dest := range []string{".kb", ".kb/sub", "chats", "chats/x", "agents", "agents/some-agent-id"} {
		if _, err := v.ImportFile(ws, ImportInput{
			Data:     []byte("a,b\n1,2\n"),
			Filename: "x.csv",
			DestDir:  dest,
		}); err == nil {
			t.Errorf("DestDir %q: expected rejection of a system-managed area", dest)
		}
	}
}

// TestImportFileRejectsDotSegmentBypass is the Finding-1 regression test.
// topSegment used to read DestDir's raw first slash-segment, UNNORMALIZED,
// while the note's actual path is built with path.Join(destDir, name+".md"),
// which DOES collapse ".." segments. That let a caller spell a destination
// whose first raw segment looked innocuous (e.g. "notes") while the cleaned,
// actually-written path landed inside "agents/" — the very directory the
// guard exists to keep out of reach. Every case here must be refused, and
// refused BEFORE any write (checked via the files/ dir count, mirroring
// TestImportFileRefusesDuringBuild).
func TestImportFileRejectsDotSegmentBypass(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	cases := []string{
		"notes/../agents",    // raw first segment "notes" (guard would pass); cleans to "agents"
		"./agents",           // raw first segment "." (guard would pass); cleans to "agents"
		"notes/../../agents", // cleans to "../agents" — escapes the vault-relative root entirely
		"chats/../.kb",       // cleans to ".kb" — a different system dir than the raw first segment
	}
	for _, dest := range cases {
		if _, err := v.ImportFile(ws, ImportInput{
			Data:     []byte("a,b\n1,2\n"),
			Filename: "sneaky.csv",
			DestDir:  dest,
		}); err == nil {
			t.Errorf("DestDir %q: expected rejection (dot-segment bypass of the system-dir guard)", dest)
		}
	}

	// None of the rejected attempts should have written anything — not even
	// the preserved original — matching ImportFile's "checked before anything
	// is written" contract for the system-dir guard.
	files, _ := os.ReadDir(filepath.Join(v.Root(ws), FilesDir))
	if len(files) != 0 {
		t.Errorf("a rejected DestDir must not preserve an original; found %d entries", len(files))
	}
	agentsDir, _ := os.ReadDir(filepath.Join(v.Root(ws), "agents"))
	if len(agentsDir) != 0 {
		t.Errorf("a rejected DestDir must never land a note inside agents/; found %d entries", len(agentsDir))
	}
}

func TestImportFileKeepsCyrillicFilename(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "Отчет по продажам.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.Contains(res.NotePath, "Отчет") {
		t.Errorf("NotePath = %q, want the Cyrillic title preserved, not collapsed to a timestamp", res.NotePath)
	}
}

func TestImportFileKeepsCJKFilename(t *testing.T) {
	v := New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	res, err := v.ImportFile(ws, ImportInput{Data: []byte("a,b\n1,2\n"), Filename: "売上報告書.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if !strings.Contains(res.NotePath, "売上報告書") {
		t.Errorf("NotePath = %q, want the CJK title preserved, not collapsed to a timestamp", res.NotePath)
	}
}

// TestImportLockDistinctPerWorkspaceStablePerWorkspace pins the structural
// property the per-workspace lock map depends on: two different workspaces
// get two different mutexes (so they don't serialize each other), and the
// SAME workspace always gets back the SAME mutex (so repeated calls — from
// different *Vault instances, exactly as production has more than one
// *vault.Vault over the same on-disk data — still exclude each other).
func TestImportLockDistinctPerWorkspaceStablePerWorkspace(t *testing.T) {
	a := importLock("ws-a")
	b := importLock("ws-b")
	if a == b {
		t.Fatal("importLock(\"ws-a\") == importLock(\"ws-b\"): different workspaces must not share a mutex")
	}
	if again := importLock("ws-a"); again != a {
		t.Fatal("importLock(\"ws-a\") returned a different mutex on a second call: the per-workspace lock must be stable")
	}
}

// TestImportFileConcurrentDifferentWorkspacesBothSucceed is the Task-2
// regression test: two DIFFERENT workspaces, each imported through its OWN
// *Vault instance (mirroring production's separate coder/runner and web
// *vault.Vault objects over the same on-disk data), import concurrently.
// Both must succeed and produce correct, independent results — proving the
// per-workspace lock does not accidentally cross-serialize or cross-link
// unrelated workspaces. Run with -race.
func TestImportFileConcurrentDifferentWorkspacesBothSucceed(t *testing.T) {
	dir := t.TempDir()
	// Two separate *Vault instances over the same on-disk root, matching how
	// cmd/simple-agents and web.NewServer each construct their own.
	va := New(dir)
	vb := New(dir)

	const wsA = "ws-a-concurrent"
	const wsB = "ws-b-concurrent"
	if err := va.EnsureScaffold(wsA); err != nil {
		t.Fatalf("scaffold ws-a: %v", err)
	}
	if err := vb.EnsureScaffold(wsB); err != nil {
		t.Fatalf("scaffold ws-b: %v", err)
	}

	var (
		wg         sync.WaitGroup
		resA, resB ImportResult
		errA, errB error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resA, errA = va.ImportFile(wsA, ImportInput{
			Data:     []byte("a,b\n1,2\n"),
			Filename: "shared-name.csv",
		})
	}()
	go func() {
		defer wg.Done()
		resB, errB = vb.ImportFile(wsB, ImportInput{
			Data:     []byte("c,d\n3,4\n"),
			Filename: "shared-name.csv",
		})
	}()
	wg.Wait()

	if errA != nil {
		t.Fatalf("workspace A import: %v", errA)
	}
	if errB != nil {
		t.Fatalf("workspace B import: %v", errB)
	}

	noteA, err := va.ReadNote(wsA, resA.NotePath)
	if err != nil {
		t.Fatalf("read note A: %v", err)
	}
	if !strings.Contains(string(noteA), "| 1 | 2 |") {
		t.Errorf("workspace A note has wrong content: %s", noteA)
	}

	noteB, err := vb.ReadNote(wsB, resB.NotePath)
	if err != nil {
		t.Fatalf("read note B: %v", err)
	}
	if !strings.Contains(string(noteB), "| 3 | 4 |") {
		t.Errorf("workspace B note has wrong content: %s", noteB)
	}
}
