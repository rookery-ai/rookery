package chat

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// The fixtures below are REAL assistant rows taken from a live install's
// chat_messages table, not invented examples. That matters for the two
// documentation cases: the shapes a substring-based stripper mangles are
// exactly the shapes a model actually produces when asked what the markers are.
func TestCleanReplyRemovesLeakedMarkers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare opener on its own line",
			in:   "[CHAT]\nHere are the last 10 emails in your primary inbox:",
			want: "Here are the last 10 emails in your primary inbox:",
		},
		{
			// The live data indents the marker by six spaces on more than half
			// its occurrences, so a `trimmed == "[CHAT]"` comparison is not enough.
			name: "opener indented by whitespace",
			in:   "      [CHAT]\n## 🎵 Your Last Liked Song on Spotify",
			want: "## 🎵 Your Last Liked Song on Spotify",
		},
		{
			name: "opener sharing its line with content",
			in:   "[CHAT] Fetching your last 10 emails.",
			want: "Fetching your last 10 emails.",
		},
		{
			name: "stray close tag",
			in:   "      [CHAT]\n7 + 10 = 17\n[/CHAT]",
			want: "7 + 10 = 17",
		},
		{
			// A leaked state block is machine memory. The JSON goes with it —
			// this is where CleanReply deliberately differs from
			// prompts.StripProtocolMarkers, which keeps what a marker wrapped.
			name: "state block is removed whole, json included",
			in:   "[CHAT] Searching your inbox.\n\n[STATE]{\"last_email_search\": {\"max\": 10}}[/STATE]",
			want: "Searching your inbox.",
		},
		{
			name: "orphan state opener",
			in:   "Done.\n[STATE]{\"a\": 1}",
			want: "Done.",
		},
		{
			name: "call marker line",
			in:   "Working on it.\n[CALL: weather-agent]",
			want: "Working on it.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanReply(tt.in); got != tt.want {
				t.Errorf("CleanReply(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// The property that matters most, and the one the first design got wrong. A
// marker inside a sentence or in backticks is the model explaining ITSELF; only
// a marker that OPENS a line is protocol. A substring replace cannot tell those
// apart: it emptied the code span in the [STATE] bullet below, leaving a bullet
// that described something no longer named.
func TestCleanReplyKeepsMarkersTheModelIsTalkingAbout(t *testing.T) {
	in := "I have four output markers:\n\n" +
		"- **`[CHAT]`** — sends a message to you\n" +
		"- **`[STATE]{\"key\": \"value\"}[/STATE]`** — saves data between runs\n" +
		"- **`[CALL: agent-name]`** — invokes another agent\n" +
		"- **`[SILENT]`** — tells the system the run was intentionally silent"
	if got := CleanReply(in); got != in {
		t.Errorf("a reply describing the markers was rewritten\n got %q\nwant %q", got, in)
	}
}

func TestCleanReplyLeavesOrdinaryProseByteIdentical(t *testing.T) {
	for _, in := range []string{
		"The pipeline runs on every merge to main.\n\nIt is gated on review.",
		"See [the docs](https://example.com) and the `[key]` field.",
		"Use array[0] to index it.",
	} {
		if got := CleanReply(in); got != in {
			t.Errorf("clean prose was rewritten\n got %q\nwant %q", got, in)
		}
	}
}

// Ten rows in the live database are nothing but [SILENT] — the model complying
// with "don't say anything" by reaching for the one marker the prompt gave it.
// Falling back to the raw text would re-display the exact leak this change
// removes, and an empty bubble is the worst outcome available (the lesson
// agentdesigner.UserFacingDesignText already records). So: a placeholder.
func TestCleanReplySubstitutesWhenNothingButMarkersRemain(t *testing.T) {
	for _, in := range []string{
		"[SILENT]",
		"      [SILENT]",
		"**[SILENT]**",
		"[silent]",
		"[CHAT]\n[/CHAT]",
	} {
		got := CleanReply(in)
		if got == "" {
			t.Errorf("CleanReply(%q) returned empty — an empty bubble reads as being ignored", in)
		}
		if got == in {
			t.Errorf("CleanReply(%q) returned the raw marker text — the leak survives", in)
		}
	}
}

// A silent-looking token must never swallow a reply that also has real content:
// suppression is for the marker, not for the message beside it.
func TestCleanReplyKeepsContentAlongsideASilentMarker(t *testing.T) {
	got := CleanReply("Here is the weather.\n[SILENT]")
	if got != "Here is the weather." {
		t.Errorf("got %q, want %q", got, "Here is the weather.")
	}
}

// A successful coder call that returned no text at all is a real outcome and
// has to be legible. This previously returned "" and handleChatMessage
// persisted it unguarded, so the owner got a blank bubble — four such rows
// exist on the reporting install, including the one produced by the question
// that prompted this fix. #242 covered only the marker-only case.
//
// The old test asserted the empty return, which recorded the bug rather than a
// decision: nothing downstream wanted "" — every reachable caller either
// displays the result or stores it for display.
func TestCleanReplyGivesGenuinelyEmptyOutputAPlaceholder(t *testing.T) {
	for _, in := range []string{"", "   \n  ", "\t\n\n"} {
		got := CleanReply(in)
		if strings.TrimSpace(got) == "" {
			t.Errorf("CleanReply(%q) = %q — an empty bubble reads as being ignored", in, got)
		}
		if got == markerOnlyPlaceholder {
			t.Errorf("CleanReply(%q) reused the marker-only placeholder; the causes differ "+
				"and the wording should say which happened", in)
		}
	}
}

// The marker-only case keeps its own distinct placeholder — the two causes stay
// separable, which is the distinction CleanReply's own comment draws.
func TestCleanReplyStillPlaceholdersMarkerOnlyReplies(t *testing.T) {
	if got := CleanReply("[SILENT]"); got != markerOnlyPlaceholder {
		t.Errorf("CleanReply(\"[SILENT]\") = %q, want %q", got, markerOnlyPlaceholder)
	}
}

// CleanHistory keeps the model from being re-taught by its own leaked turns:
// every prior wrapped reply is few-shot evidence to keep wrapping. User turns
// are never touched — the owner's words are not ours to rewrite, and a user who
// pastes a marker to ask about it must still be quoting it on the next turn.
func TestCleanHistoryCleansAssistantTurnsOnly(t *testing.T) {
	in := []db.ChatMessage{
		{Role: "user", Content: "[CHAT] is what I keep seeing"},
		{Role: "assistant", Content: "[CHAT]\nHere you go."},
	}
	got := CleanHistory(in)
	if got[0].Content != in[0].Content {
		t.Errorf("a user turn was rewritten: %q", got[0].Content)
	}
	if got[1].Content != "Here you go." {
		t.Errorf("assistant turn not cleaned: %q", got[1].Content)
	}
}

// CleanHistory must not mutate the caller's slice: handleChatMessage reuses the
// rows it loaded, and a surprise in-place edit is the kind of bug that only
// shows up somewhere else entirely.
func TestCleanHistoryDoesNotMutateItsInput(t *testing.T) {
	in := []db.ChatMessage{{Role: "assistant", Content: "[CHAT]\nHi."}}
	_ = CleanHistory(in)
	if in[0].Content != "[CHAT]\nHi." {
		t.Errorf("input was mutated: %q", in[0].Content)
	}
}
