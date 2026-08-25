package coder

import "strings"

// ChatAllowedTools is the CLI-coder tool grant for one-off chat, in ONE place.
//
// It exists because this list was previously duplicated across four call sites
// — two in the chat-adapter path (Telegram/Discord/Slack) and two in the web-UI
// chat handler — with nothing tying them together. When web access was added to
// chat, two sites were updated and two were missed, which silently gave one
// surface a capability the other lacked while both shared a system prompt that
// claimed the capability existed. Platform parity is a hard rule here, so the
// list is single-sourced rather than kept in step by discipline.
//
// The grant is deliberately narrow: file tools, the two read-only web tools, and
// — only when the corresponding loopback bridge is available — a Bash
// permission scoped to a single subcommand: `connector exec` for connected
// services, `kb` for the knowledge-base bridge (save_to_kb/search over
// convert+import). Chat never gets arbitrary shell.
//
// connectorBin, kbBin and mcpBin are each the absolute path of the rookery
// binary, or "" when that bridge isn't wired for this chat. They are separate
// parameters (rather than one shared bin) because each bridge can be
// available independently of the others — a workspace may have connected services
// but no MCP server, or the reverse.
// browserBin is threaded separately from the other bridges for the reason given
// below: each bridge can be available independently, and the browser is the one
// most likely to be absent, since it needs a ~500 MB runtime the others do not.
func ChatAllowedTools(connectorBin, kbBin, mcpBin, browserBin string) string {
	// WebFetch/WebSearch are safe here for the same reason they sit outside the
	// API engine's exec gate: read-only, no secrets, and unable to reach private
	// address space (see netguard.go).
	grants := []string{"Read,Write,Edit,Glob,Grep,WebFetch,WebSearch"}
	if connectorBin != "" {
		grants = append(grants, "Bash("+connectorBin+" connector exec:*)")
	}
	if kbBin != "" {
		grants = append(grants, "Bash("+kbBin+" kb:*)")
	}
	if mcpBin != "" {
		grants = append(grants, "Bash("+mcpBin+" mcp exec:*)")
	}
	if browserBin != "" {
		// `browser read` ONLY. A blanket `browser:*` would reach `browser act`,
		// and chat must never be able to click or type — the same reasoning that
		// keeps DesignAllowedTools off the blanket `kb:*` grant because it
		// reaches `kb convert`, which writes.
		grants = append(grants, "Bash("+browserBin+" browser read:*)")
	}
	return strings.Join(grants, ",")
}

// DesignAllowedTools is the CLI-coder tool grant for a DESIGN conversation.
//
// It is ChatAllowedTools minus everything that changes state. Two differences
// are load-bearing rather than cosmetic:
//
//   - No Write/Edit. A design conversation is a questioning phase; it must not
//     alter the user's vault before anything has been approved.
//   - The kb grants name subcommands INDIVIDUALLY. ChatAllowedTools uses a
//     blanket `kb:*`, which reaches `kb convert` — and convert WRITES a note
//     into the vault. Widening this back to `kb:*` silently reintroduces a
//     write path that the rest of this profile exists to remove.
//
// kbBin is the absolute path of the rookery binary, or "" when no KB bridge is
// wired for this call. The designers wire none today, so they pass "" and get
// the bare read-only set. Nothing is lost on the API engine, which reaches
// kb_file_map/kb_table_query as native host tools regardless of any bridge.
func DesignAllowedTools(kbBin string) string {
	grants := []string{"Read,Glob,Grep,WebFetch,WebSearch"}
	if kbBin != "" {
		grants = append(grants,
			"Bash("+kbBin+" kb search:*)",
			"Bash("+kbBin+" kb map:*)",
			"Bash("+kbBin+" kb table:*)",
		)
	}
	return strings.Join(grants, ",")
}
