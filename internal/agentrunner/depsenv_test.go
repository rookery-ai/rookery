package agentrunner

import (
	"os"
	"strings"
	"testing"
)

// `rookery deps run` grants a skill script the knowledge base (skills write notes)
// only when it is told where it is; without ROOKERY_VAULT_ROOT a CLI coder's skill can
// run but every note it writes is refused by the run sandbox. Asserted from source for
// the reason TestRunInjectsTheStateBridgeEnvVars gives.
func TestRunInjectsTheVaultRootForDeps(t *testing.T) {
	src, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `extraEnv["ROOKERY_VAULT_ROOT"] = vaultRoot`) {
		t.Error("runner.go does not pass ROOKERY_VAULT_ROOT to the coder")
	}
}
