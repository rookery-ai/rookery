//go:build windows

package main

import (
	"fmt"
	"os"
)

// oldBinarySuffix marks a binary moved aside by an upgrade. A previous
// upgrade's leftover is deleted by the next one, which is the first moment
// anything is allowed to: it is no longer the running image by then.
const oldBinarySuffix = ".old"

// swapBinary moves the staged file over the target.
//
// Windows cannot do what the POSIX implementation does. A running executable
// is held with a share mode that denies delete, so renaming over it fails with
// "Access is denied" — and `rookery upgrade` is ALWAYS replacing the image it
// is itself executing from, so the straightforward os.Rename could never
// succeed here. It was not a matter of stopping the server first: the upgrade
// process is the lock.
//
// What Windows does permit is renaming the running image itself — the lock
// denies delete, not rename. So the target is moved aside, the new binary takes
// its place, and the displaced file is removed on a best-effort basis: that
// removal fails while this very process is still running it, which is expected,
// and the next upgrade clears it.
//
// A failure after the move-aside puts the original back, so the outcome matches
// the POSIX one: either the old binary or the new one is on PATH, never
// neither.
func swapBinary(staged, target string) error {
	old := target + oldBinarySuffix

	// A leftover from a previous upgrade. Now that nothing is executing it,
	// this succeeds; if it does not, the rename below would fail anyway and
	// report the more useful error.
	_ = os.Remove(old)

	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("move %s aside: %w", target, err)
	}
	if err := os.Rename(staged, target); err != nil {
		// Put it back. Losing the binary entirely is the one outcome worse
		// than a failed upgrade.
		if restore := os.Rename(old, target); restore != nil {
			return fmt.Errorf("install %s failed (%v) AND restoring %s failed (%v) — "+
				"the previous binary is at %s", target, err, target, restore, old)
		}
		return fmt.Errorf("install %s: %w", target, err)
	}

	// Expected to fail while this process is still executing the displaced
	// image. Deliberately not reported: the upgrade succeeded, and the file is
	// cleared by the next one.
	_ = os.Remove(old)
	return nil
}

// removeSelf deletes the running binary.
//
// Windows will not delete an executing image, and `rookery uninstall` is always
// running the very file it is asked to remove — so os.Remove alone could never
// succeed, and the resulting warning told the user to retry with more
// privileges, which was never the problem.
//
// The image is moved aside instead. That much Windows does allow, and it is
// enough to take the binary off PATH under its own name, which is what
// uninstalling means to the person running it.
//
// It is NOT the same as deleting it, so the returned caveat says so: the
// displaced file cannot be removed until this process exits, and claiming a
// clean removal while leaving a stray .old the user never hears about is the
// kind of quiet half-success this command exists to avoid.
func removeSelf(self string) (string, error) {
	old := self + oldBinarySuffix
	_ = os.Remove(old)
	if err := os.Rename(self, old); err != nil {
		return "", err
	}
	return fmt.Sprintf("Windows cannot delete a running program, so it was moved to %s.\n"+
		"  Delete that file once this window is closed.", old), nil
}

// removeSelfHint explains a removal that failed. On Windows the likely cause is
// a lock, not permissions — a running `rookery serve`, or the folder open in
// Explorer.
func removeSelfHint(self string) string {
	return fmt.Sprintf("stop any running rookery (including `rookery serve`) and delete %s by hand.", self)
}
