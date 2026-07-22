package coder

import (
	"path/filepath"
	"strings"

	"github.com/ilijad1/simple-agents/internal/prompts"
)

// BuildSpec describes what the current BUILD must produce, so the API engine's
// finish-verification gates are not hard-coded to one caller's shape.
//
// Before this existed, verifyFinishNudge stat'd "AGENT.md" unconditionally. A skill
// build therefore spent its entire nudge budget being told to write the agent
// definition — a file a skill must never contain — and never produced SKILL.md. The
// engine is shared; what the build must produce is the caller's business.
type BuildSpec struct {
	// Deliverable is the file that must exist at the work-dir root before the build
	// may finish ("AGENT.md" / "SKILL.md").
	Deliverable string

	// IsScript reports whether a work-dir-relative path is an ENTRY-POINT helper the
	// script-verification gate should require real output from. Library modules, test
	// files and package scaffolding must return false.
	IsScript func(path string) bool

	// ProgressNoun goes in the live progress line, e.g. "the agent's script".
	ProgressNoun string

	// MissingDeliverableNudge and UnverifiedScriptNudge produce gate 1 / gate 2 text.
	// last is true on the final nudge, when the model must stop iterating and finish.
	MissingDeliverableNudge func(last bool) string
	UnverifiedScriptNudge   func(last bool) string
}

// AgentBuildSpec is the historical behaviour and the default when no spec is set.
var AgentBuildSpec = BuildSpec{
	Deliverable:  "AGENT.md",
	IsScript:     isAgentScriptPath,
	ProgressNoun: "the agent's script",
	MissingDeliverableNudge: func(last bool) string {
		return prompts.BuildMissingDeliverableNudge("AGENT.md", last)
	},
	UnverifiedScriptNudge: func(last bool) string {
		return prompts.BuildUnverifiedScriptNudge("AGENT.md", last)
	},
}

// SkillBuildSpec is the skill-creator build. Gate 2 is KEPT: skill-creator already
// mandates a smoke run (`python3 scripts/x.py --help`, or against a small fixture) and
// such a run satisfies the gate, so the gate enforces the contract the skill format
// already requires. What differs is the wording — the agent nudge talks about reaching a
// live service, which would send a skill build chasing data its script was never meant to
// fetch.
var SkillBuildSpec = BuildSpec{
	Deliverable:  "SKILL.md",
	IsScript:     isSkillScriptPath,
	ProgressNoun: "the skill's script",
	MissingDeliverableNudge: func(last bool) string {
		return prompts.BuildMissingDeliverableNudge("SKILL.md", last)
	},
	UnverifiedScriptNudge: func(last bool) string {
		return prompts.BuildUnverifiedScriptNudge("SKILL.md", last)
	},
}

// isSkillScriptPath reports whether path is an entry-point script in a skill's scripts/
// directory. Test files and library modules are excluded for the same reason they are in
// isAgentScriptPath: they are not the thing that must produce output.
func isSkillScriptPath(path string) bool {
	p := filepath.ToSlash(strings.TrimSpace(path))
	if !strings.HasSuffix(p, ".py") && !strings.HasSuffix(p, ".sh") {
		return false
	}
	if !strings.HasPrefix(p, "scripts/") && !strings.Contains(p, "/scripts/") {
		return false
	}
	base := filepath.Base(p)
	if base == "__init__.py" || base == "conftest.py" ||
		strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "lib" || seg == "tests" {
			return false
		}
	}
	return true
}
