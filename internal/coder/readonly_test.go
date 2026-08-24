package coder

import (
	"strings"
	"testing"
)

// TestDesignAllowedToolsGrantsNoWriteAndNoConvert pins the CLI grant for a
// design conversation. The blanket "kb:*" grant that ChatAllowedTools uses
// would reach `kb convert`, which WRITES a note into the vault — so the design
// grant must name subcommands individually.
func TestDesignAllowedToolsGrantsNoWriteAndNoConvert(t *testing.T) {
	got := DesignAllowedTools("/usr/bin/rookery")

	for _, banned := range []string{"Write", "Edit", "kb convert", "kb:*"} {
		if strings.Contains(got, banned) {
			t.Errorf("DesignAllowedTools granted %q; got %q", banned, got)
		}
	}
	for _, want := range []string{
		"Read", "Glob", "Grep", "WebFetch", "WebSearch",
		"Bash(/usr/bin/rookery kb search:*)",
		"Bash(/usr/bin/rookery kb map:*)",
		"Bash(/usr/bin/rookery kb table:*)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("DesignAllowedTools missing %q; got %q", want, got)
		}
	}
}

// TestDesignAllowedToolsOmitsKBGrantsWithoutABridge covers the designers, which
// wire no KB bridge: with no binary there is nothing to authorise, and an empty
// Bash grant must not be emitted.
func TestDesignAllowedToolsOmitsKBGrantsWithoutABridge(t *testing.T) {
	got := DesignAllowedTools("")
	if strings.Contains(got, "Bash(") {
		t.Errorf("DesignAllowedTools(\"\") emitted a Bash grant: %q", got)
	}
	if got != "Read,Glob,Grep,WebFetch,WebSearch" {
		t.Errorf("DesignAllowedTools(\"\") = %q, want the bare read-only set", got)
	}
}

// TestReadOnlyProfileNeverSendsAnEmptyAllowedTools guards the documented
// indefinite hang: claudeBackend.buildArgs emits NO --allowedTools flag when
// noTools is false and allowedTools is empty, alongside --setting-sources "",
// which makes the subprocess block forever.
func TestReadOnlyProfileNeverSendsAnEmptyAllowedTools(t *testing.T) {
	c := (&Coder{}).WithReadOnlyTools()
	if c.noTools {
		t.Fatal("WithReadOnlyTools must not set noTools")
	}
	if c.effectiveAllowedTools() == "" {
		t.Fatal("read-only profile resolved to an empty allowedTools (subprocess would hang)")
	}
}

// TestEffectiveAllowedToolsLeavesEveryOtherCallerAlone is the regression guard
// for the CLI arg path: the helper must be a no-op unless the read-only profile
// is set, whatever the caller configured.
func TestEffectiveAllowedToolsLeavesEveryOtherCallerAlone(t *testing.T) {
	if got := (&Coder{}).effectiveAllowedTools(); got != "" {
		t.Errorf("default coder resolved allowedTools to %q, want empty", got)
	}
	runSet := "Bash,WebFetch,Read,Write,Edit"
	if got := (&Coder{allowedTools: runSet}).effectiveAllowedTools(); got != runSet {
		t.Errorf("run profile allowedTools = %q, want %q", got, runSet)
	}
	// An explicit grant still wins under the read-only profile, so a caller that
	// wires a KB bridge can widen it to the scoped kb subcommands.
	explicit := DesignAllowedTools("/usr/bin/rookery")
	if got := (&Coder{readOnlyTools: true, allowedTools: explicit}).effectiveAllowedTools(); got != explicit {
		t.Errorf("explicit read-only grant = %q, want %q", got, explicit)
	}
}

// TestWithReadOnlyToolsDoesNotMutateTheReceiver mirrors every other With* modifier.
func TestWithReadOnlyToolsDoesNotMutateTheReceiver(t *testing.T) {
	base := &Coder{}
	_ = base.WithReadOnlyTools()
	if base.readOnlyTools {
		t.Fatal("WithReadOnlyTools mutated its receiver")
	}
}
