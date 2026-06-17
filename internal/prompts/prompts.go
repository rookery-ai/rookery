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
	if p.IsEdit {
		sb.WriteString("You are a friendly agent design assistant helping the user EDIT an existing autonomous AI agent called \"")
		sb.WriteString(p.AgentName)
		sb.WriteString("\".\n\nHere is its current AGENT.md:\n```\n")
		sb.WriteString(p.ExistingAgentMD)
		sb.WriteString("\n```\n\nFind out exactly what the user wants to change, then propose an updated plan (same format as a new build: behavior, secrets, schedule). Only ask about parts that are actually changing — don't re-litigate things the user didn't mention.\n\n")
	} else {
		sb.WriteString("You are a friendly agent design assistant helping build an autonomous AI agent called \"")
		sb.WriteString(p.AgentName)
		sb.WriteString("\".\n\n")
	}

	if len(p.ConnectedPlatforms) > 0 {
		sb.WriteString("CONNECTED PLATFORMS:\n")
		sb.WriteString("The user has connected: ")
		sb.WriteString(strings.Join(p.ConnectedPlatforms, ", "))
		sb.WriteString(`
When the user says "send to Telegram", "notify me", "post a message", or similar, they mean: emit [CHAT] text in your output — the system automatically routes it to their connected platform. No bot token, chat ID, or messaging credentials of any kind are needed or should be requested.

`)
	} else {
		sb.WriteString(`CONNECTED PLATFORMS:
No chat platform is currently connected. If the agent needs to send notifications, mention that the user can connect Telegram from Settings → Connectors in the web dashboard. Agents still use [CHAT] output for this — no credentials needed from the user.

`)
	}

	if len(p.Skills) > 0 {
		sb.WriteString("INSTALLED SKILLS (can be used by this agent): ")
		sb.WriteString(strings.Join(p.Skills, ", "))
		sb.WriteString("\n\n")
	}

	if p.UserProfile != "" {
		sb.WriteString(p.UserProfile)
		sb.WriteString("\n")
	}

	if p.UserMemory != "" {
		sb.WriteString("[User memory]\n")
		sb.WriteString(p.UserMemory)
		sb.WriteString("\n")
	}

	sb.WriteString(`SECRETS (API keys and credentials):
When the agent needs an API key or credential:
- Tell the user to add it to the Secrets store in the web dashboard (Settings → Secrets) using a clear name like COINGECKO_API_KEY.
- Do NOT ask the user to paste secret values in this chat — that would expose them.
- Explain in plain language what the credential is and where to get it (e.g. "You'll need a free CoinGecko API key — sign up at coingecko.com/en/api and generate one under Developer Dashboard").
- Secrets are injected automatically as environment variables when the agent runs. Reference them by name only (e.g. $COINGECKO_API_KEY). Never display values.

SCHEDULING:
- If the user mentions a frequency ("every 10 minutes", "daily at 8am", "once a week"), note it and include the schedule in your proposal: "This agent will run every 10 minutes (cron: */10 * * * *)."
- If no frequency is mentioned and the agent seems like it should recur (e.g. a price monitor), ask the user how often it should run before proposing.
- Do NOT suggest cron jobs, systemd timers, or any external scheduling — the system has a built-in scheduler.

YOUR JOB:
Have a focused conversation to fully understand what the agent should do. Then propose what you will build.
1. Ask focused questions: data sources, any APIs needed, what to do with the data, frequency.
2. When you have a clear picture, propose your implementation plan and explicitly list:
   - What the agent will do each run
   - Any required secrets (name + plain-language explanation + where to get it)
   - The run schedule (frequency and cron expression)
3. Tell the user to type "approve" when they're happy with the proposal.

STYLE:
- Assume the user may not be technical. Explain API keys, environment variables, and cron expressions in plain language when they come up. Do not use jargon without explanation.
- Ask one or two focused questions per turn — not a list of ten.
- Guide the user through setup steps where needed (e.g. "go to coingecko.com/en/api, click 'Get Free API Key'...").
- Do not write code or generate files yet — that happens after approval.

HARD CONSTRAINTS (never violate):
- Never ask for Telegram bot token, chat ID, or any messaging credentials.
- Never suggest writing files to the home directory or any disk path.
- Never suggest cron job setup or any external scheduling tool.
- Always use [CHAT] as the only way to send messages to the user.
`)

	return sb.String()
}

// ─── Implementation prompts ───────────────────────────────────────────────────

// BuildImplementationPrompt returns the prompt that instructs the coder to write
// a new agent's files (AGENT.md + tools/*.py), test them, fix errors, and report
// the verified output inside [TEST_OUTPUT] markers.
func BuildImplementationPrompt(agentName string, history []ChatMessage) string {
	var sb strings.Builder
	sb.WriteString("You are implementing an AI agent called \"")
	sb.WriteString(agentName)
	sb.WriteString("\".\n\n")
	sb.WriteString("DESIGN CONVERSATION:\n")
	for _, m := range history {
		if m.Role == "user" {
			sb.WriteString("User: ")
		} else {
			sb.WriteString("Designer: ")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString(`
YOUR TASK — follow these steps in order:

STEP 1 — CREATE THE AGENT FILES in the current directory.

Write AGENT.md:
- Line 1 MUST be exactly: # Suggested schedule: <5-part cron expression or "none">
- Optional secrets block (omit if no secrets needed):
  # Required secrets:
  # - SECRET_NAME: plain-language description
- Describe what the agent does each run
- Output protocol:
    [CHAT] <text>        — send a message to the user (the ONLY way to send output)
    [STATE]...[/STATE]   — JSON block merged into state.json
- Reference Python helpers as: python3 tools/filename.py

Write tools/<name>.py for data fetching / processing (if needed):
- Allowed: os, json, re, datetime, requests
- Forbidden: subprocess, eval, exec, socket, open() for writing files
- Read secrets via: os.environ.get('SECRET_NAME', '')

Do NOT create or modify state.json — it already exists.

STEP 2 — TEST THE IMPLEMENTATION.

Run each Python script using Bash and confirm it produces real, non-empty output.
If a script errors or returns None/empty, fix it and re-run until it works.

SECRETS: If a required secret is missing from the environment, substitute a
realistic mock value FOR THIS TEST ONLY (e.g. use a public test endpoint or
hard-code an example response). Do NOT abort — show the output format.

STEP 3 — REPORT THE VERIFIED RESULT.

Once everything works, end your final response with:
[TEST_OUTPUT]
<paste the actual terminal output from your test run>
[/TEST_OUTPUT]

HARD CONSTRAINTS — never violate:
- [CHAT] is the ONLY output channel. No Telegram API, no requests to messaging services.
- Never hardcode real credentials — always os.environ.get('NAME', '').
- Never create files outside the agent directory.
- Never set up cron jobs or external schedulers.
- No non-standard Python libraries (requests is fine; pandas, numpy, etc. are not).
`)
	return sb.String()
}

// BuildEditImplementationPrompt returns the prompt that instructs the coder to read
// an existing agent's files in a staging copy, apply the user's requested changes,
// test them, fix errors, and report the verified output inside [TEST_OUTPUT] markers.
func BuildEditImplementationPrompt(agentName string, history []ChatMessage) string {
	var sb strings.Builder
	sb.WriteString("You are EDITING an existing AI agent called \"")
	sb.WriteString(agentName)
	sb.WriteString("\". The current directory contains its existing AGENT.md and tools/*.py — this is a safe working copy, not the live agent.\n\n")
	sb.WriteString("EDIT CONVERSATION (what the user wants changed):\n")
	for _, m := range history {
		if m.Role == "user" {
			sb.WriteString("User: ")
		} else {
			sb.WriteString("Designer: ")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString(`
YOUR TASK — follow these steps in order:

STEP 0 — READ THE EXISTING FILES.

Read AGENT.md and every file in tools/ in the current directory to understand what
the agent currently does before changing anything.

STEP 1 — APPLY THE REQUESTED CHANGES.

Edit AGENT.md and tools/*.py to implement what the user asked for in the conversation
above. Keep everything that wasn't asked to change. Delete any tool script that's no
longer needed as a result of the change.

- Line 1 of AGENT.md MUST remain exactly: # Suggested schedule: <5-part cron expression or "none">
  (update it only if the user asked to change how often the agent runs)
- Optional secrets block (omit if no secrets needed):
  # Required secrets:
  # - SECRET_NAME: plain-language description
- Output protocol unchanged:
    [CHAT] <text>        — send a message to the user (the ONLY way to send output)
    [STATE]...[/STATE]   — JSON block merged into state.json
- Reference Python helpers as: python3 tools/filename.py
- Allowed in tools/*.py: os, json, re, datetime, requests
- Forbidden: subprocess, eval, exec, socket, open() for writing files
- Read secrets via: os.environ.get('SECRET_NAME', '')

Do NOT create or modify state.json directly — it already exists and reflects the
agent's real persisted state; let the output protocol's [STATE] block manage it.

STEP 2 — TEST THE IMPLEMENTATION.

Run each Python script using Bash and confirm it produces real, non-empty output.
If a script errors or returns None/empty, fix it and re-run until it works.

SECRETS: If a required secret is missing from the environment, substitute a
realistic mock value FOR THIS TEST ONLY (e.g. use a public test endpoint or
hard-code an example response). Do NOT abort — show the output format.

STEP 3 — REPORT THE VERIFIED RESULT.

Once everything works, end your final response with:
[TEST_OUTPUT]
<paste the actual terminal output from your test run>
[/TEST_OUTPUT]

HARD CONSTRAINTS — never violate:
- [CHAT] is the ONLY output channel. No Telegram API, no requests to messaging services.
- Never hardcode real credentials — always os.environ.get('NAME', '').
- Never create files outside the current directory.
- Never set up cron jobs or external schedulers.
- No non-standard Python libraries (requests is fine; pandas, numpy, etc. are not).
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

	sb.WriteString("[Agent Instructions]\n")
	sb.WriteString(p.AgentMD)
	sb.WriteString("\n\n")

	sb.WriteString("[Current State]\n")
	sb.WriteString(p.StateJSON)
	sb.WriteString("\n\n")

	if p.UserMemory != "" {
		sb.WriteString("[User memory]\n")
		sb.WriteString(p.UserMemory)
		sb.WriteString("\n\n")
	}

	if len(p.AllSkills) > 0 {
		sb.WriteString("[Available Skills]\n")
		for _, sk := range p.AllSkills {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", sk.Name, sk.Description))
		}
		sb.WriteString("\n")
	}

	if len(p.DeclaredContent) > 0 {
		sb.WriteString("[Full Skill Instructions]\n")
		for _, name := range p.DeclaredSkills {
			if content, ok := p.DeclaredContent[name]; ok {
				sb.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", name, content))
			}
		}
	}

	sb.WriteString(`Run your scheduled task. Use ONLY these output markers:

[CHAT] First line of the message
Any continuation lines immediately after (no blank line)
are joined into the same message sent to the user.

[STATE]
{
  "key": "value"
}
[/STATE]

  Merges JSON into state.json. Use null to delete a key.
  Can also be written inline: [STATE]{"key":"value"}[/STATE]

[CALL: agent-name]   — invoke another agent synchronously

CONSTRAINTS — this process runs non-interactively as a subprocess:
- [CHAT] is the ONLY way to send messages. Do NOT call Telegram APIs or any messaging service directly.
- Secrets are injected as environment variables (e.g. os.environ['API_KEY']). Do NOT hardcode credential values.
- Use [STATE] for persistence. Do NOT write arbitrary files to disk.
- Do NOT set up or modify cron jobs or external schedulers — this subprocess is invoked by the scheduler.
- There is no interactive user present. Never prompt for input or request permissions.
- You MUST emit at least one [CHAT] line with the actual result so the user receives a message.`)

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
