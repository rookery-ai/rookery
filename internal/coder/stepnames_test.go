package coder

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/llm"
)

// call builds a milestone through the real producer, so a change to its format
// fails here rather than quietly degrading what a chat app shows.
func call(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return toolMilestone(llm.ToolCall{Name: name, Args: b}, "/data/vaults/w1", "/data/home")
}

func TestFriendlyStepNamesKnownTools(t *testing.T) {
	cases := []struct {
		name       string
		args       map[string]any
		doing, did string
	}{
		{"search_files", map[string]any{"query": "report"}, "📚 Searching knowledge base: `report`", "📚 Searched knowledge base: `report`"},
		{"read_file", map[string]any{"path": "notes/my_file.md"}, "📄 Reading `notes/my_file.md`", "📄 Read `notes/my_file.md`"},
		{"web_search", map[string]any{"query": "example topic"}, "🌐 Searching the web: `example topic`", "🌐 Searched the web: `example topic`"},
		{"web_fetch", map[string]any{"url": "https://example.com/a/b?token=abc"}, "🌐 Fetching `example.com`", "🌐 Fetched `example.com`"},
		{"bash", map[string]any{"command": "ls"}, "💻 Running a command", "💻 Ran a command"},
		{"gmail_send_email", map[string]any{"to": "a@example.com"}, "🔌 Using `gmail_send_email`", "🔌 Used `gmail_send_email`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doing, did := FriendlyStep(call(t, c.name, c.args))
			if doing != c.doing {
				t.Errorf("doing = %q, want %q", doing, c.doing)
			}
			if did != c.did {
				t.Errorf("did = %q, want %q", did, c.did)
			}
		})
	}
}

// These lines go through a third party and can be read by a whole group.
func TestFriendlyStepHidesSensitiveDetail(t *testing.T) {
	cases := []struct {
		name   string
		args   map[string]any
		hidden []string
	}{
		{"bash", map[string]any{"command": "curl -H 'Authorization: Bearer abc123' https://example.com"}, []string{"abc123", "curl"}},
		{"run_script", map[string]any{"path": "tools/secret_thing.py"}, []string{"secret_thing"}},
		{"web_fetch", map[string]any{"url": "https://example.com/reset?token=abc123"}, []string{"abc123", "reset"}},
		{"browser_read", map[string]any{"url": "https://example.com/account/42"}, []string{"account", "42"}},
		{"save_to_kb", map[string]any{"source": "https://example.com/?key=abc123"}, []string{"abc123"}},
		{"gmail_send_email", map[string]any{"to": "a@example.com", "body": "private text"}, []string{"a@example.com", "private"}},
		{"mcp__srv__do", map[string]any{"value": "private text"}, []string{"private"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := call(t, c.name, c.args)
			doing, did := FriendlyStep(line)
			for _, h := range c.hidden {
				if strings.Contains(doing, h) || strings.Contains(did, h) {
					t.Errorf("%q leaked into %q / %q", h, doing, did)
				}
			}
		})
	}
}

func TestFriendlyStepHidesBrowserValues(t *testing.T) {
	h := &hostToolSet{}
	h.rememberRefs([]browser.Element{{Ref: "e1", Name: "Home address"}})
	for _, tc := range []llm.ToolCall{
		{Name: "browser_fill", Args: json.RawMessage(`{"ref":"e1","value":"1 Example Street"}`)},
		{Name: "browser_click", Args: json.RawMessage(`{"ref":"e1"}`)},
	} {
		line := h.browserMilestone(tc)
		if line == "" {
			t.Fatalf("%s produced no milestone", tc.Name)
		}
		doing, did := FriendlyStep(line)
		for _, s := range []string{doing, did} {
			if strings.Contains(s, "Example Street") || strings.Contains(s, "Home address") {
				t.Errorf("%s leaked its element or value: %q", tc.Name, s)
			}
		}
	}
}

// toolMilestone falls back to raw JSON arguments when no known field is set.
func TestFriendlyStepDropsRawJSONFallback(t *testing.T) {
	line := call(t, "read_file", map[string]any{"section": "private heading"})
	doing, did := FriendlyStep(line)
	if strings.Contains(doing, "private") || strings.Contains(did, "private") {
		t.Errorf("raw arguments reached the label: %q / %q", doing, did)
	}
	if doing != "📄 Reading a file" {
		t.Errorf("doing = %q", doing)
	}
}

func TestFriendlyStepPassesOtherLinesThrough(t *testing.T) {
	line := "🔁 answer was cut off — retrying with more room…"
	doing, did := FriendlyStep(line)
	if doing != line || did != line {
		t.Errorf("got %q / %q", doing, did)
	}
}

func TestFriendlyStepKeepsDetailInsideItsCodeSpan(t *testing.T) {
	doing, _ := FriendlyStep(call(t, "search_files", map[string]any{"query": "a`b\nc"}))
	if doing != "📚 Searching knowledge base: `a'b c`" {
		t.Errorf("doing = %q", doing)
	}
}
