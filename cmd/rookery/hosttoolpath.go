package main

import (
	"log/slog"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/onboard"
	"github.com/rookery-ai/rookery/internal/sandbox"
)

// The host tools are resolved once, at startup, and the directories holding any
// that PATH could not already reach are added to it.
//
// Doing it here rather than in each command is the point. Every consumer of
// these tools goes through exec.LookPath — internal/health, internal/convert,
// internal/vault's searcher, the agent-tool AST guardrail, and the sandbox
// grant that makes the interpreter executable inside Landlock — so fixing them
// one at a time would leave the next one, written later, silently wrong. It
// also guarantees the property internal/health's agreement test pins: setup and
// /healthz cannot give different answers, because after this runs they are
// asking the same question of the same environment.
//
// On Linux and macOS this is almost always a no-op; these tools live in
// /usr/bin. Windows is where it matters, and it is why the whole thing exists.

// augmentableCommands is the inverse of the two exclusions below, expressed as
// a predicate so it can be tested without running a command.
//
// The hidden helpers are skipped deliberately. Both exist to re-exec something
// they were handed — the sandbox helper confines itself and execve's the real
// command, the browser host launches a browser under a caller-built spec — and
// changing the environment underneath a process whose entire job is to hand it
// on is not this function's business. Their parents have already augmented, and
// the environment the child gets is the one the parent chose to give it.
func shouldAugmentHostToolPath(args []string) bool {
	for _, a := range args {
		if a == sandbox.HelperCommand || a == browser.HostCommand {
			return false
		}
	}
	return true
}

// augmentHostToolPath extends PATH and says so.
//
// The log line is not decoration. Mutating a process's environment invisibly is
// how a machine ends up behaving differently from an identical one for reasons
// nobody can find, so the one case where this changes anything reports itself.
func augmentHostToolPath(args []string) {
	if !shouldAugmentHostToolPath(args) {
		return
	}
	if added := onboard.AugmentProcessPath(); len(added) > 0 {
		slog.Info("host tools found outside PATH; extended PATH for this process",
			"dirs", added)
	}
}
