// Package ui embeds the built single-page app (web/ui/dist). The dist tree is
// produced by `make ui` (npm run build) and is NOT committed — only a .gitkeep
// placeholder keeps the embed pattern valid, so `go build` works without node.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// DistFS returns the built SPA rooted at dist/. ok is false when the UI has
// not been built into this binary (no index.html present).
func DistFS() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
