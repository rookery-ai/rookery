package web

import (
	"strings"
	"testing"
	"time"
)

// A client that attaches at the moment a turn ends must still be told WHY it
// failed.
//
// The SSE handler snapshots (lines, done) together and, when done is already
// true, replays the backlog and stops. So if `done` becomes observable before
// the "⚠️ …" milestone is appended, a client landing in that window replays a
// backlog without the reason and the stream closes immediately: the activity
// card flashes and nothing says what happened.
//
// That window was microseconds inside a minute-long turn until an unreachable
// coder was classified as terminal, which makes a failing turn end in about a
// second — putting the client's attach right on top of it. Reported from a real
// session as "the tool box appears and disappears and afterwards no message
// what happened".
//
// The property under test is an INVARIANT, not a timing: whenever `done` reads
// true, every line the turn will ever produce is already in the snapshot.
func TestAFailedTurnIsNeverDoneBeforeItsReasonIsRecorded(t *testing.T) {
	// A few repeats, not a soak. The property is an ordering INVARIANT rather
	// than a rare interleaving: against the old code this fails on the FIRST
	// iteration (verified), so the repeats only guard against a future change
	// making it probabilistic.
	//
	// Kept deliberately small because each iteration builds a server and a
	// SQLite database, and this package runs under -race in CI where it is
	// already the slowest thing in the suite (~343s, 13x its non-race time). An
	// earlier 20-iteration version cost 38s of that on its own.
	for i := 0; i < 5; i++ {
		s, workspaceID, chatID := chatTurnFixture(t)
		s.testCoderErr = "provider exploded"

		if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
			t.Fatal("startChatTurn refused")
		}

		// Poll exactly as the SSE handler does — one atomic snapshot — and
		// check the invariant the FIRST time done reads true.
		deadline := time.Now().Add(5 * time.Second)
		for {
			st := s.chatTurn(chatID)
			if st == nil {
				t.Fatal("turn vanished")
			}
			lines, done, _ := st.snapshot()
			if done {
				if !containsSubstring(lines, "⚠️") {
					t.Fatalf("turn reported done with no failure milestone; lines = %q", lines)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("turn never finished")
			}
		}
	}
}

// The same ordering on the SUCCESS path: a client that sees done refetches the
// chat, so marking done before the reply is persisted would hand it a
// conversation missing the very message it waited for.
func TestASuccessfulTurnIsNeverDoneBeforeItsReplyIsPersisted(t *testing.T) {
	for i := 0; i < 3; i++ {
		s, workspaceID, chatID := chatTurnFixture(t)
		// No testCoderErr and no coder configured: runChatCoder returns an
		// error, so drive the success path through the reply seam instead.
		s.testCoderReply = "here is your answer"

		if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
			t.Fatal("startChatTurn refused")
		}

		deadline := time.Now().Add(5 * time.Second)
		for {
			st := s.chatTurn(chatID)
			if st == nil {
				t.Fatal("turn vanished")
			}
			_, done, err := st.snapshot()
			if done {
				if err != nil {
					t.Fatalf("unexpected turn error: %v", err)
				}
				msgs, _ := s.db.ListChatMessages(chatID)
				var haveAssistant bool
				for _, m := range msgs {
					if m.Role == "assistant" && strings.Contains(m.Content, "here is your answer") {
						haveAssistant = true
					}
				}
				if !haveAssistant {
					t.Fatalf("turn reported done before the reply was persisted; messages = %d", len(msgs))
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("turn never finished")
			}
		}
	}
}
