package web

import (
	"bytes"
	"testing"

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
// populated settingsPageData (all profile fields) and a real *db.User so the
// navbar's {{if .User}} branch is exercised too.
func TestSettingsTemplateRenders(t *testing.T) {
	tmpl, err := parseTemplates("templates")
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	cases := []string{"Balanced", "Sarcastic but helpful", "", "Short & concise"}
	for _, tone := range cases {
		data := &settingsPageData{
			pageData:    &pageData{Title: "Settings", User: &db.User{Username: "ilija", Role: "user"}},
			DisplayName: "Ilija",
			Email:       "ilija@example.com",
			Location:    "Skopje, North Macedonia",
			Timezone:    "Europe/Skopje",
			Tone:        tone,
			Language:    "English",
			Notes:       "Prefers concise replies",
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
