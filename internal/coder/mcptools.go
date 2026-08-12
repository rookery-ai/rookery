package coder

import (
	"context"

	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/mcp"
)

// mcpTools returns the native typed tools for every bound MCP server's enabled tools.
//
// Naming and resolution are defined once in internal/mcp (shared with the CLI
// bridge), exactly as connectorTools defers to internal/connectors — so both coder
// kinds expose identical tool names and a workspace switching coder kind sees no
// difference.
func (h *hostToolSet) mcpTools() []llm.Tool {
	defs := mcp.ToolDefs(h.boundMCP)
	if len(defs) == 0 {
		return nil
	}
	out := make([]llm.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, llm.Tool{Name: d.Name, Description: d.Description, Parameters: d.Params})
	}
	return out
}

// resolveMCPTool maps an exposed tool name back to (server, tool).
func (h *hostToolSet) resolveMCPTool(name string) (mcp.BoundServer, mcp.Tool, bool) {
	return mcp.ResolveTool(h.boundMCP, name)
}

// executeMCPTool runs one MCP tool call through mcp.Execute — the same choke point
// the CLI bridge reaches, carrying the build guard and the approval parker.
func (h *hostToolSet) executeMCPTool(ctx context.Context, name string, args map[string]any) string {
	srv, tool, ok := h.resolveMCPTool(name)
	if !ok {
		return "error: unknown MCP tool " + name
	}
	if h.usedMCPServerIDs != nil {
		h.usedMCPServerIDs[srv.ID] = true
	}
	res, err := mcp.Execute(ctx, h.mcpCaller, srv, tool, args,
		mcp.Policy{BuildPhase: h.verifyBuild, Parker: h.mcpParker})
	if err != nil {
		return "error: " + err.Error() // mcp.Error messages are already actionable
	}
	if res.IsError {
		// A TOOL EXECUTION error, which the MCP spec says to hand to the model so it
		// can self-correct ("date must be in the future"). Deliberately NOT prefixed
		// with `error:` — that prefix is what executeOrNudge's oscillation guard
		// counts as a failing call, and counting a self-correctable result would
		// block the retry the server is inviting. A protocol or transport failure
		// comes back as a Go error above and does get the prefix.
		return "tool reported a problem: " + string(res.Data)
	}
	if len(res.Data) == 0 {
		return "(tool succeeded; no data returned)"
	}
	return truncate(string(res.Data))
}
