package web

import (
	"net/http"
	"testing"
)

// A review claimed the running-agent guard was "PUT-only, so a delete or rename
// of state.md mid-run is unguarded". That premise is WRONG, and this test is the
// evidence — written because the claim was plausible enough to nearly earn a fix
// that would have added a redundant 409 in front of an existing, stronger refusal.
//
// `agents` is in vault.protectedTopDirs, so BOTH handlers call
// protectedPathMessage first and refuse the whole subtree with 403
// protected_path — not merely while a run is in flight, but always. The 409 is
// narrower on purpose: a save is the one mutation the KB browser legitimately
// offers on state.md (the file is meant to be hand-editable), so it needs a
// run-scoped refusal rather than a blanket one.
//
// The value of pinning it is that the protection is INDIRECT. Nothing at the
// delete/rename call sites mentions agents or state.md, so removing "agents"
// from protectedTopDirs — a one-line change, in another package, for an
// unrelated reason — would silently make a running agent's memory deletable
// with a 200. These tests fail loudly if that happens.
func TestDeletingARunningAgentsStateIsRefused(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	s.runs[a.ID] = &agentRunState{} // in flight

	rec := doJSON(t, s, http.MethodDelete,
		"/api/v1/kb/note?path=agents/"+a.ID+"/state.md", nil, cookies)
	if rec.Code == http.StatusOK {
		t.Fatalf("a running agent's state.md must never be deletable through the KB API; got 200 %s",
			rec.Body.String())
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 protected_path (the agents subtree is system-managed), got %d %s",
			rec.Code, rec.Body.String())
	}
}

func TestRenamingARunningAgentsStateIsRefused(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	s.runs[a.ID] = &agentRunState{}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/rename", map[string]string{
		"from": "agents/" + a.ID + "/state.md",
		"to":   "notes/stolen-state.md",
	}, cookies)
	if rec.Code == http.StatusOK {
		t.Fatalf("a running agent's state.md must never be renamable out of the agents "+
			"subtree; got 200 %s", rec.Body.String())
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 protected_path, got %d %s", rec.Code, rec.Body.String())
	}
}

// The same refusal must hold when NO run is in flight: this protection is a
// property of the agents subtree, not of run state. Asserting it separately is
// what distinguishes "protected because system-managed" from "protected because
// busy" — if someone later replaces the protected-path rule with a run-scoped
// check, this is the test that catches the downgrade.
func TestDeletingAnIdleAgentsStateIsAlsoRefused(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID) // deliberately NOT registered as running

	rec := doJSON(t, s, http.MethodDelete,
		"/api/v1/kb/note?path=agents/"+a.ID+"/state.md", nil, cookies)
	if rec.Code != http.StatusForbidden {
		t.Errorf("an idle agent's state.md is still system-managed and must be refused; got %d %s",
			rec.Code, rec.Body.String())
	}
}
