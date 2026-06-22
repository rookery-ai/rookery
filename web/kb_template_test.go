package web

import (
	"bytes"
	"html/template"
	"testing"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/vault"
)

// TestKBTemplatesRender executes the knowledge-base templates against populated
// data so a missing/renamed field or unbalanced {{if}} surfaces here rather than
// at runtime.
func TestKBTemplatesRender(t *testing.T) {
	tmpl, err := parseTemplates("templates")
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	user := &db.User{Username: "ilija", Role: "user"}

	cases := []struct {
		name string
		data any
	}{
		{"dashboard/kb_browse.html", &kbBrowseData{
			pageData: &pageData{Title: "Knowledge Base", User: user},
			Path:     "notes",
			Crumbs:   kbBreadcrumbs("notes"),
			Nodes: []vault.Node{
				{Name: "journal", Path: "notes/journal", IsDir: true},
				{Name: "todo.md", Path: "notes/todo.md"},
			},
		}},
		{"dashboard/kb_browse.html", &kbBrowseData{
			pageData: &pageData{Title: "Search", User: user},
			Query:    "needle",
			Crumbs:   kbBreadcrumbs(""),
			Results:  []vault.SearchHit{{Path: "notes/x.md", Line: 3, Snippet: "the needle"}},
		}},
		{"dashboard/kb_view.html", &kbViewData{
			pageData:   &pageData{Title: "Knowledge Base", User: user},
			Path:       "notes/x.md",
			NoteTitle:  "x.md",
			HTML:       template.HTML("<h1>Hi</h1>"),
			IsMarkdown: true,
			ParentPath: "notes",
			Backlinks:  []kbCrumb{{Name: "notes/a.md", Path: "notes/a.md"}},
		}},
		{"dashboard/kb_edit.html", &kbEditData{
			pageData:   &pageData{Title: "Edit", User: user},
			Path:       "notes/x.md",
			Content:    "# hi",
			ParentPath: "notes",
		}},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, tc.name, tc.data); err != nil {
			t.Fatalf("%s: execute: %v", tc.name, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("%s: rendered empty", tc.name)
		}
	}
}
