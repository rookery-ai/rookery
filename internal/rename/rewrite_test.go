package rename

import (
	"errors"
	"strings"
	"testing"
)

func TestRewriteAgentCallsIsExactLikeTheRunner(t *testing.T) {
	got := RewriteAgentCalls("[CALL: Price watch]\nsay `[CALL: Price watch]`\n[CALL: price watch]\n", "Price watch", "Price tracker")
	want := "[CALL: Price tracker]\nsay `[CALL: Price tracker]`\n[CALL: price watch]\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRewriteAgentTitleTouchesOnlyAnExactHeading(t *testing.T) {
	in := "# Suggested schedule: 0 9 * * *\n\n# Price watch\n\nPrice watch runs daily.\n\n# Price watch details\n"
	want := "# Suggested schedule: 0 9 * * *\n\n# Price tracker\n\nPrice watch runs daily.\n\n# Price watch details\n"
	if got := RewriteAgentTitle(in, "Price watch", "Price tracker"); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRewriteStateHeadingOnlyOnTheFirstLine(t *testing.T) {
	in := "# State — Old\n\n*Managed*\n\n## Notes\n# State — Old\n"
	want := "# State — New\n\n*Managed*\n\n## Notes\n# State — Old\n"
	if got := RewriteStateHeading(in, "Old", "New"); got != want {
		t.Fatalf("got %q", got)
	}
	if got := RewriteStateHeading("# Something else\n", "Old", "New"); got != "# Something else\n" {
		t.Fatalf("rewrote an unrelated heading: %q", got)
	}
}

func TestRewriteRunHeadingKeepsStatusFrontmatterAndOutput(t *testing.T) {
	in := "---\ntype: agent-run\n---\n# Run of [[Old]] — ok\n\nOld reported a change. [[Old]]\n"
	want := "---\ntype: agent-run\n---\n# Run of [[New]] — ok\n\nOld reported a change. [[Old]]\n"
	if got := RewriteRunHeading(in, "Old", "New"); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRewriteSkillRefsHyphenatedNameIsReplacedAsAWholeToken(t *testing.T) {
	in := "name: report-helper\nUse the `report-helper` skill. Not my-report-helper2, report-helper_x or report-helpers.\n"
	want := "name: summary-helper\nUse the `summary-helper` skill. Not my-report-helper2, report-helper_x or report-helpers.\n"
	if got := RewriteSkillRefs(in, "report-helper", "summary-helper"); got != want {
		t.Fatalf("got %q", got)
	}
}

// A one-word name may be an ordinary word, so only structural forms move.
func TestRewriteSkillRefsOneWordNameKeepsOrdinaryWords(t *testing.T) {
	cases := []struct{ in, want string }{
		{"name: notes\n", "name: journal\n"},
		{"name: \"notes\"\n", "name: \"journal\"\n"},
		{"read skills/notes/scripts/run.py\n", "read skills/journal/scripts/run.py\n"},
		{"cd envs/notes\n", "cd envs/journal\n"},
		{`run_in_env(env="notes", script="x")`, `run_in_env(env="journal", script="x")`},
		{"env=notes\n", "env=journal\n"},
		{"rookery deps run --env notes -- x.py\n", "rookery deps run --env journal -- x.py\n"},
		{"# Skills: csv, notes, pdf\n", "# Skills: csv, journal, pdf\n"},
		{"# Skills:\n# - csv\n- notes\n\nBody about notes\n", "# Skills:\n# - csv\n- journal\n\nBody about notes\n"},
		{"Read notes/todo.md and take notes.\n", "Read notes/todo.md and take notes.\n"},
		{"Skills: my notes\n", "Skills: my notes\n"},
		{"# Skills for notes\n", "# Skills for notes\n"},
		{"mynotes skills/notesy\n", "mynotes skills/notesy\n"},
	}
	for _, c := range cases {
		if got := RewriteSkillRefs(c.in, "notes", "journal"); got != c.want {
			t.Errorf("RewriteSkillRefs(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestRewriteWikilinksKeepsAliasAndHeading(t *testing.T) {
	in := "[[Old]] [[old|shown]] [[Old#Part]] [[Old#Part|shown]] [[Older]] [[other/Old]]"
	want := "[[New]] [[New|shown]] [[New#Part]] [[New#Part|shown]] [[Older]] [[other/Old]]"
	if got := RewriteWikilinks(in, "Old", "New"); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRewriteSkillLinksFollowTheMovedFolder(t *testing.T) {
	in := "[[skills/notes/SKILL]] [[skills/notes/SKILL|see]] [a](skills/notes/SKILL.md) [b](../skills/notes/x.md) [c](skills/notes) [d](skills/notesy/x.md) skills/notes/x.md"
	want := "[[skills/journal/SKILL]] [[skills/journal/SKILL|see]] [a](skills/journal/SKILL.md) [b](../skills/journal/x.md) [c](skills/journal) [d](skills/notesy/x.md) skills/notes/x.md"
	if got := RewriteSkillLinks(in, "notes", "journal"); got != want {
		t.Fatalf("got %q", got)
	}
}

// A name is data: regexp metacharacters and $ in a replacement must be inert.
func TestRewritersTreatNamesLiterally(t *testing.T) {
	if got := RewriteWikilinks("[[a.b]] [[axb]]", "a.b", "c$1"); got != "[[c$1]] [[axb]]" {
		t.Fatalf("wikilink: %q", got)
	}
	if got := RewriteAgentTitle("# a.b\n", "a.b", "c$1"); got != "# c$1\n" {
		t.Fatalf("title: %q", got)
	}
}

func TestContainsToken(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"see Price watch.", true},
		{"Price watch", true},
		{"Price watcher", false},
		{"My-Price watch", false},
		{"none", false},
	} {
		if got := ContainsToken(c.text, "Price watch"); got != c.want {
			t.Errorf("ContainsToken(%q) = %v", c.text, got)
		}
	}
}

func TestValidateAgentName(t *testing.T) {
	got, err := ValidateAgentName("  Price tracker ")
	if err != nil || got != "Price tracker" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "   ", "a[b", "a]b", "a|b", "a#b", "a\nb", "a\tb", strings.Repeat("x", 121)} {
		if _, err := ValidateAgentName(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateAgentName(%q) err = %v, want ErrInvalidName", bad, err)
		}
	}
	if _, err := ValidateAgentName(strings.Repeat("é", 120)); err != nil {
		t.Errorf("120 multi-byte characters refused: %v", err)
	}
}
