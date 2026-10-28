package prompts

import (
	"strings"
	"testing"
)

func skillBuildPrompt(bt string) string {
	return BuildSkillImplementationPrompt("trading-consultant", nil, "", SkillDesignParams{
		BackendType:     bt,
		Connections:     []ConnectionRef{{Provider: "google_sheets", Label: "work"}},
		ConnectionTools: []string{"sheets_read_range"},
		HostPython:      "Python 3.14.7",
	})
}

// A skill build researches and writes; it runs nothing. The general capability block
// lists run_script and bash, and offering them in prose while the engine withholds them
// sends a weak model chasing tools it does not have.
func TestSkillBuildPromptDescribesResearchNotExecution(t *testing.T) {
	for _, bt := range []string{BackendToolCalling, BackendFullCoder} {
		p := skillBuildPrompt(bt)
		for _, absent := range []string{"run_script(path)", "bash(command)", "Run every script to confirm", "Report results inside:"} {
			if strings.Contains(p, absent) {
				t.Errorf("backend %q: skill build prompt still says %q", bt, absent)
			}
		}
		for _, want := range []string{"do not run", "tested when an agent that uses it is built"} {
			if !strings.Contains(p, want) {
				t.Errorf("backend %q: skill build prompt lacks %q", bt, want)
			}
		}
	}
	// Connections are offered as native tools on the API engine. A CLI coder reaches them
	// only through `rookery connector exec` in a shell, which a skill build no longer has,
	// so describing them there would send it looking for a command it cannot run.
	if !strings.Contains(skillBuildPrompt(BackendToolCalling), `google_sheets account "work"`) {
		t.Error("API-engine skill build is not told about its connected accounts")
	}
	if strings.Contains(skillBuildPrompt(BackendFullCoder), "connector exec") {
		t.Error("CLI skill build is told to run connector exec, which needs a shell it does not have")
	}
}

// The skill is written for the agent that will run it, so the builder is told exactly
// what that agent has — install and run doors, sandbox rules, the host's real Python.
func TestSkillBuildPromptDescribesTheAgentRuntime(t *testing.T) {
	p := skillBuildPrompt(BackendToolCalling)
	for _, want := range []string{"<agent_runtime>", "install_packages", "run_in_env", "rookery deps run", "$TMPDIR", "never /tmp", "Python 3.14.7", "browser",
		// A script searching for "the nearest notes/ folder" found the agent's own one.
		"$ROOKERY_VAULT_ROOT", "never by searching for a notes/ folder",
		// The route the 2026-10-27 run took for ETH and SOL, which bypassed containment.
		"never call an environment's python or pip from bash"} {
		if !strings.Contains(p, want) {
			t.Errorf("agent runtime block lacks %q", want)
		}
	}
}
