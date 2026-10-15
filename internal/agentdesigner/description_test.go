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

func TestExtractDescriptionOnRealAgentShapes(t *testing.T) {
	cases := map[string]string{
		"# D\n\nYou are a morning digest agent. Every weekday you scan Hacker News for self-hosting stories.\n": "Every weekday you scan Hacker News for self-hosting stories.",
		"# D\n\nEvery weekday at 8am (owner's local time) you scan Hacker News for stories about self-hosting (self-hosted apps, homelabs, local-first software, open-source services, privacy tools you run yourself, and so on), file a digest note, and message the owner only when something is worth it.\n": "Every weekday at 8am you scan Hacker News for stories about self-hosting, file a digest note, and message the owner only when something is worth it.",
	}
	for md, want := range cases {
		if got := extractDescription(md, "x"); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
	long := "# M\n\nEach month, summarise the owner's business spending: total last month by category, compare it with the month before, convert USD amounts to EUR at the current rate, save a summary note, and tell the owner the three biggest month-over-month changes.\n"
	got := extractDescription(long, "x")
	if !strings.HasSuffix(got, "note…") && !strings.HasSuffix(got, "rate…") {
		t.Errorf("expected a clause-boundary cut, got %q", got)
	}
}
