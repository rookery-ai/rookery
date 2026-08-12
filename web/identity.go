package web

import (
	"log/slog"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/memory"
	"github.com/rookery-ai/rookery/internal/profile"
)

// identityFor assembles the memory.Identity for a workspace from the two stores
// that still own structured values: the workspaces row (name, about) and the
// per-workspace settings table (the profile keys).
//
// This is the ONLY place the mapping lives on the server side, so the
// seed-at-setup path and the seed-at-create path cannot render different files
// from the same data. (cmd/rookery's startup backfill builds the same struct
// inline; it cannot reach a *web.Server method.)
func (s *Server) identityFor(w *db.Workspace) memory.Identity {
	p := profile.Load(s.db, w.ID)
	return memory.Identity{
		WorkspaceName:  w.Name,
		WorkspaceAbout: w.About,
		DisplayName:    p.DisplayName,
		Email:          p.Email,
		Location:       p.Location,
		Notes:          p.Notes,
		Tone:           p.Tone,
		Language:       p.Language,
	}
}

// seedIdentityFiles writes memory/ABOUT.md and memory/STYLE.md for a workspace.
//
// Best effort by design: a memory-file write must never fail the request that
// triggered it. Failing setup over this would strand the owner with a
// half-configured workspace, and the startup backfill re-attempts it on the next
// boot anyway.
func (s *Server) seedIdentityFiles(workspaceID, context string) {
	if s.memory == nil {
		return
	}
	w, err := s.db.GetWorkspaceByID(workspaceID)
	if err != nil {
		slog.Warn("seed identity files: reload workspace", "workspace", workspaceID, "context", context, "err", err)
		return
	}
	if err := s.memory.SeedIdentity(w.ID, s.identityFor(w)); err != nil {
		slog.Warn("seed identity files", "workspace", w.ID, "context", context, "err", err)
	}
}
