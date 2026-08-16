//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// disableEcho turns console echo off and returns a function restoring the
// previous mode.
//
// This exists because the POSIX implementation shells out to stty, and there is
// no stty on Windows — so its LookPath guard returned a no-op and the backup
// passphrase was printed on screen as it was typed, on every `rookery backup`
// command that prompts. Nothing failed and nothing warned; the only signal was
// the characters appearing.
//
// golang.org/x/sys/windows rather than syscall: syscall exports GetConsoleMode
// on Windows but NOT SetConsoleMode, so the pair cannot be completed there. It
// is not a new dependency — x/sys is already a direct requirement of this
// module — which is why this is preferable to hand-rolling the kernel32 call
// through a LazyDLL.
//
// windows.ENABLE_LINE_INPUT is deliberately left set. Clearing it would switch
// the console to character-at-a-time input, and readPassphrase reads a whole
// line with bufio's ReadString('\n') — it would then never see the newline the
// user pressed and would block forever. Only echo is turned off.
//
// Every failure path returns the same no-op restore the stty implementation
// returns, so the worst outcome here is the behaviour this replaces: a visible
// passphrase, never a lost or corrupted one. That matters more than usual —
// this prompt is the one credential a backup cannot recover if it is wrong, so
// a change that could break reading it would be a poor trade for hiding it.
//
// The mode is restored to exactly what was read, rather than to a computed
// "echo on": a console that already had echo off for its own reasons stays
// that way.
func disableEcho() func() {
	handle := windows.Handle(os.Stdin.Fd())

	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		// Not a console — stdin is a pipe or a file. There is nothing to echo
		// to, which is the redirected case readPassphrase already tolerates.
		return func() {}
	}
	if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(handle, mode) }
}
