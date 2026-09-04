package llm

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// isConnRefused reports whether the error is "nothing is listening on that
// port".
//
// The portable-looking `errors.Is(err, syscall.ECONNREFUSED)` is WRONG here, and
// wrong in the quietest possible way: it compiles, it links, the cross-compile
// gate passes, and it never matches. Two reasons, both verified against the
// stdlib source rather than assumed. A real Windows dial failure carries
// WSAECONNREFUSED (10061), while syscall.ECONNREFUSED on Windows is a synthetic
// value offset from APPLICATION_ERROR — a different number entirely. And
// syscall.Errno.Is on Windows maps only ErrPermission, ErrExist, ErrNotExist and
// ErrUnsupported, so there is no equivalence rule to bridge the two the way
// there is for the file-system errnos.
//
// The consequence of getting this wrong is not a build failure but a Windows
// install that keeps the exact bug this file exists to fix: a dead local model
// server still costs ~68 seconds of silent backoff and still reports nothing.
// golang.org/x/sys/windows is already a direct requirement of this module (the
// backup command's console echo control uses it), so naming the real constant
// costs nothing.
//
// syscall.ECONNREFUSED is still checked as well: it is what a synthetic or
// wrapped error inside this codebase would carry, and matching it too is free.
func isConnRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, syscall.ECONNREFUSED)
}
