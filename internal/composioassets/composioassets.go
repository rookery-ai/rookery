// Package composioassets is the single source of truth for the Composio v3 REST API
// helper scripts an agent or skill uses to call connected external services.
//
// Historically this boilerplate (composio_helper.py) existed only as prompt text that
// the coder LLM was expected to retype into a file on every single generation — no
// deterministic backstop, easy for a weaker model to garble or drop the safety logic,
// and duplicated (and drifting out of sync) with a second, independently-authored copy
// in the composio-toolkit core skill. This package embeds a verified-correct version
// (checked against the live Composio v3 API docs, not just carried forward from
// whatever was in the prompt before) and WriteHelperFiles seeds it deterministically
// into an agent's/skill's working directory BEFORE the coder ever runs — so every
// generation gets byte-identical, correct, safety-checked code regardless of which
// coder (CLI or direct-API) or which model executes the generation.
package composioassets

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed assets/composio_helper.py assets/composio_discover.py
var assetsFS embed.FS

// HelperFilename and DiscoverFilename are the exact on-disk names WriteHelperFiles uses.
// Exported so callers (guardrail scanning, tests) can recognize a seeded file by name
// without importing the embed contents.
const (
	HelperFilename   = "composio_helper.py"
	DiscoverFilename = "composio_discover.py"
)

// BuildPhaseEnvVar, when set to BuildPhaseGeneration in the coder's environment, tells
// composio_helper.py's composio_execute() that this is a build-time generation/edit
// verification pass, not a real scheduled/manual run — and to refuse actions that look
// like they deliver/remove something for real (see the blocklist in composio_helper.py)
// unless explicitly overridden. Never set this for a real run; agents must be able to
// actually act when they run for real.
const (
	BuildPhaseEnvVar     = "SA_BUILD_PHASE"
	BuildPhaseGeneration = "generation"
)

// IsSeededFilename reports whether name is one of the files WriteHelperFiles writes —
// used to skip LLM-authorship guardrail checks on them (they're Go-authored, not
// something the coder wrote, so there's nothing to vet).
func IsSeededFilename(name string) bool {
	return name == HelperFilename || name == DiscoverFilename
}

// WriteHelperFiles writes composio_helper.py and composio_discover.py into dir
// (typically <agentDir>/tools or <skillStagingDir>/scripts), overwriting any previous
// copy. Idempotent and safe to call on every generation/edit pass so the files can never
// drift from the verified-correct version, and any prior LLM-authored corruption
// self-heals on the next generation.
func WriteHelperFiles(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for _, name := range []string{HelperFilename, DiscoverFilename} {
		data, err := assetsFS.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o640); err != nil {
			return err
		}
	}
	return nil
}
