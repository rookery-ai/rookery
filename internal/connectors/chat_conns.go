package connectors

import "github.com/rookery-ai/rookery/internal/db"

// ActiveBoundConns maps ACTIVE service-connection rows to BoundConns (skips non-ACTIVE rows),
// for exposing a workspace's whole connection set to one-off chat (chat isn't an agent — there
// are no per-agent bindings, so every active connection is offered).
func ActiveBoundConns(rows []db.ServiceConnection) []BoundConn {
	var out []BoundConn
	for _, c := range rows {
		if c.Status != "ACTIVE" {
			continue
		}
		out = append(out, BoundConn{
			ID:              c.ID,
			Provider:        c.Provider,
			AccountLabel:    c.AccountLabel,
			AccountIdentity: c.AccountIdentity,
			Extra:           ParseExtra(c.Extra),
		})
	}
	return out
}
