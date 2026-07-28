// Package buildinfo carries the version metadata stamped into the binary at
// link time. It is its own package (rather than variables in main) so that any
// package can report the build without importing main — notably internal/health,
// which serves /healthz.
package buildinfo

import "fmt"

// Set via -ldflags -X at release time. The defaults are what a plain
// `go build` produces, and they are deliberately not "unknown": a developer
// build should say so.
var (
	Version = "0.0.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// Short returns just the version string.
func Short() string { return Version }

// String returns the full human-readable build identity.
func String() string {
	return fmt.Sprintf("%s (%s, built %s)", Version, Commit, Date)
}
