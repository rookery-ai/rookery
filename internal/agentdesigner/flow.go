package agentdesigner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/robfig/cron/v3"
)

// DesignState is the current step in the conversational agent creation wizard.
type DesignState int

const (
	StateIdle       DesignState = iota
	StateDescribing             // Telegram: waiting for description after /agent create <name>
	StateDesigning              // free-form Q&A until user says "approve"
	StateDone
)

func (s DesignState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateDescribing:
		return "describing"
	case StateDesigning:
		return "designing"
	case StateDone:
		return "done"
	}
	return "unknown"
}

// DesignSession holds all state for one in-progress agent creation.
type DesignSession struct {
	UserID             string
	AgentID            string
	AgentName          string
	State              DesignState
	History            []db.ChatMessage // full conversation fed to coder on every turn
	Skills             []string         // installed skill names, loaded once on Start
	ConnectedPlatforms []string         // e.g. ["telegram"] — loaded from platform_connections
	CreatedAt          time.Time
}

type dbDesignStore interface {
	ListSkills(userID string) ([]*db.Skill, error)
	ListUserPlatformConnections(userID string) ([]*db.PlatformConnection, error)
	UpsertAgentSchedule(s *db.AgentSchedule) error
}

// Flow manages per-user design sessions and drives the FSM.
// It is safe for concurrent use.
type Flow struct {
	mu       sync.Mutex
	sessions map[string]*DesignSession // keyed by userID

	coderFor func(userID string) *coder.Coder
	designer *AgentDesigner
	db       dbDesignStore
}

// NewFlow creates a Flow. coderResolver maps a userID to the right coder.
func NewFlow(coderResolver func(userID string) *coder.Coder, designer *AgentDesigner) *Flow {
	return &Flow{
		sessions: make(map[string]*DesignSession),
		coderFor: coderResolver,
		designer: designer,
	}
}

// WithDB attaches a database handle so the Flow can list skills, query connected platforms,
// and create schedules during agent creation.
func (f *Flow) WithDB(database dbDesignStore) *Flow {
	f.db = database
	return f
}

// Start creates a new Telegram design session for userID.
// Returns the opening prompt asking for a description.
func (f *Flow) Start(userID, agentName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.sessions[userID]; exists {
		return "", fmt.Errorf("you already have an active design session; send /agent cancel to start over")
	}

	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	f.sessions[userID] = &DesignSession{
		UserID:             userID,
		AgentID:            uuid.New().String(),
		AgentName:          agentName,
		State:              StateDescribing,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		CreatedAt:          time.Now(),
	}

	return fmt.Sprintf(
		"Starting agent \"%s\".\n\nDescribe what this agent should do. Be specific: what should it monitor or fetch, what actions should it take, and what should it output?",
		agentName,
	), nil
}

// StartDesign is the web path: creates a session already in StateDesigning
// with the user's first message and returns the coder's first response.
func (f *Flow) StartDesign(ctx context.Context, userID, agentName, firstMessage string) (string, error) {
	f.mu.Lock()

	if _, exists := f.sessions[userID]; exists {
		f.mu.Unlock()
		return "", fmt.Errorf("design session already active; cancel it first")
	}

	skills := f.loadSkillNames(userID)
	platforms := f.loadConnectedPlatforms(userID)
	sess := &DesignSession{
		UserID:             userID,
		AgentID:            uuid.New().String(),
		AgentName:          agentName,
		State:              StateDesigning,
		Skills:             skills,
		ConnectedPlatforms: platforms,
		CreatedAt:          time.Now(),
	}
	f.sessions[userID] = sess
	f.mu.Unlock()

	return f.callCoder(ctx, userID, firstMessage)
}

// Step processes one message and advances the FSM.
// Returns (response, isDone, agentID, err).
// agentID is non-empty only when isDone=true.
func (f *Flow) Step(ctx context.Context, userID, input string) (string, bool, string, error) {
	f.mu.Lock()
	sess, ok := f.sessions[userID]
	if !ok {
		f.mu.Unlock()
		return "", false, "", fmt.Errorf("no active design session; use /agent create <name> to start one")
	}
	state := sess.State
	f.mu.Unlock()

	switch state {
	case StateDescribing:
		return f.stepDescribing(ctx, userID, input)
	case StateDesigning:
		return f.stepDesigning(ctx, userID, input)
	default:
		return "", false, "", fmt.Errorf("unexpected state: %s", state)
	}
}

// Cancel removes the user's active session without saving.
func (f *Flow) Cancel(userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, userID)
}

// GetSession returns the user's active session, or nil.
func (f *Flow) GetSession(userID string) *DesignSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[userID]
}

// ─── FSM step handlers ────────────────────────────────────────────────────────

// stepDescribing (Telegram only): user sends their first description.
func (f *Flow) stepDescribing(ctx context.Context, userID, description string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	sess.State = StateDesigning
	f.mu.Unlock()

	response, err := f.callCoder(ctx, userID, description)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// stepDesigning: free-form Q&A until "approve".
func (f *Flow) stepDesigning(ctx context.Context, userID, input string) (string, bool, string, error) {
	if isApproval(input) {
		return f.runGeneration(ctx, userID)
	}

	response, err := f.callCoder(ctx, userID, input)
	if err != nil {
		return "", false, "", err
	}
	return response, false, "", nil
}

// ─── Coder conversation ───────────────────────────────────────────────────────

// callCoder sends a conversational turn to the coder and appends to session history.
func (f *Flow) callCoder(ctx context.Context, userID, userMessage string) (string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	coderSvc := f.coderFor(userID)
	f.mu.Unlock()

	if coderSvc == nil {
		return "", fmt.Errorf("no coder configured for this user")
	}

	systemPrompt := f.buildSystemPrompt(sess)

	// Use WithNoTools so the design conversation outputs plain text and never
	// attempts to write files or request permissions.
	result, err := coderSvc.WithNoTools().Chat(ctx, userID, sess.History, systemPrompt, userMessage)
	if err != nil {
		return "", fmt.Errorf("coder: %w", err)
	}

	f.mu.Lock()
	sess.History = append(sess.History,
		db.ChatMessage{Role: "user", Content: userMessage},
		db.ChatMessage{Role: "assistant", Content: result.Text},
	)
	f.mu.Unlock()

	return result.Text, nil
}

func (f *Flow) buildSystemPrompt(sess *DesignSession) string {
	var sb strings.Builder
	sb.WriteString("You are a friendly agent design assistant helping build an autonomous AI agent called \"")
	sb.WriteString(sess.AgentName)
	sb.WriteString("\".\n\n")

	// Connected platform context.
	if len(sess.ConnectedPlatforms) > 0 {
		sb.WriteString("CONNECTED PLATFORMS:\n")
		sb.WriteString("The user has connected: ")
		sb.WriteString(strings.Join(sess.ConnectedPlatforms, ", "))
		sb.WriteString(`
When the user says "send to Telegram", "notify me", "post a message", or similar, they mean: emit [CHAT] text in your output — the system automatically routes it to their connected platform. No bot token, chat ID, or messaging credentials of any kind are needed or should be requested.

`)
	} else {
		sb.WriteString(`CONNECTED PLATFORMS:
No chat platform is currently connected. If the agent needs to send notifications, mention that the user can connect Telegram from Settings → Connectors in the web dashboard. Agents still use [CHAT] output for this — no credentials needed from the user.

`)
	}

	if len(sess.Skills) > 0 {
		sb.WriteString("INSTALLED SKILLS (can be used by this agent): ")
		sb.WriteString(strings.Join(sess.Skills, ", "))
		sb.WriteString("\n\n")
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

// ─── Generation (triggered by approval) ──────────────────────────────────────

func (f *Flow) runGeneration(ctx context.Context, userID string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[userID]
	coderSvc := f.coderFor(userID)
	// Snapshot session fields under the lock so we don't hold it during LLM calls.
	agentIDSnap := sess.AgentID
	agentNameSnap := sess.AgentName
	skillsSnap := sess.Skills
	historySnap := make([]db.ChatMessage, len(sess.History))
	copy(historySnap, sess.History)
	f.mu.Unlock()

	if coderSvc == nil {
		return "", false, "", fmt.Errorf("no coder configured for this user")
	}

	generationPrompt := `Based on our conversation, implement the agent now.

REQUIRED CONSTRAINTS:
1. The VERY FIRST line of AGENT.md must be the suggested schedule:
   # Suggested schedule: */10 * * * *
   (Replace with the cron expression we discussed. Use "none" if this agent runs on-demand only.)
2. Do NOT include instructions to acquire Telegram credentials, write files to disk, or set up cron jobs.
3. Secrets are injected as environment variables at runtime (e.g. os.environ['COINGECKO_API_KEY'] in Python). NEVER hardcode values or include instructions to obtain them at runtime — the user adds them to the Secrets store separately.
4. [CHAT] is the only way to send messages. No messaging libraries or Telegram API calls.
5. Tool scripts live in tools/ — reference them as tools/filename.py. Do NOT instruct the agent to create or write files.

Output the following — in this exact order, with no markdown fences:

1. The complete AGENT.md file. This is the instruction file the coder reads on every scheduled run.
   Line 1: # Suggested schedule: <cron expression or "none">
   Then: a comment block listing required secrets (omit if none):
   # Required secrets:
   # - SECRET_NAME: what this is and where to get it

   AGENT.md must describe:
   - What the agent does on each run
   - Output protocol: [CHAT] <text> to send messages, [STATE]...[/STATE] to persist JSON state, [CALL: agent-name] to call another agent
   - How to use any tool scripts in tools/ (reference as tools/filename.py)
   - Environment variables it reads (os.environ['NAME'])

2. If Python helper scripts are needed for data fetching or reusable logic, output each as:
   [TOOL: filename.py]
   (complete script — no subprocess, no eval, no exec, no socket)
   [/TOOL]
   These will be placed in tools/ automatically.

Output raw text only. No markdown code fences. No explanations outside the files above.`

	// Build a single prompt that embeds the full conversation history so claude
	// sees this as a text-output task, not an interactive conversation where it
	// might try to write files. WithNoTools() ensures no file tools are available.
	var genPrompt strings.Builder
	genPrompt.WriteString("Design conversation for agent \"")
	genPrompt.WriteString(agentNameSnap)
	genPrompt.WriteString("\":\n\n")
	for _, m := range historySnap {
		if m.Role == "user" {
			genPrompt.WriteString("User: ")
		} else {
			genPrompt.WriteString("Designer: ")
		}
		genPrompt.WriteString(m.Content)
		genPrompt.WriteString("\n\n")
	}
	genPrompt.WriteString("---\n\n")
	genPrompt.WriteString(generationPrompt)

	result, err := coderSvc.WithNoTools().Generate(ctx, userID, genPrompt.String())
	if err != nil {
		return "", false, "", fmt.Errorf("generate agent: %w", err)
	}

	agentMD, tools := parseGeneratedOutput(result.Text)
	agentMD = strings.TrimSpace(agentMD)

	if agentMD == "" {
		f.mu.Lock()
		delete(f.sessions, userID)
		f.mu.Unlock()
		return "", false, "", fmt.Errorf("coder returned empty AGENT.md — please try again")
	}

	// Run guardrails.
	if err := CheckEthics(agentMD, ""); err != nil {
		return fmt.Sprintf("Generated agent failed safety checks: %s\n\nPlease rephrase your request.", err.Error()),
			false, "", nil
	}
	for filename, code := range tools {
		if err := RunFullGuardrails(code, ""); err != nil {
			return fmt.Sprintf("Generated tool %s failed safety checks: %s\n\nPlease rephrase.", filename, err.Error()),
				false, "", nil
		}
	}

	requiredSecrets := parseRequiredSecrets(agentMD)
	description := extractDescription(agentMD, agentNameSnap)

	if err := f.designer.SaveAgent(userID, agentIDSnap, agentNameSnap, description, agentMD, tools, skillsSnap, requiredSecrets); err != nil {
		return "", false, "", fmt.Errorf("save agent: %w", err)
	}

	// Auto-create schedule if coder embedded a suggested cron expression.
	scheduleMsg := ""
	if cronExpr := parseSuggestedSchedule(agentMD); cronExpr != "" && f.db != nil {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if sched, err := parser.Parse(cronExpr); err == nil {
			nextRun := sched.Next(time.Now())
			_ = f.db.UpsertAgentSchedule(&db.AgentSchedule{
				ID:        uuid.New().String(),
				AgentID:   agentIDSnap,
				UserID:    userID,
				CronExpr:  cronExpr,
				NextRunAt: &nextRun,
				Enabled:   true,
			})
			scheduleMsg = fmt.Sprintf(" Schedule set: %s.", cronExpr)
		}
	}

	f.mu.Lock()
	delete(f.sessions, userID)
	f.mu.Unlock()

	return fmt.Sprintf(
		"Agent \"%s\" created!%s Use /run %s to test it manually.",
		agentNameSnap, scheduleMsg, agentNameSnap,
	), true, agentIDSnap, nil
}

// parseSuggestedSchedule reads the first line of AGENT.md for "# Suggested schedule: <cron>".
// Returns "" if the line is missing, "none", or the expression fails validation.
func parseSuggestedSchedule(agentMD string) string {
	first := strings.SplitN(strings.TrimSpace(agentMD), "\n", 2)[0]
	after, ok := strings.CutPrefix(strings.TrimSpace(first), "# Suggested schedule:")
	if !ok {
		return ""
	}
	expr := strings.TrimSpace(after)
	if expr == "" || strings.EqualFold(expr, "none") {
		return ""
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(expr); err != nil {
		return ""
	}
	return expr
}

// ─── Output parsing ───────────────────────────────────────────────────────────

// parseGeneratedOutput splits coder output into AGENT.md content and tool scripts.
func parseGeneratedOutput(text string) (agentMD string, tools map[string]string) {
	tools = make(map[string]string)

	firstTool := strings.Index(text, "[TOOL:")
	if firstTool < 0 {
		return text, tools
	}

	agentMD = text[:firstTool]

	rest := text[firstTool:]
	for {
		start := strings.Index(rest, "[TOOL:")
		if start < 0 {
			break
		}
		headerEnd := strings.Index(rest[start:], "]")
		if headerEnd < 0 {
			break
		}
		header := rest[start : start+headerEnd+1]
		filename := strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(header, "]"), "[TOOL:"))

		contentStart := start + headerEnd + 1
		endMarker := strings.Index(rest[contentStart:], "[/TOOL]")
		if endMarker < 0 {
			break
		}

		content := rest[contentStart : contentStart+endMarker]
		if filename != "" {
			tools[filename] = strings.TrimSpace(content)
		}

		rest = rest[contentStart+endMarker+len("[/TOOL]"):]
	}

	return agentMD, tools
}

// parseRequiredSecrets extracts SECRET_NAME from "# - SECRET_NAME: description" header lines.
func parseRequiredSecrets(agentMD string) []string {
	var secrets []string
	inBlock := false
	for _, line := range strings.Split(agentMD, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "# Required secrets:" {
			inBlock = true
			continue
		}
		if inBlock {
			if !strings.HasPrefix(trimmed, "# -") {
				break
			}
			after := strings.TrimPrefix(trimmed, "# -")
			after = strings.TrimSpace(after)
			if idx := strings.Index(after, ":"); idx > 0 {
				name := strings.TrimSpace(after[:idx])
				if name != "" {
					secrets = append(secrets, name)
				}
			}
		}
	}
	return secrets
}

// extractDescription builds a one-line description from the AGENT.md content.
func extractDescription(agentMD, fallback string) string {
	for _, line := range strings.Split(agentMD, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 200 {
			line = line[:200] + "..."
		}
		return line
	}
	return fallback
}

// ─── Approval detection ───────────────────────────────────────────────────────

// isApproval matches only explicit approval phrases that cannot arise naturally
// in Q&A answers. "yes", "ok", "sure" are deliberately excluded.
func isApproval(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	for _, trigger := range []string{"approve", "go ahead", "build it", "create it", "/approve"} {
		if lower == trigger {
			return true
		}
	}
	return false
}

// ─── Skills helpers ───────────────────────────────────────────────────────────

func (f *Flow) loadSkillNames(userID string) []string {
	if f.db == nil {
		return nil
	}
	skills, _ := f.db.ListSkills(userID)
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, s.Name)
	}
	return names
}

func (f *Flow) loadConnectedPlatforms(userID string) []string {
	if f.db == nil {
		return nil
	}
	conns, _ := f.db.ListUserPlatformConnections(userID)
	out := make([]string, 0, len(conns))
	for _, c := range conns {
		out = append(out, c.Platform)
	}
	return out
}

// ─── Text helpers ─────────────────────────────────────────────────────────────

// cleanCodeFence strips leading/trailing markdown code fences from LLM output.
func cleanCodeFence(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"```python", "```"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// codePreview returns at most maxLines lines with a truncation marker.
func codePreview(code string, maxLines int) string {
	lines := strings.Split(code, "\n")
	if len(lines) <= maxLines {
		return code
	}
	return strings.Join(lines[:maxLines], "\n") + "\n# ... (truncated)"
}
