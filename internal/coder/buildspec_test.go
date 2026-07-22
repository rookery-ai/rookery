package coder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A skill build must be nudged toward SKILL.md, never AGENT.md — the bug that made
// every skill build fail (see the SP10 spec, §1.1).
func TestVerifyFinishNudgeUsesTheBuildSpecDeliverable(t *testing.T) {
	dir := t.TempDir()

	h := &hostToolSet{verifyBuild: true, workDir: dir, spec: SkillBuildSpec}
	nudge := h.verifyFinishNudge()
	require.Contains(t, nudge, "SKILL.md")
	require.NotContains(t, nudge, "AGENT.md")

	h2 := &hostToolSet{verifyBuild: true, workDir: dir, spec: AgentBuildSpec}
	require.Contains(t, h2.verifyFinishNudge(), "AGENT.md")
}

// Once the deliverable exists and no script is unverified, the build may finish.
func TestVerifyFinishNudgeAllowsFinishOnceDeliverableExists(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\nbody\n"), 0o600))

	h := &hostToolSet{verifyBuild: true, workDir: dir, spec: SkillBuildSpec}
	require.Equal(t, "", h.verifyFinishNudge())
}

// Gate 2 applies to a skill's scripts/, which the agent-shaped predicate never matched.
func TestSkillBuildSpecRecognisesItsScripts(t *testing.T) {
	require.True(t, SkillBuildSpec.IsScript("scripts/extract.py"))
	require.True(t, SkillBuildSpec.IsScript("scripts/extract.sh"))
	require.False(t, SkillBuildSpec.IsScript("SKILL.md"))
	require.False(t, SkillBuildSpec.IsScript("references/api.md"))
	require.False(t, SkillBuildSpec.IsScript("scripts/tests/test_extract.py"))

	require.True(t, AgentBuildSpec.IsScript("tools/fetch.py"))
	require.False(t, AgentBuildSpec.IsScript("scripts/extract.py"))
}

// An unset spec must behave exactly as the agent build did before this change.
func TestZeroSpecDefaultsToAgent(t *testing.T) {
	dir := t.TempDir()
	h := &hostToolSet{verifyBuild: true, workDir: dir}
	require.Contains(t, h.verifyFinishNudge(), "AGENT.md")
}
