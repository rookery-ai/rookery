package web

import (
	"bytes"
	"testing"
	"time"

	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
)

// TestSetupTemplateRenders parses the real templates dir and renders
// auth/setup.html for every wizard step (1-5), the way they actually
// execute via html/template reflection — a clean `go build` cannot catch a
// missing/renamed field reference or an unbalanced {{if}}/{{end}} that only
// surfaces at Execute time.
func TestSetupTemplateRenders(t *testing.T) {
	tmpl, err := parseTemplates("templates")
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	for step := 1; step <= 5; step++ {
		data := &setupData{
			pageData:    &pageData{Title: "Setup Your Account"},
			Step:        step,
			BotUsername: "test_bot",
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "auth/setup.html", data); err != nil {
			t.Fatalf("step %d: execute: %v", step, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("step %d: rendered empty output", step)
		}
	}
}

// TestSettingsTemplateRenders renders dashboard/settings.html with a fully
// populated settingsPageData (all profile fields) and a real *db.Workspace so the
// navbar's {{if .User}} branch is exercised too.
func TestSettingsTemplateRenders(t *testing.T) {
	tmpl, err := parseTemplates("templates")
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	cases := []string{"Balanced", "Sarcastic but helpful", "", "Short & concise"}
	for _, tone := range cases {
		data := &settingsPageData{
			pageData:    &pageData{Title: "Settings", Workspace: &db.Workspace{Name: "ilija", CoderBin: "claude"}},
			DisplayName: "Ilija",
			Email:       "ilija@example.com",
			Location:    "Skopje, North Macedonia",
			Timezone:    "Europe/Skopje",
			Tone:        tone,
			Language:    "English",
			Notes:       "Prefers concise replies",
			// Non-empty so the coder-picker <select> branch is exercised (guards the
			// coder_bin selected-option logic from an execute-time type error).
			DetectedCoders: []coder.Installed{{Name: "Claude Code", Bin: "claude", BackendType: "claude"}},
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "dashboard/settings.html", data); err != nil {
			t.Fatalf("tone=%q: execute: %v", tone, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("tone=%q: rendered empty output", tone)
		}
	}
}

// TestAgentTemplatesRenderWithRunning exercises the agents list and detail templates
// in both the idle and "running" states, so an execute-time reference to the new
// .Running field / index $.Running .ID can't silently break.
func TestAgentTemplatesRenderWithRunning(t *testing.T) {
	tmpl, err := parseTemplates("templates")
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	user := &db.Workspace{Name: "ilija"}
	agent := &db.Agent{ID: "a1", Name: "Payroll Finder", Description: "finds payroll emails", Active: true, CreatedAt: time.Now()}

	// (Running, LiveRun): idle, scheduled-only (badge but no SSE), live manual run.
	cases := []struct{ running, live bool }{{false, false}, {true, false}, {true, true}}
	for _, tc := range cases {
		// List page.
		listData := &agentsPageData{
			pageData: &pageData{Title: "My Agents", Workspace: user},
			Agents:   []*db.Agent{agent},
			Running:  map[string]bool{"a1": tc.running},
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "dashboard/agents.html", listData); err != nil {
			t.Fatalf("running=%v: agents.html execute: %v", tc.running, err)
		}

		// Detail page.
		detailData := &agentDetailData{
			pageData: &pageData{Title: "Agent", Workspace: user},
			Agent:    agent,
			Running:  tc.running,
			LiveRun:  tc.live,
		}
		buf.Reset()
		if err := tmpl.ExecuteTemplate(&buf, "dashboard/agent_detail.html", detailData); err != nil {
			t.Fatalf("running=%v live=%v: agent_detail.html execute: %v", tc.running, tc.live, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("running=%v live=%v: agent_detail.html rendered empty", tc.running, tc.live)
		}
	}
}
