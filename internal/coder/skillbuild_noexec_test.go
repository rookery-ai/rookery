package coder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rookery-ai/rookery/internal/buildphase"
)

// A skill build researches and writes; it never runs anything. Installing and
// executing during the build is what made skill builds take 14-20 minutes, and a
// skill's scripts are tested where they are actually used: by the agent designer,
// when it builds an agent that uses the skill.
func TestSkillBuildOffersNoExecutionTools(t *testing.T) {
	dir := t.TempDir()
	c := newTestCoder(t, dir)
	ws := "ws1"
	staging := filepath.Join(dir, "vaults", ws, "skills", ".staging-x")
	mustMkdir(t, staging)
	c = c.WithDir(staging).WithBuildSpec(SkillBuildSpec).
		WithExtraEnv(map[string]string{buildphase.EnvVar: buildphase.Generation, buildphase.KindEnvVar: buildphase.KindSkill})

	offered := map[string]bool{}
	for _, tool := range c.buildHostTools(ws).tools() {
		offered[tool.Name] = true
	}
	for _, banned := range []string{"bash", "run_script", "install_packages", "run_in_env", "get_state", "set_state"} {
		if offered[banned] {
			t.Errorf("skill build offers %s", banned)
		}
	}
	for _, needed := range []string{"read_file", "write_file", "edit_file", "web_fetch", "web_search", "search_files"} {
		if !offered[needed] {
			t.Errorf("skill build lacks %s", needed)
		}
	}
}

// With nothing to run, the "run your script before you finish" gate can never be
// satisfied — it would only nudge the build to its limit. It stays off for a skill
// build; the deliverable gate (SKILL.md must exist) stays on.
func TestSkillBuildDoesNotDemandAScriptRun(t *testing.T) {
	dir := t.TempDir()
	h := &hostToolSet{verifyBuild: true, workDir: dir, spec: SkillBuildSpec}
	if nudge := h.verifyFinishNudge(); nudge == "" {
		t.Fatal("a missing SKILL.md must still be nudged")
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: y\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.trackScriptProgress(toolCall("write_file", `{"path":"scripts/run.py"}`), "ok", false)
	if nudge := h.verifyFinishNudge(); nudge != "" {
		t.Errorf("a skill build was nudged to run its script: %q", nudge)
	}
}

// Fewer steps than an agent build: research plus writing does not need fifty, and
// every extra step re-sends the whole growing conversation.
func TestSkillBuildHasATighterTurnBudget(t *testing.T) {
	skill := newTurnBudget(true, false, SkillBuildSpec.MaxTurns)
	agent := newTurnBudget(true, false, AgentBuildSpec.MaxTurns)
	if skill.base >= agent.base {
		t.Errorf("skill build budget %d is not below the agent build's %d", skill.base, agent.base)
	}
	if skill.base != 20 {
		t.Errorf("skill build budget = %d, want 20", skill.base)
	}
}
