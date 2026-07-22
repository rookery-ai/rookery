package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatAllowedToolsShape(t *testing.T) {
	base := ChatAllowedTools("")
	for _, want := range []string{"Read", "Write", "Edit", "Glob", "Grep", "WebFetch", "WebSearch"} {
		if !strings.Contains(base, want) {
			t.Errorf("chat grant missing %q: %s", want, base)
		}
	}
	if strings.Contains(base, "Bash") {
		t.Errorf("chat must not get Bash without a connector bridge: %s", base)
	}

	withBridge := ChatAllowedTools("/usr/local/bin/simple-agents")
	if !strings.Contains(withBridge, "Bash(/usr/local/bin/simple-agents connector exec:*)") {
		t.Errorf("connector grant must be scoped to the single exec command: %s", withBridge)
	}
	// A bare Bash grant would hand chat arbitrary shell.
	if strings.Contains(withBridge, "Bash,") || strings.HasSuffix(withBridge, "Bash") {
		t.Errorf("unscoped Bash grant leaked into the chat tool set: %s", withBridge)
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
