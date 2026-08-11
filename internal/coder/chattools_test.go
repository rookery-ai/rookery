package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatAllowedToolsShape(t *testing.T) {
	base := ChatAllowedTools("", "", "")
	for _, want := range []string{"Read", "Write", "Edit", "Glob", "Grep", "WebFetch", "WebSearch"} {
		if !strings.Contains(base, want) {
			t.Errorf("chat grant missing %q: %s", want, base)
		}
	}
	if strings.Contains(base, "Bash") {
		t.Errorf("chat must not get Bash without a connector/kb bridge: %s", base)
	}

	withBridge := ChatAllowedTools("/usr/local/bin/rookery", "", "")
	if !strings.Contains(withBridge, "Bash(/usr/local/bin/rookery connector exec:*)") {
		t.Errorf("connector grant must be scoped to the single exec command: %s", withBridge)
	}
	// A bare Bash grant would hand chat arbitrary shell.
	if strings.Contains(withBridge, "Bash,") || strings.HasSuffix(withBridge, "Bash") {
		t.Errorf("unscoped Bash grant leaked into the chat tool set: %s", withBridge)
	}

	withKB := ChatAllowedTools("", "/usr/local/bin/rookery", "")
	if !strings.Contains(withKB, "Bash(/usr/local/bin/rookery kb:*)") {
		t.Errorf("kb grant must be scoped to the kb subcommand: %s", withKB)
	}

	withBoth := ChatAllowedTools("/usr/local/bin/rookery", "/usr/local/bin/rookery", "")
	if !strings.Contains(withBoth, "connector exec:*") || !strings.Contains(withBoth, "kb:*") {
		t.Errorf("both bridges must be able to grant independently: %s", withBoth)
	}
}

// TestChatAllowedToolsIsSingleSourced is the regression guard for a real defect:
// the chat tool list was duplicated across the chat-adapter path and the web-UI
// chat handler, and a change updated only one of them — giving Telegram web
// access while the web UI silently lacked it, with both sharing a system prompt
// that advertised the capability. Any new inline grant reintroduces that class.
func TestChatAllowedToolsIsSingleSourced(t *testing.T) {
	// ".." is internal/; the duplicated grants live in web/ and cmd/, so scan the repo root.
	root := "../.."
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.Contains(path, "chattools.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, `WithAllowedTools("Read,`) {
				offenders = append(offenders, path+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("chat tool grants must come from ChatAllowedTools, found inline literals:\n%s",
			strings.Join(offenders, "\n"))
	}
}

// TestChatMCPGrantIsScopedToTheSubcommand pins that a chat coder given the MCP bridge
// gets permission to run exactly `<bin> mcp exec` and nothing more.
//
// Chat is otherwise file-only. A broader Bash grant would hand every chat turn
// arbitrary shell as a side effect of the owner connecting one MCP server.
func TestChatMCPGrantIsScopedToTheSubcommand(t *testing.T) {
	withMCP := ChatAllowedTools("", "", "/usr/local/bin/rookery")
	if !strings.Contains(withMCP, "Bash(/usr/local/bin/rookery mcp exec:*)") {
		t.Fatalf("missing scoped MCP grant: %s", withMCP)
	}
	if strings.Contains(ChatAllowedTools("", "", ""), "mcp exec") {
		t.Fatal("granted MCP shell access with no bridge wired")
	}
	all := ChatAllowedTools("/usr/local/bin/rookery", "/usr/local/bin/rookery", "/usr/local/bin/rookery")
	for _, want := range []string{"connector exec:*", "kb:*", "mcp exec:*"} {
		if !strings.Contains(all, want) {
			t.Errorf("grant %q missing from %s", want, all)
		}
	}
}
