//go:build !windows

package main

import "os"

// swapBinary moves the staged file over the target.
//
// POSIX unlinks the old directory entry rather than the open file, so renaming
// over an executable that is currently running is ordinary and safe: the
// running process keeps the inode it started from, and the next exec picks up
// the new one.
func swapBinary(staged, target string) error {
	return os.Rename(staged, target)
}

// removeSelf deletes the running binary, returning any caveat the caller should
// print alongside its success line. Ordinary on POSIX, for the same reason
// swapBinary is: the unlink removes the directory entry, not the open file — so
// there is never a caveat here.
func removeSelf(self string) (string, error) { return "", os.Remove(self) }

// removeSelfHint explains a removal that failed. Here it can only be a
// permission or ownership problem — the file being in use is not a reason
// POSIX refuses.
func removeSelfHint(string) string {
	return "remove it by hand, or re-run with the privileges that installed it."
}
