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
