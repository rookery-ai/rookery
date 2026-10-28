package skilldeps

import (
	"path/filepath"

	"github.com/rookery-ai/rookery/internal/sandbox"
)

// installEnv is the whole environment of an install step. Built from scratch rather than
// filtered from the caller's, so no secret, bridge token or pip configuration can leak
// in by being forgotten: pip config files can name an extra index, which is exactly the
// dependency-confusion route ValidateRequirement closes for the command line.
func installEnv(envDir string) []string {
	return []string{
		"PATH=" + filepath.Join(envDir, "bin") + ":/usr/bin:/bin",
		"HOME=" + envDir,
		"TMPDIR=" + filepath.Join(envDir, ".tmp"),
		"PIP_CONFIG_FILE=/dev/null",
		"PIP_NO_INPUT=1",
		"PIP_DISABLE_PIP_VERSION_CHECK=1",
		"PIP_CACHE_DIR=" + filepath.Join(envDir, ".cache"),
		"LANG=C.UTF-8",
	}
}

// interpreterDirs grants the directories an interpreter (or git) needs to execute: the
// path as given and, when it is a symlink — a venv's bin/python always is — its target.
func interpreterDirs(bins ...string) []string {
	var out []string
	for _, b := range bins {
		if b == "" {
			continue
		}
		out = append(out, filepath.Dir(b))
		if real, err := filepath.EvalSymlinks(b); err == nil {
			out = append(out, filepath.Dir(real))
		}
	}
	return out
}

// installSpec confines an install step to writing its own environment and nothing else.
func installSpec(envDir string, cmd []string, python, git string) sandbox.Spec {
	return sandbox.Spec{
		Command:        cmd,
		Dir:            envDir,
		Env:            installEnv(envDir),
		ReadWritePaths: []string{envDir},
		ReadOnlyPaths:  append(sandbox.SystemReadOnlyPaths(), interpreterDirs(python, git)...),
		ReadWriteFiles: sandbox.SystemReadWriteFiles(),
		NoFile:         8192,
	}
}

// runSpec confines a script that imports an environment's packages. It may write the
// environment (which holds its private TMPDIR), the caller's working directory and the
// knowledge base — skills write notes — but NOT the workspace home, where the coder's
// credentials and every other environment live.
func runSpec(envDir, workDir, vaultRoot string, cmd, env []string, python string) sandbox.Spec {
	rw := []string{envDir}
	for _, p := range []string{workDir, vaultRoot} {
		if p != "" {
			rw = append(rw, p)
		}
	}
	return sandbox.Spec{
		Command:        cmd,
		Dir:            workDir,
		Env:            env,
		ReadWritePaths: rw,
		ReadOnlyPaths:  append(sandbox.SystemReadOnlyPaths(), interpreterDirs(python)...),
		ReadWriteFiles: sandbox.SystemReadWriteFiles(),
		NoFile:         8192,
	}
}
