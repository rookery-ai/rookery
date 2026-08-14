package web

import (
	"strings"

	"github.com/rookery-ai/rookery/internal/db"
)

// workspaceCoderReady reports whether a workspace has a coder that can actually
// run — the predicate behind the setup wizard's closing action.
//
// It is deliberately NOT `w.CoderKind != ""`. db.coderKindOrDefault fills that
// column on every write, so it is non-empty for a workspace that skipped the
// coder step entirely, and a Done screen inferring "has a coder" from it would
// invite the owner into a chat that cannot answer. Asking for the fields the
// engine actually needs is the only question whose answer means anything: an
// API coder needs a provider and a model, a local coder needs a binary.
//
// A local coder's binary is NOT probed on the filesystem here. Detection
// answers "is one on PATH right now", which is a different question from "did
// the owner configure one" — the same distinction ROOKERY_CODER_MODE draws
// between policy and detection — and probing would make an endpoint that
// decides which button to draw hit the disk to do it.
func workspaceCoderReady(w *db.Workspace) bool {
	if w == nil {
		return false
	}
	switch w.CoderKind {
	case "api":
		return strings.TrimSpace(w.CoderProvider) != "" && strings.TrimSpace(w.CoderModel) != ""
	case "local":
		return strings.TrimSpace(w.CoderBin) != ""
	default:
		return false
	}
}
