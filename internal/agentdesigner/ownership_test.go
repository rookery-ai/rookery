package agentdesigner

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// Every surface that can create a session must stamp its origin. A session with
// no origin is owned by nobody, which is exactly how a web-started build ended
// up announcing itself in Telegram.

func TestStartStampsChatOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))

	if _, err := flow.Start(workspaceID, "price-tracker", OriginChat); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess := flow.GetSession(workspaceID)
	if sess == nil {
		t.Fatal("no session created")
	}
	if sess.Origin != OriginChat {
		t.Errorf("Origin = %q, want %q", sess.Origin, OriginChat)
	}
}

// EVERY creation entry point must stamp the origin. This is the test that
// licenses the strict `origin != OriginChat` delivery check in main.go: that
// comparison fails CLOSED, so a path that forgot `Origin: origin` in its
// session literal would silently withhold a chat-owned build's result from
// chat — the inverse of the reported bug, and invisible to a test that only
// checks the paths it remembered. Setting the field is six hand-written lines
// in six literals; nothing but an exhaustive check catches a missed one.
func TestEveryCreationPathStampsOrigin(t *testing.T) {
	// AGENT.md on disk + the DB row, so the two edit paths can load an agent.
	seedAgent := func(t *testing.T, flow *Flow, database *db.DB, workspaceID string) string {
		t.Helper()
		agentID := uuid.New().String()
		if err := database.CreateAgent(&db.Agent{
			ID: agentID, WorkspaceID: workspaceID, Name: "seeded", Description: "d", Active: true,
		}); err != nil {
			t.Fatal(err)
		}
		dir := AgentDir(flow.designer.agentsDir, workspaceID, agentID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		md := "# Suggested schedule: none\n# Skills: none\nDo the thing.\n"
		if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
		return agentID
	}

	cases := []struct {
		name   string
		origin Origin
		start  func(t *testing.T, flow *Flow, database *db.DB, workspaceID string) error
	}{
		{"Start", OriginChat, func(_ *testing.T, f *Flow, _ *db.DB, ws string) error {
			_, err := f.Start(ws, "price-tracker", OriginChat)
			return err
		}},
		{"StartDesign", OriginWeb, func(_ *testing.T, f *Flow, _ *db.DB, ws string) error {
			_, err := f.StartDesign(context.Background(), ws, "price-tracker", "watch prices", OriginWeb)
			return err
		}},
		{"StartEdit", OriginChat, func(t *testing.T, f *Flow, d *db.DB, ws string) error {
			_, err := f.StartEdit(ws, seedAgent(t, f, d, ws), OriginChat)
			return err
		}},
		{"StartEditDesign", OriginWeb, func(t *testing.T, f *Flow, d *db.DB, ws string) error {
			_, err := f.StartEditDesign(context.Background(), ws, seedAgent(t, f, d, ws), "change it", OriginWeb)
			return err
		}},
		{"ResumeDraft", OriginWeb, func(t *testing.T, f *Flow, d *db.DB, ws string) error {
			if err := d.UpsertAgentDraft(&db.AgentDraft{
				WorkspaceID: ws, AgentID: uuid.New().String(), AgentName: "drafted",
				State: "designing", ExpiresAt: time.Now().Add(24 * time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			_, err := f.ResumeDraft(context.Background(), ws, OriginWeb)
			return err
		}},
		{"OfferDraftResume", OriginChat, func(_ *testing.T, f *Flow, _ *db.DB, ws string) error {
			_, err := f.OfferDraftResume(ws, "price-tracker",
				&db.AgentDraft{AgentID: "a1", AgentName: "drafted"}, OriginChat)
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
			if err := c.start(t, flow, flow.db.(*db.DB), workspaceID); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			sess := flow.GetSession(workspaceID)
			if sess == nil {
				t.Fatalf("%s created no session", c.name)
			}
			if sess.Origin != c.origin {
				t.Errorf("%s stamped Origin = %q, want %q", c.name, sess.Origin, c.origin)
			}
		})
	}
}

func TestOfferDraftResumeStampsOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))

	draft := &db.AgentDraft{AgentID: "a1", AgentName: "price-tracker"}
	if _, err := flow.OfferDraftResume(workspaceID, "price-tracker", draft, OriginChat); err != nil {
		t.Fatalf("OfferDraftResume: %v", err)
	}
	if got := flow.GetSession(workspaceID).Origin; got != OriginChat {
		t.Errorf("Origin = %q, want %q", got, OriginChat)
	}
}

// A chat message aimed at a web-owned session must be refused WITHOUT touching
// the session: the whole point of exclusive ownership is that two surfaces
// cannot drive one FSM.
func TestStepRefusesNonOwnerAndLeavesSessionAlone(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
		Origin:    OriginWeb,
		History:   []db.ChatMessage{{Role: "assistant", Content: "hello"}},
	}
	flow.mu.Unlock()

	resp, isDone, agentID, err := flow.Step(context.Background(), workspaceID, "approve", OriginChat)
	if err != nil {
		t.Fatalf("a refusal is a normal answer, not an error: %v", err)
	}
	if isDone || agentID != "" {
		t.Errorf("refused turn must not finish anything: (%v, %q)", isDone, agentID)
	}
	if !strings.Contains(resp, "the web app") {
		t.Errorf("refusal = %q, want it to name the owning surface", resp)
	}
	if !strings.Contains(resp, "/agent cancel") {
		t.Errorf("refusal = %q, want it to name the escape hatch", resp)
	}
	sess := flow.GetSession(workspaceID)
	if sess.State != StateDesigning {
		t.Errorf("state = %v, want it untouched", sess.State)
	}
	if len(sess.History) != 1 {
		t.Errorf("history len = %d, want the refused turn NOT recorded", len(sess.History))
	}
}

// A session created by a test, or by a build predating the Origin field, must
// stay drivable — Owns fails open and this pins that end to end.
func TestStepAllowsZeroOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID) // no Origin set

	resp, _, _, err := flow.Step(context.Background(), workspaceID, "tell me more", OriginChat)
	if err != nil {
		t.Fatalf("zero-origin session must stay drivable: %v", err)
	}
	if strings.Contains(resp, "please continue there") {
		t.Errorf("zero-origin session was refused: %q", resp)
	}
}

// Starting a second session must say WHERE the first one lives — "you already
// have an active design session" told the user neither where to go nor how out.
func TestStartNamesTheOwningSurface(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	if _, err := flow.Start(workspaceID, "first", OriginWeb); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err := flow.Start(workspaceID, "second", OriginChat)
	if err == nil {
		t.Fatal("want a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "the web app") {
		t.Errorf("err = %q, want it to name the web app", err)
	}
	if !strings.Contains(err.Error(), "/agent cancel") {
		t.Errorf("err = %q, want it to name the escape hatch", err)
	}
}

// The bug this whole change exists for: a build started in the web must not be
// announced in Telegram. The completion hook is registered once at wiring time
// and cannot see which surface the user is on, so the origin has to travel WITH
// the result.
func TestBuildCompleteCarriesTheSessionOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
		Origin:    OriginWeb,
	}
	flow.mu.Unlock()

	got := make(chan Origin, 1)
	flow.OnBuildComplete(func(_ string, origin Origin, _ string, _ bool, _ string, _ error) {
		got <- origin
	})

	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}

	select {
	case origin := <-got:
		if origin != OriginWeb {
			t.Errorf("origin = %q, want %q", origin, OriginWeb)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("build never completed")
	}
}

// A build must be traceable end to end from the logs alone. The incident that
// motivated this change produced ZERO designer log lines across a whole build,
// which is why the diagnosis had to come from the database instead.
func TestBuildEmitsCorrelatedLifecycleLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker", State: StateDesigning, Origin: OriginWeb,
	}
	flow.mu.Unlock()

	done := make(chan struct{})
	flow.OnBuildComplete(func(string, Origin, string, bool, string, error) { close(done) })
	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("build never completed")
	}

	out := buf.String()
	for _, want := range []string{
		"build start", "build coder returned", "build decision",
		"build outcome", "build finished", "build_id=", "origin=web",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("logs missing %q:\n%s", want, out)
		}
	}

	// One build, one id: the whole point is that a single grep reconstructs it.
	ids := map[string]bool{}
	for _, field := range strings.Fields(out) {
		if strings.HasPrefix(field, "build_id=") {
			ids[field] = true
		}
	}
	if len(ids) != 1 {
		t.Errorf("build_id values = %v, want exactly one across the lifecycle", ids)
	}
}

// Snapshot carries the origin because /design/state is how the SPA learns it is
// a read-only mirror rather than the driver.
func TestSnapshotCarriesOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	if _, err := flow.Start(workspaceID, "price-tracker", OriginChat); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := flow.Snapshot(workspaceID).Origin; got != OriginChat {
		t.Errorf("Snapshot().Origin = %q, want %q", got, OriginChat)
	}
}
