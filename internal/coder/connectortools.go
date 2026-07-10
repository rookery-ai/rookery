package coder

import (
	"context"
	"strings"

	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/llm"
)

// providerCounts returns how many bound connections each provider has — used to decide
// whether an action's tool needs an account suffix (multi-account disambiguation).
func (h *hostToolSet) providerCounts() map[string]int {
	m := map[string]int{}
	for _, b := range h.boundConns {
		m[b.Provider]++
	}
	return m
}

// toolName is the bare action name for a single-account provider, or action__<label>
// when the workspace bound multiple accounts of the same provider.
func (h *hostToolSet) toolName(action string, b connectors.BoundConn, counts map[string]int) string {
	if counts[b.Provider] > 1 {
		return action + "__" + b.AccountLabel
	}
	return action
}

// connectorTools returns the native typed tools for every bound connection's actions.
func (h *hostToolSet) connectorTools() []llm.Tool {
	if h.connReg == nil || len(h.boundConns) == 0 {
		return nil
	}
	counts := h.providerCounts()
	var out []llm.Tool
	for _, b := range h.boundConns {
		for _, a := range h.connReg.Actions(b.Provider) {
			desc := a.Description
			if counts[b.Provider] > 1 {
				desc = "[" + b.AccountLabel + " / " + b.AccountIdentity + "] " + desc
			}
			out = append(out, llm.Tool{
				Name:        h.toolName(a.Name, b, counts),
				Description: desc,
				Parameters:  a.Params,
			})
		}
	}
	return out
}

// resolveConnectorTool maps a tool name back to (connection, base action). It reverses
// the __<label> suffix used for multi-account providers.
func (h *hostToolSet) resolveConnectorTool(name string) (connectors.BoundConn, string, bool) {
	if h.connReg == nil {
		return connectors.BoundConn{}, "", false
	}
	counts := h.providerCounts()
	base, label := name, ""
	if i := strings.LastIndex(name, "__"); i >= 0 {
		base, label = name[:i], name[i+2:]
	}
	for _, b := range h.boundConns {
		if _, ok := h.connReg.Action(b.Provider, base); !ok {
			continue
		}
		if counts[b.Provider] > 1 {
			if b.AccountLabel == label {
				return b, base, true
			}
			continue
		}
		if label == "" {
			return b, base, true
		}
	}
	return connectors.BoundConn{}, "", false
}

// executeConnectorTool runs one connector tool call through connectors.Execute. During
// a build (verifyBuild) the mutating actions are refused by Execute itself.
func (h *hostToolSet) executeConnectorTool(ctx context.Context, name string, args map[string]any) string {
	b, action, ok := h.resolveConnectorTool(name)
	if !ok {
		return "error: unknown connector tool " + name
	}
	res, err := connectors.Execute(ctx, h.connReg, h.connStore, h.httpClient,
		connectors.ConnRef{ID: b.ID, Provider: b.Provider, AccountIdentity: b.AccountIdentity},
		action, args, h.verifyBuild)
	if err != nil {
		return "error: " + err.Error() // ConnectorError messages are already actionable
	}
	if len(res.Data) == 0 {
		return "(action succeeded; no data returned)"
	}
	return truncate(string(res.Data))
}
