package agentdesigner_test

import (
	"testing"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validCode = `#!/usr/bin/env python3
"""Agent: test"""

import os
import json

# ======= USER LOGIC =======

def main():
    name = os.environ.get("TARGET", "world")
    print(f"[CHAT] Hello {name}!")

if __name__ == "__main__":
    main()

# ======= SYSTEM INJECTED =======
`

func TestGuardrails_ValidCode(t *testing.T) {
	err := agentdesigner.RunFullGuardrails(validCode, "")
	require.NoError(t, err)
}

func TestGuardrails_EthicsFilter(t *testing.T) {
	code := validCode + "\n# rm -rf /\n"
	err := agentdesigner.RunFullGuardrails(code, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethics filter")
}

func TestGuardrails_PlainToolScriptAllowed(t *testing.T) {
	// Tool scripts in tools/ are plain Python helpers — no markers required.
	code := `#!/usr/bin/env python3
import requests

def fetch_price(symbol):
    r = requests.get(f"https://api.example.com/price/{symbol}")
    return r.json()["price"]
`
	err := agentdesigner.RunFullGuardrails(code, "")
	require.NoError(t, err)
}

func TestGuardrails_ASTBlocksEval(t *testing.T) {
	if !agentdesigner.PythonAvailable() {
		t.Skip("python3 not available")
	}
	code := validCode + "\nresult = eval('1+1')\n"
	err := agentdesigner.RunFullGuardrails(code, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ast check")
}

func TestGuardrails_ASTBlocksOsSystem(t *testing.T) {
	if !agentdesigner.PythonAvailable() {
		t.Skip("python3 not available")
	}
	code := validCode + "\nimport os\nos.system('ls')\n"
	err := agentdesigner.RunFullGuardrails(code, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ast check")
}

func TestGuardrails_ASTBlocksSubprocess(t *testing.T) {
	if !agentdesigner.PythonAvailable() {
		t.Skip("python3 not available")
	}
	code := validCode + "\nimport subprocess\nsubprocess.run(['ls'])\n"
	err := agentdesigner.RunFullGuardrails(code, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ast check")
}

// TestEthics_DocVsCodeScoping covers F1: destructive-command keywords are legitimate as
// prose in a DOCUMENT (CheckEthics) but still block in CODE (RunToolGuardrails). Malicious
// intent keywords block in both.
func TestEthics_DocVsCodeScoping(t *testing.T) {
	// Legitimate destructive descriptions in an AGENT.md/SKILL.md must PASS the doc gate.
	for _, doc := range []string{
		"# Suggested schedule: none\nEach run, drop table temp_imports.",
		"This agent wipes stale cache entries once a week.",
		"It reads the private key path from a secret and signs the payload.",
	} {
		if err := agentdesigner.CheckEthics(doc, ""); err != nil {
			t.Errorf("CheckEthics(doc) rejected legitimate prose %q: %v", doc, err)
		}
	}

	// The SAME destructive commands, as executable code, must still BLOCK.
	for _, code := range []string{
		"import os\nos.remove('x')\n# rm -rf /tmp/data\n",
		"cur.execute('drop table users')\n",
	} {
		if err := agentdesigner.RunToolGuardrails("tools/x.py", code); err == nil {
			t.Errorf("RunToolGuardrails must still block destructive code: %q", code)
		}
	}

	// Malicious-intent keywords block EVERYWHERE, including a document.
	for _, doc := range []string{
		"This agent will steal and exfil the user's contacts.",
	} {
		if err := agentdesigner.CheckEthics(doc, ""); err == nil {
			t.Errorf("CheckEthics must block malicious intent even in a document: %q", doc)
		}
	}
}
