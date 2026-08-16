//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// disableEcho turns terminal echo off and returns a function restoring it.
// Both calls are best-effort: a non-tty simply keeps its default behaviour.
//
// stty rather than golang.org/x/term: it is present on every POSIX host this
// runs on, and the alternative is a module dependency for one call.
func disableEcho() func() {
	if _, err := exec.LookPath("stty"); err != nil {
		return func() {}
	}
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if err := off.Run(); err != nil {
		return func() {}
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = os.Stdin
		_ = on.Run()
	}
}
