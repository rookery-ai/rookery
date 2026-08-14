package web

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// The predicate behind the setup wizard's single closing action.
//
// The case that matters is the first one: db.coderKindOrDefault fills
// coder_kind on every write, so a workspace that SKIPPED the coder step still
// has a non-empty value there. A Done screen that read the column directly
// would invite that owner into a chat with no engine behind it.
func TestWorkspaceCoderReady(t *testing.T) {
	cases := []struct {
		name string
		w    *db.Workspace
		want bool
	}{
		{
			"a skipped coder step leaves the default kind but nothing configured",
			&db.Workspace{CoderKind: "local", CoderBin: ""},
			false,
		},
		{"local with a binary", &db.Workspace{CoderKind: "local", CoderBin: "/usr/bin/claude"}, true},
		{"api with provider and model", &db.Workspace{CoderKind: "api", CoderProvider: "openrouter", CoderModel: "glm-5.2"}, true},
		{"api with no model is not runnable", &db.Workspace{CoderKind: "api", CoderProvider: "openrouter"}, false},
		{"api with no provider is not runnable", &db.Workspace{CoderKind: "api", CoderModel: "glm-5.2"}, false},
		{"whitespace is not configuration", &db.Workspace{CoderKind: "local", CoderBin: "   "}, false},
		{"an unknown kind is not runnable", &db.Workspace{CoderKind: "future"}, false},
		{"no workspace at all", nil, false},
	}
	for _, c := range cases {
		if got := workspaceCoderReady(c.w); got != c.want {
			t.Errorf("%s: workspaceCoderReady = %v, want %v", c.name, got, c.want)
		}
	}
}
