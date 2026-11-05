package vault

import (
	"strings"
	"testing"
	"time"
)

func TestStoppedRunNote(t *testing.T) {
	v := New(t.TempDir())
	r := v.Reflector()
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	err := r.ReflectAgentRun("user-1", RunNote{
		RunID: "run-1", AgentID: "agent-1", AgentName: "Looper", Trigger: "manual",
		ExitCode: -1, Stopped: true, StartedAt: start, FinishedAt: start,
		ChatLines: []string{"half a message"},
		Activity:  []string{"🔧 read_file(notes.md)"},
		Output:    "raw",
	})
	if err != nil {
		t.Fatalf("ReflectAgentRun: %v", err)
	}
	note, err := v.ReadNote("user-1", RunNotePath("agent-1", start))
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	s := string(note)

	if !strings.Contains(s, "status: stopped") {
		t.Errorf("frontmatter should say stopped, got:\n%s", s)
	}
	if strings.Contains(s, "failed") {
		t.Errorf("a stopped run must not read as failed:\n%s", s)
	}
	if !strings.Contains(s, "read_file(notes.md)") {
		t.Errorf("tool calls should be recorded as usual:\n%s", s)
	}
	// Nothing was delivered, so the usual heading would be untrue.
	if strings.Contains(s, "## Output sent to user") {
		t.Errorf("undelivered lines must not be labelled as sent:\n%s", s)
	}
	if !strings.Contains(s, "half a message") {
		t.Errorf("undelivered lines should still be kept in the log:\n%s", s)
	}
	idx := strings.LastIndex(s, "## ")
	if idx < 0 || !strings.HasPrefix(s[idx:], "## Stopped") {
		t.Errorf("the stop notice should be the note's last section:\n%s", s)
	}
}

func TestNormalRunNoteHasNoStopNotice(t *testing.T) {
	v := New(t.TempDir())
	r := v.Reflector()
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := r.ReflectAgentRun("user-1", RunNote{
		RunID: "run-2", AgentID: "agent-1", AgentName: "Calm", Trigger: "cron",
		StartedAt: start, FinishedAt: start, ChatLines: []string{"hello"},
	}); err != nil {
		t.Fatalf("ReflectAgentRun: %v", err)
	}
	note, err := v.ReadNote("user-1", RunNotePath("agent-1", start))
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	s := string(note)
	if strings.Contains(s, "## Stopped") || !strings.Contains(s, "## Output sent to user") {
		t.Errorf("a normal run's note should be unchanged:\n%s", s)
	}
}
