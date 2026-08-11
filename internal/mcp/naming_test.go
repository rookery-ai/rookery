package mcp

import (
	"regexp"
	"strings"
	"testing"
)

// providerToolName is the charset both OpenAI and Anthropic enforce on a function
// name. A provider rejects the WHOLE tool list when one name violates it, so a single
// badly-named MCP tool would take out every other tool the agent has — connector
// actions included. That blast radius is why this is tested rather than assumed.
var providerToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func TestExposedToolNameSurvivesLegalMCPNames(t *testing.T) {
	// Every one of these is a SPEC-COMPLIANT MCP tool name. The dot case is drawn
	// straight from the spec's own examples (`admin.tools.list`), which is exactly
	// why unslugged names cannot be passed through.
	cases := []struct{ slug, tool string }{
		{"sentry", "admin.tools.list"},
		{"home", "DATA_EXPORT_v2"},
		{"paperless", "search documents"},
		{"immich", "get/asset"},
		{"grafana", strings.Repeat("very_long_tool_name", 7)}, // 133 chars: legal, over the limit
		{"a-server", "tool.with.many.dots.indeed"},
	}
	for _, c := range cases {
		got := ExposedToolName(c.slug, c.tool, nil)
		if !providerToolName.MatchString(got) {
			t.Errorf("ExposedToolName(%q, %q) = %q, which a provider would reject", c.slug, c.tool, got)
		}
		if !strings.HasPrefix(got, ToolNamePrefix+"__") {
			t.Errorf("ExposedToolName(%q, %q) = %q, lost its namespace prefix", c.slug, c.tool, got)
		}
	}
}

func TestExposedToolNameIsDeterministic(t *testing.T) {
	// The result is persisted in mcp_tools.tool_name. If it were not reproducible, a
	// re-sync would rename tools the model has already been taught within a
	// conversation, and every in-flight tool call would resolve to nothing.
	long := strings.Repeat("x", 200)
	for i := 0; i < 5; i++ {
		if a, b := ExposedToolName("srv", long, nil), ExposedToolName("srv", long, nil); a != b {
			t.Fatalf("not deterministic: %q vs %q", a, b)
		}
	}
}

func TestExposedToolNameDisambiguatesTruncatedCollisions(t *testing.T) {
	// Two distinct tools that share a long prefix truncate onto the same stem. The
	// hash suffix is what keeps them apart; without it one tool would silently
	// shadow the other and calls would go to the wrong place.
	prefix := strings.Repeat("report_", 12)
	a := ExposedToolName("srv", prefix+"alpha", nil)
	b := ExposedToolName("srv", prefix+"beta", nil)
	if a == b {
		t.Fatalf("distinct tools collapsed to the same exposed name: %q", a)
	}
	for _, n := range []string{a, b} {
		if !providerToolName.MatchString(n) {
			t.Errorf("%q would be rejected by a provider", n)
		}
	}
}

func TestExposedToolNameDistinguishesNamesThatSlugAlike(t *testing.T) {
	// "a.b" and "a_b" both slug to "a_b". Hashing the UNSLUGGED identity is what
	// keeps them distinct once truncation is in play.
	long := strings.Repeat("q", 80)
	a := ExposedToolName("srv", long+".b", nil)
	b := ExposedToolName("srv", long+"_b", nil)
	if a == b {
		t.Fatalf("names that slug alike produced the same exposed name: %q", a)
	}
}

func TestExposedToolNameHonoursTaken(t *testing.T) {
	first := ExposedToolName("srv", "search", nil)
	second := ExposedToolName("srv", "search", map[string]bool{first: true})
	if first == second {
		t.Fatalf("taken map ignored: both %q", first)
	}
	if !providerToolName.MatchString(second) {
		t.Errorf("%q would be rejected by a provider", second)
	}
}

func TestTwoServersExposingSearchDoNotCollide(t *testing.T) {
	// The spec explicitly warns that aggregating clients will hit this, which is why
	// there is no bare-name case the way connectors have one.
	a := ExposedToolName("sentry", "search", nil)
	b := ExposedToolName("linear", "search", nil)
	if a == b {
		t.Fatalf("two servers' search tools collided: %q", a)
	}
}

func TestSlugForLowercasesAndSanitises(t *testing.T) {
	if got := SlugFor("My Home Server!"); got != "my_home_server_" {
		t.Fatalf("SlugFor = %q", got)
	}
	if got := SlugFor("   "); got != "x" {
		t.Fatalf("SlugFor(blank) = %q, want the non-empty fallback", got)
	}
}
