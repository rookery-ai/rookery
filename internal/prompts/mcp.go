package prompts

import (
	"fmt"
	"strings"
)

// MCPServerRef names one MCP server exposed to a coder, for the prompt block.
type MCPServerRef struct {
	Name string
}

// MCPToolsBlock tells a coder that MCP tools exist and how to reach them.
//
// It is a sibling of connectedToolsBlock rather than an extension of it, because the
// two describe genuinely different things and conflating them would mislead the
// model: a connector action is a curated call against a known API, while an MCP tool
// is whatever a server the owner added chose to advertise. The model needs to know
// that distinction to pick sensibly between two tools that sound alike.
//
// Like every other prompt in this codebase the text lives here and nowhere else, so
// the API engine and the CLI path cannot describe the same capability differently.
//
// mcpBin is the absolute path of the rookery binary when the CLI bridge is wired, or
// "" (then the bare name is used).
func MCPToolsBlock(servers []MCPServerRef, toolNames []string, backendType, mcpBin string) string {
	if len(servers) == 0 {
		return ""
	}
	bin := mcpBin
	if bin == "" {
		bin = "rookery"
	}

	var sb strings.Builder
	sb.WriteString("<mcp_server_tools>\n")
	sb.WriteString("The user has connected these MCP servers. Their tools are already available to you:\n")
	for _, s := range servers {
		fmt.Fprintf(&sb, "- %s\n", s.Name)
	}

	if backendType == BackendToolCalling {
		sb.WriteString("\nEach one is a NATIVE tool named `mcp__<server>__<tool>`. Call it directly with typed arguments. ")
		sb.WriteString("Do NOT write scripts to speak the MCP protocol, and do NOT look for server URLs or credentials — they are not in your environment and the tools already ARE the interface.\n")
	} else {
		sb.WriteString("\nRun one with:\n")
		fmt.Fprintf(&sb, "  %s mcp exec <tool> --args '<json-object>'\n", bin)
		sb.WriteString("It prints a JSON object: `{\"data\": …}` on success, `{\"error\": \"…\"}` on failure. ")
		sb.WriteString("The server URL and credential stay in the host process — you never handle them.\n")
	}

	if len(toolNames) > 0 {
		sb.WriteString("\nAvailable tools: ")
		sb.WriteString(strings.Join(toolNames, ", "))
		sb.WriteString("\n")
	}

	// Two behavioural notes the model genuinely needs, both consequences of MCP
	// tools being third-party rather than vendored.
	sb.WriteString("\nNotes:\n")
	sb.WriteString("- A tool may report a problem instead of failing outright (\"tool reported a problem: …\"). That is the server telling you what to fix — adjust the arguments and try again rather than giving up.\n")
	sb.WriteString("- Some tools require the owner's approval before they run. If a call comes back queued for approval, it has NOT happened: say so plainly and never report it as done.\n")
	sb.WriteString("</mcp_server_tools>\n")
	return sb.String()
}

// MCPBuildBlock is MCPToolsBlock plus the instruction to declare which servers the
// agent needs, for use during a BUILD.
//
// The header requirement has to be injected wherever the model is asked to produce
// AGENT.md — not only in the design conversation. This project already learned that
// the expensive way with `# Skills:`, which for a long time was requested only during
// design and left `agent_skills` empty across the whole install.
func MCPBuildBlock(servers []MCPServerRef, toolNames []string, backendType, mcpBin string) string {
	block := MCPToolsBlock(servers, toolNames, backendType, mcpBin)
	if block == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(block)
	sb.WriteString("\n<mcp_declaration>\n")
	sb.WriteString("In AGENT.md you MUST include a line declaring exactly which of these servers this agent needs, alongside the schedule line:\n")
	sb.WriteString("  # MCP: <server name>, <server name>\n")
	sb.WriteString("Declare ONLY the servers the agent actually uses — never all of them. ")
	sb.WriteString("If it needs none, write `# MCP: none`. Do not omit the line.\n")
	sb.WriteString("</mcp_declaration>\n")
	return sb.String()
}
