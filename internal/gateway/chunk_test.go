package gateway

import (
	"strings"
	"testing"
)

// TestMsgLenCountsUTF16CodeUnits is the measurement the platforms actually use.
// Designer output is dense with emoji and every astral-plane rune is TWO UTF-16
// units, so rune counting would under-count exactly the messages that were being
// rejected — and the platform would still answer 400.
func TestMsgLenCountsUTF16CodeUnits(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"héllo", 5}, // BMP, one unit each
		{"🔧", 2},     // astral, surrogate pair
		{"🔧🔧", 4},    // two pairs
		{"a🔧b", 4},   // mixed
		{"⏳", 1},     // U+23F3 is BMP despite looking like an emoji
	} {
		if got := msgLen(tc.in); got != tc.want {
			t.Errorf("msgLen(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestSplitMessageLeavesShortTextAlone: the overwhelmingly common case must be
// byte-identical, so chunking cannot change behaviour for ordinary replies.
func TestSplitMessageLeavesShortTextAlone(t *testing.T) {
	for _, in := range []string{"", "hello", "a\n\nb", strings.Repeat("x", 2000)} {
		got := splitMessage(in, 2000)
		if len(got) != 1 || got[0] != in {
			t.Errorf("splitMessage(%d chars) = %d chunks, want the input unchanged", len(in), len(got))
		}
	}
}

// TestSplitMessageNeverReturnsEmpty: callers loop over the result and send each
// chunk, so an empty slice would silently drop the message — the exact failure
// mode this file exists to remove.
func TestSplitMessageNeverReturnsEmpty(t *testing.T) {
	for _, in := range []string{"", "\n", "\n\n\n"} {
		if got := splitMessage(in, 10); len(got) == 0 {
			t.Errorf("splitMessage(%q) returned no chunks", in)
		}
	}
}

// TestSplitMessageRespectsTheLimit is the core guarantee. A chunk over the limit
// is rejected by the platform, which is where this started.
func TestSplitMessageRespectsTheLimit(t *testing.T) {
	cases := map[string]string{
		"long paragraphs": strings.Repeat("This is a sentence about agents.\n\n", 300),
		"one huge line":   strings.Repeat("x", 9000),
		"emoji heavy":     strings.Repeat("🔧 running a tool call\n", 400),
		"no newlines":     strings.Repeat("word ", 2000),
		"mixed":           strings.Repeat("🔧 x\n\n", 200) + strings.Repeat("y", 5000),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			for i, chunk := range splitMessage(in, discordMessageLimit) {
				if n := msgLen(chunk); n > discordMessageLimit {
					t.Errorf("chunk %d is %d units, limit is %d", i, n, discordMessageLimit)
				}
			}
		})
	}
}

// TestSplitMessagePreservesContent: the whole point is delivering the agent
// overview, so no text may be lost. Newlines at split points and reopened fence
// markers are the only permitted differences.
func TestSplitMessagePreservesContent(t *testing.T) {
	in := strings.Repeat("A line of the agent overview that the user must read.\n", 200)

	joined := strings.Join(splitMessage(in, discordMessageLimit), "\n")
	strip := func(s string) string {
		return strings.Join(strings.Fields(s), " ")
	}
	if strip(joined) != strip(in) {
		t.Error("content was lost or reordered while splitting")
	}
}

// TestSplitMessageKeepsCodeFencesBalanced: a split inside a fenced block would
// render half as prose and leave the other half unterminated. Agent builds emit
// code and test output constantly, so this is the common case, not an edge one.
func TestSplitMessageKeepsCodeFencesBalanced(t *testing.T) {
	in := "Here is the tool it wrote:\n\n```python\n" +
		strings.Repeat("print('a line of generated code')\n", 200) +
		"```\n\nThat is all."

	chunks := splitMessage(in, discordMessageLimit)
	if len(chunks) < 2 {
		t.Fatalf("expected the fixture to split, got %d chunk(s)", len(chunks))
	}
	for i, chunk := range chunks {
		if n := strings.Count(chunk, "```"); n%2 != 0 {
			t.Errorf("chunk %d has %d fence markers — unbalanced:\n%s", i, n, chunk)
		}
	}
}

// TestSplitMessageSplitsOnBoundariesWhenItCan: a mid-word cut is legible but
// ugly, and unnecessary when a line boundary is available.
func TestSplitMessageSplitsOnBoundariesWhenItCan(t *testing.T) {
	line := strings.Repeat("x", 100)
	in := strings.Repeat(line+"\n", 60) // 6060 units, limit 2000

	for i, chunk := range splitMessage(in, 2000) {
		for _, l := range strings.Split(strings.TrimSpace(chunk), "\n") {
			if l != "" && l != line {
				t.Errorf("chunk %d contains a partial line %q — a boundary was available", i, l)
			}
		}
	}
}

// TestSplitMessageNeverSplitsMidRune: a cut inside a multi-byte rune emits
// invalid UTF-8, which a platform rejects just as firmly as an over-long
// message.
func TestSplitMessageNeverSplitsMidRune(t *testing.T) {
	in := strings.Repeat("🔧", 3000) // one 4-byte rune, no boundaries at all

	for i, chunk := range splitMessage(in, 100) {
		if !isValidUTF8(chunk) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}
		if n := msgLen(chunk); n > 100 {
			t.Errorf("chunk %d is %d units, limit is 100", i, n)
		}
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' && !strings.Contains(s, "�") {
			return false
		}
	}
	// range over a string yields RuneError for invalid bytes; compare the
	// round-trip instead, which is exact.
	return string([]rune(s)) == s
}

// TestSendChunkedIsSequentialAndStopsOnError. Ordering is not guaranteed across
// concurrent sends on either platform, and an interleaved agent overview is
// worse than a truncated one.
func TestSendChunkedIsSequentialAndStopsOnError(t *testing.T) {
	in := strings.Repeat("line of text\n", 500)

	var got []string
	if err := sendChunked(in, 200, func(s string) error {
		got = append(got, s)
		return nil
	}); err != nil {
		t.Fatalf("sendChunked: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("expected several chunks, got %d", len(got))
	}
	if strings.Join(got, "\n") != strings.Join(splitMessage(in, 200), "\n") {
		t.Error("sendChunked did not deliver the chunks in order")
	}

	// A failing send must stop rather than firing the rest at a platform that
	// has already refused one.
	calls := 0
	err := sendChunked(in, 200, func(string) error {
		calls++
		return errFake
	})
	if err == nil {
		t.Error("sendChunked must surface the send error")
	}
	if calls != 1 {
		t.Errorf("sendChunked made %d calls after an error, want 1", calls)
	}
}

var errFake = &fakeErr{}

type fakeErr struct{}

func (e *fakeErr) Error() string { return "send failed" }

// TestPlatformLimitsMatchTheDocumentedCaps. These are the numbers the platforms
// enforce; drifting from them reintroduces the silent drop.
func TestPlatformLimitsMatchTheDocumentedCaps(t *testing.T) {
	if discordMessageLimit != 2000 {
		t.Errorf("discord limit = %d, Discord enforces 2000", discordMessageLimit)
	}
	if telegramMessageLimit != 4096 {
		t.Errorf("telegram limit = %d, Telegram enforces 4096", telegramMessageLimit)
	}
}
