package agentdesigner

import (
	"strings"
	"testing"
)

func TestReadScheduleHeader(t *testing.T) {
	cases := []struct {
		md, want string
		present  bool
	}{
		{"# Suggested schedule: 0 9 * * *\nbody", "0 9 * * *", true},
		{"\n\n# Suggested schedule: none\nbody", "none", true},
		{"# Suggested schedule: not cron", "not cron", true},
		{"# Title\n# Suggested schedule: 0 9 * * *", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, present := ReadScheduleHeader(c.md)
		if got != c.want || present != c.present {
			t.Errorf("ReadScheduleHeader(%q) = %q,%v want %q,%v", c.md, got, present, c.want, c.present)
		}
	}
}

func TestSetScheduleHeader(t *testing.T) {
	cases := []struct{ name, md, cron, want string }{
		{"replaces", "# Suggested schedule: 0 9 * * *\n# Skills: csv\n\nBody.\n", "0 8 * * 1", "# Suggested schedule: 0 8 * * 1\n# Skills: csv\n\nBody.\n"},
		{"none", "# Suggested schedule: 0 9 * * *\nBody", "", "# Suggested schedule: none\nBody"},
		{"inserts", "Body only.\n", "0 8 * * 1", "# Suggested schedule: 0 8 * * 1\nBody only.\n"},
		{"header is whole file", "# Suggested schedule: 0 9 * * *", "1 2 * * *", "# Suggested schedule: 1 2 * * *"},
		{"never a later line", "# Title\n# Suggested schedule: 0 9 * * *\n", "1 2 * * *", "# Suggested schedule: 1 2 * * *\n# Title\n# Suggested schedule: 0 9 * * *\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SetScheduleHeader(c.md, c.cron); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestReplaceCronLiteralWholeTokenOnly(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The schedule (`0 9 * * *`) is local.", "The schedule (`5 8 * * 1`) is local."},
		{"0 9 * * * and again 0 9 * * *", "5 8 * * 1 and again 5 8 * * 1"},
		{"Not this: 10 9 * * *", "Not this: 10 9 * * *"},
		{"Not this: 0 9 * * *,5", "Not this: 0 9 * * *,5"},
		{"Six fields: 0 9 * * * 2030", "Six fields: 0 9 * * * 2030"},
		{"Six fields: 5 0 9 * * *", "Six fields: 5 0 9 * * *"},
		{"Nothing here.", "Nothing here."},
	}
	for _, c := range cases {
		if got := ReplaceCronLiteral(c.in, "0 9 * * *", "5 8 * * 1"); got != c.want {
			t.Errorf("ReplaceCronLiteral(%q) = %q want %q", c.in, got, c.want)
		}
	}
	if got := ReplaceCronLiteral("x 0 9 * * * y", "", "1 1 * * *"); got != "x 0 9 * * * y" {
		t.Errorf("empty old expression changed the text: %q", got)
	}
}

func TestParseScheduleProse(t *testing.T) {
	edits, review, err := ParseScheduleProse("Here you go:\n```json\n{\"edits\":[{\"find\":\"a\",\"replace\":\"b\"}],\"review\":[\" compare with yesterday \",\"\"]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 || edits[0].Find != "a" || edits[0].Replace != "b" {
		t.Errorf("edits = %+v", edits)
	}
	if len(review) != 1 || review[0] != "compare with yesterday" {
		t.Errorf("review = %+v", review)
	}
	for _, bad := range []string{"", "no object", "{not json}"} {
		if _, _, err := ParseScheduleProse(bad); err == nil {
			t.Errorf("ParseScheduleProse(%q) accepted", bad)
		}
	}
}

const scheduleEditFixture = `# Suggested schedule: 0 9 * * *
# Skills: daily-report
# Required secrets:
# - DAILY_KEY: runs daily
# Weekly report

Every day at 9am, fetch the report.
Fetch the report again if it failed.
[CALL: daily helper] runs daily too.
`

func TestApplyScheduleEditsGuards(t *testing.T) {
	cases := []struct {
		name string
		edit ScheduleEdit
		ok   bool
	}{
		{"timing sentence", ScheduleEdit{"Every day at 9am", "Every Monday at 8am"}, true},
		{"a title is prose", ScheduleEdit{"# Weekly report", "# Monthly report"}, true},
		{"the header", ScheduleEdit{"# Suggested schedule: 0 9 * * *", "# Suggested schedule: 1 1 * * *"}, false},
		{"skills line", ScheduleEdit{"daily-report", "weekly-report"}, false},
		{"multi-line secret item", ScheduleEdit{"runs daily\n", "runs weekly\n"}, false},
		{"secret item text", ScheduleEdit{"DAILY_KEY: runs daily", "DAILY_KEY: runs weekly"}, false},
		{"call line", ScheduleEdit{"[CALL: daily helper] runs daily too.", "[CALL: daily helper] runs weekly."}, false},
		{"not unique", ScheduleEdit{"the report", "that report"}, false},
		{"missing", ScheduleEdit{"Every hour", "Every day"}, false},
		{"multi-line find", ScheduleEdit{"9am, fetch the report.\nFetch", "x"}, false},
		{"multi-line replace", ScheduleEdit{"Every day at 9am", "a\nb"}, false},
		{"blank replace deletes", ScheduleEdit{"Every day at 9am", "  "}, false},
		{"protocol marker", ScheduleEdit{"Every day at 9am", "[SILENT] Every day"}, false},
		{"far longer", ScheduleEdit{"Every day at 9am", strings.Repeat("x", 200)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, applied, refused := ApplyScheduleEdits(scheduleEditFixture, []ScheduleEdit{c.edit})
			if c.ok {
				if len(applied) != 1 || refused != 0 || !strings.Contains(out, c.edit.Replace) {
					t.Errorf("not applied: applied=%v refused=%d", applied, refused)
				}
				return
			}
			if out != scheduleEditFixture || len(applied) != 0 || refused != 1 {
				t.Errorf("guard did not hold: applied=%v refused=%d out=%q", applied, refused, out)
			}
		})
	}
}

func TestApplyScheduleEditsCapsTheBatch(t *testing.T) {
	var md strings.Builder
	var edits []ScheduleEdit
	for i := range 12 {
		s := "line-" + string(rune('a'+i))
		md.WriteString(s + "\n")
		edits = append(edits, ScheduleEdit{s, s + "!"})
	}
	_, applied, refused := ApplyScheduleEdits(md.String(), edits)
	if len(applied) != maxScheduleEdits || refused != 2 {
		t.Errorf("applied=%d refused=%d", len(applied), refused)
	}
}

func TestRefreshDescription(t *testing.T) {
	oldMD := "# Suggested schedule: 0 9 * * *\n\nEvery day at 9am, fetch the report.\n"
	newMD := "# Suggested schedule: 0 8 * * 1\n\nEvery Monday at 8am, fetch the report.\n"
	derived := extractDescription(oldMD, "x")
	next, ok := RefreshDescription(derived, oldMD, newMD, "x")
	if !ok || !strings.Contains(next, "Every Monday") {
		t.Errorf("derived description not refreshed: %q %v", next, ok)
	}
	if _, ok := RefreshDescription("Written by someone else.", oldMD, newMD, "x"); ok {
		t.Error("a description not derived from the file was replaced")
	}
	if _, ok := RefreshDescription(derived, oldMD, oldMD, "x"); ok {
		t.Error("an unchanged file reported a change")
	}
}
