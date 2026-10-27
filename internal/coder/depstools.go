package coder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/skilldeps"
)

// depsTools are the API engine's door to internal/skilldeps — the same code a CLI coder
// reaches through `rookery deps`. Offered wherever code runs (agent runs and builds),
// never in chat.
func depsTools() []llm.Tool {
	return []llm.Tool{
		{
			Name: "install_packages",
			Description: "Install Python packages into a named environment (usually the skill's name) so a script can import them. " +
				"Runs in its own sandbox with no secrets; every resolved package is checked against a known-malicious list first. " +
				"Packages are names with an optional version (requests==2.32.4) or an https/git+https URL. " +
				"An environment persists between runs, so install once and reuse it. Never pip install with bash.",
			Parameters: rawSchema(`{"type":"object","properties":{"env":{"type":"string","description":"environment name: lowercase letters, digits, hyphens"},"packages":{"type":"array","items":{"type":"string"},"description":"requirements to install"}},"required":["env","packages"]}`),
		},
		{
			Name: "run_in_env",
			Description: "Run a Python script with a named environment's interpreter, so it can import what install_packages installed. " +
				"The script gets ONLY the secrets you name in `secrets` (no others) and cannot read your home folder; it can write your working directory and the knowledge base. " +
				"Use this instead of run_script for any script that imports installed packages.",
			Parameters: rawSchema(`{"type":"object","properties":{"env":{"type":"string","description":"environment name"},"script":{"type":"string","description":"path to the .py file, relative to your working directory or absolute within the vault"},"args":{"type":"array","items":{"type":"string"},"description":"command-line arguments"},"secrets":{"type":"array","items":{"type":"string"},"description":"names of secrets to pass to the script as environment variables"},"stdin":{"type":"string","description":"text to feed to the script's stdin"}},"required":["env","script"]}`),
		},
	}
}

// logTail keeps the last 600 bytes of a failure text for one log line: the end is where a
// traceback or pip names what went wrong.
func logTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return "…" + s[len(s)-600:]
	}
	return s
}

// depsRunner returns the injected runner, or one built from this tool set's sandbox
// settings with the production malware check.
func (h *hostToolSet) depsRunner() skilldeps.Runner {
	r := h.deps
	if r.SelfExe == "" {
		r.SelfExe = h.selfExe
		r.Sandbox = h.sandbox
	}
	if r.Checker == nil {
		r.Checker = skilldeps.NewOSVClient("", nil)
	}
	return r
}

func (h *hostToolSet) installPackages(ctx context.Context, env string, pkgs []string) (string, error) {
	if skilldeps.SkillBuildActive(func(k string) string { return h.subprocessEnv[k] }) {
		return "", skilldeps.ErrSkillBuild
	}
	res, err := h.depsRunner().Install(ctx, skilldeps.InstallRequest{Home: h.userHomeDir(), Env: env, Agent: h.agentName, Specs: pkgs})
	if err != nil {
		// The model sees the error as its tool result; nothing else did, so a failed
		// install could not be diagnosed from the server.
		slog.Warn("coder: install_packages failed", "workspace_id", h.workspaceID, "agent", h.agentName,
			"env", env, "packages", strings.Join(pkgs, " "), "err", logTail(h.redactSecrets(err.Error())))
		return "", err
	}
	return fmt.Sprintf("installed %s: %d packages (malware check: %s)\n%s", res.Env, len(res.Resolved), res.MalwareCheck, res.Output), nil
}

func (h *hostToolSet) runInEnv(ctx context.Context, env, script string, args, secretNames []string, stdin string) (string, error) {
	secrets := map[string]string{}
	for _, name := range secretNames {
		v, ok := h.subprocessEnv[name]
		if !ok {
			return "", fmt.Errorf("secret %s is not set in this run", name)
		}
		secrets[name] = v
	}
	// Same journal bracket as run_script and bash: the run sandbox grants the vault
	// read-write (skills write notes), so in a rehearsal its writes must be undoable.
	var stdout, stderr string
	err := h.journal.AroundExec(func() error {
		var runErr error
		stdout, stderr, runErr = h.depsRunner().Run(ctx, skilldeps.RunRequest{
			Home: h.userHomeDir(), Env: env, WorkDir: h.workDir, VaultRoot: h.vaultRootPath(),
			Script: script, Args: args, Secrets: secrets, Stdin: stdin,
		})
		return runErr
	})
	if err != nil {
		exit := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		}
		// stderr carries the script's own explanation; the stdout tail carries the JSON a
		// skill script prints on failure. Both redacted: a script may echo its key.
		slog.Warn("coder: run_in_env failed", "workspace_id", h.workspaceID, "agent", h.agentName,
			"env", env, "script", script, "exit", exit, "err", err.Error(),
			"stderr_tail", logTail(h.redactSecrets(stderr)), "stdout_tail", logTail(h.redactSecrets(stdout)))
		return "", fmt.Errorf("script failed: %w\nstdout: %s\nstderr: %s", err, truncate(stdout), truncate(stderr))
	}
	if strings.TrimSpace(stdout) == "" && stderr != "" {
		return noStdoutSentinel + "\n" + truncate(stderr), nil
	}
	return stdout, nil
}
