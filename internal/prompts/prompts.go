// Package prompts centralizes all LLM prompt construction for the coder CLI.
// Every string that gets sent to the coder as a system prompt or generation
// prompt lives here, making it easy to find, review, and tune them in one place.
package prompts

import (
	"fmt"
	"strings"
)

// ChatMessage is a minimal conversation turn. It mirrors db.ChatMessage so this
// package stays free of the db import.
type ChatMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

// SkillRef is a name+description pair for a user skill. Mirrors db.Skill.
type SkillRef struct {
	Name        string
	Description string
}

// ─── Design system prompt ─────────────────────────────────────────────────────

// DesignSystemParams is the dynamic context injected into the design conversation
// system prompt.
type DesignSystemParams struct {
	AgentName          string
	IsEdit             bool
	ExistingAgentMD    string
	ConnectedPlatforms []string
	Skills             []string
	UserProfile        string
	UserMemory         string
}

// BuildDesignSystemPrompt returns the system prompt for the conversational agent
// design/edit wizard. It guides the coder to act as a design assistant that asks
// focused questions and proposes an implementation plan before any code is written.
func BuildDesignSystemPrompt(p DesignSystemParams) string {
	var sb strings.Builder

	// ── Role ──────────────────────────────────────────────────────────────────
	if p.IsEdit {
		sb.WriteString("<role>\nYou are a friendly agent design assistant helping the user EDIT an existing autonomous AI agent called \"")
		sb.WriteString(p.AgentName)
		sb.WriteString("\".\n\nHere is its current AGENT.md so you understand what it already does:\n<current_agent_md>\n")
		sb.WriteString(p.ExistingAgentMD)
		sb.WriteString("\n</current_agent_md>\n</role>\n\n")
	} else {
		sb.WriteString("<role>\nYou are a friendly agent design assistant helping build a new autonomous AI agent called \"")
		sb.WriteString(p.AgentName)
		sb.WriteString("\".\n</role>\n\n")
	}

	// ── Hard constraints (first — LLMs follow early rules more reliably) ──────
	sb.WriteString(`<constraints>
NEVER do any of the following — no exceptions:
- Ask the user for a Telegram bot token, chat ID, webhook URL, or any messaging credential.
- Tell the user to paste API keys, passwords, or secret values in this chat.
- Suggest setting up cron jobs, systemd timers, or any external scheduling tool.
- Write code or generate files during the design conversation.
- Describe implementation details unless the user specifically asks.
- Ask more than two questions in a single reply.
</constraints>

`)

	// ── Platform context ──────────────────────────────────────────────────────
	sb.WriteString("<platform_context>\n")
	if len(p.ConnectedPlatforms) > 0 {
		sb.WriteString("The user has connected: ")
		sb.WriteString(strings.Join(p.ConnectedPlatforms, ", "))
		sb.WriteString(`
When the user says "send to Telegram", "notify me", "post a message", or similar — they mean: the system will route the agent's output to their connected platform automatically. No bot token, chat ID, or messaging setup is needed or should be mentioned.
`)
	} else {
		sb.WriteString(`No chat platform is currently connected. If the agent needs to send notifications, mention that the user can connect Telegram from Settings → Connectors in the web dashboard. No credentials are needed — the platform handles routing automatically.
`)
	}
	sb.WriteString("</platform_context>\n\n")

	// ── Skills ────────────────────────────────────────────────────────────────
	if len(p.Skills) > 0 {
		sb.WriteString("<available_skills>\n")
		sb.WriteString("This agent can use these pre-built skills: ")
		sb.WriteString(strings.Join(p.Skills, ", "))
		sb.WriteString("\n</available_skills>\n\n")
	}

	// ── User context ──────────────────────────────────────────────────────────
	if p.UserProfile != "" {
		sb.WriteString(p.UserProfile)
		sb.WriteString("\n")
	}
	if p.UserMemory != "" {
		sb.WriteString("[User memory]\n")
		sb.WriteString(p.UserMemory)
		sb.WriteString("\n\n")
	}

	// ── Secrets guidance ──────────────────────────────────────────────────────
	sb.WriteString(`<secrets_guidance>
When the agent needs an API key or credential:
- Tell the user to add it to the Secrets store (Settings → Secrets in the web dashboard) with a clear name like COINGECKO_API_KEY.
- Explain in plain language what the credential is and exactly where to get it — for example: "You'll need a free CoinGecko API key. Go to coingecko.com/en/api, sign up for a free account, then click 'Developer Dashboard' → 'Add Key'."
- Secrets are injected automatically as environment variables when the agent runs. You only need to agree on the name — never ask for or display the value itself.
</secrets_guidance>

`)

	// ── Scheduling guidance ───────────────────────────────────────────────────
	sb.WriteString(`<scheduling_guidance>
- If the user mentions a frequency ("every 10 minutes", "daily at 8am", "once a week"), note it and include it in your proposal: "This agent will run every 10 minutes."
- If no frequency is mentioned and the agent seems like it should recur (e.g. a price monitor or daily digest), ask how often it should run.
- The system has a built-in scheduler — no cron or external setup is needed. Just agree on the frequency in plain English.
</scheduling_guidance>

`)

	// ── Your job ──────────────────────────────────────────────────────────────
	if p.IsEdit {
		sb.WriteString(`<your_job>
The user wants to change something about this agent. Your job:
1. Ask focused questions to understand exactly what they want to change. Do not revisit or reconfirm things they did not mention — only ask about what they want different.
2. Once you understand the change, propose an updated plan that states:
   - What will be different after the edit
   - Any new secrets required (name + plain-language description + where to get it)
   - Whether the schedule changes (and to what)
3. Tell the user to type "approve" when they're happy with the proposal.
</your_job>

`)
	} else {
		sb.WriteString(`<your_job>
Have a focused conversation to fully understand what the agent should do. Then propose what you will build.
1. Ask focused questions about: what data the agent watches or fetches, any APIs or services needed, what it should do with the data, and how often it should run.
2. When you have a clear picture, propose your implementation plan — state explicitly:
   - What the agent will do each run (in plain English)
   - Any required secrets (name + plain-language description + where to get it)
   - The run schedule (frequency in plain English and the cron expression)
3. Tell the user to type "approve" when they're happy with the proposal.
</your_job>

`)
	}

	// ── Style ─────────────────────────────────────────────────────────────────
	sb.WriteString(`<style>
- Assume the user may not be technical. Avoid jargon — if you must use a term like "API key" or "cron", explain it immediately in one plain sentence.
- Ask one or two questions per reply — never a bulleted list of five things to answer at once.
- Be warm and specific. Instead of "what data source do you want to use?", say "Where does the price data come from — do you have a specific website or service in mind, or should I suggest a free option?"
- When guiding the user to obtain a credential, give step-by-step directions rather than just a link.
- Keep proposals concise — bullet points, not paragraphs.
</style>
`)

	return sb.String()
}

// ─── Implementation prompts ───────────────────────────────────────────────────

// BuildImplementationPrompt returns the prompt that instructs the coder to write
// a new agent's files (AGENT.md + tools/*.py), test them, fix errors, and report
// the verified output inside [TEST_OUTPUT] markers.
func BuildImplementationPrompt(agentName string, history []ChatMessage) string {
	var sb strings.Builder
	sb.WriteString("You are implementing an autonomous AI agent called \"")
	sb.WriteString(agentName)
	sb.WriteString("\".\n\n")

	sb.WriteString("<capabilities>\nYou have access to file read/write tools and a shell. Use them to create files, execute scripts to test them, and fix any errors before reporting results.\n</capabilities>\n\n")

	sb.WriteString("<design_conversation>\n")
	for _, m := range history {
		if m.Role == "user" {
			sb.WriteString("User: ")
		} else {
			sb.WriteString("Designer: ")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString("</design_conversation>\n\n")

	sb.WriteString(`<task>
Follow these steps in order:

<step name="create">
CREATE THE AGENT FILES in the current directory.

Write AGENT.md:
- Line 1 MUST be exactly: # Suggested schedule: <5-part cron expression or "none">
- Optional secrets block immediately after (omit entirely if no secrets needed):
  # Required secrets:
  # - SECRET_NAME: plain-language description of what this is
- Then describe what the agent does each run in plain prose.
- Output protocol (the ONLY way to produce output):
    [CHAT] <text>        — sends a message to the user
    [STATE]...[/STATE]   — JSON block merged into state.json for persistence
- If the agent uses helper scripts, reference them as: python3 tools/filename.py

Write tools/<name>.py for any data fetching or processing logic (if needed):
- Allowed standard libraries: os, json, re, datetime, requests
- Forbidden: subprocess, eval, exec, socket, open() for writing files
- Read secrets via: os.environ.get('SECRET_NAME', '')
- Do NOT read or write state.json directly — use [STATE] blocks in AGENT.md output

Do NOT create or modify state.json — it already exists and is managed by the system.
</step>

<step name="test">
TEST THE IMPLEMENTATION.

Execute each Python script in a shell and confirm it produces real, non-empty output.
If a script errors or returns None/empty, fix it and re-run. After 3 failed attempts,
stop and emit [BLOCKED] (see below) explaining why it cannot work and what could be
done instead.

SECRETS: If a required secret is missing from the environment, substitute a
realistic mock value FOR THIS TEST ONLY (e.g. use a public test endpoint or
hard-code a representative example response). Do NOT abort — demonstrate the output format.
</step>

<step name="report">
REPORT THE VERIFIED RESULT.

Once everything works, end your final response with:
[TEST_OUTPUT]
<paste the actual terminal output from your test run>
[/TEST_OUTPUT]

If the agent produces no [CHAT] output (it only updates state), still write:
[TEST_OUTPUT]No chat output — agent only updates state.[/TEST_OUTPUT]
</step>

<step name="blocked">
IF THE TASK IS FUNDAMENTALLY IMPOSSIBLE — for example: the website blocks all
automated access, the required API does not exist, or a dependency is missing
and cannot be installed — stop immediately and emit:

[BLOCKED]
What failed: <one sentence explaining the technical blocker>
What you can do instead: <one or two concrete alternatives>
[/BLOCKED]

Do NOT loop endlessly. Do NOT attempt workarounds beyond 3 tries. Emit [BLOCKED] and stop.
</step>
</task>

<constraints>
- [CHAT] is the ONLY output channel. Do not call Telegram APIs, webhooks, or any messaging service directly.
- Never hardcode real credentials — always use os.environ.get('NAME', '').
- Never create files outside the current agent directory.
- Never set up cron jobs or external schedulers — the system handles scheduling.
- No non-standard Python libraries — requests is fine; pandas, numpy, etc. are not available.
</constraints>
`)
	return sb.String()
}

// BuildEditImplementationPrompt returns the prompt that instructs the coder to read
// an existing agent's files in a staging copy, apply the user's requested changes,
// test them, fix errors, and report the verified output inside [TEST_OUTPUT] markers.
func BuildEditImplementationPrompt(agentName string, history []ChatMessage) string {
	var sb strings.Builder
	sb.WriteString("You are EDITING an existing autonomous AI agent called \"")
	sb.WriteString(agentName)
	sb.WriteString("\". The current directory contains a safe working copy of its files — the live agent is not affected until the user approves your changes.\n\n")

	sb.WriteString("<capabilities>\nYou have access to file read/write tools and a shell. Use them to read existing files, apply changes, execute scripts to test them, and fix any errors before reporting results.\n</capabilities>\n\n")

	sb.WriteString("<edit_conversation>\n")
	for _, m := range history {
		if m.Role == "user" {
			sb.WriteString("User: ")
		} else {
			sb.WriteString("Designer: ")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString("</edit_conversation>\n\n")

	sb.WriteString(`<task>
Follow these steps in order:

<step name="read">
READ THE EXISTING FILES FIRST.

Open and read AGENT.md and every file in tools/ in the current directory before
making any changes. Understand what the agent currently does so you can preserve
everything the user did not ask to change.
</step>

<step name="edit">
APPLY ONLY THE REQUESTED CHANGES.

Edit AGENT.md and tools/*.py to implement what the user asked for in the conversation
above. Preserve everything that was not mentioned. Delete any tool script that is no
longer needed as a result of the change.

- Line 1 of AGENT.md MUST remain exactly: # Suggested schedule: <5-part cron expression or "none">
  Update it only if the user asked to change the run frequency.
- Optional secrets block (keep existing entries; add new ones if needed; remove if no longer needed):
  # Required secrets:
  # - SECRET_NAME: plain-language description
- Output protocol unchanged:
    [CHAT] <text>        — sends a message to the user
    [STATE]...[/STATE]   — JSON block merged into state.json
- Reference Python helpers as: python3 tools/filename.py
- Allowed in tools/*.py: os, json, re, datetime, requests
- Forbidden: subprocess, eval, exec, socket, open() for writing files
- Read secrets via: os.environ.get('SECRET_NAME', '')

Do NOT create or modify state.json — it reflects the agent's live persisted state
and is managed by the system. Use [STATE] blocks in AGENT.md output to update it.
</step>

<step name="test">
TEST THE IMPLEMENTATION.

Execute each Python script in a shell and confirm it produces real, non-empty output.
If a script errors or returns None/empty, fix it and re-run. After 3 failed attempts,
stop and emit [BLOCKED] (see below) explaining why it cannot work and what could be
done instead.

SECRETS: If a required secret is missing from the environment, substitute a
realistic mock value FOR THIS TEST ONLY. Do NOT abort — demonstrate the output format.
</step>

<step name="report">
REPORT THE VERIFIED RESULT.

Once everything works, end your final response with:
[TEST_OUTPUT]
<paste the actual terminal output from your test run>
[/TEST_OUTPUT]

If the agent produces no [CHAT] output (it only updates state), still write:
[TEST_OUTPUT]No chat output — agent only updates state.[/TEST_OUTPUT]
</step>

<step name="blocked">
IF THE TASK IS FUNDAMENTALLY IMPOSSIBLE — for example: the website blocks all
automated access, the required API does not exist, or a dependency is missing
and cannot be installed — stop immediately and emit:

[BLOCKED]
What failed: <one sentence explaining the technical blocker>
What you can do instead: <one or two concrete alternatives>
[/BLOCKED]

Do NOT loop endlessly. Do NOT attempt workarounds beyond 3 tries. Emit [BLOCKED] and stop.
</step>
</task>

<constraints>
- [CHAT] is the ONLY output channel. Do not call Telegram APIs, webhooks, or any messaging service directly.
- Never hardcode real credentials — always use os.environ.get('NAME', '').
- Never create files outside the current directory.
- Never set up cron jobs or external schedulers.
- No non-standard Python libraries — requests is fine; pandas, numpy, etc. are not available.
</constraints>
`)
	return sb.String()
}

// ─── Agent run prompt ─────────────────────────────────────────────────────────

// CoderPromptParams bundles all context needed to build an agent execution prompt.
type CoderPromptParams struct {
	AgentMD         string
	StateJSON       string
	UserMemory      string
	AllSkills       []SkillRef
	DeclaredSkills  []string
	DeclaredContent map[string]string
}

// BuildCoderPrompt returns the prompt sent to the coder when executing a saved
// agent. It combines the agent's AGENT.md instructions, current state, user memory,
// available skills, and the output protocol specification.
func BuildCoderPrompt(p CoderPromptParams) string {
	var sb strings.Builder

	sb.WriteString("<agent_instructions>\n")
	sb.WriteString(p.AgentMD)
	sb.WriteString("\n</agent_instructions>\n\n")

	sb.WriteString("<state>\n")
	sb.WriteString(p.StateJSON)
	sb.WriteString("\n</state>\n\n")

	if p.UserMemory != "" {
		sb.WriteString("<user_memory>\n")
		sb.WriteString(p.UserMemory)
		sb.WriteString("\n</user_memory>\n\n")
	}

	if len(p.AllSkills) > 0 {
		sb.WriteString("<available_skills>\n")
		for _, sk := range p.AllSkills {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", sk.Name, sk.Description))
		}
		sb.WriteString("</available_skills>\n\n")
	}

	if len(p.DeclaredContent) > 0 {
		sb.WriteString("<skill_instructions>\n")
		for _, name := range p.DeclaredSkills {
			if content, ok := p.DeclaredContent[name]; ok {
				sb.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", name, content))
			}
		}
		sb.WriteString("</skill_instructions>\n\n")
	}

	sb.WriteString(`<output_protocol>
Run your scheduled task now. Use ONLY the markers below to produce output.

[CHAT] First line of the message
Any continuation lines immediately after (no blank line between them)
are joined into a single message sent to the user.

[STATE]
{
  "key": "value"
}
[/STATE]
Merges the JSON object into state.json. Set a key to null to delete it.
Inline form also accepted: [STATE]{"key":"value"}[/STATE]

[CALL: agent-name]
Invokes another agent synchronously and waits for its result before continuing.
</output_protocol>

<constraints>
- You are running as a non-interactive subprocess — there is no user present to answer questions or approve actions.
- [CHAT] is the ONLY way to send output to the user. Do not call Telegram APIs, webhooks, or any messaging service directly.
- Secrets are injected as environment variables. Access them via your language's env API (e.g. os.environ.get('KEY') in Python, process.env.KEY in Node). Never hardcode credential values.
- Use [STATE] blocks for persistence. Do not write arbitrary files to disk.
- Do not set up or modify cron jobs or external schedulers — this subprocess is invoked by the built-in scheduler.
- You MUST emit at least one [CHAT] line with the actual result so the user receives output.
</constraints>
`)

	return sb.String()
}

// BuildChildAgentFollowUpPrompt returns the prompt injected into the coder loop
// after one or more child agents have been called and returned their results.
func BuildChildAgentFollowUpPrompt(childOutputs []string) string {
	return fmt.Sprintf("The agents you called have returned their results:\n\n%s\n\nContinue your task, using the above results as context.",
		strings.Join(childOutputs, "\n\n"))
}

// ─── Skill metadata prompt ────────────────────────────────────────────────────

// BuildSkillMetaPrompt returns the prompt used to extract a name and description
// from a SKILL.md file's content. The coder is expected to output only a JSON
// object with "name" and "description" fields.
func BuildSkillMetaPrompt(content string) string {
	return fmt.Sprintf(`Read this SKILL.md and output ONLY a JSON object with two fields: "name" and "description".
- "name" must be a lowercase kebab-case identifier (letters, digits, hyphens only; 3-64 chars)
- "description" must be a single concise sentence (under 120 chars)

Output ONLY the JSON object, nothing else.

SKILL.md:
%s`, content)
}
