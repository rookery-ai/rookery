package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/secrets"
)

// MaxEnabledToolsPerServer caps how many of a server's tools are offered to a model.
//
// The cap exists because tool-list size is a shared budget: a server advertising 80
// tools does not merely make itself unwieldy, it degrades the model's selection
// across every OTHER tool the agent has, connector actions included. Sync enables up
// to the cap and reports how many were held back — never a silent truncation.
const MaxEnabledToolsPerServer = 48

// Store is the slice of the database this package needs. Taking an interface keeps
// the sync logic testable without a live DB, and documents the exact surface.
type Store interface {
	ListMCPTools(ctx context.Context, serverID string) ([]*db.MCPTool, error)
	UpsertMCPTool(ctx context.Context, t *db.MCPTool) error
	MarkMCPToolsMissing(ctx context.Context, serverID string, keep []string) error
	SetMCPServerSync(ctx context.Context, id string, ttlMs int, serverInfo string) error
	SetMCPServerStatus(ctx context.Context, id, status, lastErr string) error
}

// Lister runs discovery against a server. Client implements it.
type Lister interface {
	ListTools(ctx context.Context, srv BoundServer) (Catalog, error)
}

// SyncReport describes what a sync did, for the UI and the audit line.
type SyncReport struct {
	Discovered int
	Added      int
	Missing    int
	// HeldBack counts tools discovered but left disabled because the per-server cap
	// was reached. Surfaced so the owner can see the list is deliberately partial.
	HeldBack int
	TTLMs    int
}

// Sync fetches a server's catalog and reconciles it into the database.
//
// Two rules make this safe to run repeatedly:
//
//   - Upsert, never replace. The owner's read_only / approval_mode / enabled columns
//     are authored locally and must survive a server restating its catalog. A
//     delete-and-reinsert would quietly reset "require approval" to auto.
//   - A vanished tool is MARKED missing, not deleted, for the same reason: a server
//     that briefly serves a short list must not cost the owner their settings.
//
// The enable policy is asymmetric on purpose. On the FIRST sync (the server has no
// rows yet) tools arrive enabled, because the owner is adding this server and reading
// its tool list right then — making them tick thirty boxes is friction with no
// security payoff. On every LATER sync a newly appeared tool arrives disabled, so a
// server cannot grow a live tool between runs. That asymmetry is the actual control.
func Sync(ctx context.Context, s Store, l Lister, srv BoundServer) (SyncReport, error) {
	cat, err := l.ListTools(ctx, srv)
	if err != nil {
		status := db.MCPStatusUnreachable
		if e, ok := err.(*Error); ok && e.Kind == KindAuth {
			status = db.MCPStatusNeedsAuth
		}
		_ = s.SetMCPServerStatus(ctx, srv.ID, status, err.Error())
		return SyncReport{}, err
	}

	existing, err := s.ListMCPTools(ctx, srv.ID)
	if err != nil {
		return SyncReport{}, err
	}
	firstSync := len(existing) == 0
	known := make(map[string]*db.MCPTool, len(existing))
	for _, t := range existing {
		known[t.Name] = t
	}

	// Count what is already live, so the cap governs the TOTAL a server offers
	// rather than only what one sync adds.
	enabledCount := 0
	for _, t := range existing {
		if t.Enabled && !t.Missing {
			enabledCount++
		}
	}

	taken := map[string]bool{}
	for _, t := range existing {
		taken[t.ToolName] = true
	}

	rep := SyncReport{Discovered: len(cat.Tools), TTLMs: cat.TTLMs}
	keep := make([]string, 0, len(cat.Tools))

	for _, dt := range cat.Tools {
		keep = append(keep, dt.Name)
		prev, seen := known[dt.Name]

		row := &db.MCPTool{
			ServerID:    srv.ID,
			Name:        dt.Name,
			Title:       dt.Title,
			Description: dt.Description,
			InputSchema: string(dt.InputSchema),
		}

		if seen {
			// Preserve everything the owner authored; UpsertMCPTool only rewrites
			// the server-supplied columns, but carrying them here keeps the struct
			// honest for callers reading it back.
			row.ID = prev.ID
			row.ToolName = prev.ToolName
			row.ReadOnly = prev.ReadOnly
			row.ApprovalMode = prev.ApprovalMode
			row.Enabled = prev.Enabled
		} else {
			row.ID = uuid.NewString()
			row.ToolName = ExposedToolName(srv.Slug, dt.Name, taken)
			taken[row.ToolName] = true
			// The server's hint SEEDS the owner's column and never overrides it
			// later; from here on read_only is owner-authored.
			row.ReadOnly = dt.ReadOnlyHint
			row.ApprovalMode = db.ApprovalModeAuto
			if firstSync && enabledCount < MaxEnabledToolsPerServer {
				row.Enabled = true
				enabledCount++
			} else if firstSync {
				rep.HeldBack++
			}
			rep.Added++
		}

		if err := s.UpsertMCPTool(ctx, row); err != nil {
			return rep, err
		}
	}

	if err := s.MarkMCPToolsMissing(ctx, srv.ID, keep); err != nil {
		return rep, err
	}
	for _, t := range existing {
		if !containsStr(keep, t.Name) {
			rep.Missing++
		}
	}

	if err := s.SetMCPServerSync(ctx, srv.ID, cat.TTLMs, cat.ServerInfo); err != nil {
		return rep, err
	}
	return rep, nil
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ── Binding ───────────────────────────────────────────────────────────────────

// BoundServersFor builds the BoundServer set a run, chat turn or build is given,
// decrypting each server's credential.
//
// A server whose credential cannot be decrypted is SKIPPED rather than failing the
// whole set: one broken row must not remove every other server's tools from an
// otherwise healthy agent.
func BoundServersFor(ctx context.Context, database *db.DB, systemKey []byte, servers []*db.MCPServer) ([]BoundServer, error) {
	out := []BoundServer{}
	for _, s := range servers {
		if !s.Enabled {
			continue
		}
		tools, err := database.ListEnabledMCPTools(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		if len(tools) == 0 {
			// A server with nothing enabled contributes no tools; skipping it keeps
			// the prompt block honest about what the agent can actually do.
			continue
		}
		token := ""
		if s.EncryptedToken != "" {
			tok, err := secrets.DecryptWithSystemKey(s.EncryptedToken, systemKey)
			if err != nil {
				continue
			}
			token = tok
		}
		bs := BoundServer{
			ID:          s.ID,
			WorkspaceID: s.WorkspaceID,
			Name:        s.Name,
			Slug:        s.Slug,
			URL:         s.URL,
			AuthKind:    s.AuthKind,
			HeaderName:  s.HeaderName,
			Token:       token,
		}
		for _, t := range tools {
			bs.Tools = append(bs.Tools, Tool{
				Name:         t.Name,
				ToolName:     t.ToolName,
				Title:        t.Title,
				Description:  t.Description,
				InputSchema:  []byte(t.InputSchema),
				ReadOnly:     t.ReadOnly,
				ApprovalMode: t.ApprovalMode,
			})
		}
		out = append(out, bs)
	}
	return out, nil
}

// ActiveBoundServers returns every enabled MCP server in a workspace — what one-off
// chat and an agent BUILD get. Chat is not an agent and has no binding; a build has
// not declared its bindings yet.
func ActiveBoundServers(ctx context.Context, database *db.DB, systemKey []byte, workspaceID string) ([]BoundServer, error) {
	servers, err := database.ListMCPServers(workspaceID)
	if err != nil {
		return nil, err
	}
	return BoundServersFor(ctx, database, systemKey, servers)
}

// BoundServersForAgent returns only the servers an agent is bound to — what a RUN
// gets, mirroring how agent_connections narrows connector exposure.
func BoundServersForAgent(ctx context.Context, database *db.DB, systemKey []byte, agentID string) ([]BoundServer, error) {
	servers, err := database.ListAgentMCPServers(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return BoundServersFor(ctx, database, systemKey, servers)
}

// UniqueSlug derives a workspace-unique slug for a new server name.
func UniqueSlug(ctx context.Context, database *db.DB, workspaceID, name, exceptID string) (string, error) {
	base := SlugFor(name)
	if base == "" {
		base = "server"
	}
	slug := base
	for i := 2; i < 100; i++ {
		taken, err := database.MCPSlugTaken(ctx, workspaceID, slug, exceptID)
		if err != nil {
			return "", err
		}
		if !taken {
			return slug, nil
		}
		slug = fmt.Sprintf("%s%d", base, i)
	}
	return "", fmt.Errorf("could not derive a unique slug for %q", name)
}
