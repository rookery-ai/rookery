package coder

import "testing"

func TestBackendForBin(t *testing.T) {
	cases := map[string]string{
		"claude":                              "claude",
		"/usr/bin/claude-code":                "claude",
		"/home/rookie/.opencode/bin/opencode": "opencode",
		"codex":                               "codex",
		"gemini":                              "gemini",
		"cursor-agent":                        "cursor",
		"cursor":                              "cursor",
		"something-unknown":                   "",
	}
	for bin, want := range cases {
		if got := BackendForBin(bin); got != want {
			t.Errorf("BackendForBin(%q) = %q, want %q", bin, got, want)
		}
	}
}
