package coder

import (
	"os"
	"path/filepath"
	"strings"
)

// RunScratchPrefix names the per-run scratch folders the agent runner creates under
// a workspace home's tmp/ (tmp/run-<run id>). SweepRunScratch removes exactly these.
const RunScratchPrefix = "run-"

// WithScratchDir returns a shallow copy of the Coder whose subprocesses get dir as
// TMPDIR/TMP/TEMP instead of the workspace home's shared tmp/: the CLI coder, and
// the API engine's bash and run_script.
//
// The agent runner sets it to a folder private to one run and deletes that folder
// when the run ends, whatever the model did — so nothing a run downloads or parses
// outlives it, and two agents in one workspace no longer share scratch space. It
// must sit inside the workspace home, which the sandbox already grants read-write.
// Callers that never set it (chat, builds, dry runs) keep <home>/tmp.
func (c *Coder) WithScratchDir(dir string) *Coder {
	c2 := *c
	c2.scratchDir = dir
	return &c2
}

// tempDirFor is the TMPDIR a subprocess started from homeDir gets.
func tempDirFor(homeDir, scratchDir string) string {
	if scratchDir != "" {
		return scratchDir
	}
	return filepath.Join(homeDir, "tmp")
}

// SweepRunScratch removes every per-run scratch folder (<homesDir>/<workspace>/tmp/
// run-*) and returns how many it removed. It is for server startup, when no run can
// be in progress: a run's own folder is deleted when the run ends, so one left
// behind means the server was killed mid-run. Nothing else under tmp/ is touched.
func SweepRunScratch(homesDir string) (int, error) {
	homes, err := os.ReadDir(homesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var removed int
	var firstErr error
	for _, home := range homes {
		if !home.IsDir() {
			continue
		}
		tmp := filepath.Join(homesDir, home.Name(), "tmp")
		entries, err := os.ReadDir(tmp)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), RunScratchPrefix) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(tmp, e.Name())); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			removed++
		}
	}
	return removed, firstErr
}
