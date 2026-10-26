package coder

import (
	"context"
	"fmt"
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
	stdout, stderr, err := h.depsRunner().Run(ctx, skilldeps.RunRequest{
		Home: h.userHomeDir(), Env: env, WorkDir: h.workDir, VaultRoot: h.vaultRootPath(),
		Script: script, Args: args, Secrets: secrets, Stdin: stdin,
	})
	if err != nil {
		return "", fmt.Errorf("script failed: %w\nstdout: %s\nstderr: %s", err, truncate(stdout), truncate(stderr))
	}
	if strings.TrimSpace(stdout) == "" && stderr != "" {
		return noStdoutSentinel + "\n" + truncate(stderr), nil
	}
	return stdout, nil
}
