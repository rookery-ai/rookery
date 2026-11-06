package rename

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/skilldeps"
	"github.com/rookery-ai/rookery/internal/skilllibrary"
	"github.com/rookery-ai/rookery/internal/skillstore"
	"github.com/rookery-ai/rookery/internal/vault"
)

const (
	// maxEditableBytes keeps a stray large file from being read into memory.
	maxEditableBytes = 5 << 20
	// maxStillMentions bounds the report; it points the owner somewhere, it is not an
	// index.
	maxStillMentions = 20
)

// Service renames agents and skills.
type Service struct {
	db       *db.DB
	vault    *vault.Vault
	homesDir string
}

// New builds a Service. homesDir holds per-workspace homes, where skill environments
// live; "" means environments are not touched.
func New(database *db.DB, v *vault.Vault, homesDir string) *Service {
	return &Service{db: database, vault: v, homesDir: homesDir}
}

// Result reports a finished rename.
type Result struct {
	Name string
	// StillMentions lists vault files whose prose still contains the old name. They
	// were left alone on purpose; this says where to look.
	StillMentions []string
	// Warnings are file edits that failed after the database committed. The rename has
	// happened; these say what is still stale.
	Warnings []string
	// EnvRemoved reports that the skill's Python environment was deleted, so the next
	// run reinstalls it.
	EnvRemoved bool
}

// Plan is a prepared rename. Nothing has changed yet: the caller inspects who refers to
// the name, refuses if one of them is running, and only then calls Apply.
type Plan struct {
	NewName string
	// Unchanged means the name is already the requested one; Apply does nothing.
	Unchanged bool
	// ReferencingAgentIDs lists the agents a rename would affect: the agent itself, the
	// agents that call it, or the agents using the skill.
	ReferencingAgentIDs []string

	apply func() (*Result, error)
}

// Apply performs the rename.
func (p *Plan) Apply() (*Result, error) {
	if p.Unchanged {
		return &Result{Name: p.NewName, StillMentions: []string{}, Warnings: []string{}}, nil
	}
	return p.apply()
}

// fileEdit is one file read at plan time.
type fileEdit struct {
	orig, text string
	mode       fs.FileMode
}

// editSet collects edits so several rules can apply to one file before anything is
// written, and a file no rule changed is never touched.
type editSet map[string]*fileEdit

// load returns a file's pending text. ok is false for anything that must not be edited:
// a missing file, a symlink, a large file, or one that is not text.
func (e editSet) load(path string) (string, bool) {
	if f, ok := e[path]; ok {
		return f.text, true
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxEditableBytes {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		return "", false
	}
	e[path] = &fileEdit{orig: string(data), text: string(data), mode: info.Mode().Perm()}
	return string(data), true
}

// edit applies fn to a file and reports whether it changed the text.
func (e editSet) edit(path string, fn func(string) string) bool {
	text, ok := e.load(path)
	if !ok {
		return false
	}
	next := fn(text)
	if next == text {
		return false
	}
	e[path].text = next
	return true
}

// write commits every changed file, atomically per file and with its mode intact (a
// skill's script may be executable). remap redirects paths inside a moved folder.
func (e editSet) write(remap func(string) string) []string {
	warnings := []string{}
	paths := make([]string, 0, len(e))
	for path := range e {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		f := e[path]
		if f.text == f.orig {
			continue
		}
		target := remap(path)
		if err := writeAtomic(target, f.text, f.mode); err != nil {
			slog.Warn("rename: could not update a file", "path", target, "err", err)
			warnings = append(warnings, fmt.Sprintf("could not update %s: %v", filepath.Base(target), err))
		}
	}
	return warnings
}

func writeAtomic(path, text string, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-rename-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func samePath(p string) string { return p }

// walkFiles visits every regular file under dir, skipping dot folders and bytecode.
func walkFiles(dir string, visit func(path string)) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			visit(path)
		}
		return nil
	})
}

// scanNotes visits the vault's markdown that can hold a link: everything except chats
// (regenerated from the database) and run logs (machine transcripts), the same sources
// the backlink graph ignores. walkFiles already skips the internal .kb folder.
func (s *Service) scanNotes(workspaceID string, visit func(rel, path string)) {
	root := s.vault.Root(workspaceID)
	walkFiles(root, func(path string) {
		rel, err := filepath.Rel(root, path)
		if err != nil || !strings.EqualFold(filepath.Ext(path), ".md") {
			return
		}
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		if parts[0] == "chats" || (parts[0] == "agents" && len(parts) > 2 && parts[2] == "logs") {
			return
		}
		visit(rel, path)
	})
}

// nameResolvesToNote reports whether a bare [[name]] points at a real note, in which
// case that link is not about the agent or skill sharing its name. When the index cannot
// be built the answer is true, because leaving a link alone is the recoverable mistake.
func (s *Service) nameResolvesToNote(workspaceID, name string) bool {
	idx, err := s.vault.BuildLinkIndex(workspaceID)
	if err != nil {
		slog.Warn("rename: could not build the link index", "workspace_id", workspaceID, "err", err)
		return true
	}
	return idx.Resolve(name) != ""
}

// draftEdit rewrites the workspace's agent draft, or returns nil when there is none or
// nothing in it changes.
func (s *Service) draftEdit(workspaceID string, rewriteMD, rewriteTools func(*db.AgentDraft, string) string) (*db.DraftEdit, error) {
	dr, err := s.db.GetAgentDraft(workspaceID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent draft: %w", err)
	}
	md, tools := rewriteMD(dr, dr.PendingAgentMD), rewriteTools(dr, dr.PendingToolsJSON)
	if md == dr.PendingAgentMD && tools == dr.PendingToolsJSON {
		return nil, nil
	}
	return &db.DraftEdit{AgentMD: md, ToolsJSON: tools}, nil
}

// PrepareAgent plans renaming an agent. It returns ErrInvalidName or db.ErrNameTaken.
func (s *Service) PrepareAgent(workspaceID string, a *db.Agent, rawName string) (*Plan, error) {
	newName, err := ValidateAgentName(rawName)
	if err != nil {
		return nil, err
	}
	oldName := a.Name
	if newName == oldName {
		return &Plan{NewName: newName, Unchanged: true}, nil
	}
	agents, err := s.db.ListAgents(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	// Case-insensitive because a name typed into a chat app is matched that way, and two
	// agents differing only in case could not be told apart there. The agent itself is
	// excluded so that fixing a name's capitalisation works.
	for _, other := range agents {
		if other.ID != a.ID && strings.EqualFold(other.Name, newName) {
			return nil, db.ErrNameTaken
		}
	}

	edits := editSet{}
	referencing := []string{a.ID}
	for _, other := range agents {
		path := filepath.Join(s.vault.AgentDir(workspaceID, other.ID), "AGENT.md")
		if edits.edit(path, func(t string) string { return RewriteAgentCalls(t, oldName, newName) }) && other.ID != a.ID {
			referencing = append(referencing, other.ID)
		}
	}
	own := s.vault.AgentDir(workspaceID, a.ID)
	edits.edit(filepath.Join(own, "AGENT.md"), func(t string) string { return RewriteAgentTitle(t, oldName, newName) })
	edits.edit(filepath.Join(own, "state.md"), func(t string) string { return RewriteStateHeading(t, oldName, newName) })
	logs, _ := filepath.Glob(filepath.Join(own, "logs", "run_*.md"))
	for _, path := range logs {
		edits.edit(path, func(t string) string { return RewriteRunHeading(t, oldName, newName) })
	}

	draft, err := s.draftEdit(workspaceID,
		func(dr *db.AgentDraft, md string) string {
			md = RewriteAgentCalls(md, oldName, newName)
			if dr.AgentID == a.ID {
				md = RewriteAgentTitle(md, oldName, newName)
			}
			return md
		},
		func(_ *db.AgentDraft, tools string) string { return tools })
	if err != nil {
		return nil, err
	}

	resolves := s.nameResolvesToNote(workspaceID, oldName)
	mentions := []string{}
	s.scanNotes(workspaceID, func(rel, path string) {
		if !resolves {
			edits.edit(path, func(t string) string { return RewriteWikilinks(t, oldName, newName) })
		}
		if text, ok := edits.load(path); ok && ContainsToken(text, oldName) && len(mentions) < maxStillMentions {
			mentions = append(mentions, rel)
		}
	})

	return &Plan{
		NewName:             newName,
		ReferencingAgentIDs: referencing,
		apply: func() (*Result, error) {
			// The database decides what works, and a failed commit must leave nothing
			// changed, so it goes first. Every file edit after it is safe to repeat.
			if err := s.db.RenameAgent(a.ID, newName, draft); err != nil {
				return nil, err
			}
			warnings := edits.write(samePath)
			if err := s.vault.Reflector().RenameAgentInRuns(workspaceID, a.ID, newName); err != nil {
				slog.Warn("rename: could not update run sidecars", "agent_id", a.ID, "err", err)
				warnings = append(warnings, fmt.Sprintf("could not update run history: %v", err))
			}
			s.vault.Indexer().Invalidate(workspaceID)
			return &Result{Name: newName, StillMentions: mentions, Warnings: warnings}, nil
		},
	}, nil
}

// PrepareSkill plans renaming a user skill. It returns ErrInvalidName or db.ErrNameTaken.
func (s *Service) PrepareSkill(workspaceID string, sk *db.Skill, rawName string) (*Plan, error) {
	newName := skillstore.SanitizeName(rawName)
	if !skillstore.ValidName(newName) {
		return nil, fmt.Errorf("%w: use lowercase letters, digits and hyphens, 3-64 characters", ErrInvalidName)
	}
	oldName := sk.Name
	if newName == oldName {
		return &Plan{NewName: newName, Unchanged: true}, nil
	}
	if skilllibrary.IsCoreSkill(newName) {
		return nil, db.ErrNameTaken
	}
	if existing, err := s.db.GetSkillByName(workspaceID, newName); err == nil && existing != nil {
		return nil, db.ErrNameTaken
	}

	skillsDir := s.vault.SkillsDir(workspaceID)
	oldDir, newDir := filepath.Join(skillsDir, oldName), filepath.Join(skillsDir, newName)
	oldExists, newExists := dirExists(oldDir), dirExists(newDir)
	// With the old folder gone and the new one there, a previous rename was interrupted
	// between the move and the commit: this one finishes it.
	if oldExists && newExists {
		return nil, db.ErrNameTaken
	}
	currentDir := oldDir
	if !oldExists {
		currentDir = newDir
	}

	edits := editSet{}
	rewrite := func(t string) string { return RewriteSkillRefs(t, oldName, newName) }
	walkFiles(currentDir, func(path string) { edits.edit(path, rewrite) })

	agents, err := s.db.ListAgents(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	referencing := []string{}
	for _, a := range agents {
		dir := s.vault.AgentDir(workspaceID, a.ID)
		refers := edits.edit(filepath.Join(dir, "AGENT.md"), rewrite)
		walkFiles(filepath.Join(dir, "tools"), func(path string) {
			refers = edits.edit(path, rewrite) || refers
		})
		attached, err := s.db.ListAgentSkillNames(a.ID)
		if err != nil {
			return nil, fmt.Errorf("list agent skills: %w", err)
		}
		if refers || slices.Contains(attached, oldName) {
			referencing = append(referencing, a.ID)
		}
	}

	draft, err := s.draftEdit(workspaceID,
		func(_ *db.AgentDraft, md string) string { return rewrite(md) },
		func(_ *db.AgentDraft, tools string) string { return rewrite(tools) })
	if err != nil {
		return nil, err
	}

	resolves := s.nameResolvesToNote(workspaceID, oldName)
	// A one-word name is probably an ordinary word, so reporting every use of it would
	// send the owner looking at files with nothing wrong in them.
	reportMentions := strings.Contains(oldName, "-")
	mentions := []string{}
	s.scanNotes(workspaceID, func(rel, path string) {
		edits.edit(path, func(t string) string {
			t = RewriteSkillLinks(t, oldName, newName)
			if !resolves {
				t = RewriteWikilinks(t, oldName, newName)
			}
			return t
		})
		if !reportMentions || len(mentions) >= maxStillMentions {
			return
		}
		if text, ok := edits.load(path); ok && ContainsToken(text, oldName) {
			mentions = append(mentions, rel)
		}
	})

	description := rewrite(sk.Description)
	remap := func(path string) string {
		if strings.HasPrefix(path, oldDir+string(filepath.Separator)) {
			return newDir + strings.TrimPrefix(path, oldDir)
		}
		return path
	}

	return &Plan{
		NewName:             newName,
		ReferencingAgentIDs: referencing,
		apply: func() (*Result, error) {
			// The folder moves first because the commit is what makes the new path the
			// one in use: a commit pointing at a folder that failed to move would leave
			// the skill unreadable, while a failed commit can simply move it back.
			if oldExists {
				if err := os.Rename(oldDir, newDir); err != nil {
					return nil, fmt.Errorf("move skill folder: %w", err)
				}
			}
			if err := s.db.RenameSkill(workspaceID, sk.ID, oldName, newName, description, draft); err != nil {
				if oldExists {
					if mvErr := os.Rename(newDir, oldDir); mvErr != nil {
						slog.Error("rename: could not move the skill folder back", "skill", oldName, "err", mvErr)
					}
				}
				return nil, err
			}
			result := &Result{Name: newName, StillMentions: mentions, Warnings: edits.write(remap)}
			// Orphaned: the caller refuses a rename while a draft holds this name.
			if err := os.RemoveAll(filepath.Join(skillsDir, ".staging-"+oldName)); err != nil {
				slog.Warn("rename: could not remove a staging folder", "skill", oldName, "err", err)
			}
			result.EnvRemoved = s.removeEnv(workspaceID, oldName, result)
			s.vault.Indexer().Invalidate(workspaceID)
			return result, nil
		},
	}, nil
}

// removeEnv deletes the skill's Python environment. A virtual environment holds absolute
// paths, so a moved one is subtly broken; a missing one is reported to the next run,
// which reinstalls it.
func (s *Service) removeEnv(workspaceID, name string, result *Result) bool {
	if s.homesDir == "" {
		return false
	}
	dir, err := skilldeps.EnvDir(filepath.Join(s.homesDir, workspaceID), name)
	if err != nil || !dirExists(dir) {
		return false
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("rename: could not remove a skill environment", "skill", name, "err", err)
		result.Warnings = append(result.Warnings, fmt.Sprintf("could not remove the old Python environment: %v", err))
		return false
	}
	return true
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
