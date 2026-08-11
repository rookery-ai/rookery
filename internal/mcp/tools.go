package mcp

import (
	"encoding/json"
	"strings"
)

// maxDescriptionLen bounds how much server-authored text reaches the model's tool
// list.
//
// A tool description is UNTRUSTED REMOTE TEXT with no connector analogue — a
// connector's descriptions come from vendored YAML, an MCP server's come from
// whoever runs it. The bound is not a defence against a determined injection (the
// owner chose to trust this server), but it stops a verbose or hostile server from
// crowding out every other tool's description in a finite context.
const maxDescriptionLen = 1024

// emptyObjectSchema is what a tool with no declared parameters gets. A provider
// rejects a function whose schema is null or absent, so an MCP server that omitted
// inputSchema must not produce one.
var emptyObjectSchema = json.RawMessage(`{"type":"object"}`)

// ToolDef is one MCP tool exposed to a coder. It is the coder-agnostic definition
// both the API engine (→ llm.Tool) and the CLI bridge build from, so tool naming and
// parameters are defined exactly once — the same contract connectors.ToolDef holds.
type ToolDef struct {
	Name        string
	Description string
	Params      json.RawMessage
	ReadOnly    bool
	Server      BoundServer
	// Tool is the server's bare tool name (no namespace), which is what tools/call
	// is invoked with.
	Tool string
}

// ToolDefs returns the definitions for every enabled tool of every bound server.
//
// Unlike connectors.ToolDefs there is no single-vs-multi-account branch: an MCP tool
// is ALWAYS namespaced, because two servers exposing `search` is the expected case
// rather than the exception.
func ToolDefs(bound []BoundServer) []ToolDef {
	var out []ToolDef
	for _, s := range bound {
		for _, t := range s.Tools {
			params := t.InputSchema
			if len(params) == 0 || string(params) == "null" {
				params = emptyObjectSchema
			}
			desc := t.Description
			if desc == "" {
				desc = t.Title
			}
			if len(desc) > maxDescriptionLen {
				desc = desc[:maxDescriptionLen] + "…"
			}
			// Name the server in the description. With several servers bound, a bare
			// "Search issues" gives the model no way to choose between two of them.
			desc = "[" + s.Name + "] " + desc

			out = append(out, ToolDef{
				Name:        t.ToolName,
				Description: desc,
				Params:      params,
				ReadOnly:    t.ReadOnly,
				Server:      s,
				Tool:        t.Name,
			})
		}
	}
	return out
}

// ResolveTool maps an exposed tool name back to (server, tool).
//
// It matches on the STORED ToolName rather than recomputing the slug, so the mapping
// cannot drift from what the model was told even if the naming rules change in a
// later release.
func ResolveTool(bound []BoundServer, name string) (BoundServer, Tool, bool) {
	for _, s := range bound {
		for _, t := range s.Tools {
			if t.ToolName == name {
				return s, t, true
			}
		}
	}
	return BoundServer{}, Tool{}, false
}

// IsMCPToolName reports whether a tool name belongs to this package's namespace.
// The bridge and the API engine use it to route a call without consulting the
// catalog first.
func IsMCPToolName(name string) bool {
	return strings.HasPrefix(name, ToolNamePrefix+"__")
}

// ToolNames lists the exposed names of every bound tool, for the prompt block that
// tells a CLI coder which names `rookery mcp exec` accepts.
func ToolNames(bound []BoundServer) []string {
	var out []string
	for _, s := range bound {
		for _, t := range s.Tools {
			out = append(out, t.ToolName)
		}
	}
	return out
}
