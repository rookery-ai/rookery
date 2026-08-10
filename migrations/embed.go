// Package migrations carries the SQL schema migrations compiled into the binary.
//
// They are embedded rather than read from disk because the deb, rpm and every
// release archive ship the binary alone. An on-disk lookup made all of them fail
// on first use with "read migrations dir: no such file or directory", while only
// the container image worked — and only because its Dockerfile copied this
// directory next to the binary purely to satisfy that lookup.
//
// The pattern matches *.sql rather than *.up.sql on purpose: the down files are
// never executed today, but the narrower pattern would silently drop them the
// moment a down runner is wired.
package migrations

import "embed"

// FS holds every .up.sql and .down.sql file in this directory, flat at the root.
// Read it with fs.ReadDir(FS, ".") and fs.ReadFile(FS, name).
//
// //go:embed fails the BUILD when its pattern matches nothing, so a missing
// migration set can no longer reach a user as a first-run runtime error.
//
//go:embed *.sql
var FS embed.FS
