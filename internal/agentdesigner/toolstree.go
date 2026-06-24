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

// ReadToolsTree walks toolsDir recursively and returns a relpath→content map of
// the agent's project files. Subdirectories and non-.py files are included so an
// agent can ship helper modules, tests, and a requirements.txt. __pycache__/ and
// other dotted/cache directories and *.pyc files are skipped; per-file and total
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
