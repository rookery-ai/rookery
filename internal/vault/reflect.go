package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Reflector mirrors structured database rows into the vault as human- and
// agent-readable markdown notes, each paired with a JSON sidecar under
// .kb/db-export/<table>/<id>.json for lossless backup/restore. The SQLite database
// remains the system-of-record; these notes are a derived, browsable projection.
//
// All methods are best-effort and safe to call from background goroutines: writes
// are atomic and a failure to reflect never blocks the underlying operation.
type Reflector struct {
	v *Vault
}

// Reflector returns a Reflector for this vault.
func (v *Vault) Reflector() *Reflector { return &Reflector{v: v} }

// Notifications are deliberately NOT reflected. An inbox message is a delivery
// record, not knowledge: the row lives in inbox_messages, the Home inbox renders
// it, and for an agent run the exact delivered text is already archived in
// agents/<id>/logs/run_<ts>.md under "Output sent to user". Projecting it a
// third time into inbox/<uuid>.md gave every note the same non-distinguishing
// heading ("⏰ Reminder", "🤖 weather (cron)"), grew the vault by one file per
// notification forever, and — because inbox/ was never added to
// kbExcludedDirs — fed a stream of "🌤 25°C, clear sky" into the agent- and
// skill-designer retrieval that is supposed to quote the user's own knowledge.
// Reminders in particular were never meant to reach the vault at all.
//
// vault.RemoveLegacyInboxNotes sweeps what earlier builds wrote.

// ChatNote is the reflected view of a chat and its messages.
type ChatNote struct {
	ID        string
	Name      string
	Platform  string
	CreatedAt time.Time
	Messages  []ChatTurn
}

// ChatTurn is one line of a reflected transcript.
type ChatTurn struct {
	Role      string
	Content   string
	CreatedAt time.Time
}

// ReflectChat writes chats/<id>.md (a full transcript) plus its sidecar.
func (r *Reflector) ReflectChat(workspaceID string, n ChatNote) error {
	if r == nil {
		return nil
	}
	title := n.Name
	if title == "" {
		title = "Chat"
	}
	fm := frontmatter(map[string]string{
		"type":       "chat",
		"id":         n.ID,
		"platform":   n.Platform,
		"created_at": ts(n.CreatedAt),
	})
	var b strings.Builder
	b.WriteString(fm)
	b.WriteString("# " + title + "\n\n")
	for _, m := range n.Messages {
		b.WriteString(fmt.Sprintf("**%s** · %s\n\n%s\n\n", capitalize(m.Role), ts(m.CreatedAt), strings.TrimSpace(m.Content)))
	}
	return r.write(workspaceID, filepath.Join("chats", safeName(n.ID)+".md"), b.String(), "chats", n.ID, n)
}

// RunNote is the reflected view of an agent run.
type RunNote struct {
	RunID      string
	AgentID    string
	AgentName  string
	Trigger    string
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Output     string   // raw coder output
	ChatLines  []string // user-facing [CHAT] lines
	Warnings   []string
	// ToolCalls is what the agent DID: the progress milestones it reported as
	// it worked. The note recorded the coder's words and never its actions, so
	// a run log could show an agent concluding nothing had changed without
	// showing whether it had actually looked.
	ToolCalls []string
	// Token usage, reported by the API coder (direct LLM provider). Zero for
	// CLI coders; omitted from the run log when all zero.
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ReflectAgentRun writes a markdown run log into the agent's own logs directory
// (agents/<agentID>/logs/run_<ts>.md) plus a sidecar. The note lives inside the
// agent's writable area, linked back to the agent.
func (r *Reflector) ReflectAgentRun(workspaceID string, n RunNote) error {
	if r == nil {
		return nil
	}
	stamp := n.StartedAt.UTC().Format("20060102_150405")
	rel := filepath.Join("agents", safeName(n.AgentID), "logs", "run_"+stamp+".md")
	status := "ok"
	if n.ExitCode != 0 {
		status = fmt.Sprintf("failed (exit %d)", n.ExitCode)
	}
	kv := map[string]string{
		"type":        "agent-run",
		"run_id":      n.RunID,
		"agent_id":    n.AgentID,
		"trigger":     n.Trigger,
		"status":      status,
		"started_at":  ts(n.StartedAt),
		"finished_at": ts(n.FinishedAt),
	}
	if n.TotalTokens > 0 {
		kv["prompt_tokens"] = strconv.Itoa(n.PromptTokens)
		kv["completion_tokens"] = strconv.Itoa(n.CompletionTokens)
		kv["total_tokens"] = strconv.Itoa(n.TotalTokens)
	}
	fm := frontmatter(kv)
	var b strings.Builder
	b.WriteString(fm)
	b.WriteString(fmt.Sprintf("# Run of [[%s]] — %s\n\n", agentLinkTarget(n.AgentName, n.AgentID), status))
	if n.TotalTokens > 0 {
		b.WriteString(fmt.Sprintf("> **Tokens:** %d prompt / %d completion / %d total\n\n", n.PromptTokens, n.CompletionTokens, n.TotalTokens))
	}
	if len(n.ChatLines) > 0 {
		b.WriteString("## Output sent to user\n\n")
		for _, l := range n.ChatLines {
			b.WriteString("> " + l + "\n")
		}
		b.WriteString("\n")
	}
	if len(n.Warnings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, w := range n.Warnings {
			b.WriteString("- " + w + "\n")
		}
		b.WriteString("\n")
	}
	if len(n.ToolCalls) > 0 {
		b.WriteString("## Tool calls\n\n")
		for _, t := range n.ToolCalls {
			b.WriteString("- " + t + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## Raw output\n\n```\n")
	b.WriteString(strings.TrimRight(n.Output, "\n"))
	b.WriteString("\n```\n")
	return r.write(workspaceID, rel, b.String(), "agent_runs", n.RunID, n)
}

// Unreflect is the inverse of write: it removes a reflected note and its JSON
// sidecar, for when the underlying database row is deleted.
//
// Reflection is a projection of the DB, so deleting a row without unreflecting
// it leaves the note behind as a ghost — the deleted chat/inbox message keeps
// appearing in the knowledge base browser and in search results, which is
// exactly how a "deleted" item appears not to have been deleted.
//
// Both removals are best-effort and a missing file is not an error: reflection
// itself is best-effort (a note may never have been written), and the caller has
// already committed the DB delete, so failing here would report an error for an
// operation that did succeed. Errors that are NOT "already gone" are returned so
// a caller that cares can log them.
//
// Passing an empty table or id skips the sidecar, for notes written without one.
func (r *Reflector) Unreflect(workspaceID, relMarkdown, table, id string) error {
	if r == nil {
		return nil
	}
	var firstErr error
	remove := func(rel string) {
		if err := r.v.Delete(workspaceID, rel); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	if relMarkdown != "" {
		remove(relMarkdown)
	}
	if table != "" && id != "" {
		remove(filepath.Join(InternalDir, "db-export", table, safeName(id)+".json"))
	}
	return firstErr
}

// UnreflectChat names the note path and table alongside the ReflectX method that
// wrote them, so the two halves cannot drift: a change to where a note is
// written is a compile-visible change here too.
func (r *Reflector) UnreflectChat(workspaceID, chatID string) error {
	return r.Unreflect(workspaceID, filepath.Join("chats", safeName(chatID)+".md"), "chats", chatID)
}

// UnreflectAgentRuns drops the db-export sidecars of every run belonging to one
// agent. The run NOTES live inside the agent's own directory and are removed
// wholesale with it, but the sidecars live under .kb/db-export/agent_runs/ keyed
// by RUN id, so nothing about their path identifies the agent — the only way to
// find them is to read each one back and check its AgentID.
//
// Returns the number removed. A sidecar that cannot be read or parsed is skipped
// rather than deleted: an unreadable file is not evidence that it belongs to the
// agent being deleted.
func (r *Reflector) UnreflectAgentRuns(workspaceID, agentID string) (int, error) {
	if r == nil || agentID == "" {
		return 0, nil
	}
	dir := filepath.Join(InternalDir, "db-export", "agent_runs")
	entries, err := r.v.List(workspaceID, dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var removed int
	var firstErr error
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		data, err := r.v.ReadNote(workspaceID, e.Path)
		if err != nil {
			continue
		}
		var sidecar struct {
			AgentID string `json:"AgentID"`
		}
		if err := json.Unmarshal(data, &sidecar); err != nil || sidecar.AgentID != agentID {
			continue
		}
		if err := r.v.Delete(workspaceID, e.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

// write persists the markdown note and its JSON sidecar. The sidecar holds the
// full structured value for restore fidelity.
func (r *Reflector) write(workspaceID, relMarkdown, content, table, id string, sidecar any) error {
	if err := r.v.WriteNote(workspaceID, relMarkdown, []byte(content)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		return err
	}
	sidecarRel := filepath.Join(InternalDir, "db-export", table, safeName(id)+".json")
	return r.v.WriteNote(workspaceID, sidecarRel, data)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// frontmatter renders a YAML frontmatter block, omitting empty values, with keys
// in a stable order.
func frontmatter(kv map[string]string) string {
	order := []string{"type", "id", "run_id", "agent_id", "platform", "trigger",
		"status", "created_at", "started_at", "finished_at",
		"prompt_tokens", "completion_tokens", "total_tokens"}
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range order {
		if v := kv[k]; v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	b.WriteString("---\n\n")
	return b.String()
}

// safeName makes an id safe to use as a filename segment.
func safeName(id string) string {
	repl := func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}
	return strings.Map(repl, id)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func agentLinkTarget(name, id string) string {
	if name != "" {
		return name
	}
	return id
}
