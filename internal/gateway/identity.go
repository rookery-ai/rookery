package gateway

import "strings"

// agentPrefixMark is the emoji every agent-authored chat message leads with.
//
// It is FIXED rather than per-agent on purpose: db.Agent has no icon column,
// and the thing a user actually needs in order to tell two agents apart is the
// NAME. A per-agent icon would mean a schema migration, an API field and a
// picker UI — none of which the reported problem requires.
const agentPrefixMark = "🤖"

// AgentPrefixed labels a message with the agent that produced it.
//
// Composed as neutral CommonMark, deliberately UPSTREAM of render.For(platform):
// the router emits neutral markdown and each adapter renders on send (Telegram
// via a goldmark MarkdownV2 renderer, Discord passthrough, Slack mrkdwn), so
// emitting platform-specific markup here would break Telegram escaping.
//
// Applied at the three sites where SendOutput is the real chat sender — the
// chat /run handler, the scheduler and the web run tracker — and NOT inside
// agentrunner. runCoderAgent reuses SendOutput as a COLLECTOR for child-agent
// recursion, and that collected text is fed into the PARENT agent's LLM prompt;
// prefixing there would inject chat chrome into model input rather than into
// chat. The inbox needs no prefix either, because inbox_messages carries
// AgentName as its own column.
//
// Blank inputs pass through untouched, and text already carrying this exact
// header is returned unchanged, so a message cannot be double-labelled if two
// call sites ever overlap.
func AgentPrefixed(agentName, text string) string {
	name := strings.TrimSpace(agentName)
	if name == "" || strings.TrimSpace(text) == "" {
		return text
	}
	header := agentPrefixMark + " **" + name + "**"
	if strings.HasPrefix(text, header) {
		return text
	}
	return header + "\n\n" + text
}
