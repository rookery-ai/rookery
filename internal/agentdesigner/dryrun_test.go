package agentdesigner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
)

// A dry run's only job is to produce something the user can look at. The parser has
// to pull the agent's own [CHAT] output back out of the coder's reply and drop the
// protocol scaffolding, exactly as a real run does — otherwise the review shows
// markers instead of a message.
func TestDryRunOutputExtractsChatAndDropsMarkers(t *testing.T) {
	raw := "[STATE]{\"seen\":[\"a.com\"]}[/STATE]\n[CHAT] 3 files changed: notes.md, plan.md, budget.xlsx\n"

	got, ok := dryRunOutput(raw)
	if !ok {
		t.Fatal("a reply containing [CHAT] must yield output")
	}
	if strings.Contains(got, "[STATE]") || strings.Contains(got, "[CHAT]") {
		t.Errorf("protocol markers leaked into the review sample: %q", got)
	}
	if !strings.Contains(got, "3 files changed") {
		t.Errorf("the message was lost: %q", got)
	}
}

// A silent agent is behaving correctly, and the review must say so rather than
// showing an empty box — "it ran and chose to say nothing" is a real, useful result.
func TestDryRunOutputReportsAnIntentionallySilentRun(t *testing.T) {
	got, ok := dryRunOutput("[STATE]{}[/STATE]\n[SILENT]\n")
	if !ok {
		t.Fatal("a silent run is a successful run and must produce a sample")
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("a silent run's sample must not be empty")
	}
	if !strings.Contains(strings.ToLower(got), "nothing to report") {
		t.Errorf("a silent run should be described plainly: %q", got)
	}
}

// Nothing usable means the caller falls back rather than showing an empty review.
func TestDryRunOutputRejectsAnEmptyReply(t *testing.T) {
	if _, ok := dryRunOutput("   \n  "); ok {
		t.Error("an empty reply must not be offered as a dry run result")
	}
}

// dryRunWiringScript makes the fake coder answer differently on each invocation, so a
// single build can be observed twice: first as the BUILD (writes AGENT.md, emits a
// [TEST_OUTPUT] sample), then as the DRY RUN (records the environment it was handed and
// emits a [CHAT] line). It counts invocations through a file in its CWD, which the fake
// coder sets to the agent workDir.
const dryRunWiringScript = `import os
n = 0
if os.path.exists('invocations'):
    n = int(open('invocations').read().strip())
n += 1
open('invocations', 'w').write(str(n))
if n == 1:
    with open('AGENT.md', 'w') as f:
        f.write("# Suggested schedule: none\n# Skills: none\nWatches a page.\n")
    print("[TEST_OUTPUT]built[/TEST_OUTPUT]")
else:
    open('buildphase.txt', 'w').write(os.environ.get('ROOKERY_BUILD_PHASE', ''))
    print("[CHAT] real dry run output")
`

// TestRunGeneration_CreateBuildDryRunsTheAgent is the wiring guard: the three parser
// tests above are pure and stay green even if the dry run is never called, misplaced
// below reconcileBlockedOutcome (which derives outcome.message from decision.message),
// or mis-gated. Two things are asserted, and the second is the safety property:
//
//  1. the review message carries the agent's OWN output, not the build's [TEST_OUTPUT]
//     sample — proof the dry run ran and replaced the sample the user is shown;
//  2. the dry run's environment carries the build-phase marker, which is the single
//     thing stopping a "test run" from really sending the mail / publishing the post.
func TestRunGeneration_CreateBuildDryRunsTheAgent(t *testing.T) {
	fake := newFakeCoder(t, dryRunWiringScript)
	flow, workspaceID, agentsDir := newGenFlow(t, fake)

	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentID:     uuid.New().String(),
		AgentName:   "watcher",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "watch a page"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if !strings.Contains(msg, "real dry run output") {
		t.Fatalf("the review must show the agent's REAL output from a dry run; got %q", msg)
	}
	if strings.Contains(msg, "didn't run") {
		t.Errorf("a dry run happened, so the review must not disclaim that nothing ran; got %q", msg)
	}

	workDir := DraftAgentDir(agentsDir, workspaceID, "watcher")
	phase, err := os.ReadFile(filepath.Join(workDir, "buildphase.txt"))
	if err != nil {
		t.Fatalf("the dry run must have executed in the build's workDir; read marker: %v", err)
	}
	if string(phase) != buildphase.Generation {
		t.Fatalf("the dry run ran WITHOUT the build-phase marker (%s=%q) — connectors.Execute "+
			"would have allowed mutating actions against the user's real accounts",
			buildphase.EnvVar, string(phase))
	}
}

// TestRunGeneration_EditBuildDoesNotDryRun pins the create-only gate: an edit already has
// a live agent the user has seen work, and re-running it is a second full agent run's
// worth of tokens for no new information.
func TestRunGeneration_EditBuildDoesNotDryRun(t *testing.T) {
	fake := newFakeCoder(t, dryRunWiringScript)
	database, workspaceID := testDB(t)
	agentsDir := t.TempDir()
	flow := &Flow{
		sessions: make(map[string]*DesignSession),
		designer: NewDesigner(database, agentsDir),
		db:       database,
		coderFor: func(string) *coder.Coder { return fake },
	}

	agentID := uuid.New().String()
	seedAgent(t, database, agentsDir, workspaceID, agentID,
		"# Suggested schedule: none\nWatches a page.\n", nil)

	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID:     workspaceID,
		AgentID:         agentID,
		AgentName:       "watcher",
		State:           StateDesigning,
		IsEdit:          true,
		ExistingAgentMD: "# Suggested schedule: none\nWatches a page.\n",
		History: []db.ChatMessage{
			{Role: "user", Content: "watch it hourly instead"},
			{Role: "assistant", Content: "Approve to build."},
		},
	}

	msg, _, _, err := flow.runGeneration(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("runGeneration: %v", err)
	}
	if strings.Contains(msg, "real dry run output") {
		t.Errorf("an edit build must not dry-run the agent; got %q", msg)
	}
}
