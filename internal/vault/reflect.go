package vault

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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

// ReminderNote is the reflected view of a reminder row.
type ReminderNote struct {
	ID         string
	Message    string
	RemindAt   time.Time
	Recurrence string
	Sent       bool
	CreatedAt  time.Time
}

// ReflectReminder writes reminders/<id>.md plus its sidecar.
func (r *Reflector) ReflectReminder(workspaceID string, n ReminderNote) error {
	if r == nil {
		return nil
	}
	status := "pending"
	if n.Sent {
		status = "sent"
	}
	fm := frontmatter(map[string]string{
		"type":       "reminder",
		"id":         n.ID,
		"remind_at":  ts(n.RemindAt),
		"recurrence": n.Recurrence,
		"status":     status,
		"created_at": ts(n.CreatedAt),
	})
	body := fmt.Sprintf("# Reminder\n\n%s\n\n- **When:** %s\n- **Status:** %s\n",
		n.Message, ts(n.RemindAt), status)
	if n.Recurrence != "" {
		body += "- **Repeats:** " + n.Recurrence + "\n"
	}
	return r.write(workspaceID, filepath.Join("reminders", safeName(n.ID)+".md"), fm+body, "reminders", n.ID, n)
}

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
	fm := frontmatter(map[string]string{
		"type":        "agent-run",
		"run_id":      n.RunID,
		"agent_id":    n.AgentID,
		"trigger":     n.Trigger,
		"status":      status,
		"started_at":  ts(n.StartedAt),
		"finished_at": ts(n.FinishedAt),
	})
	var b strings.Builder
	b.WriteString(fm)
	b.WriteString(fmt.Sprintf("# Run of [[%s]] — %s\n\n", agentLinkTarget(n.AgentName, n.AgentID), status))
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
	b.WriteString("## Raw output\n\n```\n")
	b.WriteString(strings.TrimRight(n.Output, "\n"))
	b.WriteString("\n```\n")
	return r.write(workspaceID, rel, b.String(), "agent_runs", n.RunID, n)
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
		"status", "remind_at", "recurrence", "created_at", "started_at", "finished_at"}
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
