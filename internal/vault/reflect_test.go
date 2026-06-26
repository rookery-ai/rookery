package vault

import (
	"strings"
	"testing"
	"time"
)

func TestReflectReminderWritesNoteAndSidecar(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	r := v.Reflector()

	when := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	err := r.ReflectReminder(user, ReminderNote{
		ID: "rem-1", Message: "check the oven", RemindAt: when, Sent: true, CreatedAt: when,
	})
	if err != nil {
		t.Fatalf("ReflectReminder: %v", err)
	}

	note, err := v.ReadNote(user, "reminders/rem-1.md")
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	s := string(note)
	if !strings.Contains(s, "check the oven") || !strings.Contains(s, "status: sent") {
		t.Errorf("note missing content/frontmatter: %q", s)
	}

	// JSON sidecar exists for restore fidelity.
	if _, err := v.ReadNote(user, ".kb/db-export/reminders/rem-1.json"); err != nil {
		t.Errorf("sidecar missing: %v", err)
	}
}

func TestReflectAgentRunLandsInAgentDir(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	r := v.Reflector()

	start := time.Date(2026, 6, 22, 10, 30, 0, 0, time.UTC)
	err := r.ReflectAgentRun(user, RunNote{
		RunID: "run-1", AgentID: "agent-1", AgentName: "price-tracker",
		Trigger: "cron", ExitCode: 0, StartedAt: start, FinishedAt: start,
		Output: "raw stuff", ChatLines: []string{"BTC is $66k"},
	})
	if err != nil {
		t.Fatalf("ReflectAgentRun: %v", err)
	}
	note, err := v.ReadNote(user, "agents/agent-1/logs/run_20260622_103000.md")
	if err != nil {
		t.Fatalf("run note missing: %v", err)
	}
	s := string(note)
	if !strings.Contains(s, "[[price-tracker]]") || !strings.Contains(s, "BTC is $66k") {
		t.Errorf("run note missing link/output: %q", s)
	}
}

func TestReflectChatTranscript(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	r := v.Reflector()

	when := time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
	err := r.ReflectChat(user, ChatNote{
		ID: "sess-1", Name: "Planning chat", Platform: "telegram", CreatedAt: when,
		Messages: []ChatTurn{
			{Role: "user", Content: "hi", CreatedAt: when},
			{Role: "assistant", Content: "hello", CreatedAt: when},
		},
	})
	if err != nil {
		t.Fatalf("ReflectChat: %v", err)
	}
	note, _ := v.ReadNote(user, "chats/sess-1.md")
	s := string(note)
	if !strings.Contains(s, "# Planning chat") || !strings.Contains(s, "**User**") || !strings.Contains(s, "hello") {
		t.Errorf("transcript missing parts: %q", s)
	}
}
