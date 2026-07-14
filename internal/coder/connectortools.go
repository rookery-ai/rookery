package coder

import (
	"context"

	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/llm"
)

// connectorTools returns the native typed tools for every bound connection's actions.
// Tool naming/resolution is defined once in the connectors package (shared with the CLI
// bridge) so both coder types expose identical tools.
func (h *hostToolSet) connectorTools() []llm.Tool {
	defs := h.connReg.ToolDefs(h.boundConns)
	if len(defs) == 0 {
		return nil
	}
	out := make([]llm.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, llm.Tool{Name: d.Name, Description: d.Description, Parameters: d.Params})
	}
	return out
}

// resolveConnectorTool maps a tool name back to (connection, base action).
func (h *hostToolSet) resolveConnectorTool(name string) (connectors.BoundConn, string, bool) {
	return h.connReg.ResolveTool(h.boundConns, name)
}

// executeConnectorTool runs one connector tool call through connectors.Execute. During a
// build (verifyBuild) the mutating actions are refused by Execute itself.
func (h *hostToolSet) executeConnectorTool(ctx context.Context, name string, args map[string]any) string {
	b, action, ok := h.resolveConnectorTool(name)
	if !ok {
		return "error: unknown connector tool " + name
	}
	if h.usedConnIDs != nil {
		h.usedConnIDs[b.ID] = true
	}
	res, err := connectors.Execute(ctx, h.connReg, h.connStore, h.httpClient,
		connectors.ConnRef{ID: b.ID, Provider: b.Provider, AccountIdentity: b.AccountIdentity, Extra: b.Extra},
		action, args, h.verifyBuild)
	if err != nil {
		return "error: " + err.Error() // ConnectorError messages are already actionable
	}
	if len(res.Data) == 0 {
		return "(action succeeded; no data returned)"
	}
	return truncate(string(res.Data))
}
