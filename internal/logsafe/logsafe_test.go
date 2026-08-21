package logsafe

import (
	"strings"
	"testing"
)

// The whole point: a value carrying a newline must not be able to fabricate a
// log entry that reads like the server's own.
func TestValueNeutralisesNewlines(t *testing.T) {
	evil := "notes/a.md\n2026-08-21 13:00:00 INFO the server was compromised"
	got := Value(evil)
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("a line break survived: %q", got)
	}
	// The text is kept, not deleted — losing it would hide the evidence that
	// something odd was passed in the first place.
	if !strings.Contains(got, "compromised") {
		t.Errorf("the value's text was dropped rather than neutralised: %q", got)
	}
}

func TestValueStripsControlCharacters(t *testing.T) {
	got := Value("a\x00b\x1bc\x7fd")
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Errorf("control character survived in %q", got)
		}
	}
	if !strings.Contains(got, "a") || !strings.Contains(got, "d") {
		t.Errorf("ordinary characters were dropped: %q", got)
	}
}

// An ordinary value must come back untouched, or every log line in the project
// gets quietly rewritten.
func TestValueLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{
		"notes/card-transactions.md",
		"3fcec3b9-489c-4965-98b9-c735a71cf4dc",
		"сметка за струја",
	} {
		if got := Value(s); got != s {
			t.Errorf("Value(%q) = %q, want it unchanged", s, got)
		}
	}
}

// A model-supplied path has no length limit of its own.
func TestValueTruncatesAndCutsOnARuneBoundary(t *testing.T) {
	got := Value(strings.Repeat("СМЕТКА", 200))
	if len(got) > maxLoggedValue+8 {
		t.Errorf("value is %d bytes, over the cap", len(got))
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("cut landed mid-rune: %q", got)
		}
	}
}
