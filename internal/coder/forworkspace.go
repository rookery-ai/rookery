package coder

import (
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
)

// ForWorkspace builds a Coder from a workspace's inlined coder config, falling
// back to the provided system defaults when a field is unset.
//
// Only the 'local' coder kind (a host CLI binary) is implemented. The 'api' kind
// (direct provider API calls using a secret-store key) is reserved for future
// work; until then it falls back to the local/default binary.
func ForWorkspace(w *db.Workspace, homesDir, dataDir, defaultBin string, defaultTimeout time.Duration, enableSandbox bool) *Coder {
	bin := defaultBin
	timeout := defaultTimeout
	backendType := ""

	if w != nil && w.CoderKind != "api" {
		if w.CoderBin != "" {
			bin = w.CoderBin
		}
		if w.CoderTimeoutS > 0 {
			timeout = time.Duration(w.CoderTimeoutS) * time.Second
		}
		backendType = w.CoderBackendType
	}

	return New(bin, timeout, homesDir, dataDir).
		WithBackendType(backendType).
		WithSandbox(enableSandbox)
}
