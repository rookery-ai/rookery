package skilldeps

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRequirementAccepts(t *testing.T) {
	for _, s := range []string{
		"requests",
		"requests==2.32.4",
		"pandas>=3.0,<4",
		"tradingagents[bedrock]==0.3.1",
		"git+https://github.com/TauricResearch/TradingAgents.git@v0.3.1",
		"https://files.pythonhosted.org/packages/x/requests-2.32.4-py3-none-any.whl",
	} {
		if err := ValidateRequirement(s); err != nil {
			t.Errorf("%q rejected: %v", s, err)
		}
	}
}

// Anything pip would read as an option, or a source that hides where it downloads from,
// never reaches pip. The whitespace case is the one that matters most: pip would split
// it and honour the smuggled --extra-index-url.
func TestValidateRequirementRejects(t *testing.T) {
	for _, s := range []string{
		"",
		"-r req.txt",
		"--extra-index-url=https://x.example",
		"requests --extra-index-url http://x.example",
		"http://example.com/x.whl",
		"file:///tmp/x",
		"git+https://1.2.3.4/x.git",
		"https://user:pw@github.com/x",
		"../local",
		"git+ssh://git@github.com/x.git",
	} {
		if err := ValidateRequirement(s); err == nil {
			t.Errorf("%q accepted, want rejection", s)
		}
	}
}

func TestEnvDirValidatesTheName(t *testing.T) {
	got, err := EnvDir("/h", "trading-agents")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/h", ".local/share/rookery/envs", "trading-agents"); got != want {
		t.Errorf("EnvDir = %q, want %q", got, want)
	}
	for _, bad := range []string{"../../.claude", "Trading Agents", "", strings.Repeat("a", 65), "-x"} {
		if _, err := EnvDir("/h", bad); err == nil {
			t.Errorf("env name %q accepted", bad)
		}
	}
}

func TestSkillBuildActive(t *testing.T) {
	env := map[string]string{"ROOKERY_BUILD_KIND": "skill"}
	if !SkillBuildActive(func(k string) string { return env[k] }) {
		t.Error("skill build not detected")
	}
	if SkillBuildActive(func(string) string { return "" }) {
		t.Error("detected a skill build where there is none")
	}
}
