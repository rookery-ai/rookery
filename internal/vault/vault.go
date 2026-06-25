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
// Layout (per user, under <dataDir>/vaults/<safeID(userID)>):
//
//	README.md                      vault home/index note
//	notes/                         user-authored notes, journals, plans, todos
//	memory/USER.md                 user profile (name, location, background)
//	memory/SOUL.md                 communication style and preferences
//	memory/<name>.md               any additional context files the user creates
//	skills/<name>/SKILL.md         migrated skills
//	agents/<agentID>/              an agent's own writable area
//	sessions/<id>.md               reflected chat transcripts
//	.kb/                           internal index/sidecar data (hidden from "knowledge")
//
// Secret VALUES never enter the vault — they stay encrypted in the database and
// are injected into agents as environment variables at run time.
//
// claude-homes/<userID>/ deliberately stays OUTSIDE the vault — it holds .claude
// credentials that must never be backed up.
package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InternalDir is the hidden directory holding indexes and JSON sidecars. It is
// excluded from search, the web file tree, and the agent write-guard's view of
// "knowledge".
const InternalDir = ".kb"

// ErrEscapes is returned by Resolve when a relative path would escape the vault.
var ErrEscapes = errors.New("path escapes vault")

// Vault provides safe, per-user access to knowledge-base files on disk.
type Vault struct {
	dataDir string // the application data dir; vaults live at <dataDir>/vaults
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
func (v *Vault) Root(userID string) string {
	return filepath.Join(v.VaultsDir(), userID)
}

// AgentsDir returns the directory holding a user's agent workspaces. This is the
// base the agentdesigner manifest path helpers join against, replacing the old
// flat <dataDir>/agents.
func (v *Vault) AgentsDir(userID string) string {
	return filepath.Join(v.Root(userID), "agents")
}

// AgentDir returns a single agent's own writable directory.
func (v *Vault) AgentDir(userID, agentID string) string {
	return filepath.Join(v.AgentsDir(userID), agentID)
}

// MemoryDir returns the directory holding a user's memory notes.
func (v *Vault) MemoryDir(userID string) string {
	return filepath.Join(v.Root(userID), "memory")
}

// SkillsDir returns the directory holding a user's skills.
func (v *Vault) SkillsDir(userID string) string {
	return filepath.Join(v.Root(userID), "skills")
}

// internalSub returns an absolute path inside the user's hidden .kb directory.
func (v *Vault) internalSub(userID string, parts ...string) string {
	return filepath.Join(append([]string{v.Root(userID), InternalDir}, parts...)...)
}

// Resolve is the security primitive every read/write/delete path must use. It
// cleans relPath, rejects absolute paths and any traversal that would escape the
// user's vault, and returns the absolute on-disk path. relPath is always
// interpreted relative to the vault root, never the process CWD.
func (v *Vault) Resolve(userID, relPath string) (string, error) {
	root := v.Root(userID)
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
func (v *Vault) Rel(userID, abs string) (string, error) {
	root := v.Root(userID)
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
func (v *Vault) EnsureScaffold(userID string) error {
	root := v.Root(userID)
	for _, sub := range []string{"notes", "memory", "skills", "agents", "sessions", InternalDir} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o750); err != nil {
			return fmt.Errorf("scaffold %s: %w", sub, err)
		}
	}
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); errors.Is(err, os.ErrNotExist) {
		content := "# Knowledge Base\n\n" +
			"This is your personal knowledge base. Everything you and your agents " +
			"create lives here as interlinked markdown notes.\n\n" +
			"- [[notes]] — your notes, journals, plans and todos\n" +
			"- [[memory]] — your profile and context (USER.md, SOUL.md, and more)\n" +
			"- [[agents]] — your agents and their run logs\n" +
			"- [[sessions]] — chat transcripts\n"
		if err := writeFileAtomic(readme, []byte(content), 0o640); err != nil {
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
func (v *Vault) ReadNote(userID, relPath string) ([]byte, error) {
	abs, err := v.Resolve(userID, relPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// WriteNote writes data to a vault-relative file, creating parent directories as
// needed. The write is atomic (temp file + rename), matching the discipline used
// for agent state.json elsewhere in the codebase.
func (v *Vault) WriteNote(userID, relPath string, data []byte) error {
	abs, err := v.Resolve(userID, relPath)
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
func (v *Vault) Delete(userID, relPath string) error {
	abs, err := v.Resolve(userID, relPath)
	if err != nil {
		return err
	}
	if abs == v.Root(userID) || abs == v.internalSub(userID) {
		return errors.New("refusing to delete protected path")
	}
	return os.RemoveAll(abs)
}

// Rename moves a vault-relative file or directory to a new vault-relative path.
func (v *Vault) Rename(userID, fromRel, toRel string) error {
	from, err := v.Resolve(userID, fromRel)
	if err != nil {
		return err
	}
	to, err := v.Resolve(userID, toRel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
		return err
	}
	return os.Rename(from, to)
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
func (v *Vault) List(userID, relDir string) ([]Node, error) {
	abs, err := v.Resolve(userID, relDir)
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
		rel, err := v.Rel(userID, filepath.Join(abs, name))
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

// writeFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so readers never observe a partial write.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
