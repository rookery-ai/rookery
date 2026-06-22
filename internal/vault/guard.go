package vault

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Guard enforces the rule "an agent may read the whole vault but write only to
// its own directory" using a detective post-run check. Because every subprocess
// runs as the same OS user (env-based isolation, no firejail), nothing physically
// prevents an agent from writing elsewhere; the Guard instead snapshots the user's
// authored content before a run and reverts any out-of-scope create/modify/delete
// afterward, reporting what it had to undo.
//
// The protected region is the user's *authored* knowledge — notes, journals,
// plans, memory, skills, README — i.e. everything EXCEPT directories that are
// system-owned and continuously rewritten by other parts of the app:
//   - agents/   : each agent's own workspace (concurrent agent runs write here)
//   - sessions/ : chat transcripts reflected by the session poller mid-run
//   - reminders/: reminders reflected by the reminder poller mid-run
//   - .kb/      : internal indexes and JSON sidecars
//
// Those are excluded for two reasons: agents are *allowed* to write their own
// agent dir, and the background reflection goroutines (which can fire for the same
// user while an agent runs for minutes) must not have their writes mistaken for an
// out-of-scope agent write and reverted. All four are re-derivable from the
// database, so leaving them unguarded is a safe trade.
type Guard struct {
	v *Vault
}

// NewGuard returns a Guard for this vault.
func (v *Vault) NewGuard() *Guard { return &Guard{v: v} }

// Snapshot captures the protected region's files and contents prior to a run.
type Snapshot struct {
	userID string
	files  map[string]fileState // vault-relative path → state
}

type fileState struct {
	hash    [32]byte
	content []byte
}

// systemOwnedDirs are the top-level vault directories excluded from the guard
// because the app itself (agents, reflection goroutines) writes them.
var systemOwnedDirs = []string{InternalDir, "agents", "sessions", "reminders"}

// isProtected reports whether a vault-relative path is in the guarded region —
// the user's authored content. System-owned, DB-derivable directories are excluded
// (see the Guard doc comment).
func isProtected(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range systemOwnedDirs {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return false
		}
	}
	return true
}

// Snapshot walks the protected region and records each file's hash and content so
// changes can be reverted after the run. A nil Guard yields a nil snapshot, and
// all Guard operations tolerate a nil receiver/snapshot so callers need no guards
// of their own.
func (g *Guard) Snapshot(userID string) (*Snapshot, error) {
	if g == nil {
		return nil, nil
	}
	root := g.v.Root(userID)
	snap := &Snapshot{userID: userID, files: map[string]fileState{}}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := g.v.Rel(userID, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if !isProtected(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isProtected(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		snap.files[rel] = fileState{hash: sha256.Sum256(data), content: data}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// Restore reverts any out-of-scope change the agent made to the protected region
// and returns the vault-relative paths it had to undo (empty when the agent
// behaved). Modified files are rewritten to their snapshot content, deleted files
// are recreated, and files the agent newly created in the protected region are
// removed.
func (g *Guard) Restore(snap *Snapshot) ([]string, error) {
	if g == nil || snap == nil {
		return nil, nil
	}
	var violations []string

	// Detect new files that appeared in the protected region and delete them.
	root := g.v.Root(snap.userID)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := g.v.Rel(snap.userID, path)
		if err != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if !isProtected(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isProtected(rel) {
			return nil
		}
		if _, known := snap.files[rel]; !known {
			if err := os.Remove(path); err == nil {
				violations = append(violations, rel+" (created)")
			}
		}
		return nil
	})

	// Revert modifications and recreate deletions.
	for rel, st := range snap.files {
		abs, err := g.v.Resolve(snap.userID, rel)
		if err != nil {
			continue
		}
		cur, err := os.ReadFile(abs)
		if err != nil {
			// File was deleted by the agent — recreate it.
			if err := g.v.WriteNote(snap.userID, rel, st.content); err == nil {
				violations = append(violations, rel+" (deleted)")
			}
			continue
		}
		if sha256.Sum256(cur) != st.hash {
			if err := g.v.WriteNote(snap.userID, rel, st.content); err == nil {
				violations = append(violations, rel+" (modified)")
			}
		}
	}
	return violations, nil
}
