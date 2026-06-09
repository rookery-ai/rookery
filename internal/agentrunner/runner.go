// Package agentrunner loads an agent from disk, injects secrets via Proxy(),
// executes it in a Firejail sandbox, and routes [CHAT] output back to the
// user's platform gateway.
//
// SECURITY INVARIANT: Proxy() is called only here — never in the coder or
// anywhere else. The resolved script is passed directly to the sandbox via
// a temp file and is deleted immediately after execution. It is never logged
// or written to the DB.
package agentrunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/secrets"
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
}

// Runner executes agents.
type Runner struct {
	db        *db.DB
	systemKey []byte
	agentsDir string
}

// New creates a Runner.
func New(database *db.DB, systemKey []byte, agentsDir string) *Runner {
	return &Runner{db: database, systemKey: systemKey, agentsDir: agentsDir}
}

// Run executes the agent identified by input.AgentID.
// It loads the agent code, proxies secrets, sandboxes execution, persists
// the run record, and delivers [CHAT] lines via input.SendOutput.
func (r *Runner) Run(ctx context.Context, input RunInput) error {
	// Load agent from DB.
	agent, err := r.db.GetAgent(input.AgentID)
	if err != nil {
		return fmt.Errorf("load agent: %w", err)
	}
	if agent.UserID != input.UserID {
		return fmt.Errorf("agent not found")
	}

	// Read source from disk.
	codePath := agentdesigner.AgentCodePath(r.agentsDir, input.UserID, input.AgentID)
	rawCode, err := os.ReadFile(codePath)
	if err != nil {
		return fmt.Errorf("read agent code: %w", err)
	}

	// Resolve secrets in-memory. The resolved code is NEVER written to disk
	// or logs — it goes straight to a temp file that is deleted after use.
	resolvedCode, envVars, err := r.resolveSecrets(ctx, input.UserID, input.MasterPw, string(rawCode))
	if err != nil {
		return fmt.Errorf("resolve secrets: %w", err)
	}

	// Write resolved code to a temp file (deleted immediately after run).
	tmpFile, err := writeTempScript(resolvedCode)
	if err != nil {
		return fmt.Errorf("write temp script: %w", err)
	}
	defer os.Remove(tmpFile) // always delete the resolved script

	// Create run record.
	runID := uuid.New().String()
	run := &db.AgentRun{
		ID:      runID,
		AgentID: input.AgentID,
		UserID:  input.UserID,
		Trigger: input.Trigger,
	}
	if err := r.db.CreateAgentRun(run); err != nil {
		return fmt.Errorf("create run record: %w", err)
	}

	// Execute in sandbox.
	result, execErr := sandbox.Run(ctx, tmpFile, sandbox.RunOptions{
		Timeout:  60 * time.Second,
		Env:      envVars,
		WorkDir:  filepath.Dir(codePath),
		AllowNet: true, // agents may need network access
	})

	exitCode := 0
	var stdout, stderr string
	if result != nil {
		exitCode = result.ExitCode
		// Strip resolved secret values from output before persisting.
		stdout = redactSecrets(result.Stdout, envVars)
		stderr = redactSecrets(result.Stderr, envVars)
	}
	if execErr != nil {
		exitCode = -1
		stderr += "\n[RUNNER] " + execErr.Error()
	}

	// Finish run record (output stored without secret values).
	_ = r.db.FinishAgentRun(runID, exitCode, stdout, stderr)

	// Deliver [CHAT] lines to the user.
	if input.SendOutput != nil && result != nil {
		chatLines := sandbox.ParseChatLines(result.Stdout)
		if len(chatLines) > 0 {
			input.SendOutput(strings.Join(chatLines, "\n"))
		} else if execErr != nil {
			input.SendOutput(fmt.Sprintf("Agent %s failed (exit %d)", agent.Name, exitCode))
		}
	}

	if execErr != nil {
		return execErr
	}
	return nil
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

// ─── Internal ─────────────────────────────────────────────────────────────────

// resolveSecrets resolves ${NAME} placeholders using the secrets service.
// Returns the resolved code and a slice of KEY=VALUE env vars.
// Neither the resolved code nor the values appear in any persistent store.
func (r *Runner) resolveSecrets(ctx context.Context, userID, masterPw, code string) (resolvedCode string, envVars []string, err error) {
	if masterPw == "" {
		// No master password — return code as-is (no secret injection).
		return code, nil, nil
	}

	u, err := r.db.GetUserByID(userID)
	if err != nil {
		return "", nil, fmt.Errorf("load user: %w", err)
	}

	svc := secrets.New(r.db, userID, masterPw, u.SecretsSalt)

	// List all secret names to build env var set.
	names, err := r.db.ListSecretNames(userID)
	if err != nil {
		return "", nil, fmt.Errorf("list secrets: %w", err)
	}

	for _, name := range names {
		val, err := svc.Get(ctx, name)
		if err != nil {
			continue // skip unresolvable secrets
		}
		envVars = append(envVars, name+"="+val)
	}

	// Replace ${NAME} placeholders in the code.
	resolved, err := svc.Proxy(ctx, code)
	if err != nil {
		return "", nil, fmt.Errorf("proxy secrets: %w", err)
	}

	return resolved, envVars, nil
}

// writeTempScript writes code to a temporary file and returns the path.
// The caller must delete the file when done (defer os.Remove(path)).
func writeTempScript(code string) (string, error) {
	f, err := os.CreateTemp("", "sa-agent-*.py")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(code)
	return f.Name(), err
}

// redactSecrets removes secret values from output strings to prevent leaks.
// envVars is a slice of "NAME=VALUE" pairs.
func redactSecrets(s string, envVars []string) string {
	for _, kv := range envVars {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[1] != "" {
			s = strings.ReplaceAll(s, parts[1], "[REDACTED]")
		}
	}
	return s
}
