package agentdesigner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/prompts"
	"github.com/rookery-ai/rookery/internal/vault"
)

// kbClobberingScript stands in for the failure this whole change exists to fix:
// a coder that, while building and then while rehearsing, writes into the USER's
// notes rather than its own directory.
//
// It reaches them the way a real one did — a path relative to its working
// directory, which is <vault>/<ws>/agents/draft_<slug>, so "../../notes" is the
// user's own folder. It clobbers on BOTH invocations, so the build's journal and
// the dry run's journal are each exercised.
const kbClobberingScript = `import os, sys
n = 0
if os.path.exists('invocations'):
    n = int(open('invocations').read().strip())
n += 1
open('invocations', 'w').write(str(n))
os.makedirs('../../notes', exist_ok=True)
open('../../notes/journal.md', 'w').write('CLOBBERED BY INVOCATION %d' % n)
os.makedirs('../../notes/invented', exist_ok=True)
open('../../notes/invented/report.md', 'w').write('should not survive')
if n == 1:
    with open('AGENT.md', 'w') as f:
        f.write("# Suggested schedule: none\n# Skills: none\nWatches a page.\n")
    open('state.md', 'w').write('BUILD STATE')
    print("[TEST_OUTPUT]built[/TEST_OUTPUT]")
else:
    open('state.md', 'w').write('DRY RUN STATE')
    print("[CHAT] real dry run output")
`

// newGenFlowWithVault mirrors newGenFlow but attaches a vault, which is what the
// write journal needs.
//
// The designer's base is v.VaultsDir(), NOT the data dir — DraftAgentDir joins
// <vaultsBase>/<ws>/agents/... while Vault.Root is <dataDir>/vaults/<ws>, so
// handing the same directory to both puts the agent's working directory OUTSIDE
// the vault. Nothing is then journaled and nothing is clobbered, and a test
// written that way passes while proving nothing. It did, before this comment.
func newGenFlowWithVault(t *testing.T, fake *coder.Coder) (*Flow, string, string, *vault.Vault) {
	t.Helper()
	database, workspaceID := testDB(t)
	v := vault.New(t.TempDir())
	base := v.VaultsDir()
	flow := &Flow{
		sessions: make(map[string]*DesignSession),
		designer: NewDesigner(database, base),
		db:       database,
		vlt:      v,
		coderFor: func(string) *coder.Coder { return fake },
	}
	return flow, workspaceID, base, v
}

// TestBuildAndDryRunLeaveTheUsersKnowledgeBaseAsTheyFoundIt is the regression
// guard for the reported data-integrity bug: a build and its dry run replaced a
// real note twice, and the code asserted it could not happen.
//
// Both phases here really do write into the user's notes — that is the point,
// and refusing the write would stop a knowledge-base-writing agent rehearsing
// the only thing it does. What is asserted is that nothing survives: the
// overwritten note holds the user's own bytes again, and the invented one is
// gone along with the folder it needed.
func TestBuildAndDryRunLeaveTheUsersKnowledgeBaseAsTheyFoundIt(t *testing.T) {
	fake := newFakeCoder(t, kbClobberingScript)
	flow, workspaceID, base, v := newGenFlowWithVault(t, fake)

	const original = "the user's own words"
	notes := filepath.Join(v.Root(workspaceID), "notes")
	if err := os.MkdirAll(notes, 0o750); err != nil {
		t.Fatal(err)
	}
	journalNote := filepath.Join(notes, "journal.md")
	if err := os.WriteFile(journalNote, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

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

	// Sanity: the rehearsal really ran, or this test would pass by doing nothing.
	if !strings.Contains(msg, "real dry run output") {
		t.Fatalf("the dry run did not run, so this test proves nothing; msg = %q", msg)
	}
	workDir := DraftAgentDir(base, workspaceID, "watcher")
	if n, err := os.ReadFile(filepath.Join(workDir, "invocations")); err != nil || string(n) != "2" {
		t.Fatalf("expected a build AND a dry run to have written notes; invocations = %q (%v)", n, err)
	}

	got, err := os.ReadFile(journalNote)
	if err != nil {
		t.Fatalf("the user's note is gone entirely: %v", err)
	}
	if string(got) != original {
		t.Errorf("the user's note still holds rehearsal output: %q, want %q\n"+
			"a build and a dry run rehearse an agent the user has not approved; "+
			"their writes must be undone", string(got), original)
	}

	if _, err := os.Stat(filepath.Join(notes, "invented", "report.md")); !os.IsNotExist(err) {
		t.Error("a note the rehearsal invented survived into the user's knowledge base")
	}
	if _, err := os.Stat(filepath.Join(notes, "invented")); !os.IsNotExist(err) {
		t.Error("the folder the rehearsal created survived; a directory it made should be pruned")
	}
}

// TestBuildKeepsWhatItWroteInTheAgentsOwnDirectory is the other half, and the
// one that would catch an over-eager revert: the build's OUTPUT lives under
// agents/, so a journal that reverted everything would delete the agent it just
// built and the failure would read as "the build produced nothing".
func TestBuildKeepsWhatItWroteInTheAgentsOwnDirectory(t *testing.T) {
	fake := newFakeCoder(t, kbClobberingScript)
	flow, workspaceID, base, _ := newGenFlowWithVault(t, fake)

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

	if _, _, _, err := flow.runGeneration(context.Background(), workspaceID); err != nil {
		t.Fatalf("runGeneration: %v", err)
	}

	agentMD := filepath.Join(DraftAgentDir(base, workspaceID, "watcher"), "AGENT.md")
	data, err := os.ReadFile(agentMD)
	if err != nil {
		t.Fatalf("the build's own AGENT.md was reverted away: %v", err)
	}
	if !strings.Contains(string(data), "Watches a page.") {
		t.Errorf("AGENT.md lost the build's content: %q", string(data))
	}
}

// TestStateFileHasExactlyOneRestorer is the disjointness check between the two
// best-effort restores dryRun now registers.
//
// restoreDryRunState puts state.md back to what the BUILD wrote; the write
// journal puts the knowledge base back to what the USER had. They run as nested
// defers over the same workDir, so if both claimed state.md the one running
// second would silently win and which version shipped would be an accident of
// registration order.
//
// They are disjoint because isProtected excludes agents/, and a create build's
// workDir is <vault>/<ws>/agents/draft_<slug> — so the journal records nothing
// under it at all. That is reasoning about two files in two packages, which is
// exactly the kind of claim that stops being true quietly, so it is asserted
// here on the file that actually has two candidate owners.
func TestStateFileHasExactlyOneRestorer(t *testing.T) {
	fake := newFakeCoder(t, kbClobberingScript)
	flow, workspaceID, base, _ := newGenFlowWithVault(t, fake)

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

	if _, _, _, err := flow.runGeneration(context.Background(), workspaceID); err != nil {
		t.Fatalf("runGeneration: %v", err)
	}

	statePath := filepath.Join(DraftAgentDir(base, workspaceID, "watcher"), "state.md")
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state.md is gone; something restored it out of existence: %v", err)
	}
	if string(got) != "BUILD STATE" {
		t.Errorf("state.md = %q, want %q.\n"+
			"The rehearsal's state must not ship as the saved agent's memory (a "+
			"change-detection agent would open its first real run believing it had "+
			"seen everything), and the journal must not be restoring this file either.",
			string(got), "BUILD STATE")
	}
}

// TestDryRunPromptCarriesTheVaultRoot pins a REVERSED decision, which is why it
// is spelled out rather than left to the wiring test.
//
// dryRunPrompt used to withhold VaultRoot deliberately, on the theory that an
// agent never told where the vault is would not write there. It was not true —
// the host tools resolve any path under the vault root and advertise as much in
// their own descriptions — so the omission bought nothing and cost the rehearsal
// the fidelity that is its entire purpose. Removing the field again would look
// like restoring a safety property and would only make the rehearsal diverge
// from the run it predicts.
func TestDryRunPromptCarriesTheVaultRoot(t *testing.T) {
	const root = "/data/vaults/ws1"
	got := dryRunPrompt("Watches a page.", prompts.BackendToolCalling, "[Current context]\n",
		root, root+"/agents/draft_watcher", nil, false)

	if !strings.Contains(got, root) {
		t.Fatal("the dry run's prompt no longer names the vault root, so the rehearsal is " +
			"told nothing about the knowledge base the real run will see. Containment is " +
			"vault.WriteJournal's job, not this omission's — see dryRunPrompt.")
	}
}
