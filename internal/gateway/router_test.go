package gateway_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/skilldesigner"
)

const testWorkspaceID = "ws-router-test"

// newTestRouter builds a Router backed by a real temp DB and both design flows.
// Neither flow needs a coder: agentdesigner.Flow.Start and the /skill create path
// both open a session in StateDescribing without calling one.
func newTestRouter(t *testing.T) (*gateway.Router, *db.DB, *agentdesigner.Flow, *skilldesigner.Flow) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
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
		PlatformUserID: "1843540314",
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
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
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
