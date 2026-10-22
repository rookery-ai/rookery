package agentdesigner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/prompts"
)

func TestAPITierCeilingViolation(t *testing.T) {
	existing := map[string]string{"fetch.py": "print('a')\n", "lib/util.py": "x = 1\n"}
	cases := []struct {
		name     string
		backend  string
		existing map[string]string
		built    map[string]string
		want     []string
	}{
		{"cli coder may author scripts", prompts.BackendFullCoder, nil,
			map[string]string{"fetch.py": "print(1)\n"}, nil},
		{"api create with no tools is fine", prompts.BackendToolCalling, nil, map[string]string{}, nil},
		{"api create with a script is refused", prompts.BackendToolCalling, nil,
			map[string]string{"b.py": "1", "a.py": "2"}, []string{"a.py", "b.py"}},
		{"api edit keeping the CLI-built tools is fine", prompts.BackendToolCalling, existing,
			map[string]string{"fetch.py": "print('a')\n", "lib/util.py": "x = 1\n"}, nil},
		{"api edit deleting a tool is fine", prompts.BackendToolCalling, existing,
			map[string]string{"fetch.py": "print('a')\n"}, nil},
		{"api edit changing a tool is refused", prompts.BackendToolCalling, existing,
			map[string]string{"fetch.py": "print('b')\n", "lib/util.py": "x = 1\n"}, []string{"fetch.py"}},
		{"api edit adding a tool is refused", prompts.BackendToolCalling, existing,
			map[string]string{"fetch.py": "print('a')\n", "lib/util.py": "x = 1\n", "new.py": "1"}, []string{"new.py"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := apiTierCeilingViolation(tc.backend, tc.existing, tc.built)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// scriptBuildScript is a fake coder build that authors a helper script, with a clean
// [TEST_OUTPUT] — the shape a small API model produced for a TIER 2 agent.
const scriptBuildScript = `import os
os.makedirs('tools', exist_ok=True)
with open('tools/fetch.py', 'w') as f:
    f.write("print('rows')\n")
with open('AGENT.md', 'w') as f:
    f.write("# Suggested schedule: none\n# Skills: none\nFetches rows.\n")
print("[TEST_OUTPUT]rows[/TEST_OUTPUT]")
`

// TestRunGeneration_APICoderCannotBuildTier2 is the reported failure: a small API model
// sent to TIER 2 wrote a script it could not make work. On the API engine a create build
// that authors helper code must stop, say a CLI coder is needed, and leave nothing
// saveable — "keep it as-is" would otherwise save the script agent anyway.
func TestRunGeneration_APICoderCannotBuildTier2(t *testing.T) {
	fake := newFakeCoder(t, scriptBuildScript).WithBackendType("api")
	flow, workspaceID, agentsDir := newGenFlow(t, fake)
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "rows",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "fetch all the rows"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}

	msg, done, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if done || msg != tierCeilingMessage(false) {
		t.Fatalf("an API-engine build with helper code must stop at the tier ceiling; done=%v msg=%q", done, msg)
	}
	sess := flow.sessions[workspaceID]
	if sess.State != StateDesigning {
		t.Errorf("state = %v, want designing", sess.State)
	}
	if sess.HasSaveableBuild {
		t.Error("a ceiling hit must not offer keep-as-is: it would save the script agent anyway")
	}
	if !sess.ForceTier1 {
		t.Error("the retry must carry the binding zero-code override")
	}
	workDir := DraftAgentDir(agentsDir, workspaceID, "rows")
	if _, err := os.Stat(filepath.Join(workDir, "tools", "fetch.py")); !os.IsNotExist(err) {
		t.Errorf("the refused script must be removed from the draft so the retry is not refused for it again; stat err=%v", err)
	}
}

// TestRunGeneration_CLICoderStillBuildsTier2 pins the other half: a CLI coder is exactly
// who TIER 2 is reserved for, and the same build advances there.
func TestRunGeneration_CLICoderStillBuildsTier2(t *testing.T) {
	fake := newFakeCoder(t, scriptBuildScript) // generic CLI → BackendFullCoder
	flow, workspaceID, agentsDir := newGenFlow(t, fake)
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "rows",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "fetch all the rows"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}
	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if msg == tierCeilingMessage(false) || flow.sessions[workspaceID].State != StateVerifying {
		t.Fatalf("a CLI coder must still build a TIER 2 agent; state=%v msg=%q", flow.sessions[workspaceID].State, msg)
	}
	if _, err := os.Stat(filepath.Join(DraftAgentDir(agentsDir, workspaceID, "rows"), "tools", "fetch.py")); err != nil {
		t.Errorf("the CLI-built script must survive: %v", err)
	}
}

// editKeepsToolsScript edits AGENT.md only and emits NO [TEST_OUTPUT] — the formatting slip
// a weak model makes — leaving the CLI-built tools/ exactly as staged.
const editKeepsToolsScript = `with open('AGENT.md', 'w') as f:
    f.write("# Suggested schedule: 0 * * * *\n# Skills: none\nFetches rows hourly.\n")
print("Updated the schedule.")
`

const editChangesToolsScript = `with open('tools/fetch.py', 'w') as f:
    f.write("print('rewritten')\n")
print("[TEST_OUTPUT]rewritten[/TEST_OUTPUT]")
`

func newEditFlow(t *testing.T, fake *coder.Coder) (*Flow, string, string, string) {
	t.Helper()
	database, workspaceID := testDB(t)
	agentsDir := t.TempDir()
	flow := &Flow{
		sessions: make(map[string]*DesignSession),
		designer: NewDesigner(database, agentsDir),
		db:       database,
		coderFor: func(string) *coder.Coder { return fake },
	}
	agentID := uuid.New().String()
	agentMD := "# Suggested schedule: none\n# Skills: none\nFetches rows.\n"
	seedAgent(t, database, agentsDir, workspaceID, agentID, agentMD, map[string]string{"fetch.py": "print('rows')\n"})
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID:     workspaceID,
		AgentID:         agentID,
		AgentName:       "rows",
		State:           StateDesigning,
		IsEdit:          true,
		ExistingAgentMD: agentMD,
		ExistingTools:   map[string]string{"fetch.py": "print('rows')\n"},
		History: []db.ChatMessage{
			{Role: "user", Content: "run it hourly"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}
	return flow, workspaceID, agentsDir, agentID
}

// TestRunGeneration_APICoderEditKeepsCLIBuiltTools is the "build on Claude Code, run on a
// cheap API model" workflow: editing such an agent on the API engine must neither refuse
// it for the CLI coder's script nor strip that script. The fake omits [TEST_OUTPUT] on
// purpose — the weak-backend "unverified authored script" gate used to block exactly this,
// describing code the edit never touched as "the helper it wrote".
func TestRunGeneration_APICoderEditKeepsCLIBuiltTools(t *testing.T) {
	fake := newFakeCoder(t, editKeepsToolsScript).WithBackendType("api")
	flow, workspaceID, _, _ := newEditFlow(t, fake)

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	sess := flow.sessions[workspaceID]
	if sess.State != StateVerifying {
		t.Fatalf("an API-engine edit that leaves the CLI-built tools alone must advance; state=%v msg=%q", sess.State, msg)
	}
	if got := sess.PendingTools["fetch.py"]; got != "print('rows')\n" {
		t.Errorf("the CLI-built tool must be carried into the pending build unchanged; got %q", got)
	}
}

// TestRunGeneration_APICoderEditMayNotRewriteTools: changing that code is TIER 2 work, so
// it is refused like a create, and the live agent's script is left as it was.
func TestRunGeneration_APICoderEditMayNotRewriteTools(t *testing.T) {
	fake := newFakeCoder(t, editChangesToolsScript).WithBackendType("api")
	flow, workspaceID, agentsDir, agentID := newEditFlow(t, fake)

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if msg != tierCeilingMessage(true) {
		t.Fatalf("an API-engine edit rewriting tools must stop at the tier ceiling; msg=%q", msg)
	}
	live, err := os.ReadFile(filepath.Join(AgentDir(agentsDir, workspaceID, agentID), "tools", "fetch.py"))
	if err != nil || string(live) != "print('rows')\n" {
		t.Errorf("the live agent's script must be untouched; got %q err=%v", live, err)
	}
	if !strings.Contains(msg, "CLI coder") {
		t.Errorf("the message must name the way through; got %q", msg)
	}
}
