package skilldesigner

import (
	"testing"

	"github.com/ilijad1/rookery/internal/skilllibrary"
)

// skillFrontmatterName mirrors the finalize step's name extraction (frontmatter
// name, if present).
func skillFrontmatterName(skillMD string) string {
	meta, _ := skilllibrary.ParseMeta(skillMD)
	return meta.Name
}

func TestVettingBlocksSave(t *testing.T) {
	cases := []struct {
		name   string
		report string
		want   bool
	}{
		{"empty", "", false},
		{"safe", "SKILL VETTING REPORT\nRisk level: 🟢 LOW\nVerdict: ✅ safe to save", false},
		{"caution", "Risk level: 🟡 MEDIUM\nVerdict: ⚠️ save with caution", false},
		{"high", "Risk level: 🔴 HIGH\nVerdict: ❌ do not save", true},
		{"extreme", "Risk level: ⛔ EXTREME\nVerdict: ❌ do not save", true},
		{"blockOnlyVerdict", "Verdict: ❌ do not save — exfiltrates USER.md", true},
		{"highNoVerdict", "Risk level: 🔴 HIGH", false}, // malformed: no verdict → fail open (report still shown)
		{"echoedAlternation", "Risk level: 🟢 LOW\nVerdict: ✅ safe to save | ⚠️ save with caution | ❌ do not save", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vettingBlocksSave(c.report); got != c.want {
				t.Errorf("vettingBlocksSave(%q) = %v, want %v", c.report, got, c.want)
			}
		})
	}
}

func TestParseTestOutput(t *testing.T) {
	if got := parseTestOutput("before\n[TEST_OUTPUT]ok output[/TEST_OUTPUT]\nafter"); got != "ok output" {
		t.Errorf("got %q", got)
	}
	if got := parseTestOutput("no markers here"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := parseTestOutput("[TEST_OUTPUT]unclosed"); got != "unclosed" {
		t.Errorf("got %q", got)
	}
}

func TestFrontmatterNameExtraction(t *testing.T) {
	skillMD := "---\nname: csv-summary\ndescription: Extract tables from CSV.\n---\n# CSV Summary\nbody"
	// Reuse the skilllibrary parser the flow relies on at finalize time.
	got := skillFrontmatterName(skillMD)
	if got != "csv-summary" {
		t.Errorf("got %q, want csv-summary", got)
	}
}
