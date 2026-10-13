package agentdesigner

import "github.com/rookery-ai/rookery/internal/db"

// secretNames finds ListSecretNames by an optional-interface assertion, which
// would silently find nothing if the method were renamed. Pin that the real
// store still provides it.
var _ interface {
	ListSecretNames(workspaceID string) ([]string, error)
} = (*db.DB)(nil)
