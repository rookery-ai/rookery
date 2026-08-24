package vault

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// WriteJournal records the prior state of every protected-region path a
// REHEARSAL touches, so the whole lot can be undone when the rehearsal ends.
//
// A rehearsal is an agent BUILD or the create-build DRY RUN. Both deliberately
// run the real thing against live services — that is the only way a review step
// can show what an agent actually does rather than what the model says it will
// — and both therefore make real writes into the user's knowledge base. Those
// writes must not survive: the user did not approve this agent yet, and its
// first real run has to start from a clean slate.
//
// Why a journal and not prevention: an agent that cannot write the knowledge
// base cannot rehearse a knowledge-base-writing agent, which is most of them.
// So the writes happen and are undone, rather than being refused.
//
// Why a journal and not Guard: Guard diffs the WHOLE protected region between
// two points in time, so everything that changed in between is reverted —
// including a legitimate concurrent write from a chat turn or a scheduled run.
// A dry run is a full agent run lasting minutes, and reverting a minutes-wide
// diff would trade a data-integrity bug for a worse one. The journal instead
// records each write as it happens, so only paths the rehearsal itself touched
// are ever restored. AroundExec is the escape hatch for the one case with no
// per-write signal (see its own doc comment).
//
// The protected region is Guard's: the user's authored knowledge, excluding the
// system-owned directories (agents/, chats/, .kb/) that other parts of the app
// rewrite continuously. An agent's own directory is therefore NOT journaled —
// it is the rehearsal's legitimate workspace, and the build's output lives
// there. state.md is restored separately by the designer, which has to keep the
// BUILD's version rather than the pre-build one.
//
// Nil-safe throughout, exactly like Guard: every method tolerates a nil
// receiver, so a caller with no journal needs no branch of its own. That is
// what keeps this invisible to chat, real runs and the design conversation —
// they pass nil and behave exactly as before.
type WriteJournal struct {
	v           *Vault
	workspaceID string

	mu    sync.Mutex
	order []string              // vault-relative paths, insertion order
	prior map[string]priorState // vault-relative path → state before the rehearsal
}

// priorState is what a path looked like BEFORE the rehearsal first touched it.
// existed=false means the rehearsal created it, so reverting means deleting it.
type priorState struct {
	existed bool
	content []byte
	mode    os.FileMode
	// newDirs are ancestor directories that did not exist when this path was
	// recorded — i.e. ones the write itself was about to create. Revert removes
	// these and only these, so a directory the user already had is never
	// deleted even if the revert leaves it empty.
	newDirs []string
}

// NewWriteJournal returns a journal for this vault and workspace.
func (v *Vault) NewWriteJournal(workspaceID string) *WriteJournal {
	return &WriteJournal{
		v:           v,
		workspaceID: workspaceID,
		prior:       map[string]priorState{},
	}
}

// Record captures abs's current state before the caller overwrites it.
//
// It is a no-op for a nil journal, for a path outside the vault, and for a path
// outside the protected region — an agent writing into its own directory is
// doing its job, and reverting that would delete the build's output.
//
// FIRST WRITE WINS. A path already in the journal is never re-recorded, so what
// Revert restores is the state from before the rehearsal began rather than the
// state from before the most recent of several writes. Getting this backwards
// would leave the user's note holding the rehearsal's second-to-last draft.
func (j *WriteJournal) Record(abs string) error {
	if j == nil {
		return nil
	}
	rel, err := j.v.Rel(j.workspaceID, abs)
	if err != nil || rel == "." || !isProtected(rel) {
		return nil
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if _, seen := j.prior[rel]; seen {
		return nil
	}
	j.prior[rel] = capturePrior(abs)
	j.order = append(j.order, rel)
	return nil
}

// capturePrior reads a path's current state. A path that does not exist yields
// existed=false, which is the signal to DELETE it on revert. A read error is
// treated the same way as absence on purpose: the alternative is recording a
// path we cannot restore, which would make Revert silently write empty bytes
// over a file it failed to read.
func capturePrior(abs string) priorState {
	st := priorState{newDirs: missingAncestors(abs)}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return st
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return st
	}
	st.existed = true
	st.content = data
	st.mode = info.Mode().Perm()
	return st
}

// missingAncestors lists the ancestor directories of abs that do not exist yet,
// deepest first — the ones a write to abs is about to create. It stops at the
// first existing ancestor, so it never proposes removing anything that was
// already there.
func missingAncestors(abs string) []string {
	var out []string
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		out = append(out, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return out
}

// AroundExec brackets an OPAQUE call — one whose individual writes this process
// cannot observe — and folds whatever it changed in the protected region into
// the journal.
//
// Two callers need it, for the same reason: the write happens in a subprocess.
// The API engine wraps each run_script/bash call, which keeps the attribution
// window down to a single call — seconds. A CLI coder has no host tools at all,
// so its whole Generate call is wrapped and the window is the whole build.
//
// The cost of the wide window is real and is why the narrow one exists: every
// protected-region change inside the bracket is attributed to the rehearsal, so
// a legitimate concurrent write landing inside it would be reverted too. Prefer
// the narrowest bracket a caller can offer.
//
// fn's error is returned untouched. A snapshot failure does NOT fail the call:
// a rehearsal that cannot be journaled is still a rehearsal the caller asked
// for, and failing it would turn a cleanup problem into a broken build.
func (j *WriteJournal) AroundExec(fn func() error) error {
	if j == nil {
		return fn()
	}
	before, _ := j.v.NewGuard().Snapshot(j.workspaceID)
	err := fn()
	if before != nil {
		j.foldSnapshotDiff(before)
	}
	return err
}

// foldSnapshotDiff compares the protected region against a snapshot taken
// before an opaque call and journals every path that differs.
//
// Both directions matter: a file present in the snapshot and now missing was
// DELETED by the call and must be recreated, while a file absent from the
// snapshot and now present was CREATED and must be removed. A naive walk of
// what exists now would miss the first case entirely.
func (j *WriteJournal) foldSnapshotDiff(before *Snapshot) {
	root := j.v.Root(j.workspaceID)

	// Paths that existed before: recreate if deleted, restore if modified.
	for rel, was := range before.files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err == nil && sha256.Sum256(data) == was.hash {
			continue // untouched
		}
		j.recordKnownPrior(rel, priorState{
			existed: true,
			content: was.content,
			mode:    0o640,
		})
	}

	// Paths that exist now but did not before: created by the call.
	_ = walkProtected(j.v, j.workspaceID, func(rel, abs string) {
		if _, had := before.files[rel]; had {
			return
		}
		j.recordKnownPrior(rel, priorState{newDirs: nil})
	})
}

// recordKnownPrior journals a prior state discovered by diffing rather than by
// observing the write. Same first-write-wins rule as Record.
func (j *WriteJournal) recordKnownPrior(rel string, st priorState) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, seen := j.prior[rel]; seen {
		return
	}
	j.prior[rel] = st
	j.order = append(j.order, rel)
}

// walkProtected visits every file in the protected region, mirroring
// Guard.Snapshot's traversal (dotfile-bearing system dirs pruned wholesale).
func walkProtected(v *Vault, workspaceID string, fn func(rel, abs string)) error {
	root := v.Root(workspaceID)
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := v.Rel(workspaceID, path)
		if relErr != nil || rel == "." {
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
		fn(rel, path)
		return nil
	})
}

// Revert undoes every recorded change and returns the vault-relative paths it
// restored, sorted for a stable log line.
//
// Newest first: a rehearsal that created a directory and then wrote three files
// into it must have the files removed before the directory is pruned.
//
// A per-path failure does not abort the rest. The caller is a build that is
// already on disk and past its guardrails, so a revert that manages nine of ten
// paths is strictly better than one that stops at the first error — and the
// returned list plus the error let the caller say which is which.
func (j *WriteJournal) Revert() ([]string, error) {
	if j == nil {
		return nil, nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	root := j.v.Root(j.workspaceID)
	var reverted []string
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for i := len(j.order) - 1; i >= 0; i-- {
		rel := j.order[i]
		st := j.prior[rel]
		abs := filepath.Join(root, filepath.FromSlash(rel))

		if !st.existed {
			// The rehearsal created it. Remove it, then prune only the
			// directories the write itself created.
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				note(err)
				continue
			}
			pruneNewDirs(st.newDirs)
			reverted = append(reverted, rel)
			continue
		}

		// The rehearsal modified or deleted it. Put the original bytes back —
		// but only if they are not already there, so a rehearsal that wrote a
		// file and then restored it itself is not counted as a violation.
		if cur, err := os.ReadFile(abs); err == nil && bytes.Equal(cur, st.content) {
			continue
		}
		mode := st.mode
		if mode == 0 {
			mode = 0o640
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			note(err)
			continue
		}
		if err := os.WriteFile(abs, st.content, mode); err != nil {
			note(err)
			continue
		}
		reverted = append(reverted, rel)
	}

	j.order = nil
	j.prior = map[string]priorState{}
	sort.Strings(reverted)
	return reverted, firstErr
}

// pruneNewDirs removes directories the rehearsal created, deepest first, and
// only while they are empty. os.Remove refuses a non-empty directory, which is
// exactly the guard we want — a sibling file the rehearsal legitimately left
// behind, or one the user created meanwhile, stops the prune.
func pruneNewDirs(dirs []string) {
	for _, d := range dirs {
		if err := os.Remove(d); err != nil {
			return
		}
	}
}
