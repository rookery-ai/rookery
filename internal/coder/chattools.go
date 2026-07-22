package coder

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
// — only when a connector bridge is available — a Bash permission scoped to the
// single `connector exec` command. Chat never gets arbitrary shell.
//
// connectorBin is the absolute path of the simple-agents binary, or "" when no
// connector bridge is wired for this chat.
func ChatAllowedTools(connectorBin string) string {
	// WebFetch/WebSearch are safe here for the same reason they sit outside the
	// API engine's exec gate: read-only, no secrets, and unable to reach private
	// address space (see netguard.go).
	const base = "Read,Write,Edit,Glob,Grep,WebFetch,WebSearch"
	if connectorBin == "" {
		return base
	}
	return base + ",Bash(" + connectorBin + " connector exec:*)"
}
