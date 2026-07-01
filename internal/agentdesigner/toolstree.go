package agentdesigner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Caps on what a multi-file agent project may contain, so reading a tools/ tree
// back from disk can never pull in runaway junk or binaries (e.g. an agent that
// pip-installs into its own dir, or accidentally writes a large data file).
const (
	maxToolFileBytes   = 1 << 20 // 1 MiB per file
	maxToolsTotalBytes = 5 << 20 // 5 MiB across all files
	maxToolFiles       = 200
)

// safeToolPath cleans a tools-relative path and rejects absolute paths or any
// traversal that would escape baseDir. Mirrors vault.Resolve — the same security
// primitive used everywhere else for user-influenced paths.
func safeToolPath(baseDir, rel string) (string, error) {
	clean := filepath.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(os.PathSeparator)) ||
		filepath.IsAbs(clean) {
		return "", fmt.Errorf("unsafe tool path: %q", rel)
	}
	abs := filepath.Join(baseDir, clean)
	if abs != baseDir && !strings.HasPrefix(abs, baseDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe tool path: %q", rel)
	}
	return abs, nil
}

// WriteToolsTree writes a relpath→content map into toolsDir, creating any parent
// subdirectories as needed so an agent can be a real multi-file project
// (tools/lib/parser.py, tools/tests/test_parser.py, tools/requirements.txt, …).
// Keys are interpreted relative to toolsDir; unsafe keys are rejected. Callers
// that want full-replacement semantics wipe toolsDir before calling.
func WriteToolsTree(toolsDir string, tools map[string]string) error {
	for rel, code := range tools {
		dest, err := safeToolPath(toolsDir, rel)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return fmt.Errorf("create tool dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dest, []byte(code), 0o640); err != nil {
			return fmt.Errorf("write tool %s: %w", rel, err)
		}
	}
	return nil
}

// isTestArtifact reports whether a file under the agent work dir is a build-time test
// artifact (binary download, run output, scratch probe) rather than shipping agent source.
// toolsDir is the absolute path to the agent's tools/ directory; it is used to detect
// _-prefixed scratch probes that sit at the tools/ top level (real modules are plain-named
// there; dunders like __init__.py and __main__.py are kept).
func isTestArtifact(absPath, name, toolsDir string) bool {
	// Binary downloads / saved emails — never shipping source.
	artifactExts := map[string]bool{
		".pdf": true, ".eml": true, ".msg": true,
		".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
		".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".7z": true,
		".mp3": true, ".mp4": true, ".mov": true, ".avi": true,
		".htm": true, ".html": true, ".pyc": true,
	}
	// Well-known run-output file names (matched with or without a leading _).
	artifactNames := map[string]bool{
		"run.json": true, "run.err": true, "run.out": true,
		"output.json": true, "results.json": true, "results.txt": true,
		"captured.json": true, "captured.txt": true, "testcfg.json": true,
	}

	lower := strings.ToLower(name)
	if artifactExts[strings.ToLower(filepath.Ext(name))] {
		return true
	}
	if artifactNames[lower] || artifactNames[strings.TrimPrefix(lower, "_")] {
		return true
	}
	// *.out / *.err / *.log run-output suffixes.
	if strings.HasSuffix(lower, ".out") || strings.HasSuffix(lower, ".err") || strings.HasSuffix(lower, ".log") {
		return true
	}
	// _-prefixed scratch probes at tools/ top level. Real agent modules are plain-named;
	// dunders (__init__.py, __main__.py) are legitimate.
	if strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "__") {
		td, err1 := filepath.Abs(toolsDir)
		parent, err2 := filepath.Abs(filepath.Dir(absPath))
		if err1 == nil && err2 == nil && parent == td {
			return true
		}
	}
	return false
}

// ReadToolsTree walks toolsDir recursively and returns a relpath→content map of
// the agent's project files. Subdirectories and non-.py files are included so an
// agent can ship helper modules, tests, and a requirements.txt. __pycache__/ and
// other dotted/cache directories, *.pyc files, and build-time test artifacts
// (binary downloads, run outputs, scratch probes) are skipped; per-file and total
// size caps prevent reading back junk or binaries. Returns an empty map if
// toolsDir does not exist.
func ReadToolsTree(toolsDir string) (map[string]string, error) {
	result := make(map[string]string)
	var total int64
	count := 0

	err := filepath.WalkDir(toolsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == toolsDir && os.IsNotExist(walkErr) {
				return fs.SkipAll
			}
			return walkErr
		}
		if d.IsDir() {
			// Skip generated/cache dirs (__pycache__, .pytest_cache, .git, …).
			if path != toolsDir && (d.Name() == "__pycache__" || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".pyc") {
			return nil
		}
		// Skip test artifacts so they never corrupt the pending-tools map or trip guardrails.
		if isTestArtifact(path, d.Name(), toolsDir) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxToolFileBytes {
			return fmt.Errorf("tool file %s exceeds %d bytes", d.Name(), maxToolFileBytes)
		}
		count++
		if count > maxToolFiles {
			return fmt.Errorf("agent has too many tool files (max %d)", maxToolFiles)
		}
		total += info.Size()
		if total > maxToolsTotalBytes {
			return fmt.Errorf("agent tool files exceed total size cap of %d bytes", maxToolsTotalBytes)
		}
		rel, err := filepath.Rel(toolsDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		result[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
