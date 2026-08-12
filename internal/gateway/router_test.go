package gateway_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/gateway"
	"github.com/rookery-ai/rookery/internal/skilldesigner"
)

const testWorkspaceID = "ws-router-test"

// newTestRouter builds a Router backed by a real temp DB and both design flows.
// Neither flow needs a coder: agentdesigner.Flow.Start and the /skill create path
// both open a session in StateDescribing without calling one.
func newTestRouter(t *testing.T) (*gateway.Router, *db.DB, *agentdesigner.Flow, *skilldesigner.Flow) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := database.CreateWorkspace(&db.Workspace{ID: testWorkspaceID, Name: "router-tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// Both flows need a real saver/designer: DismissDraft removes the draft's
	// working dir under it.
	agentFlow := agentdesigner.NewFlow(nil, agentdesigner.NewDesigner(database, t.TempDir())).WithDB(database)
	// A real saver: DismissDraft cleans up the staging dir under SkillsDir().
	saver := skilldesigner.NewSaver(database, t.TempDir())
	skillFlow := skilldesigner.NewSkillFlow(nil, saver).WithDB(database)

	r := gateway.NewRouter(database, nil, nil, agentFlow, nil).WithSkillFlow(skillFlow)
	return r, database, agentFlow, skillFlow
}

func testMsg(text string) gateway.Message {
	return gateway.Message{
		WorkspaceID:    testWorkspaceID,
		Platform:       "telegram",
		PlatformUserID: "100000001",
		Text:           text,
	}
}

// collect returns a send func plus a pointer to the accumulated replies.
func collect() (func(string), *[]string) {
	var got []string
	return func(s string) { got = append(got, s) }, &got
}

func seedAgentDraft(t *testing.T, database *db.DB, name string) {
	t.Helper()
	err := database.UpsertAgentDraft(&db.AgentDraft{
		WorkspaceID: testWorkspaceID,
		AgentID:     "agent-draft-1",
		AgentName:   name,
		State:       "designing",
		HistoryJSON: "[]",
		UpdatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("seed agent draft: %v", err)
	}
}

func seedSkillDraft(t *testing.T, database *db.DB, name string) {
	t.Helper()
	err := database.UpsertSkillDraft(&db.SkillDraft{
		WorkspaceID: testWorkspaceID,
		SkillName:   name,
		State:       "designing",
		HistoryJSON: "[]",
		UpdatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("seed skill draft: %v", err)
	}
}

// TestSkillCancelDiscardSparesAgentDraft is the data-loss guard from the SP9 spec.
//
// Before flow-aware pending-cancel, resolveCancelChoice was hard-wired to the agent
// flow: replying "discard" to a *skill* cancel prompt destroyed the user's *agent*
// draft instead. Both drafts exist here, so a handler that dispatches to the wrong
// flow is caught rather than silently passing.
func TestSkillCancelDiscardSparesAgentDraft(t *testing.T) {
	r, database, _, _ := newTestRouter(t)

	seedAgentDraft(t, database, "daily-digest")
	seedSkillDraft(t, database, "pdf-summarizer")

	// /skill cancel → asks save or discard.
	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/skill cancel"), send, nil, nil, nil); err != nil {
		t.Fatalf("/skill cancel: %v", err)
	}
	if len(*replies) == 0 {
		t.Fatal("/skill cancel produced no reply")
	}

	// "discard" must dismiss the SKILL draft only.
	send2, _ := collect()
	if err := r.Handle(t.Context(), testMsg("discard"), send2, nil, nil, nil); err != nil {
		t.Fatalf("discard: %v", err)
	}

	if d, _ := database.GetSkillDraft(testWorkspaceID); d != nil {
		t.Errorf("skill draft should have been discarded, still present: %q", d.SkillName)
	}
	agentDraft, _ := database.GetAgentDraft(testWorkspaceID)
	if agentDraft == nil {
		t.Fatal("agent draft was destroyed by a skill cancel — flow-aware pending-cancel is not wired")
	}
	if agentDraft.AgentName != "daily-digest" {
		t.Errorf("agent draft = %q, want daily-digest", agentDraft.AgentName)
	}
}

// TestSkillCreateEntersDescribing covers a state that had never executed before
// SP9: StateDescribing was declared and dispatched by Step, but nothing assigned
// it. /skill create is its only entry point.
func TestSkillCreateEntersDescribing(t *testing.T) {
	r, _, _, skillFlow := newTestRouter(t)

	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/skill create pdf-summarizer"), send, nil, nil, nil); err != nil {
		t.Fatalf("/skill create: %v", err)
	}

	sess := skillFlow.GetSession(testWorkspaceID)
	if sess == nil {
		t.Fatal("no skill session was opened")
	}
	if sess.State != skilldesigner.StateDescribing {
		t.Errorf("state = %v, want StateDescribing", sess.State)
	}
	if sess.SkillName != "pdf-summarizer" {
		t.Errorf("skill name = %q, want pdf-summarizer", sess.SkillName)
	}
	// The user must be asked what the skill does — not dropped into generation.
	if len(*replies) == 0 || !strings.Contains(strings.ToLower((*replies)[0]), "describe") {
		t.Errorf("reply should ask for a description, got %q", *replies)
	}
}

// TestDesignSessionsAreMutuallyExclusive is the spec's central decision: at most
// one conversational design session per workspace, in both directions.
func TestDesignSessionsAreMutuallyExclusive(t *testing.T) {
	t.Run("skill blocked by live agent session", func(t *testing.T) {
		r, _, agentFlow, skillFlow := newTestRouter(t)

		send, _ := collect()
		if err := r.Handle(t.Context(), testMsg("/agent create daily-digest"), send, nil, nil, nil); err != nil {
			t.Fatalf("/agent create: %v", err)
		}
		if agentFlow.GetSession(testWorkspaceID) == nil {
			t.Fatal("agent session did not start")
		}

		send2, replies := collect()
		if err := r.Handle(t.Context(), testMsg("/skill create pdf-summarizer"), send2, nil, nil, nil); err != nil {
			t.Fatalf("/skill create: %v", err)
		}
		if skillFlow.GetSession(testWorkspaceID) != nil {
			t.Error("skill session started despite a live agent session")
		}
		if agentFlow.GetSession(testWorkspaceID) == nil {
			t.Error("the live agent session was clobbered")
		}
		joined := strings.Join(*replies, " ")
		if !strings.Contains(joined, "daily-digest") || !strings.Contains(joined, "/agent cancel") {
			t.Errorf("refusal should name the live agent session and how to end it, got %q", joined)
		}
	})

	t.Run("agent blocked by live skill session", func(t *testing.T) {
		r, _, agentFlow, skillFlow := newTestRouter(t)

		send, _ := collect()
		if err := r.Handle(t.Context(), testMsg("/skill create pdf-summarizer"), send, nil, nil, nil); err != nil {
			t.Fatalf("/skill create: %v", err)
		}
		if skillFlow.GetSession(testWorkspaceID) == nil {
			t.Fatal("skill session did not start")
		}

		send2, replies := collect()
		if err := r.Handle(t.Context(), testMsg("/agent create daily-digest"), send2, nil, nil, nil); err != nil {
			t.Fatalf("/agent create: %v", err)
		}
		if agentFlow.GetSession(testWorkspaceID) != nil {
			t.Error("agent session started despite a live skill session")
		}
		joined := strings.Join(*replies, " ")
		if !strings.Contains(joined, "pdf-summarizer") || !strings.Contains(joined, "/skill cancel") {
			t.Errorf("refusal should name the live skill session and how to end it, got %q", joined)
		}
	})
}

// TestSkillCommandWithoutFlow: an operator running without the skill flow wired
// gets a message, not a panic — the same contract designFlow has.
func TestSkillCommandWithoutFlow(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: testWorkspaceID, Name: "no-skill-flow"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	r := gateway.NewRouter(database, nil, nil, nil, nil) // no WithSkillFlow

	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/skill create x"), send, nil, nil, nil); err != nil {
		t.Fatalf("/skill create: %v", err)
	}
	if len(*replies) == 0 || !strings.Contains(strings.ToLower((*replies)[0]), "not yet available") {
		t.Errorf("want a not-available message, got %q", *replies)
	}
}

// TestSkillListIncludesCoreSkills: core skills are always available to every
// agent, so a list showing only the user's own would misrepresent reality.
func TestSkillListIncludesCoreSkills(t *testing.T) {
	r, _, _, _ := newTestRouter(t)

	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/skill list"), send, nil, nil, nil); err != nil {
		t.Fatalf("/skill list: %v", err)
	}
	joined := strings.Join(*replies, " ")
	if !strings.Contains(joined, "skill-creator") {
		t.Errorf("core skills missing from /skill list, got %q", joined)
	}
}

func seedReminder(t *testing.T, database *db.DB, id, message string, at time.Time) {
	t.Helper()
	err := database.CreateReminder(&db.Reminder{
		ID:          id,
		WorkspaceID: testWorkspaceID,
		Message:     message,
		RemindAt:    at,
	})
	if err != nil {
		t.Fatalf("seed reminder: %v", err)
	}
}

func TestRemindList(t *testing.T) {
	r, database, _, _ := newTestRouter(t)

	future := time.Now().Add(24 * time.Hour)
	seedReminder(t, database, "rem-1", "check the oven", future)
	seedReminder(t, database, "rem-2", "call the doctor", future.Add(time.Hour))

	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/remind list"), send, nil, nil, nil); err != nil {
		t.Fatalf("/remind list: %v", err)
	}
	joined := strings.Join(*replies, " ")
	for _, want := range []string{"check the oven", "call the doctor", "1.", "2."} {
		if !strings.Contains(joined, want) {
			t.Errorf("listing missing %q, got:\n%s", want, joined)
		}
	}
}

func TestRemindListEmpty(t *testing.T) {
	r, _, _, _ := newTestRouter(t)

	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/remind list"), send, nil, nil, nil); err != nil {
		t.Fatalf("/remind list: %v", err)
	}
	joined := strings.Join(*replies, " ")
	if !strings.Contains(strings.ToLower(joined), "no reminders") {
		t.Errorf("want an empty-state message, got %q", joined)
	}
}

// TestRemindDelete: the number must index the same ordering /remind list rendered.
func TestRemindDelete(t *testing.T) {
	r, database, _, _ := newTestRouter(t)

	future := time.Now().Add(24 * time.Hour)
	seedReminder(t, database, "rem-1", "check the oven", future)
	seedReminder(t, database, "rem-2", "call the doctor", future.Add(time.Hour))

	listed, err := database.ListReminders(testWorkspaceID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	first := listed[0]

	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/remind delete 1"), send, nil, nil, nil); err != nil {
		t.Fatalf("/remind delete: %v", err)
	}
	if !strings.Contains(strings.Join(*replies, " "), first.Message) {
		t.Errorf("reply should name the deleted reminder %q, got %q", first.Message, *replies)
	}

	remaining, _ := database.ListReminders(testWorkspaceID)
	if len(remaining) != 1 {
		t.Fatalf("want 1 reminder left, got %d", len(remaining))
	}
	if remaining[0].ID == first.ID {
		t.Errorf("deleted the wrong reminder: %q survived", first.Message)
	}
}

func TestRemindDeleteOutOfRange(t *testing.T) {
	r, database, _, _ := newTestRouter(t)
	seedReminder(t, database, "rem-1", "check the oven", time.Now().Add(24*time.Hour))

	send, replies := collect()
	if err := r.Handle(t.Context(), testMsg("/remind delete 9"), send, nil, nil, nil); err != nil {
		t.Fatalf("/remind delete 9: %v", err)
	}
	if !strings.Contains(strings.Join(*replies, " "), "1") {
		t.Errorf("want a message saying how many exist, got %q", *replies)
	}
	if got, _ := database.ListReminders(testWorkspaceID); len(got) != 1 {
		t.Errorf("out-of-range delete removed something: %d left", len(got))
	}
}

// TestRemindSubcommandsDoNotShadowCreation is the regression guard for this
// feature. "list" and "delete" are ordinary English words: /remind list the
// groceries is a legitimate reminder today and must stay one. Subcommand
// matching is exact, never prefix-based.
func TestRemindSubcommandsDoNotShadowCreation(t *testing.T) {
	for _, text := range []string{
		"/remind in 10 minutes to list the groceries",
		"/remind in 10 minutes to delete the old note",
	} {
		t.Run(text, func(t *testing.T) {
			r, database, _, _ := newTestRouter(t)

			send, _ := collect()
			if err := r.Handle(t.Context(), testMsg(text), send, nil, nil, nil); err != nil {
				t.Fatalf("handle: %v", err)
			}
			got, _ := database.ListReminders(testWorkspaceID)
			if len(got) != 1 {
				t.Fatalf("%q should have created a reminder, got %d", text, len(got))
			}
		})
	}
}

// TestAgentCancelDiscardSparesSkillDraft is the mirror: the agent path must not
// reach into the skill flow either.
func TestAgentCancelDiscardSparesSkillDraft(t *testing.T) {
	r, database, _, _ := newTestRouter(t)

	seedAgentDraft(t, database, "daily-digest")
	seedSkillDraft(t, database, "pdf-summarizer")

	send, _ := collect()
	if err := r.Handle(t.Context(), testMsg("/agent cancel"), send, nil, nil, nil); err != nil {
		t.Fatalf("/agent cancel: %v", err)
	}
	send2, _ := collect()
	if err := r.Handle(t.Context(), testMsg("discard"), send2, nil, nil, nil); err != nil {
		t.Fatalf("discard: %v", err)
	}

	if d, _ := database.GetAgentDraft(testWorkspaceID); d != nil {
		t.Errorf("agent draft should have been discarded, still present: %q", d.AgentName)
	}
	if d, _ := database.GetSkillDraft(testWorkspaceID); d == nil {
		t.Fatal("skill draft was destroyed by an agent cancel")
	}
}

// TestHelpTextFileLineUniformAcrossPlatforms pins the follow-up to Fix 5:
// Slack now imports file_share attachments via SlackGateway (like Telegram
// and Discord), so /help's "send a file" line is no longer suppressed for
// Slack — it applies uniformly to every platform.
func TestHelpTextFileLineUniformAcrossPlatforms(t *testing.T) {
	r, _, _, _ := newTestRouter(t)

	slackMsg := testMsg("/help")
	slackMsg.Platform = "slack"
	send, got := collect()
	if err := r.Handle(t.Context(), slackMsg, send, nil, nil, nil); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("expected exactly one reply, got %d", len(*got))
	}
	if !strings.Contains((*got)[0], "Send a file") {
		t.Errorf("Slack /help should mention file uploads now that Slack imports them, got:\n%s", (*got)[0])
	}

	// Telegram keeps the line too — attachments work there as before.
	send2, got2 := collect()
	if err := r.Handle(t.Context(), testMsg("/help"), send2, nil, nil, nil); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !strings.Contains((*got2)[0], "Send a file") {
		t.Errorf("Telegram /help should still mention file uploads, got:\n%s", (*got2)[0])
	}
}

func TestHandleStartNamesTheActualPlatform(t *testing.T) {
	for _, tc := range []struct{ platform, want string }{
		{"telegram", "Telegram"},
		{"discord", "Discord"},
		{"slack", "Slack"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			r, _, _, _ := newTestRouter(t)
			msg := testMsg("/start")
			msg.Platform = tc.platform
			msg.PlatformUserID = "user-" + tc.platform

			var got string
			if err := r.Handle(t.Context(), msg, func(s string) { got = s }, nil, nil, nil); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("reply %q does not name %q", got, tc.want)
			}
			for _, other := range []string{"Telegram", "Discord", "Slack"} {
				if other != tc.want && strings.Contains(got, other) {
					t.Fatalf("reply %q names the wrong platform %q", got, other)
				}
			}
		})
	}
}
