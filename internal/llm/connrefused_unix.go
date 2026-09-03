//go:build !windows

package llm

import (
	"errors"
	"syscall"
)

// isConnRefused reports whether the error is "nothing is listening on that
// port". On POSIX the stdlib errno matches directly.
//
// This is split per platform because the obvious one-liner is wrong on Windows,
// in a way that compiles and links cleanly and then simply never matches — see
// connrefused_windows.go for the detail. The cross-compile gate would not have
// caught it.
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
