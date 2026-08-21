package coder

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/vault"
)

// agentToolSet mirrors a real agent run: the working directory is the agent's
// OWN folder inside the vault, not the vault root.
func agentToolSet(t *testing.T) (*hostToolSet, string) {
	t.Helper()
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	agentDir := filepath.Join(v.Root(ws), "agents", "a1")
	return &hostToolSet{workspaceID: ws, vlt: v, workDir: agentDir}, v.Root(ws)
}

// An agent told to write to the knowledge base's notes/ wrote to its OWN
// notes/ instead — a relative path resolved against its working directory — and
// nothing anywhere said so. The owner saw an agent that ran, exited 0, and
// produced no note.
//
// The write still succeeds: agents/<id>/notes/ is a legitimate place for an
// agent's own notes, so redirecting would break the correct case to fix the
// incorrect one. What the result can do is say where the file actually went.
func TestWriteFileWarnsWhenAVaultFolderLandsInTheAgentDir(t *testing.T) {
	h, root := agentToolSet(t)

	out := h.execute(context.Background(), toolCall("write_file",
		`{"path":"notes/spending-alerts/2026-08-20.md","content":"# Alerts\n"}`))

	if strings.HasPrefix(out, "error:") {
		t.Fatalf("the write should still succeed: %s", out)
	}
	if !strings.Contains(out, "not the user's knowledge base") {
		t.Errorf("no warning that the file went to the agent's own directory:\n%s", out)
	}
	// The warning must name the path that WOULD have been right, or the model
	// has to guess at the fix.
	if !strings.Contains(out, filepath.Join(root, "notes/spending-alerts/2026-08-20.md")) {
		t.Errorf("warning does not name the absolute vault path:\n%s", out)
	}
}

// An agent writing its OWN files must not be nagged.
func TestWriteFileDoesNotWarnForAnAgentsOwnFiles(t *testing.T) {
	h, _ := agentToolSet(t)
	for _, p := range []string{"tools/fetch.py", "state.md", "logs/run.md", "AGENT.md"} {
		out := h.execute(context.Background(), toolCall("write_file",
			`{"path":"`+p+`","content":"x"}`))
		if strings.Contains(out, "not the user's knowledge base") {
			t.Errorf("warned about %s, which is the agent's own file:\n%s", p, out)
		}
	}
}

// An ABSOLUTE vault path is the correct form and must pass silently.
func TestWriteFileDoesNotWarnForAnAbsoluteVaultPath(t *testing.T) {
	h, root := agentToolSet(t)
	out := h.execute(context.Background(), toolCall("write_file",
		`{"path":"`+filepath.Join(root, "notes/ok.md")+`","content":"x"}`))
	if strings.Contains(out, "not the user's knowledge base") {
		t.Errorf("warned about a correct absolute vault path:\n%s", out)
	}
}

// Chat runs AT the vault root, so a relative notes/ path is already correct
// there and the warning would be actively wrong.
func TestWriteFileDoesNotWarnWhenWorkingAtTheVaultRoot(t *testing.T) {
	v := vault.New(t.TempDir())
	const ws = "ws1"
	if err := v.EnsureScaffold(ws); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	h := &hostToolSet{workspaceID: ws, vlt: v, workDir: v.Root(ws)}

	out := h.execute(context.Background(), toolCall("write_file",
		`{"path":"notes/ok.md","content":"x"}`))
	if strings.Contains(out, "not the user's knowledge base") {
		t.Errorf("warned in chat, where the path is already correct:\n%s", out)
	}
}
