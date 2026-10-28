package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// The 2026-10-27 run executed a skill environment's interpreter from bash for two of
// three tickers, so the desk ran with every workspace secret and the whole workspace
// home instead of run_in_env's one named key. bash refuses that route; reading files an
// environment holds (a report a script left there) stays allowed.
func TestInvokesSkillEnv(t *testing.T) {
	blocked := []string{
		"~/.local/share/rookery/envs/x/bin/python s.py",
		"/h/.local/share/rookery/envs/trading-consultant/bin/python3 -m cli.main --ticker BTC-USD",
		"$HOME/.local/share/rookery/envs/x/bin/pip install requests",
		"source ~/.local/share/rookery/envs/x/bin/activate && python s.py",
		"PYTHONPATH=~/.local/share/rookery/envs/x/lib/python3.14/site-packages python3 s.py",
		"set -euo pipefail\ncd .\n~/.local/share/rookery/envs/trading-consultant/bin/python skills/t/scripts/run.py --ticker ETH-USD",
	}
	for _, c := range blocked {
		if !invokesSkillEnv(c) {
			t.Errorf("not refused: %q", c)
		}
	}
	allowed := []string{
		"ls ~/.local/share/rookery/envs/x/.tradingagents/logs/reports",
		"cat ~/.local/share/rookery/envs/x/rookery-env.json",
		"rookery deps run --env x --secret K -- skills/x/scripts/s.py",
		"python3 tools/fetch.py",
		"curl -s https://api.example.com",
	}
	for _, c := range allowed {
		if invokesSkillEnv(c) {
			t.Errorf("wrongly refused: %q", c)
		}
	}
}

func TestBashRefusesToRunASkillEnvironment(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	h := &hostToolSet{includeExecTools: true, workDir: dir}
	cmd := "touch " + marker + "; ~/.local/share/rookery/envs/x/bin/python s.py"
	out := h.execute(context.Background(), llm.ToolCall{Name: "bash", Args: []byte(`{"command":` + jsonString(cmd) + `}`)})
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "run_in_env") {
		t.Fatalf("got %q, want a refusal naming run_in_env", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the refused command was run")
	}
}

func jsonString(s string) string {
	b := strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
