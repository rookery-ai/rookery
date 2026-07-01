package agentdesigner

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
)

// TestSaveAgent_PersistsDeclaredSkillsToDB verifies the auto-detected skills path
// end-to-end: the coder's "# Skills:" header line is parsed by parseSkillsLine, fed
// to SaveAgent, and lands in the agent_skills DB table (the source of truth for the
// agent page and runner). Core skills (no skills-table row) attach by name too.
func TestSaveAgent_PersistsDeclaredSkillsToDB(t *testing.T) {
	database, userID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	// The available skills pool the designer loaded for this session: one core
	// skill (csv) + one user skill. parseSkillsLine validates against these names.
	coreCSV := skilllibrary.LoadBundled()
	var csvName string
	for _, s := range coreCSV {
		if s.Name == "csv" {
			csvName = s.Name
			break
		}
	}
	if csvName == "" {
		t.Fatalf("csv core skill not bundled")
	}
	installed := []prompts.SkillRef{
		{Name: csvName, Description: "csv core skill"},
		{Name: "my-user-skill", Description: "a user skill"},
	}

	// AGENT.md the coder generated — declares a core skill and a user skill.
	agentMD := "# Suggested schedule: none\n# Skills: csv, my-user-skill\nYou are a test agent.\n"

	declared := parseSkillsLine(agentMD, installed)
	if len(declared) != 2 {
		t.Fatalf("parseSkillsLine = %v, want 2 names", declared)
	}

	designer := NewDesigner(database, agentsDir)
	if err := designer.SaveAgent(userID, agentID, "test-agent", "desc",
		agentMD, nil, declared, nil); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}

	// The DB must now hold both declared skills — core + user, by name.
	got, err := database.ListAgentSkillNames(agentID)
	if err != nil {
		t.Fatalf("ListAgentSkillNames: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("agent_skills = %v, want 2 entries", got)
	}
	want := map[string]bool{"csv": false, "my-user-skill": false}
	for _, n := range got {
		if _, ok := want[n]; !ok {
			t.Errorf("unexpected skill %q in agent_skills", n)
		}
		want[n] = true
	}
	for n, found := range want {
		if !found {
			t.Errorf("skill %q not persisted to agent_skills (got %v)", n, got)
		}
	}

	// The manifest on disk must NOT carry the skills (DB is the source of truth;
	// AGENT.md is for the LLM).
	m, err := LoadManifest(agentsDir, userID, agentID)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Skills) != 0 {
		t.Errorf("manifest.Skills = %v, want empty (DB is the source of truth)", m.Skills)
	}
}

// TestSaveAgent_NoSkillsLinePersistsNothing confirms that when the coder forgets
// the "# Skills:" line, no skills are attached (never "all skills"). This is the
// regression guard for the old fallback-to-all behaviour.
func TestSaveAgent_NoSkillsLinePersistsNothing(t *testing.T) {
	database, userID := testDB(t)
	agentsDir := t.TempDir()
	agentID := uuid.New().String()

	installed := []prompts.SkillRef{
		{Name: "csv", Description: "csv"},
		{Name: "pdf", Description: "pdf"},
		{Name: "my-user-skill", Description: "u"},
	}
	agentMD := "# Suggested schedule: none\nYou are a test agent with no skills line.\n"

	declared := parseSkillsLine(agentMD, installed)
	if declared != nil {
		t.Fatalf("parseSkillsLine = %v, want nil (no # Skills: line)", declared)
	}
	// saveAndFinish/updateAndFinish turn nil into []string{} (no fallback to all).
	if declared == nil {
		declared = []string{}
	}

	designer := NewDesigner(database, agentsDir)
	if err := designer.SaveAgent(userID, agentID, "test-agent", "desc",
		agentMD, nil, declared, nil); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}

	got, err := database.ListAgentSkillNames(agentID)
	if err != nil {
		t.Fatalf("ListAgentSkillNames: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("agent_skills = %v, want empty (no skills declared → none attached)", got)
	}
}