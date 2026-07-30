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
// connectorBin and kbBin are each the absolute path of the rookery
// binary, or "" when that bridge isn't wired for this chat. They are separate
// parameters (rather than one shared bin) because either bridge can be
// available independently of the other.
func ChatAllowedTools(connectorBin, kbBin string) string {
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
	return strings.Join(grants, ",")
}
