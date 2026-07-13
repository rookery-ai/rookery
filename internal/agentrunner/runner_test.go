package agentrunner

import (
	"strings"
	"testing"
)

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
