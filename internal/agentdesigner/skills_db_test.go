package agentdesigner

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/prompts"
	"github.com/rookery-ai/rookery/internal/skilllibrary"
	"github.com/stretchr/testify/require"
)

// TestSaveAgent_PersistsDeclaredSkillsToDB verifies the auto-detected skills path
// end-to-end: the coder's "# Skills:" header line is parsed by parseSkillsLine, fed
// to SaveAgent, and lands in the agent_skills DB table (the source of truth for the
// agent page and runner). Core skills (no skills-table row) attach by name too.
func TestSaveAgent_PersistsDeclaredSkillsToDB(t *testing.T) {
	database, workspaceID := testDB(t)
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
	if err := designer.SaveAgent(workspaceID, agentID, "test-agent", "desc",
		agentMD, nil, declared); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}

	// The DB must now hold both declared skills — core + user, by name. There is
	// no manifest/agent.json any more to duplicate this into: agent_skills is the
	// only place an agent's skills are recorded.
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
}

// An explicit "# Skills: none" is a decision. Overriding it would make attachment
// unpredictable, so the selector must not fire.
func TestResolveAgentSkillsRespectsExplicitNone(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{{Name: "pdf", Description: "Read PDFs."}}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "# Skills: none\n\nDo a thing.\n", pool, nil, false)
	require.NotNil(t, got)
	require.Empty(t, got)
}

// A present header is used verbatim; no selector call.
func TestResolveAgentSkillsUsesHeader(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{
		{Name: "pdf", Description: "Read PDFs."},
		{Name: "csv", Description: "Read CSVs."},
	}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "# Skills: pdf\n\nDo a thing.\n", pool, nil, false)
	require.Equal(t, []string{"pdf"}, got)
}

// On an edit, existing attachments the user may have curated by hand are never replaced.
func TestResolveAgentSkillsEditKeepsExisting(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{{Name: "pdf", Description: "Read PDFs."}}
	existing := []string{"csv"}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "Do a thing with no header.\n", pool, existing, true)
	require.Equal(t, []string{"csv"}, got)
}

// With no coder wired, the selector degrades to attaching nothing rather than panicking.
func TestResolveAgentSkillsNoCoderAttachesNothing(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{{Name: "pdf", Description: "Read PDFs."}}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "Do a thing with no header.\n", pool, nil, false)
	require.NotNil(t, got)
	require.Empty(t, got)
}

// TestSaveAgent_NoSkillsLinePersistsNothing confirms that when the coder forgets
// the "# Skills:" line, no skills are attached (never "all skills"). This is the
// regression guard for the old fallback-to-all behaviour.
func TestSaveAgent_NoSkillsLinePersistsNothing(t *testing.T) {
	database, workspaceID := testDB(t)
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
	if err := designer.SaveAgent(workspaceID, agentID, "test-agent", "desc",
		agentMD, nil, declared); err != nil {
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

// An explicit header must win on an edit. Checking existing skills first meant that once
// an agent had any skill, every later edit's header was discarded — the user asks the
// designer for a change, the designer declares it, and nothing happens.
func TestResolveAgentSkillsEditHonoursANewHeader(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{
		{Name: "pdf", Description: "Read PDFs."},
		{Name: "csv", Description: "Read CSVs."},
	}
	existing := []string{"pdf"}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1",
		"# Skills: pdf, csv\n\nAlso read CSVs.\n", pool, existing, true)

	require.Equal(t, []string{"pdf", "csv"}, got,
		"the edit declared csv; discarding it would silently ignore what the user asked for")
}

func TestReconcileSkillsLine(t *testing.T) {
	cases := []struct {
		name     string
		md       string
		attached []string
		want     string
	}{
		{
			name:     "replaces a stale header with the attached truth",
			md:       "# Suggested schedule: none\n# Skills: pdf, csv\n\nBody.\n",
			attached: []string{"pdf"},
			want:     "# Suggested schedule: none\n# Skills: pdf\n\nBody.\n",
		},
		{
			name:     "nothing attached becomes an explicit none",
			md:       "# Suggested schedule: none\n# Skills: pdf\n\nBody.\n",
			attached: nil,
			want:     "# Suggested schedule: none\n# Skills: none\n\nBody.\n",
		},
		{
			name:     "inserts after the schedule line when absent",
			md:       "# Suggested schedule: */10 * * * *\n\nBody.\n",
			attached: []string{"csv"},
			want:     "# Suggested schedule: */10 * * * *\n# Skills: csv\n\nBody.\n",
		},
		{
			// parseSkillsLine is tolerant of formatting drift, so reconciliation must be
			// too — otherwise a drifted header survives and is found first at save time.
			name:     "replaces a drifted header spelling in place",
			md:       "# Suggested schedule: none\n\n## Required skills: pdf\n\nBody.\n",
			attached: []string{"csv"},
			want:     "# Suggested schedule: none\n\n# Skills: csv\n\nBody.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, reconcileSkillsLine(tc.md, tc.attached, true))
		})
	}
}

// The round trip that matters: what reconcileSkillsLine writes must be what
// parseSkillsLine reads back, or the two halves disagree and the edit path breaks.
func TestReconciledHeaderRoundTripsThroughParse(t *testing.T) {
	pool := []prompts.SkillRef{
		{Name: "pdf", Description: "Read PDFs."},
		{Name: "csv", Description: "Read CSVs."},
	}
	md := reconcileSkillsLine("# Suggested schedule: none\n\nBody.\n", []string{"pdf", "csv"}, true)
	require.Equal(t, []string{"pdf", "csv"}, parseSkillsLine(md, pool))

	none := reconcileSkillsLine("# Suggested schedule: none\n\nBody.\n", nil, true)
	got := parseSkillsLine(none, pool)
	require.NotNil(t, got, "an explicit none must parse as non-nil empty, not as absent")
	require.Empty(t, got)
}

// If the attached set cannot be read, reconciliation must leave AGENT.md untouched.
// Writing "# Skills: none" for an unreadable DB forges a declaration, and because an
// explicit header wins at save time that forgery would persist as an EMPTY skill set —
// silently wiping curated attachments over a transient SQLITE_BUSY.
func TestReconcileSkillsLineLeavesFileAloneWhenUnknown(t *testing.T) {
	md := "# Suggested schedule: none\n# Skills: pdf, csv\n\nBody.\n"
	require.Equal(t, md, reconcileSkillsLine(md, nil, false))

	pool := []prompts.SkillRef{{Name: "pdf", Description: "x"}, {Name: "csv", Description: "y"}}
	require.Equal(t, []string{"pdf", "csv"}, parseSkillsLine(reconcileSkillsLine(md, nil, false), pool),
		"the real attachments must survive an unreadable DB, not be zeroed")
}

// parseSkillsLine accepts a bare heading followed by bullets. Reconciliation must replace
// that whole block, not insert a second header above it and strand the stale bullets as
// contradictory prose.
func TestReconcileSkillsLineReplacesBulletListShape(t *testing.T) {
	md := "# Suggested schedule: none\n\n## Skills\n- pdf\n- csv\n\nBody.\n"

	got := reconcileSkillsLine(md, []string{"csv"}, true)

	require.Equal(t, "# Suggested schedule: none\n\n# Skills: csv\n\nBody.\n", got)
	require.NotContains(t, got, "- pdf", "the stale bullet list must be gone, not merely outranked")

	pool := []prompts.SkillRef{{Name: "pdf", Description: "x"}, {Name: "csv", Description: "y"}}
	require.Equal(t, []string{"csv"}, parseSkillsLine(got, pool))
}
