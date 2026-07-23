package web

import (
	"net/http"
	"testing"
)

// TestKBDisplayTitleResolvesReflectedNotes pins the fix for UUID-named results in
// global search. Reflected notes are named after a row id, so the filename tells
// the user nothing: chats/<uuid>.md, inbox/<uuid>.md, and — the case the old
// parent-dir-keyed enricher could not reach at all — agents/<uuid>/logs/run_<ts>.md,
// whose immediate parent is "logs", two levels below the dir that identifies it.
func TestKBDisplayTitleResolvesReflectedNotes(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	_, wsID := createAndEnterWorkspace(t, s, cookies)
	agent := seedAgent(t, s, wsID) // name "Digest"

	write := func(rel, body string) {
		t.Helper()
		if err := s.vault.WriteNote(wsID, rel, []byte(body)); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("chats/33139123-6939-4d12-9bf8-409b2e042d24.md",
		"---\ntype: chat\n---\n\n# Chat 2026-06-25 12:22\n\nhey\n")
	write("inbox/0a107b38-db68-4666-a400-9e2bd663eb99.md",
		"---\ntype: inbox\n---\n\n# 🤖 linkedin-post-scraper (manual)\n\nfailed\n")
	write("notes/ohrid-trip.md", "lake apartments")
	runLog := "agents/" + agent.ID + "/logs/run_20260624_101329.md"
	write(runLog, "# Run of [[Digest]] — ok\n")
	agentFile := "agents/" + agent.ID + "/AGENT.md"
	write(agentFile, "# Digest agent\n")

	cases := []struct{ path, want string }{
		{"chats/33139123-6939-4d12-9bf8-409b2e042d24.md", "Chat 2026-06-25 12:22"},
		{"inbox/0a107b38-db68-4666-a400-9e2bd663eb99.md", "🤖 linkedin-post-scraper (manual)"},
		{runLog, "Digest — run 20260624_101329"},
		{agentFile, "Digest — AGENT"},
		// A user-authored note's filename IS its title — resolution must not
		// "improve" it into the first heading, which would rename the file out
		// from under the user in the tree.
		{"notes/ohrid-trip.md", "ohrid-trip"},
	}
	for _, c := range cases {
		if got := s.kbDisplayTitle(wsID, c.path); got != c.want {
			t.Errorf("kbDisplayTitle(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestKBDisplayTitleFallsBackToStem: a reflected note with no heading (truncated,
// hand-edited, or written by an older build) must still produce something
// usable rather than an empty title the UI would render as a blank row.
func TestKBDisplayTitleFallsBackToStem(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	_, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := s.vault.WriteNote(wsID, "chats/abc-123.md", []byte("no heading here")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := s.kbDisplayTitle(wsID, "chats/abc-123.md"); got != "abc-123" {
		t.Errorf("fallback title = %q, want %q", got, "abc-123")
	}
	// An unknown agent id (agent row deleted, dir not yet swept) still names the
	// file rather than erroring out.
	if got := s.kbDisplayTitle(wsID, "agents/gone/logs/run_1.md"); got != "gone — run 1" {
		t.Errorf("unknown agent title = %q", got)
	}
	if got := s.kbDisplayTitle(wsID, ""); got != "" {
		t.Errorf("empty path should give empty title, got %q", got)
	}
}

// TestAPISearchShowsResolvedTitleAndPath is the end-to-end assertion for the
// reported symptom: searching used to surface `chats/<uuid>.md` as the visible
// result title. The path must still be present — the user asked to see both.
func TestAPISearchShowsResolvedTitleAndPath(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	rel := "chats/33139123-6939-4d12-9bf8-409b2e042d24.md"
	if err := s.vault.WriteNote(wsID, rel, []byte("# Trip planning\n\napartments in Ohrid\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec := doJSON(t, s, http.MethodGet, "/api/v1/search?q=Ohrid", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, `"title":"Trip planning"`) {
		t.Errorf("expected resolved title, got: %s", body)
	}
	if !contains(body, `"path":"`+rel+`"`) {
		t.Errorf("expected path alongside title, got: %s", body)
	}
	if contains(body, `"title":"`+rel+`"`) {
		t.Errorf("raw UUID path still used as title: %s", body)
	}
}
