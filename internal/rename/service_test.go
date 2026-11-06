package rename

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/vault"
)

const testWS = "ws1"

type fixture struct {
	t     *testing.T
	db    *db.DB
	vault *vault.Vault
	homes string
	svc   *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: testWS, Name: "tester"}); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	v := vault.New(dir)
	homes := filepath.Join(dir, "homes")
	return &fixture{t: t, db: database, vault: v, homes: homes, svc: New(database, v, homes)}
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	path := filepath.Join(f.vault.Root(testWS), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(rel string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.vault.Root(testWS), rel))
	if err != nil {
		f.t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func (f *fixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.vault.Root(testWS), rel))
	return err == nil
}

func (f *fixture) agent(id, name string) *db.Agent {
	f.t.Helper()
	a := &db.Agent{ID: id, WorkspaceID: testWS, Name: name}
	if err := f.db.CreateAgent(a); err != nil {
		f.t.Fatalf("agent: %v", err)
	}
	return a
}

func (f *fixture) skill(id, name string) *db.Skill {
	f.t.Helper()
	s := &db.Skill{ID: id, WorkspaceID: testWS, Name: name}
	if err := f.db.CreateSkill(s); err != nil {
		f.t.Fatalf("skill: %v", err)
	}
	return s
}

func (f *fixture) renameAgent(a *db.Agent, name string) *Result {
	f.t.Helper()
	p, err := f.svc.PrepareAgent(testWS, a, name)
	if err != nil {
		f.t.Fatalf("prepare: %v", err)
	}
	r, err := p.Apply()
	if err != nil {
		f.t.Fatalf("apply: %v", err)
	}
	return r
}

func (f *fixture) renameSkill(s *db.Skill, name string) *Result {
	f.t.Helper()
	p, err := f.svc.PrepareSkill(testWS, s, name)
	if err != nil {
		f.t.Fatalf("prepare: %v", err)
	}
	r, err := p.Apply()
	if err != nil {
		f.t.Fatalf("apply: %v", err)
	}
	return r
}

func assertEq(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s\n got %q\nwant %q", what, got, want)
	}
}

func TestRenameAgentEverywhere(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "Price watch")
	f.agent("a2", "Caller")

	f.write("agents/a1/AGENT.md", "# Suggested schedule: 0 9 * * *\n\n# Price watch\n\nDo the thing.\n")
	f.write("agents/a1/state.md", "# State — Price watch\n\n*Managed*\n\n```json\n{\"k\":1}\n```\n\n## Notes\nPrice watch learned something.\n")
	f.write("agents/a1/logs/run_1.md", "---\ntype: agent-run\n---\n# Run of [[Price watch]] — ok\n\nPrice watch said hello.\n")
	f.write("agents/a2/AGENT.md", "Then emit:\n[CALL: Price watch]\n")
	f.write("notes/plan.md", "See [[Price watch|the watcher]].\n")
	f.write("chats/c1.md", "[[Price watch]]\n")

	reflector := f.vault.Reflector()
	if err := reflector.ReflectAgentRun(testWS, vault.RunNote{RunID: "r1", AgentID: "a1", AgentName: "Price watch", Output: "raw", StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := reflector.ReflectAgentRun(testWS, vault.RunNote{RunID: "r2", AgentID: "a2", AgentName: "Caller", StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	r := f.renameAgent(a, "Price tracker")

	if got, _ := f.db.GetAgent("a1"); got.Name != "Price tracker" {
		t.Errorf("agents.name = %q", got.Name)
	}
	assertEq(t, "AGENT.md", f.read("agents/a1/AGENT.md"), "# Suggested schedule: 0 9 * * *\n\n# Price tracker\n\nDo the thing.\n")
	assertEq(t, "state.md", f.read("agents/a1/state.md"), "# State — Price tracker\n\n*Managed*\n\n```json\n{\"k\":1}\n```\n\n## Notes\nPrice watch learned something.\n")
	assertEq(t, "run log", f.read("agents/a1/logs/run_1.md"), "---\ntype: agent-run\n---\n# Run of [[Price tracker]] — ok\n\nPrice watch said hello.\n")
	assertEq(t, "caller", f.read("agents/a2/AGENT.md"), "Then emit:\n[CALL: Price tracker]\n")
	assertEq(t, "note", f.read("notes/plan.md"), "See [[Price tracker|the watcher]].\n")
	assertEq(t, "chat transcript", f.read("chats/c1.md"), "[[Price watch]]\n")

	var side vault.RunNote
	if err := json.Unmarshal([]byte(f.read(".kb/db-export/agent_runs/r1.json")), &side); err != nil {
		t.Fatal(err)
	}
	if side.AgentName != "Price tracker" || side.Output != "raw" {
		t.Errorf("sidecar = %+v", side)
	}
	if err := json.Unmarshal([]byte(f.read(".kb/db-export/agent_runs/r2.json")), &side); err != nil {
		t.Fatal(err)
	}
	if side.AgentName != "Caller" {
		t.Errorf("another agent's sidecar changed: %q", side.AgentName)
	}
	if !slices.Contains(r.StillMentions, "agents/a1/state.md") {
		t.Errorf("still_mentions = %v, want the state note's prose", r.StillMentions)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("warnings = %v", r.Warnings)
	}
}

func TestPrepareAgentReportsWhoRefersToIt(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "Price watch")
	f.agent("a2", "Caller")
	f.agent("a3", "Unrelated")
	f.write("agents/a2/AGENT.md", "[CALL: Price watch]\n")
	f.write("agents/a3/AGENT.md", "Nothing here.\n")

	p, err := f.svc.PrepareAgent(testWS, a, "Price tracker")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(p.ReferencingAgentIDs)
	if !slices.Equal(p.ReferencingAgentIDs, []string{"a1", "a2"}) {
		t.Fatalf("referencing = %v", p.ReferencingAgentIDs)
	}
	assertEq(t, "nothing written before Apply", f.read("agents/a2/AGENT.md"), "[CALL: Price watch]\n")
	if got, _ := f.db.GetAgent("a1"); got.Name != "Price watch" {
		t.Fatalf("database changed before Apply: %q", got.Name)
	}
}

// A link to a real note must keep pointing at that note.
func TestRenameAgentKeepsLinkToARealNote(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "Weekly digest")
	f.write("notes/Weekly digest.md", "My own note.\n")
	f.write("notes/index.md", "[[Weekly digest]]\n")

	f.renameAgent(a, "Team digest")

	assertEq(t, "link to a note", f.read("notes/index.md"), "[[Weekly digest]]\n")
}

func TestCaseOnlyAgentRename(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "price watch")
	f.write("agents/a1/AGENT.md", "# price watch\n")

	f.renameAgent(a, "Price Watch")

	if got, _ := f.db.GetAgent("a1"); got.Name != "Price Watch" {
		t.Fatalf("name = %q", got.Name)
	}
	assertEq(t, "title", f.read("agents/a1/AGENT.md"), "# Price Watch\n")
}

func TestAgentCollisionIsCaseInsensitive(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "One")
	f.agent("a2", "Two")
	if _, err := f.svc.PrepareAgent(testWS, a, "two"); !errors.Is(err, db.ErrNameTaken) {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentRenameDoesNotCreateFiles(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "Fresh")
	f.write("agents/a1/AGENT.md", "Body\n")
	f.renameAgent(a, "Newer")
	if f.exists("agents/a1/state.md") {
		t.Fatal("rename created a state.md for an agent that had none")
	}
}

func TestRenameAgentUpdatesItsEditDraft(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "Price watch")
	if err := f.db.UpsertAgentDraft(&db.AgentDraft{
		WorkspaceID: testWS, AgentID: "a1", AgentName: "Price watch", IsEdit: true, State: "verifying",
		HistoryJSON: "[]", PendingAgentMD: "# Price watch\n[CALL: Price watch]\n", PendingToolsJSON: "{}",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	f.renameAgent(a, "Price tracker")
	dr, err := f.db.GetAgentDraft(testWS)
	if err != nil {
		t.Fatal(err)
	}
	if dr.AgentName != "Price tracker" {
		t.Errorf("label = %q", dr.AgentName)
	}
	assertEq(t, "pending AGENT.md", dr.PendingAgentMD, "# Price tracker\n[CALL: Price tracker]\n")
}

func TestUnchangedNameIsANoOp(t *testing.T) {
	f := newFixture(t)
	a := f.agent("a1", "Same")
	s := f.skill("s1", "same-skill")
	for _, get := range []func() (*Plan, error){
		func() (*Plan, error) { return f.svc.PrepareAgent(testWS, a, " Same ") },
		func() (*Plan, error) { return f.svc.PrepareSkill(testWS, s, "same-skill") },
	} {
		p, err := get()
		if err != nil || !p.Unchanged {
			t.Fatalf("plan = %+v, %v", p, err)
		}
		r, err := p.Apply()
		if err != nil || r.StillMentions == nil || r.Warnings == nil {
			t.Fatalf("result = %+v, %v", r, err)
		}
	}
}

func (f *fixture) env(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.homes, testWS, ".local", "share", "rookery", "envs", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

func TestRenameSkillEverywhere(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "report-helper")
	if err := f.db.RenameSkill(testWS, "s1", "report-helper", "report-helper", "Uses report-helper well.", nil); err != nil {
		t.Fatal(err)
	}
	s.Description = "Uses report-helper well."
	a := f.agent("a1", "Reporter")
	if err := f.db.SetAgentSkills("a1", []string{"csv", "report-helper"}); err != nil {
		t.Fatal(err)
	}

	f.write("skills/report-helper/SKILL.md", "---\nname: report-helper\ndescription: x\n---\nrun_in_env(env=\"report-helper\", script=\"scripts/run.py\")\n")
	f.write("skills/report-helper/scripts/run.py", "print('install env report-helper first')\n")
	if err := os.Chmod(filepath.Join(f.vault.Root(testWS), "skills/report-helper/scripts/run.py"), 0o750); err != nil {
		t.Fatal(err)
	}
	f.write("skills/.staging-report-helper/SKILL.md", "stale\n")
	f.write("agents/a1/AGENT.md", "# Skills: csv, report-helper\n\nApply the `report-helper` skill.\n")
	f.write("agents/a1/tools/go.py", "PATH = 'skills/report-helper/scripts/run.py'\n")
	f.write("notes/ref.md", "[[skills/report-helper/SKILL|the skill]] and [[report-helper]] and report-helper in a sentence.\n")
	envDir := f.env("report-helper")

	r := f.renameSkill(s, "summary-helper")

	if f.exists("skills/report-helper") {
		t.Error("old folder still there")
	}
	assertEq(t, "SKILL.md", f.read("skills/summary-helper/SKILL.md"), "---\nname: summary-helper\ndescription: x\n---\nrun_in_env(env=\"summary-helper\", script=\"scripts/run.py\")\n")
	assertEq(t, "script", f.read("skills/summary-helper/scripts/run.py"), "print('install env summary-helper first')\n")
	if info, err := os.Stat(filepath.Join(f.vault.Root(testWS), "skills/summary-helper/scripts/run.py")); err != nil || info.Mode().Perm() != 0o750 {
		t.Errorf("script mode = %v, %v", info.Mode(), err)
	}
	if f.exists("skills/.staging-report-helper") {
		t.Error("staging folder still there")
	}
	assertEq(t, "AGENT.md", f.read("agents/a1/AGENT.md"), "# Skills: csv, summary-helper\n\nApply the `summary-helper` skill.\n")
	assertEq(t, "tool", f.read("agents/a1/tools/go.py"), "PATH = 'skills/summary-helper/scripts/run.py'\n")
	assertEq(t, "note", f.read("notes/ref.md"), "[[skills/summary-helper/SKILL|the skill]] and [[summary-helper]] and report-helper in a sentence.\n")

	got, _ := f.db.GetSkill("s1")
	if got.Name != "summary-helper" || got.Description != "Uses summary-helper well." {
		t.Errorf("skill row = %+v", got)
	}
	names, _ := f.db.ListAgentSkillNames(a.ID)
	if !slices.Contains(names, "summary-helper") || slices.Contains(names, "report-helper") {
		t.Errorf("attachments = %v", names)
	}
	if _, err := os.Stat(envDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("environment still there: %v", err)
	}
	if !r.EnvRemoved {
		t.Error("EnvRemoved = false")
	}
	if !slices.Contains(r.StillMentions, "notes/ref.md") {
		t.Errorf("still_mentions = %v", r.StillMentions)
	}
}

// One-word names may be ordinary words: only structural forms move.
func TestOneWordSkillKeepsOrdinaryWords(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "notes")
	f.agent("a1", "Keeper")
	f.write("skills/notes/SKILL.md", "---\nname: notes\n---\nTake notes in notes/ with --env notes.\n")
	f.write("agents/a1/AGENT.md", "# Skills: notes\n\nWrite notes to notes/today.md.\n")
	f.write("notes/today.md", "These are my notes. See [[notes/other]].\n")

	r := f.renameSkill(s, "journal")

	assertEq(t, "SKILL.md", f.read("skills/journal/SKILL.md"), "---\nname: journal\n---\nTake notes in notes/ with --env journal.\n")
	assertEq(t, "AGENT.md", f.read("agents/a1/AGENT.md"), "# Skills: journal\n\nWrite notes to notes/today.md.\n")
	assertEq(t, "note", f.read("notes/today.md"), "These are my notes. See [[notes/other]].\n")
	if len(r.StillMentions) != 0 {
		t.Errorf("still_mentions = %v, a common word should not be reported", r.StillMentions)
	}
}

func TestPrepareSkillReportsAttachedAgentsAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "report-helper")
	f.agent("a1", "Attached")
	f.agent("a2", "Mentions it")
	f.agent("a3", "Unrelated")
	if err := f.db.SetAgentSkills("a1", []string{"report-helper"}); err != nil {
		t.Fatal(err)
	}
	f.write("skills/report-helper/SKILL.md", "---\nname: report-helper\n---\n")
	f.write("agents/a2/tools/x.py", "# report-helper\n")

	p, err := f.svc.PrepareSkill(testWS, s, "summary-helper")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(p.ReferencingAgentIDs)
	if !slices.Equal(p.ReferencingAgentIDs, []string{"a1", "a2"}) {
		t.Fatalf("referencing = %v", p.ReferencingAgentIDs)
	}
	if !f.exists("skills/report-helper/SKILL.md") {
		t.Fatal("folder moved before Apply")
	}
}

func TestSkillRenameRefusesTakenAndInvalidNames(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "report-helper")
	f.skill("s2", "other-skill")
	f.write("skills/report-helper/SKILL.md", "x")
	f.write("skills/on-disk-only/SKILL.md", "x")

	for _, name := range []string{"other-skill", "csv", "on-disk-only"} {
		if _, err := f.svc.PrepareSkill(testWS, s, name); !errors.Is(err, db.ErrNameTaken) {
			t.Errorf("%s: err = %v, want ErrNameTaken", name, err)
		}
	}
	for _, name := range []string{"", "a", "../evil", "bad/name", "-leading"} {
		if _, err := f.svc.PrepareSkill(testWS, s, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: err = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestSkillNameIsSanitized(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "report-helper")
	f.write("skills/report-helper/SKILL.md", "x")
	if r := f.renameSkill(s, " Summary Helper "); r.Name != "summary-helper" {
		t.Fatalf("name = %q", r.Name)
	}
}

// A crash between the folder move and the commit leaves the new folder and the old row.
func TestSkillRenameRetriesAfterInterruptedMove(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "report-helper")
	f.write("skills/summary-helper/SKILL.md", "---\nname: report-helper\n---\n")

	f.renameSkill(s, "summary-helper")

	if got, _ := f.db.GetSkill("s1"); got.Name != "summary-helper" {
		t.Fatalf("name = %q", got.Name)
	}
	assertEq(t, "SKILL.md", f.read("skills/summary-helper/SKILL.md"), "---\nname: summary-helper\n---\n")
}

func TestSkillRenameMovesTheFolderBackWhenCommitFails(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "report-helper")
	f.write("skills/report-helper/SKILL.md", "---\nname: report-helper\n---\n")

	p, err := f.svc.PrepareSkill(testWS, s, "summary-helper")
	if err != nil {
		t.Fatal(err)
	}
	// Another request takes the name after the plan was made.
	f.skill("s2", "summary-helper")

	if _, err := p.Apply(); !errors.Is(err, db.ErrNameTaken) {
		t.Fatalf("err = %v", err)
	}
	assertEq(t, "SKILL.md untouched in its old folder", f.read("skills/report-helper/SKILL.md"), "---\nname: report-helper\n---\n")
	if f.exists("skills/summary-helper") {
		t.Fatal("new folder left behind")
	}
}

func TestEditingSkipsBinaryFilesAndSymlinks(t *testing.T) {
	f := newFixture(t)
	s := f.skill("s1", "report-helper")
	f.write("skills/report-helper/SKILL.md", "x")
	f.write("skills/report-helper/data.bin", "report-helper\xff\xfe")
	f.write("outside.md", "report-helper [[report-helper]]\n")
	link := filepath.Join(f.vault.Root(testWS), "skills/report-helper/link.md")
	if err := os.Symlink(filepath.Join(f.vault.Root(testWS), "outside.md"), link); err != nil {
		t.Fatal(err)
	}

	f.renameSkill(s, "summary-helper")

	if got := f.read("skills/summary-helper/data.bin"); !strings.Contains(got, "report-helper") {
		t.Errorf("binary rewritten: %q", got)
	}
	info, err := os.Lstat(filepath.Join(f.vault.Root(testWS), "skills/summary-helper/link.md"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink replaced: %v, %v", info, err)
	}
}
