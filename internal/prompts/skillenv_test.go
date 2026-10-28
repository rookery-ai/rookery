package prompts

import (
	"strings"
	"testing"
)

func TestSkillEnvBlockTellsTheAgentHowToInstall(t *testing.T) {
	out := SkillEnvBlock(nil, []SkillPyEnv{{Skill: "trading-agents", Env: "trading-agents", Packages: []string{"requests==2.32.4", "rich"}, Secrets: []string{"CODER_OPENROUTER_KEY"}}}, "/h", "/v", "/usr/bin/rookery")
	for _, want := range []string{
		"is NOT installed yet",
		`install_packages(env="trading-agents", packages=["requests==2.32.4", "rich"])`,
		"/usr/bin/rookery deps install --env trading-agents --package requests==2.32.4 --package rich",
		"/usr/bin/rookery deps run --env trading-agents --secret CODER_OPENROUTER_KEY -- <script>",
		`secrets=["CODER_OPENROUTER_KEY"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("block lacks %q:\n%s", want, out)
		}
	}
}

func TestSkillEnvBlockReadyEnv(t *testing.T) {
	out := SkillEnvBlock(nil, []SkillPyEnv{{Skill: "s", Env: "s", Packages: []string{"x"}, Ready: true}}, "/h", "/v", "rookery")
	if !strings.Contains(out, "Python environment `s` for skill s is ready") {
		t.Errorf("ready env not reported:\n%s", out)
	}
	if strings.Contains(out, "NOT installed") {
		t.Error("a ready env must not ask for an install")
	}
}

func TestSkillEnvBlockEmpty(t *testing.T) {
	if got := SkillEnvBlock(nil, nil, "/h", "", "rookery"); got != "" {
		t.Errorf("nothing to say, got %q", got)
	}
}
