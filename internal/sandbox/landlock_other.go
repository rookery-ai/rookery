//go:build !linux

package sandbox

import "errors"

// Supported always reports false off Linux: Landlock is a Linux-only LSM.
// Strong per-user filesystem confinement on Windows/macOS is a future phase.
func Supported() bool { return false }

// SystemReadOnlyPaths returns nil off Linux (no confinement is applied).
func SystemReadOnlyPaths() []string { return nil }

// SystemReadWriteFiles returns nil off Linux (no confinement is applied).
func SystemReadWriteFiles() []string { return nil }

// Exec is never reached off Linux because Supported() is false and callers do
// not Wrap commands; it exists only so the package compiles cross-platform.
func Exec(_ Spec) error {
	return errors.New("sandbox: Landlock confinement is only available on Linux")
}
