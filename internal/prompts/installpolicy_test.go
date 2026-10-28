package prompts

import (
	"strings"
	"testing"
)

var forbiddenInstallBans = []string{"Do not install packages.", "Do not pip-install anything", "Do NOT pip install or add any new package"}

// Agents on both coder kinds may install a skill's dependencies; every prompt that
// used to forbid it outright now routes installs through install_packages. A single
// surviving ban leaves one coder kind or one phase still told "no", which is exactly
// how an agent ends up unable to run the skill it was given.
func TestNoPromptForbidsInstallingOutright(t *testing.T) {
	for _, bt := range []string{BackendToolCalling, BackendFullCoder, ""} {
		prompts := map[string]string{
			"coder":          BuildCoderPrompt(CoderPromptParams{BackendType: bt}),
			"implementation": BuildImplementationPrompt("a", nil, ImplementationParams{BackendType: bt}),
			"skill":          BuildSkillImplementationPrompt("s", nil, "", SkillDesignParams{BackendType: bt}),
		}
		for name, p := range prompts {
			for _, ban := range forbiddenInstallBans {
				if strings.Contains(p, ban) {
					t.Errorf("%s prompt (backend %q) still says %q", name, bt, ban)
				}
			}
		}
		for _, name := range []string{"coder", "implementation"} {
			if !strings.Contains(prompts[name], "install_packages") {
				t.Errorf("%s prompt (backend %q) does not name install_packages", name, bt)
			}
		}
	}
}

// A skill build writes the install step; it does not install, and a missing live run
// is not a reason to block.
func TestSkillBuildPromptDeclaresInsteadOfInstalling(t *testing.T) {
	p := BuildSkillImplementationPrompt("trading-agents", nil, "", SkillDesignParams{BackendType: BackendToolCalling})
	for _, want := range []string{
		installNotDuringBuildLine,
		"kind: pip",
		"after argument parsing",
		"NOT a reason to emit [BLOCKED]",
		"Never write a secret's value to disk",
		"run_in_env",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("skill implementation prompt lacks %q", want)
		}
	}
	if strings.Contains(p, installAtRuntimeLine) {
		t.Error("a skill build must not be told to install at run time itself")
	}
}
