// Package agentrunner loads an agent from disk and executes it via the coder CLI.
// Output [CHAT] lines are routed back to the user.
package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

const (
	maxCallDepth = 3 // maximum agent-to-agent call depth
	maxTurns     = 5 // maximum coder.Generate calls per top-level run
	maxStateSize = 65536
)

// SendFunc delivers a message back to the user's chat platform.
type SendFunc func(msg string)

// RunInput describes one agent execution request.
type RunInput struct {
	AgentID    string
	UserID     string
	Trigger    string // "chat", "cron", "manual"
	MasterPw   string // user's master password for secret decryption
	SendOutput SendFunc
	depth      int            // internal: recursion depth (0 = top-level)
	visited    map[string]bool // internal: cycle detection for agent-to-agent calls
}

// Runner executes agents.
type Runner struct {
	db        *db.DB
	systemKey []byte
	agentsDir string
	homesDir  string // per-user home dirs root (for per-user sandbox binding)
	dataDir   string // root data dir (blacklisted inside sandbox)
	coderSvc  *coder.Coder
	skillsDir string // root dir for per-user skills: <dataDir>/skills
}

// New creates a Runner.
func New(database *db.DB, systemKey []byte, agentsDir, homesDir, dataDir string, coderSvc *coder.Coder, skillsDir string) *Runner {
	return &Runner{
		db:        database,
		systemKey: systemKey,
		agentsDir: agentsDir,
		homesDir:  homesDir,
		dataDir:   dataDir,
		coderSvc:  coderSvc,
		skillsDir: skillsDir,
	}
}

// Run executes the agent identified by input.AgentID.
func (r *Runner) Run(ctx context.Context, input RunInput) error {
	if input.visited == nil {
		input.visited = make(map[string]bool)
	}

	agent, err := r.db.GetAgent(input.AgentID)
	if err != nil {
		return fmt.Errorf("load agent: %w", err)
	}
	if agent.UserID != input.UserID {
		return fmt.Errorf("agent not found")
	}

	manifest, _ := agentdesigner.LoadManifest(r.agentsDir, input.UserID, input.AgentID)
	if manifest == nil {
		manifest = &agentdesigner.AgentManifest{ID: agent.ID, Name: agent.Name}
	}

	return r.runCoderAgent(ctx, agent, manifest, input)
}

// RunByName looks up an agent by name and runs it.
func (r *Runner) RunByName(ctx context.Context, userID, agentName, masterPw string, send SendFunc) error {
	agent, err := r.db.GetAgentByName(userID, agentName)
	if err != nil {
		return fmt.Errorf("agent %q not found", agentName)
	}
	return r.Run(ctx, RunInput{
		AgentID:    agent.ID,
		UserID:     userID,
		Trigger:    "chat",
		MasterPw:   masterPw,
		SendOutput: send,
	})
}

// ─── Coder agent execution ────────────────────────────────────────────────────

// coderRunContext tracks mutable state across the turns of one top-level run.
type coderRunContext struct {
	turnsUsed int
	chatLines []string
	warnings  []string
}

func (r *Runner) runCoderAgent(ctx context.Context, agent *db.Agent, manifest *agentdesigner.AgentManifest, input RunInput) error {
	agentDir := filepath.Join(r.agentsDir, input.UserID, input.AgentID)

	// Read AGENT.md instructions (fall back to CLAUDE.md for legacy agents).
	agentMD, err := os.ReadFile(agentdesigner.AgentDescPath(r.agentsDir, input.UserID, input.AgentID))
	if err != nil {
		legacyPath := filepath.Join(agentDir, "CLAUDE.md")
		agentMD, err = os.ReadFile(legacyPath)
		if err != nil {
			return fmt.Errorf("read AGENT.md (and CLAUDE.md fallback): %w", err)
		}
	}

	// Read state (default empty object).
	stateRaw, err := os.ReadFile(filepath.Join(agentDir, "state.json"))
	if err != nil {
		stateRaw = []byte("{}")
	}

	// Load skills context.
	allSkills, _ := r.db.ListSkills(input.UserID)
	declaredContent, _ := r.loadDeclaredSkillContent(input.UserID, manifest.Skills)

	prompt := buildCoderPrompt(string(agentMD), string(stateRaw), allSkills, manifest.Skills, declaredContent)

	// Inject user secrets as env vars when master password is available.
	coderSvc := r.coderSvc
	if input.MasterPw != "" && coderSvc != nil {
		if user, err := r.db.GetUserByID(input.UserID); err == nil {
			svc := secrets.New(r.db, input.UserID, input.MasterPw, user.SecretsSalt)
			if allSecrets, err := svc.GetAll(ctx); err == nil && len(allSecrets) > 0 {
				coderSvc = coderSvc.WithExtraEnv(allSecrets)
			}
		}
	}

	runID := uuid.New().String()
	if err := r.db.CreateAgentRun(&db.AgentRun{
		ID: runID, AgentID: input.AgentID,
		UserID: input.UserID, Trigger: input.Trigger,
	}); err != nil {
		return fmt.Errorf("create run record: %w", err)
	}

	rctx := &coderRunContext{}

	if err := r.runCoderTurns(ctx, agent, manifest, input, agentDir, stateRaw, prompt, coderSvc, rctx); err != nil {
		_ = r.db.FinishAgentRun(runID, -1, strings.Join(rctx.chatLines, "\n"), strings.Join(rctx.warnings, "\n")+"\n"+err.Error())
		return err
	}

	_ = r.db.FinishAgentRun(runID, 0,
		strings.Join(rctx.chatLines, "\n"),
		strings.Join(rctx.warnings, "\n"))

	if input.SendOutput != nil && len(rctx.chatLines) > 0 {
		input.SendOutput(strings.Join(rctx.chatLines, "\n"))
	}

	return nil
}

// runCoderTurns drives the multi-turn coder loop for one agent invocation.
func (r *Runner) runCoderTurns(
	ctx context.Context,
	agent *db.Agent,
	manifest *agentdesigner.AgentManifest,
	input RunInput,
	agentDir string,
	stateRaw []byte,
	initialPrompt string,
	coderSvc *coder.Coder,
	rctx *coderRunContext,
) error {
	if coderSvc == nil {
		return fmt.Errorf("no coder service configured for md/hybrid agents")
	}

	// Load current state so we can merge updates.
	var currentState map[string]interface{}
	_ = json.Unmarshal(stateRaw, &currentState)
	if currentState == nil {
		currentState = make(map[string]interface{})
	}

	prompt := initialPrompt

	for {
		if rctx.turnsUsed >= maxTurns {
			rctx.warnings = append(rctx.warnings, fmt.Sprintf("max turns (%d) reached", maxTurns))
			break
		}

		result, err := coderSvc.Generate(ctx, input.UserID, prompt)
		rctx.turnsUsed++
		if err != nil {
			return fmt.Errorf("coder generate: %w", err)
		}

		parsed := parseCoderOutput(result.Text)
		rctx.chatLines = append(rctx.chatLines, parsed.chatLines...)
		rctx.warnings = append(rctx.warnings, parsed.warnings...)

		// Merge state updates.
		if len(parsed.stateUpdates) > 0 {
			for _, update := range parsed.stateUpdates {
				mergeState(currentState, update)
			}
			if err := saveState(agentDir, currentState); err != nil {
				rctx.warnings = append(rctx.warnings, "state save failed: "+err.Error())
			}
		}

		// Write raw output to timestamped log (all runs kept).
		writeRunLog(agentDir, result.Text, time.Now())

		// Handle agent-to-agent calls.
		if len(parsed.callAgents) == 0 {
			break
		}

		var childOutputs []string
		for _, callName := range parsed.callAgents {
			if input.depth >= maxCallDepth {
				warn := fmt.Sprintf("skipping [CALL: %s]: max depth (%d) reached", callName, maxCallDepth)
				rctx.warnings = append(rctx.warnings, warn)
				slog.Warn("agentrunner: "+warn, "agent_id", agent.ID)
				continue
			}
			if input.visited[callName] {
				warn := fmt.Sprintf("skipping [CALL: %s]: cycle detected", callName)
				rctx.warnings = append(rctx.warnings, warn)
				slog.Warn("agentrunner: "+warn, "agent_id", agent.ID)
				continue
			}

			child, err := r.db.GetAgentByName(input.UserID, callName)
			if err != nil {
				warn := fmt.Sprintf("skipping [CALL: %s]: agent not found", callName)
				rctx.warnings = append(rctx.warnings, warn)
				slog.Warn("agentrunner: "+warn, "agent_id", agent.ID)
				continue
			}

			var childChat []string
			newVisited := make(map[string]bool)
			for k, v := range input.visited {
				newVisited[k] = v
			}
			newVisited[agent.Name] = true

			childInput := RunInput{
				AgentID:  child.ID,
				UserID:   input.UserID,
				Trigger:  input.Trigger,
				MasterPw: input.MasterPw,
				SendOutput: func(msg string) {
					childChat = append(childChat, msg)
				},
				depth:   input.depth + 1,
				visited: newVisited,
			}

			slog.Info("agentrunner: calling child agent", "parent", agent.Name, "child", callName, "depth", input.depth+1)
			if err := r.Run(ctx, childInput); err != nil {
				warn := fmt.Sprintf("child agent %s failed: %v", callName, err)
				rctx.warnings = append(rctx.warnings, warn)
			}

			if len(childChat) > 0 {
				childOutputs = append(childOutputs,
					fmt.Sprintf("=== Agent %q responded ===\n%s", callName, strings.Join(childChat, "\n")))
			}
		}

		if len(childOutputs) == 0 {
			break
		}

		// Build follow-up prompt injecting child results.
		prompt = fmt.Sprintf("The agents you called have returned their results:\n\n%s\n\nContinue your task, using the above results as context.",
			strings.Join(childOutputs, "\n\n"))
	}

	return nil
}

// ─── Output parsing ───────────────────────────────────────────────────────────

type parsedOutput struct {
	chatLines    []string
	stateUpdates []map[string]interface{}
	callAgents   []string
	warnings     []string
}

// parseCoderOutput scans the coder's text response for protocol markers.
func parseCoderOutput(text string) parsedOutput {
	var out parsedOutput
	lines := strings.Split(text, "\n")

	var stateAcc strings.Builder
	inState := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inState && trimmed == "[STATE]" {
			inState = true
			stateAcc.Reset()
			continue
		}

		if inState {
			if trimmed == "[/STATE]" {
				inState = false
				var update map[string]interface{}
				if err := json.Unmarshal([]byte(stateAcc.String()), &update); err != nil {
					out.warnings = append(out.warnings,
						fmt.Sprintf("state parse error: %s (json: %.200s)", err, stateAcc.String()))
				} else {
					out.stateUpdates = append(out.stateUpdates, update)
				}
				stateAcc.Reset()
			} else {
				stateAcc.WriteString(line)
				stateAcc.WriteByte('\n')
			}
			continue
		}

		if strings.HasPrefix(trimmed, "[CHAT] ") {
			out.chatLines = append(out.chatLines, strings.TrimPrefix(trimmed, "[CHAT] "))
			continue
		}

		if strings.HasPrefix(trimmed, "[CALL: ") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "[CALL: "), "]")
			name = strings.TrimSpace(name)
			if name != "" {
				out.callAgents = append(out.callAgents, name)
			}
			continue
		}
	}

	if inState {
		out.warnings = append(out.warnings, "unclosed [STATE] block in coder output — discarded")
	}

	return out
}

// ─── State management ─────────────────────────────────────────────────────────

// mergeState shallowly merges update into existing. A null value deletes the key.
func mergeState(existing map[string]interface{}, update map[string]interface{}) {
	for k, v := range update {
		if v == nil {
			delete(existing, k)
		} else {
			existing[k] = v
		}
	}
}

// saveState atomically writes state.json.
func saveState(agentDir string, state map[string]interface{}) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxStateSize {
		return fmt.Errorf("state too large (%d bytes > %d limit)", len(data), maxStateSize)
	}
	tmpPath := filepath.Join(agentDir, "state.json.tmp")
	if err := os.WriteFile(tmpPath, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Join(agentDir, "state.json"))
}

// writeRunLog writes raw output to a timestamped log file; all runs are kept.
func writeRunLog(agentDir, content string, t time.Time) {
	logsDir := filepath.Join(agentDir, "logs")
	_ = os.MkdirAll(logsDir, 0o750)
	name := "run_log_" + t.UTC().Format("20060102_150405") + ".txt"
	_ = os.WriteFile(filepath.Join(logsDir, name), []byte(content), 0o640)
}

// ─── Prompt building ──────────────────────────────────────────────────────────

func buildCoderPrompt(claudeMD, stateJSON string, allSkills []*db.Skill, declaredSkills []string, declaredContent map[string]string) string {
	var sb strings.Builder

	sb.WriteString("[Agent Instructions]\n")
	sb.WriteString(claudeMD)
	sb.WriteString("\n\n")

	sb.WriteString("[Current State]\n")
	sb.WriteString(stateJSON)
	sb.WriteString("\n\n")

	if len(allSkills) > 0 {
		sb.WriteString("[Available Skills]\n")
		for _, sk := range allSkills {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", sk.Name, sk.Description))
		}
		sb.WriteString("\n")
	}

	if len(declaredContent) > 0 {
		sb.WriteString("[Full Skill Instructions]\n")
		for _, name := range declaredSkills {
			if content, ok := declaredContent[name]; ok {
				sb.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", name, content))
			}
		}
	}

	sb.WriteString(`Run your scheduled task. Use the following output markers:
- [CHAT] <text>        — send a message to the user
- [STATE]...[/STATE]   — JSON block to merge into state.json (null value = delete key)
- [CALL: <agent-name>] — invoke another agent synchronously

CONSTRAINTS — this process runs non-interactively as a subprocess:
- Use [CHAT] for all user output. Do NOT call Telegram APIs or any messaging service directly.
- Secrets are injected as environment variables (e.g. os.environ['COINGECKO_API_KEY'] in Python). Do NOT hardcode credential values.
- Use [STATE]...[/STATE] for persistence. Do NOT write arbitrary files to disk.
- Do NOT set up or modify cron jobs or external schedulers — this subprocess is invoked by the scheduler.
- There is no interactive user present. Never prompt for input or request permissions.`)

	return sb.String()
}

// ─── Skills loading ───────────────────────────────────────────────────────────

func (r *Runner) loadDeclaredSkillContent(userID string, skillNames []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, name := range skillNames {
		skillMD := filepath.Join(r.skillsDir, userID, name, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			slog.Warn("agentrunner: skill not found on disk", "user_id", userID, "skill", name)
			continue
		}
		result[name] = string(data)
	}
	return result, nil
}

