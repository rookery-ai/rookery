package agentrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
)

// TestAgentStatePersistsAcrossRunsAsMarkdown proves the runner's state-loading
// path (agentdesigner.ReadState/WriteState) round-trips state through
// state.md rather than state.json — state.json must never be created.
func TestAgentStatePersistsAcrossRunsAsMarkdown(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.md")
	if err := agentdesigner.WriteState(p, "T", map[string]any{"cursor": "abc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json must not be created any more")
	}
	got, err := agentdesigner.ReadState(p)
	if err != nil || got["cursor"] != "abc" {
		t.Fatalf("state not readable: %#v %v", got, err)
	}
}

// ─── saveState / mergeState (post-run save path) ──────────────────────────
//
// These pin the highest-risk part of SP7: the merge-and-save path a real run
// hits after every [STATE] update. state.json round-tripping (above) does not
// exercise saveState/mergeState at all, so a regression there (e.g. saveState
// clobbering hand-written "## Notes" prose, or mergeState failing to honor a
// null-delete) would previously go undetected.

// TestSaveStateFirstRunCreatesWellFormedDocument covers scenario 1: saving
// state onto a nonexistent state.md creates a well-formed document (heading +
// fenced json) and the state reads back through the public ReadState API.
func TestSaveStateFirstRunCreatesWellFormedDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	state := map[string]interface{}{"cursor": "abc", "count": float64(3)}
	if err := saveState(dir, "My Agent", state); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state.md not created: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "# State — My Agent") {
		t.Fatalf("missing heading in fresh document:\n%s", text)
	}
	if !strings.Contains(text, "```json") {
		t.Fatalf("missing json fence in fresh document:\n%s", text)
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got["cursor"] != "abc" || got["count"] != float64(3) {
		t.Fatalf("state did not read back: %#v", got)
	}
}

// TestSaveStatePreservesHandWrittenNotes covers scenario 2 — the one that
// matters most: a pre-existing state.md with a "## Notes" section containing
// hand-written prose must survive a save byte-for-byte, while only the fenced
// machine-state block changes.
func TestSaveStatePreservesHandWrittenNotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	notesBlock := "## Notes\n\nDo not touch the cursor manually — it tracks the last\nprocessed message ID from the source inbox.\n"
	seed := agentdesigner.RenderStateTemplate("My Agent", `{
  "cursor": "old-value"
}`) + "\n" + notesBlock
	if err := os.WriteFile(path, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := saveState(dir, "My Agent", map[string]interface{}{"cursor": "new-value"}); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	if !strings.Contains(text, notesBlock) {
		t.Fatalf("Notes section (heading + prose) was not preserved byte-for-byte.\ngot:\n%s", text)
	}

	got, err := agentdesigner.ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got["cursor"] != "new-value" {
		t.Fatalf("fenced state was not updated: %#v", got)
	}
}

// TestMergeStateNullDeletesKey covers scenario 3: a [STATE] update whose value
// is nil removes that key from the merged state, leaving the surviving keys
// intact, and that merged result persists correctly through saveState.
func TestMergeStateNullDeletesKey(t *testing.T) {
	existing := map[string]interface{}{
		"cursor": "abc",
		"count":  float64(3),
		"stale":  "drop-me",
	}
	update := map[string]interface{}{
		"cursor": "def",
		"stale":  nil,
	}

	mergeState(existing, update)

	if _, ok := existing["stale"]; ok {
		t.Fatalf("stale key was not deleted: %#v", existing)
	}
	if existing["cursor"] != "def" {
		t.Fatalf("cursor not updated: %#v", existing)
	}
	if existing["count"] != float64(3) {
		t.Fatalf("unrelated key count was not preserved: %#v", existing)
	}

	// Persist the merged result and confirm it round-trips through the real
	// save path too — merge correctness alone doesn't guarantee the deletion
	// survives the write.
	dir := t.TempDir()
	if err := saveState(dir, "My Agent", existing); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	got, err := agentdesigner.ReadState(filepath.Join(dir, "state.md"))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if _, ok := got["stale"]; ok {
		t.Fatalf("deleted key resurfaced after save/read round-trip: %#v", got)
	}
	if got["cursor"] != "def" {
		t.Fatalf("cursor not persisted: %#v", got)
	}
}

// Scenario 4 ("no [STATE] emitted → file not rewritten at all") is NOT
// covered here. The guard that skips saveState when there are no state
// updates lives caller-side in runCoderTurns (runner.go: `if
// len(parsed.stateUpdates) > 0 { ... saveState(...) }`), not inside saveState
// itself — saveState/WriteState always write when called. Reaching that guard
// requires driving runCoderTurns through a real coder.Coder.Generate call
// (CLI subprocess or API HTTP loop), which the task explicitly rules out
// building here. See the fix report for the full reasoning.

func TestParseCoderOutputChatBlankLineDoesNotDropContent(t *testing.T) {
	// Reproduces the real failure: the runtime emitted a header, a blank line,
	// then the actual content. The old parser ended the [CHAT] block at the
	// blank line and silently discarded the content. The whole message must
	// now be captured.
	out := parseCoderOutput(strings.Join([]string{
		"[CHAT] 📝 Added a new test to your notes:",
		"",
		"**Soil Percolation Test** (Soil / site suitability test)",
		"A soil percolation test measures how quickly water drains through soil.",
		`[STATE]{"last_added_test": "Soil Percolation Test", "run_count": 1}[/STATE]`,
	}, "\n"))

	if len(out.chatLines) != 1 {
		t.Fatalf("expected 1 chat line, got %d: %q", len(out.chatLines), out.chatLines)
	}
	msg := out.chatLines[0]
	if !strings.Contains(msg, "Soil Percolation Test") {
		t.Errorf("content after blank line was dropped: %q", msg)
	}
	if !strings.Contains(msg, "percolation test measures") {
		t.Errorf("description was dropped: %q", msg)
	}
	if !strings.HasPrefix(msg, "📝 Added a new test to your notes:") {
		t.Errorf("header lost: %q", msg)
	}
	if len(out.stateUpdates) != 1 || out.stateUpdates[0]["last_added_test"] != "Soil Percolation Test" {
		t.Errorf("state not parsed: %+v", out.stateUpdates)
	}
}

func TestParseCoderOutputBareChatMarkerOwnLine(t *testing.T) {
	// Reproduces the real notion-porter-1 delivery failure: a weak model (qwen) put the
	// [CHAT] marker ALONE on its own line with the message on the FOLLOWING lines, then
	// closed with [STATE] and a trailing [SILENT]. The old parser required "[CHAT] " with an
	// inline space, so it never opened the block — chatLines stayed empty and [SILENT]
	// suppressed both the prose fallback and the empty-run warning: a silent non-delivery.
	out := parseCoderOutput(strings.Join([]string{
		"[CHAT]",
		"✅ Ported 47 pages from Notion Personal to your knowledge base",
		"",
		"• notes/Personal/Personal.md",
		"• notes/Personal/Finance.md",
		`[STATE]{"last_ported": "2026-07-10T10:50:12Z"}[/STATE]`,
		"[SILENT]",
	}, "\n"))

	if len(out.chatLines) != 1 {
		t.Fatalf("expected 1 chat line from a bare [CHAT] marker, got %d: %q", len(out.chatLines), out.chatLines)
	}
	msg := out.chatLines[0]
	if !strings.HasPrefix(msg, "✅ Ported 47 pages") {
		t.Errorf("message on the line(s) after a bare [CHAT] marker was dropped: %q", msg)
	}
	if !strings.Contains(msg, "notes/Personal/Finance.md") {
		t.Errorf("file list was dropped: %q", msg)
	}
	if len(out.stateUpdates) != 1 {
		t.Errorf("state not parsed: %+v", out.stateUpdates)
	}
	// A real [CHAT] was captured, so delivery must NOT be suppressed by the trailing [SILENT]
	// (the runner only honors silent when chatLines is empty).
	if !out.silent {
		t.Errorf("the trailing [SILENT] should still be recorded (informational)")
	}
}

func TestParseCoderOutputChatSingleLine(t *testing.T) {
	out := parseCoderOutput("[CHAT] 💭 Stay curious — momentum favors the prepared.\n")
	if len(out.chatLines) != 1 || out.chatLines[0] != "💭 Stay curious — momentum favors the prepared." {
		t.Fatalf("expected single-line chat, got %q", out.chatLines)
	}
}

func TestParseCoderOutputMultipleChatBlocks(t *testing.T) {
	// A new [CHAT] marker starts a new block; the previous block is flushed.
	out := parseCoderOutput("[CHAT] first message\n[CHAT] second message\n")
	if len(out.chatLines) != 2 || out.chatLines[0] != "first message" || out.chatLines[1] != "second message" {
		t.Fatalf("expected two separate chat lines, got %q", out.chatLines)
	}
}

func TestParseCoderOutputStrayCloseTagStripped(t *testing.T) {
	// The [CHAT] protocol has no close tag, but weak models emit "[/CHAT]" anyway.
	// It must NEVER leak into the delivered message — in any of the common forms.
	t.Run("inline trailing close tag", func(t *testing.T) {
		out := parseCoderOutput("[CHAT] Here is your quote. [/CHAT]\n")
		if len(out.chatLines) != 1 || out.chatLines[0] != "Here is your quote." {
			t.Fatalf("inline [/CHAT] must be stripped; got %q", out.chatLines)
		}
	})
	t.Run("standalone close tag line", func(t *testing.T) {
		out := parseCoderOutput("[CHAT] Here is your quote.\n[/CHAT]\n")
		if len(out.chatLines) != 1 || out.chatLines[0] != "Here is your quote." {
			t.Fatalf("standalone [/CHAT] line must be dropped; got %q", out.chatLines)
		}
	})
	t.Run("multi-line block then close tag", func(t *testing.T) {
		out := parseCoderOutput(strings.Join([]string{
			"[CHAT] Line one of the message.",
			"Line two of the message.",
			"[/CHAT]",
		}, "\n"))
		if len(out.chatLines) != 1 {
			t.Fatalf("expected one chat block, got %q", out.chatLines)
		}
		if strings.Contains(out.chatLines[0], "[/CHAT]") {
			t.Fatalf("[/CHAT] leaked into message: %q", out.chatLines[0])
		}
		if !strings.Contains(out.chatLines[0], "Line two") {
			t.Fatalf("continuation line lost: %q", out.chatLines[0])
		}
	})
}

func TestParseCoderOutputStateEndsChatBlock(t *testing.T) {
	// A [STATE] block marker terminates an open [CHAT] block.
	out := parseCoderOutput(strings.Join([]string{
		"[CHAT] done",
		"[STATE]",
		`{"k": 1}`,
		"[/STATE]",
	}, "\n"))
	if len(out.chatLines) != 1 || out.chatLines[0] != "done" {
		t.Fatalf("chat not flushed at [STATE]: %q", out.chatLines)
	}
	if len(out.stateUpdates) != 1 || out.stateUpdates[0]["k"] != float64(1) {
		t.Fatalf("state not parsed: %+v", out.stateUpdates)
	}
}

func TestParseCoderOutputSilentAgent(t *testing.T) {
	// No [CHAT] at all — a silent run. chatLines stays empty (valid).
	out := parseCoderOutput("[STATE]{\"ran\": true}[/STATE]\n")
	if len(out.chatLines) != 0 {
		t.Fatalf("silent run produced chat output: %q", out.chatLines)
	}
}

func TestParseCoderOutputCallAgent(t *testing.T) {
	out := parseCoderOutput("[CHAT] delegating\n[CALL: daily-digest]\n")
	if len(out.callAgents) != 1 || out.callAgents[0] != "daily-digest" {
		t.Fatalf("call agent not parsed: %+v", out.callAgents)
	}
	if len(out.chatLines) != 1 || out.chatLines[0] != "delegating" {
		t.Fatalf("chat before [CALL] lost: %q", out.chatLines)
	}
}

func TestParseCoderOutputSilentMarker(t *testing.T) {
	out := parseCoderOutput("[STATE]{\"ran\": true}[/STATE]\n[SILENT]\n")
	if !out.silent {
		t.Fatal("[SILENT] not detected")
	}
	if len(out.chatLines) != 0 {
		t.Fatalf("silent run produced chat: %q", out.chatLines)
	}
}

func TestParseCoderOutputEmptyChatDropped(t *testing.T) {
	// [CHAT] with only whitespace must not produce a blank delivered message.
	out := parseCoderOutput("[CHAT]   \n\n   \n")
	if len(out.chatLines) != 0 {
		t.Fatalf("empty [CHAT] delivered a blank message: %q", out.chatLines)
	}
}

func TestExtractProseMessageStripsMarkers(t *testing.T) {
	raw := strings.Join([]string{
		"Let me check the notes.",
		"[STATE]",
		`{"last_added": "A1C"}`,
		"[/STATE]",
		"Added Hemoglobin A1C to your notes.",
		"[CALL: digest]",
		"[SILENT]",
	}, "\n")
	prose := extractProseMessage(raw)
	if strings.Contains(prose, "[STATE]") || strings.Contains(prose, "last_added") {
		t.Errorf("state block leaked into prose: %q", prose)
	}
	if strings.Contains(prose, "[CALL:") || strings.Contains(prose, "[SILENT]") {
		t.Errorf("markers leaked into prose: %q", prose)
	}
	if !strings.Contains(prose, "Added Hemoglobin A1C to your notes.") {
		t.Errorf("real prose dropped: %q", prose)
	}
}

func TestExtractProseMessageEmptyWhenOnlyMarkers(t *testing.T) {
	raw := "[STATE]{\"k\":1}[/STATE]\n[SILENT]\n[CALL: x]\n"
	if extractProseMessage(raw) != "" {
		t.Fatalf("expected empty prose, got %q", extractProseMessage(raw))
	}
}

func TestExtractProseMessageStripsBlockedBlock(t *testing.T) {
	raw := "[BLOCKED]\nroot cause: bad\n[/BLOCKED]\nDone — fixed it."
	prose := extractProseMessage(raw)
	if strings.Contains(prose, "[BLOCKED]") || strings.Contains(prose, "root cause") {
		t.Errorf("blocked block leaked: %q", prose)
	}
	if !strings.Contains(prose, "Done — fixed it.") {
		t.Errorf("prose after blocked block dropped: %q", prose)
	}
}
