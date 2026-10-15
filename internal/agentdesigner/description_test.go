package agentdesigner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rookery-ai/rookery/internal/db"
)

const weekendMD = `# Suggested schedule: 0 17 * * 5
# Skills: time-and-timezones

# Weekend in Lisbon

Every Friday at 5pm (the owner's local time), plan the weekend in Lisbon and send the
owner a short message with the weekend outlook and two suggestions.

## What to do each run

1. **Get the forecast.**
`

func TestExtractDescriptionJoinsTheWrappedParagraph(t *testing.T) {
	got := extractDescription(weekendMD, "x")
	want := "Every Friday at 5pm (the owner's local time), plan the weekend in Lisbon and send the owner a short message with the weekend outlook and two suggestions."
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDescriptionKeepsWholeSentences(t *testing.T) {
	md := "# A\n\n" + strings.Repeat("Short sentence here. ", 20) + "\n"
	got := extractDescription(md, "x")
	if !strings.HasSuffix(got, ".") || utf8.RuneCountInString(got) > maxDescriptionChars {
		t.Fatalf("got %q", got)
	}
}

func TestExtractDescriptionCutsALongSentenceAtAWord(t *testing.T) {
	md := "# A\n\n" + strings.Repeat("слово ", 60) + "\n"
	got := extractDescription(md, "x")
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "слово…") {
		t.Fatalf("got %q", got)
	}
	if utf8.RuneCountInString(got) > maxDescriptionChars {
		t.Fatalf("too long: %d", utf8.RuneCountInString(got))
	}
}

func TestExtractDescriptionStripsEmphasisAndSkipsLists(t *testing.T) {
	md := "# A\n\n- a list item\n\nCheck **GitHub** for `new` issues.\nThen stop.\n"
	if got := extractDescription(md, "x"); got != "Check GitHub for new issues. Then stop." {
		t.Fatalf("got %q", got)
	}
	if got := extractDescription("# only headings\n", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

type fakeDescDB struct{ agents map[string]*db.Agent }

func (f *fakeDescDB) GetAgent(id string) (*db.Agent, error) { return f.agents[id], nil }
func (f *fakeDescDB) UpdateAgentDescription(id, d string) error {
	f.agents[id].Description = d
	return nil
}

func TestBackfillRewritesOnlyLegacyDescriptions(t *testing.T) {
	base := t.TempDir()
	for _, id := range []string{"a1", "a2"} {
		dir := filepath.Join(base, "ws", "agents", id)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(weekendMD), 0o644)
	}
	f := &fakeDescDB{agents: map[string]*db.Agent{
		"a1": {ID: "a1", Name: "W", Description: legacyExtractDescription(weekendMD, "W")},
		"a2": {ID: "a2", Name: "W", Description: "Set by hand."},
	}}
	if n := BackfillAgentDescriptions(f, base); n != 1 {
		t.Fatalf("rewrote %d", n)
	}
	if f.agents["a1"].Description != extractDescription(weekendMD, "W") || f.agents["a2"].Description != "Set by hand." {
		t.Fatalf("%+v %+v", f.agents["a1"], f.agents["a2"])
	}
	if n := BackfillAgentDescriptions(f, base); n != 0 {
		t.Fatalf("not idempotent: %d", n)
	}
}
