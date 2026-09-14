package web

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	codersvc "github.com/rookery-ai/rookery/internal/coder"
)

// A chat turn that answers "Done! Updated your note" having called no write
// tool is, in the log, indistinguishable from one that actually wrote the file.
// That is a real reported failure on the weak-model tier this platform ships,
// and it stayed a theory for as long as `milestones` (a COUNT) was all the line
// carried: two tool calls happened and nothing recorded WHICH.
//
// The engine computes the trace for every API turn and agentrunner already logs
// it; chat discarded it one layer up, in runChatCoder's `return result.Text`.
// This test pins the plumbing, because narrowing that signature back to a
// string is exactly the shape of tidy-up that would silently remove the only
// evidence this failure ever leaves behind.
func TestChatTurnLogsWhichToolsRan(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)

	s.testCoderReply = "Done! Updated your note."
	s.testCoderTrace = []codersvc.ToolCallStat{
		{Name: "read_file", Turn: 1, Bytes: 367},
		{Name: "edit_file", Turn: 2, Bytes: 54, Error: true},
	}
	s.testCoderStop = "budget"

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, ok := s.startChatTurn(workspaceID, chatID, "change Status: draft to Status: final"); !ok {
		t.Fatal("startChatTurn refused a first turn")
	}
	waitForTurn(t, s, chatID)

	got := buf.String()
	if !strings.Contains(got, "chat: turn finished") {
		t.Fatalf("no turn-finished line logged; got:\n%s", got)
	}
	// Each tool by name, with its call count and its error count — the three
	// facts that separate "never tried", "tried and was refused" and "wrote".
	for _, want := range []string{"read_file", "edit_file", "1 err", "stop_reason=budget"} {
		if !strings.Contains(got, want) {
			t.Errorf("turn-finished line is missing %q; got:\n%s", want, got)
		}
	}
}

// The other half: a turn that called nothing must SAY so rather than omitting
// the field, or "no tools ran" reads identically to "this build predates the
// logging" — which is the ambiguity the whole line exists to remove.
func TestChatTurnLogsWhenNoToolsRan(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	s.testCoderReply = "Done! Updated your note."

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
		t.Fatal("startChatTurn refused a first turn")
	}
	waitForTurn(t, s, chatID)

	if got := buf.String(); !strings.Contains(got, "(no tool calls)") {
		t.Errorf("a turn with no tool calls must say so; got:\n%s", got)
	}
}
