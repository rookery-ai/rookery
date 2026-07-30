package skilldesigner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/skillstore"
	"github.com/stretchr/testify/require"
)

// saverTestDB opens a fresh migrated SQLite DB in a temp dir and registers a
// single workspace (FK target for skills), mirroring
// internal/agentdesigner/edit_test.go's testDB helper.
func saverTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"), "../../migrations")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{
		ID:   workspaceID,
		Name: "tester",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return database, workspaceID
}

// TestSaveSkill_FullTreeSurvives is the Finding-2 regression guard: Task 5's
// entire premise was "a helper file gets silently lost between staging and the
// saved skill" — this proves the fix actually lands at the layer where it
// matters in production, SaveSkill itself. A nested scripts/lib/parse.py, a
// scripts/run.sh landing executable, and a references/api.md must all survive
// the round trip.
func TestSaveSkill_FullTreeSurvives(t *testing.T) {
	database, workspaceID := saverTestDB(t)
	vaultsBase := t.TempDir()
	saver := NewSaver(database, vaultsBase)

	skillMD := "---\nname: tree-skill\ndescription: Exercises the full generated tree.\n---\n# Tree Skill\nBody.\n"
	scripts := map[string]string{
		"scripts/lib/parse.py": "X = 1\n",
		"scripts/run.sh":       "#!/bin/bash\necho hi\n",
		"references/api.md":    "# API\nSome reference docs.\n",
	}

	skill, err := saver.SaveSkill(workspaceID, "tree-skill", "Exercises the full generated tree.", skillMD, scripts)
	require.NoError(t, err)
	require.NotNil(t, skill)

	skillDir := skillstore.SkillDir(vaultsBase, workspaceID, "tree-skill")

	gotSKILLMD, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, skillMD, string(gotSKILLMD))

	gotParse, err := os.ReadFile(filepath.Join(skillDir, "scripts", "lib", "parse.py"))
	require.NoError(t, err)
	require.Equal(t, "X = 1\n", string(gotParse))

	runShPath := filepath.Join(skillDir, "scripts", "run.sh")
	gotRunSh, err := os.ReadFile(runShPath)
	require.NoError(t, err)
	require.Equal(t, "#!/bin/bash\necho hi\n", string(gotRunSh))

	if runtime.GOOS != "windows" {
		info, err := os.Stat(runShPath)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o750), info.Mode().Perm(), "scripts/run.sh must land executable")
	}

	gotAPI, err := os.ReadFile(filepath.Join(skillDir, "references", "api.md"))
	require.NoError(t, err)
	require.Equal(t, "# API\nSome reference docs.\n", string(gotAPI))
}

// TestSaveSkill_RejectsPathEscape covers the two escape shapes SaveSkill's
// safety check must still reject after Task 5 widened the path domain from
// "top-level scripts/*.py" to the whole generated tree: a "../" traversal entry
// and an absolute path.
func TestSaveSkill_RejectsPathEscape(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"dotdot", "../escape.py"},
		{"absolute", "/etc/passwd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database, workspaceID := saverTestDB(t)
			vaultsBase := t.TempDir()
			saver := NewSaver(database, vaultsBase)

			skillMD := "---\nname: escape-skill\ndescription: Attempts a path escape.\n---\nBody.\n"
			scripts := map[string]string{
				c.path: "malicious\n",
			}

			_, err := saver.SaveSkill(workspaceID, "escape-skill", "Attempts a path escape.", skillMD, scripts)
			require.Error(t, err)
			require.Contains(t, err.Error(), "unsafe skill file path")
		})
	}
}

// TestSaveSkill_ReferenceDocProseNotBlocked is Finding 1's RED test at the
// save-time call site (internal/skilldesigner/designer.go): a references/*.md
// document describing a destructive DB operation in prose ("drop the temp
// table") must save successfully — a doc is not executable code. Before the
// fix, SaveSkill looped every ReadSkillTree entry (now including
// references/*.md) through agentdesigner.RunToolGuardrails, which applies the
// FULL code-context ethics keyword set (destructive commands included) to
// every file regardless of extension, and this save fails with "guardrails
// (references/guide.md): ethics filter: ...".
func TestSaveSkill_ReferenceDocProseNotBlocked(t *testing.T) {
	database, workspaceID := saverTestDB(t)
	vaultsBase := t.TempDir()
	saver := NewSaver(database, vaultsBase)

	skillMD := "---\nname: db-maintenance\ndescription: Helps run routine DB maintenance.\n---\n# DB Maintenance\nBody.\n"
	scripts := map[string]string{
		"references/guide.md": "# Maintenance Guide\n\nAt the end of each run, drop table staging_tmp to free space.\n",
	}

	skill, err := saver.SaveSkill(workspaceID, "db-maintenance", "Helps run routine DB maintenance.", skillMD, scripts)
	require.NoError(t, err, "a references/*.md describing a destructive DB op in prose must not be blocked")
	require.NotNil(t, skill)

	skillDir := skillstore.SkillDir(vaultsBase, workspaceID, "db-maintenance")
	got, err := os.ReadFile(filepath.Join(skillDir, "references", "guide.md"))
	require.NoError(t, err)
	require.Contains(t, string(got), "drop table")
}

// TestSaveSkill_ShellScriptWithDestructiveTextStillRejected is Finding 1's
// negative-control pair: the SAME text ("drop table") in a scripts/*.sh file
// (executable code, not prose) must still be rejected. This pins that the fix
// is extension-scoped to .md, not a blanket loosening of the destructive-
// command keyword check.
func TestSaveSkill_ShellScriptWithDestructiveTextStillRejected(t *testing.T) {
	database, workspaceID := saverTestDB(t)
	vaultsBase := t.TempDir()
	saver := NewSaver(database, vaultsBase)

	skillMD := "---\nname: bad-script-skill\ndescription: Ships a destructive helper.\n---\nBody.\n"
	scripts := map[string]string{
		"scripts/cleanup.sh": "#!/bin/bash\ndrop table staging_tmp;\n",
	}

	_, err := saver.SaveSkill(workspaceID, "bad-script-skill", "Ships a destructive helper.", skillMD, scripts)
	require.Error(t, err, "a shell script that actually contains a destructive command must still be rejected")
	require.Contains(t, err.Error(), "ethics filter")
}

// TestSaveSkill_AlwaysForbiddenKeywordStillBlockedInMarkdown is Finding 1's
// other negative control: the always-forbidden intent keywords ("steal",
// "exfil", "bitcoin wallet") have no benign use in prose either, so they must
// still be rejected even inside a .md file — the fix narrows which keyword SET
// applies to markdown, it does not exempt markdown from ethics checking
// entirely.
func TestSaveSkill_AlwaysForbiddenKeywordStillBlockedInMarkdown(t *testing.T) {
	database, workspaceID := saverTestDB(t)
	vaultsBase := t.TempDir()
	saver := NewSaver(database, vaultsBase)

	skillMD := "---\nname: notes-helper\ndescription: Ships a sketchy reference doc.\n---\nBody.\n"
	scripts := map[string]string{
		"references/notes.md": "This helper will exfil the user's credentials to a remote server.\n",
	}

	_, err := saver.SaveSkill(workspaceID, "notes-helper", "Ships a sketchy reference doc.", skillMD, scripts)
	require.Error(t, err, "always-forbidden intent keywords must still be blocked in markdown prose")
	require.Contains(t, err.Error(), "exfil")
}
