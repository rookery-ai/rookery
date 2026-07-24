// Package vault implements a per-user Obsidian-style knowledge base: a single
// directory of interlinked markdown notes that holds (almost) everything a user
// owns — notes, journals, plans, memory, agent definitions, run logs, chat
// transcripts and reflected database rows — browsable from the web UI, searchable,
// and readable by the user's agents.
//
// The vault is the system's knowledge + backup layer. The SQLite database remains
// the live system-of-record; structured rows are *reflected* into the vault (see
// reflect.go). Secrets never enter the vault — only their names.
//
// Layout (per user, under <dataDir>/vaults/<safeID(workspaceID)>):
//
//	README.md                      vault home/index note
//	notes/                         user-authored notes, journals, plans, todos
//	memory/USER.md                 user profile (name, location, background)
//	memory/SOUL.md                 communication style and preferences
//	memory/<name>.md               any additional context files the user creates
//	skills/<name>/SKILL.md         migrated skills
//	agents/<agentID>/              an agent's own writable area
//	chats/<id>.md                  reflected chat transcripts
//	.kb/                           internal index/sidecar data (hidden from "knowledge")
//
// Secret VALUES never enter the vault — they stay encrypted in the database and
// are injected into agents as environment variables at run time.
//
// claude-homes/<workspaceID>/ deliberately stays OUTSIDE the vault — it holds .claude
// credentials that must never be backed up.
package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// InternalDir is the hidden directory holding indexes and JSON sidecars. It is
// excluded from search, the web file tree, and the agent write-guard's view of
// "knowledge".
const InternalDir = ".kb"

// ErrEscapes is returned by Resolve when a relative path would escape the vault.
var ErrEscapes = errors.New("path escapes vault")

// protectedTopDirs are the system-managed, DB-backed top-level vault
// directories whose contents mirror database rows — agents, reflected chat
// transcripts, inbox notifications, skills, and reflected reminders — plus the
// hidden internal dir. Deleting or renaming any of these (or anything inside
// them) from the KB browser would orphan the backing record and leave the
// agent/chat/skill/inbox item broken, so those user-initiated mutations are
// refused at the KB API layer and hidden in the file tree — the item must be
// deleted from its own page instead. This is the single source of truth for
// *user-initiated KB-browser* mutation protection; it deliberately does NOT
// gate the vault primitives (Delete/Rename), which legitimate deletion from an
// item's own page also calls.
var protectedTopDirs = map[string]bool{
	InternalDir: true, // .kb
	"agents":    true,
	"chats":     true,
	"inbox":     true,
	"skills":    true,
	"reminders": true,
}

// IsUserMutationProtected reports whether rel lives inside a system-managed,
// DB-backed directory that the user must not delete or rename through the KB
// browser. rel is a vault-relative slash path (the file-tree form). The path is
// cleaned first, so a traversal like "notes/../chats/x.md" is judged by its
// real top segment ("chats") and cannot slip past the check.
func IsUserMutationProtected(rel string) bool {
	clean := path.Clean("/" + strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return false
	}
	top, _, _ := strings.Cut(clean, "/")
	return protectedTopDirs[top]
}

// Vault provides safe, per-user access to knowledge-base files on disk.
type Vault struct {
	dataDir string // the application data dir; vaults live at <dataDir>/vaults

	// indexer is the process-lifetime retrieval index, created on first use.
	indexOnce sync.Once
	indexer   *Indexer
}

// New creates a Vault rooted at dataDir. Vault directories are created lazily.
func New(dataDir string) *Vault {
	return &Vault{dataDir: dataDir}
}

// VaultsDir returns the parent directory that holds every user's vault.
func (v *Vault) VaultsDir() string {
	return filepath.Join(v.dataDir, "vaults")
}

// Root returns the absolute path to a user's vault root. userIDs are
// server-generated UUIDs and are used as the directory segment directly, matching
// how internal/agentdesigner, internal/memory and internal/skillstore key their
// per-user paths (whose dirs live inside this root).
func (v *Vault) Root(workspaceID string) string {
	return filepath.Join(v.VaultsDir(), workspaceID)
}

// AgentsDir returns the directory holding a user's agent workspaces. This is the
// base the agentdesigner manifest path helpers join against, replacing the old
// flat <dataDir>/agents.
func (v *Vault) AgentsDir(workspaceID string) string {
	return filepath.Join(v.Root(workspaceID), "agents")
}

// AgentDir returns a single agent's own writable directory.
func (v *Vault) AgentDir(workspaceID, agentID string) string {
	return filepath.Join(v.AgentsDir(workspaceID), agentID)
}

// MemoryDir returns the directory holding a user's memory notes.
func (v *Vault) MemoryDir(workspaceID string) string {
	return filepath.Join(v.Root(workspaceID), "memory")
}

// SkillsDir returns the directory holding a user's skills.
func (v *Vault) SkillsDir(workspaceID string) string {
	return filepath.Join(v.Root(workspaceID), "skills")
}

// internalSub returns an absolute path inside the user's hidden .kb directory.
func (v *Vault) internalSub(workspaceID string, parts ...string) string {
	return filepath.Join(append([]string{v.Root(workspaceID), InternalDir}, parts...)...)
}

// Resolve is the security primitive every read/write/delete path must use. It
// cleans relPath, rejects absolute paths and any traversal that would escape the
// user's vault, and returns the absolute on-disk path. relPath is always
// interpreted relative to the vault root, never the process CWD.
func (v *Vault) Resolve(workspaceID, relPath string) (string, error) {
	root := v.Root(workspaceID)
	// Treat the input as vault-relative. Strip a leading slash so callers can pass
	// "/notes/x.md" or "notes/x.md" interchangeably, then clean as a relative path.
	clean := filepath.Clean(strings.TrimPrefix(filepath.ToSlash(relPath), "/"))
	// Reject anything that resolves to (or escapes via) the parent, or that is
	// absolute. We reject rather than silently clamp so callers see a hard error.
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: %q", ErrEscapes, relPath)
	}
	abs := filepath.Join(root, clean)
	// Defense in depth: confirm the result is still within root.
	rootWithSep := root + string(os.PathSeparator)
	if abs != root && !strings.HasPrefix(abs, rootWithSep) {
		return "", fmt.Errorf("%w: %q", ErrEscapes, relPath)
	}
	return abs, nil
}

// Rel converts an absolute path inside the user's vault back to a vault-relative
// slash path (the form used in URLs and the file tree). It errors if abs is not
// within the vault.
func (v *Vault) Rel(workspaceID, abs string) (string, error) {
	root := v.Root(workspaceID)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q", ErrEscapes, abs)
	}
	return filepath.ToSlash(rel), nil
}

// EnsureScaffold creates a user's vault with its standard top-level structure and
// a README home note if the vault does not yet exist. Idempotent.
func (v *Vault) EnsureScaffold(workspaceID string) error {
	root := v.Root(workspaceID)
	for _, sub := range []string{"notes", "memory", "skills", "agents", "chats", InternalDir} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o750); err != nil {
			return fmt.Errorf("scaffold %s: %w", sub, err)
		}
	}
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); errors.Is(err, os.ErrNotExist) {
		// Written only when absent, and deliberately never refreshed: this file
		// is the user's to edit, and rewriting it on a later boot would discard
		// whatever they turned their home note into.
		if err := writeFileAtomic(readme, []byte(readmeTemplate), 0o640); err != nil {
			return err
		}
	}

	// Scaffold structured memory files if they don't exist yet.
	// These are the suggested starting points; users may create additional
	// .md files in memory/ and they will all be auto-injected into LLM context.
	userMD := filepath.Join(root, "memory", "USER.md")
	if _, err := os.Stat(userMD); errors.Is(err, os.ErrNotExist) {
		content := "# About Me\n\n<!-- Add your name, location, role, and background here -->\n"
		if err := writeFileAtomic(userMD, []byte(content), 0o640); err != nil {
			return err
		}
	}
	soulMD := filepath.Join(root, "memory", "SOUL.md")
	if _, err := os.Stat(soulMD); errors.Is(err, os.ErrNotExist) {
		content := "# Communication Style\n\n<!-- Add your preferred tone, language, and response style here -->\n"
		if err := writeFileAtomic(soulMD, []byte(content), 0o640); err != nil {
			return err
		}
	}
	return nil
}

// ReadNote returns the raw bytes of a vault-relative file.
func (v *Vault) ReadNote(workspaceID, relPath string) ([]byte, error) {
	abs, err := v.Resolve(workspaceID, relPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// WriteNote writes data to a vault-relative file, creating parent directories as
// needed. The write is atomic (temp file + rename), matching the discipline used
// for agent state.json elsewhere in the codebase.
func (v *Vault) WriteNote(workspaceID, relPath string, data []byte) error {
	abs, err := v.Resolve(workspaceID, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return err
	}
	return writeFileAtomic(abs, data, 0o640)
}

// Delete removes a vault-relative file or empty directory. It refuses to delete
// the vault root or the internal .kb directory.
func (v *Vault) Delete(workspaceID, relPath string) error {
	abs, err := v.Resolve(workspaceID, relPath)
	if err != nil {
		return err
	}
	if abs == v.Root(workspaceID) || abs == v.internalSub(workspaceID) {
		return errors.New("refusing to delete protected path")
	}
	return os.RemoveAll(abs)
}

// Rename moves a vault-relative file or directory to a new vault-relative path.
func (v *Vault) Rename(workspaceID, fromRel, toRel string) error {
	from, err := v.Resolve(workspaceID, fromRel)
	if err != nil {
		return err
	}
	to, err := v.Resolve(workspaceID, toRel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// topSegment returns the first slash-separated segment of a vault-relative path.
func topSegment(rel string) string {
	rel = filepath.ToSlash(rel)
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// Node is one entry in the vault file tree.
type Node struct {
	Name        string // base name (raw filename / UUID)
	Path        string // vault-relative slash path
	IsDir       bool
	Size        int64
	DisplayName string // human-readable label; empty means use Name
}

// List returns the immediate children of a vault-relative directory, directories
// first then files, both alphabetical. The hidden .kb directory is omitted at the
// top level. Passing "" or "." lists the vault root.
func (v *Vault) List(workspaceID, relDir string) ([]Node, error) {
	abs, err := v.Resolve(workspaceID, relDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var nodes []Node
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // hide internal data (.kb) and folder-keep files (.keep)
		}
		rel, err := v.Rel(workspaceID, filepath.Join(abs, name))
		if err != nil {
			continue
		}
		n := Node{Name: name, Path: rel, IsDir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			n.Size = info.Size()
		}
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes, nil
}

// ListFolders returns every folder's vault-relative path (root "" first),
// depth-first with parents before children, skipping the hidden internal .kb
// dir and dotfiles. Used by the KB "Location" / bulk-Move pickers, which need a
// flat folder list rather than one level at a time.
func (v *Vault) ListFolders(workspaceID string) ([]string, error) {
	root := v.Root(workspaceID)
	out := []string{""}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate unreadable entries
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if p == root {
			return nil // root already seeded as ""
		}
		if strings.HasPrefix(name, ".") {
			if name == InternalDir || name == "." {
				return filepath.SkipDir
			}
			return filepath.SkipDir // skip any dotdir (matches List hiding dotfiles)
		}
		rel, relErr := v.Rel(workspaceID, p)
		if relErr != nil {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// imageFileExts are the extensions ListImageFiles surfaces as embeddable image
// assets (the editor's "insert from knowledge base" picker).
var imageFileExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".ico": true, ".avif": true,
}

// ListImageFiles returns the vault-relative paths of every image file in the
// vault, sorted, skipping the hidden internal .kb dir and dotfiles. Used by the
// editor's image picker to offer already-stored images.
func (v *Vault) ListImageFiles(workspaceID string) ([]string, error) {
	root := v.Root(workspaceID)
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == InternalDir {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if !imageFileExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		if rel, relErr := v.Rel(workspaceID, p); relErr == nil {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// writeFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so readers never observe a partial write.
//
// The scratch file is created with os.CreateTemp (a random suffix), not a
// fixed ".tmp" name: two concurrent writers targeting different final paths
// in the same directory would otherwise both write to the identical scratch
// path and race — one writer's rename would find its temp file already gone
// (or truncated by the other), failing with "rename ... no such file or
// directory". That was a real, pre-existing bug in every vault write; it
// simply had no concurrent caller before vault imports gained one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Whatever the outcome, don't leave a stray scratch file behind: on
	// success the rename has already moved it to path, so Remove here is a
	// harmless no-op (ENOENT, ignored); on any failure path it cleans up.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// os.CreateTemp always creates with mode 0600 regardless of the caller's
	// intended permission; fix it up before the rename makes it visible under
	// its final name.
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
