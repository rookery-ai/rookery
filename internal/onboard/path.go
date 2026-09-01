package onboard

import (
	"os"
	"strings"
)

// Finding a tool is not the same as being able to use it, and this file is the
// difference.
//
// Every consumer of these tools resolves them through exec.LookPath:
// internal/health's have(), internal/convert (pdftotext, tesseract),
// internal/vault/search.go (rg), internal/agentdesigner/guardrails.go
// (python3), and internal/coder/hosttools.go, which additionally uses the
// resolved path to grant the interpreter's own directory read+execute inside
// the Landlock sandbox.
//
// So teaching only this package to search harder would produce a setup that
// reports "all present" while OCR, PDF extraction and the AST guardrail stayed
// broken — a silent failure replacing a loud one. Extending the process PATH
// instead fixes every one of those call sites at once, including the ones not
// written yet, and guarantees the property that actually matters: /healthz and
// setup cannot disagree, because they are asking the same question of the same
// environment.

// PathWith returns list with dirs appended, skipping any it already contains,
// and reports whether anything was added.
//
// Appended rather than prepended on purpose. These directories are only ever
// derived from tools PATH could NOT already resolve, so there is nothing to
// shadow — and appending keeps a deliberate operator override in front of
// anything this package infers.
func PathWith(list string, dirs []string, sep string) (string, bool) {
	have := map[string]bool{}
	for _, p := range strings.Split(list, sep) {
		if p != "" {
			have[p] = true
		}
	}

	added := false
	out := list
	for _, d := range dirs {
		if d == "" || have[d] {
			continue
		}
		have[d] = true
		added = true
		if out == "" {
			out = d
			continue
		}
		out += sep + d
	}
	return out, added
}

// AugmentProcessPath extends this process's PATH so the host tools installed
// off it become usable, and returns the directories it added.
//
// An empty return means the PATH was already sufficient, which is the normal
// case on Linux and macOS — these tools live in /usr/bin there. That is what
// makes this safe to ship: the only platform whose behaviour changes is the one
// that is broken.
func AugmentProcessPath() []string {
	dirs := ToolDirs(CurrentHost())
	if len(dirs) == 0 {
		return nil
	}
	next, changed := PathWith(os.Getenv("PATH"), dirs, string(os.PathListSeparator))
	if !changed {
		return nil
	}
	if err := os.Setenv("PATH", next); err != nil {
		return nil
	}
	return dirs
}
