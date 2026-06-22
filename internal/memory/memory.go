// Package memory provides a per-user store for persistent facts and context
// snippets. Each entry is one markdown note inside the user's vault
// (<vaultsBase>/<userID>/memory/<id>.md) so memories are browsable, searchable,
// and linkable like any other knowledge-base note. A small YAML frontmatter block
// preserves the entry id and timestamp; everything below it is the fact itself.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// jsonUnmarshal is a thin alias so the legacy importer reads naturally.
func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// Entry is one memory record.
type Entry struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Store manages per-user memory notes.
type Store struct {
	baseDir string // vaults base; notes live at baseDir/<userID>/memory/<id>.md
}

// New creates a Store rooted at the vaults base directory.
func New(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// Append adds a new memory note for the user.
func (s *Store) Append(userID, content string) (*Entry, error) {
	if content == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}
	e := &Entry{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	dir := s.memDir(userID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(s.notePath(userID, e.ID), []byte(renderNote(e)), 0o640); err != nil {
		return nil, fmt.Errorf("write memory note: %w", err)
	}
	return e, nil
}

// List returns all memory entries for a user, oldest first.
func (s *Store) List(userID string) ([]*Entry, error) {
	dir := s.memDir(userID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read memory dir: %w", err)
	}
	var entries []*Entry
	for _, f := range files {
		if f.IsDir() || !strings.EqualFold(filepath.Ext(f.Name()), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		e := parseNote(strings.TrimSuffix(f.Name(), ".md"), data)
		if e != nil {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.Before(entries[j].CreatedAt)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

// Delete removes the note with the given ID.
func (s *Store) Delete(userID, entryID string) error {
	err := os.Remove(s.notePath(userID, entryID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ImportJSONL converts a legacy memory.jsonl file (one {id,content,created_at}
// object per line) into per-entry markdown notes under the user's vault. Existing
// notes are never overwritten, so it is safe to re-run. Returns the number of
// entries imported.
func (s *Store) ImportJSONL(userID, jsonlPath string) (int, error) {
	data, err := os.ReadFile(jsonlPath)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	dir := s.memDir(userID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, err
	}
	imported := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Entry
		if err := jsonUnmarshal(line, &e); err != nil || e.Content == "" {
			continue
		}
		if e.ID == "" {
			e.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now().UTC()
		}
		path := s.notePath(userID, e.ID)
		if _, err := os.Stat(path); err == nil {
			continue // do not clobber an already-migrated note
		}
		if err := writeFileAtomic(path, []byte(renderNote(&e)), 0o640); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// ContextString returns all memory entries as a formatted string for LLM injection.
func (s *Store) ContextString(userID string) (string, error) {
	entries, err := s.List(userID)
	if err != nil || len(entries) == 0 {
		return "", err
	}
	var out string
	for _, e := range entries {
		out += "- " + e.Content + "\n"
	}
	return out, nil
}

func (s *Store) memDir(userID string) string {
	return filepath.Join(s.baseDir, userID, "memory")
}

func (s *Store) notePath(userID, id string) string {
	return filepath.Join(s.memDir(userID), id+".md")
}

// renderNote serialises an entry to markdown with a minimal frontmatter header.
func renderNote(e *Entry) string {
	return fmt.Sprintf("---\nid: %s\ncreated_at: %s\n---\n\n%s\n",
		e.ID, e.CreatedAt.UTC().Format(time.RFC3339), strings.TrimRight(e.Content, "\n"))
}

// parseNote reconstructs an entry from a note's bytes. fallbackID is the filename
// stem, used when the frontmatter is missing or malformed. created_at falls back
// to the file's implicit ordering (zero time) if unparmeable.
func parseNote(fallbackID string, data []byte) *Entry {
	e := &Entry{ID: fallbackID}
	text := string(data)
	body := text
	if strings.HasPrefix(text, "---") {
		rest := text[3:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			front := rest[:end]
			body = rest[end+4:]
			for _, line := range strings.Split(front, "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				k, v = strings.TrimSpace(k), strings.TrimSpace(v)
				switch k {
				case "id":
					if v != "" {
						e.ID = v
					}
				case "created_at":
					if t, err := time.Parse(time.RFC3339, v); err == nil {
						e.CreatedAt = t
					}
				}
			}
		}
	}
	e.Content = strings.TrimSpace(body)
	if e.Content == "" {
		return nil
	}
	return e
}

// writeFileAtomic writes via a temp file + rename so readers never see a partial note.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
