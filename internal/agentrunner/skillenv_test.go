package agentrunner

import (
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/skilldeps"
)

const pipSkill = `---
name: trading-agents
description: Analyse a trade. Use when trading is mentioned.
metadata:
  requires:
    env: [CODER_OPENROUTER_KEY]
  install:
    - kind: pip
      package: requests==2.32.4
---
Body.
`

func TestResolveSkillEnvsReportsReadiness(t *testing.T) {
	home := t.TempDir()
	r := &Runner{}
	content := map[string]string{"trading-agents": pipSkill}

	envs := r.resolveSkillEnvs(home, []string{"trading-agents"}, content)
	if len(envs) != 1 || envs[0].Env != "trading-agents" || envs[0].Ready || len(envs[0].Secrets) != 1 || envs[0].Secrets[0] != "CODER_OPENROUTER_KEY" {
		t.Fatalf("before install: %+v", envs)
	}

	dir, _ := skilldeps.EnvDir(home, "trading-agents")
	if err := skilldeps.AppendInstall(dir, skilldeps.InstallRecord{At: time.Now(), Requested: []string{"requests==2.32.4"}}); err != nil {
		t.Fatal(err)
	}
	if envs = r.resolveSkillEnvs(home, []string{"trading-agents"}, content); !envs[0].Ready {
		t.Fatalf("after install: %+v", envs)
	}
}

func TestResolveSkillEnvsIgnoresSkillsWithoutPip(t *testing.T) {
	r := &Runner{}
	content := map[string]string{"plain": "---\nname: plain\ndescription: d\n---\nBody\n"}
	if envs := r.resolveSkillEnvs(t.TempDir(), []string{"plain"}, content); len(envs) != 0 {
		t.Fatalf("got %+v", envs)
	}
}
