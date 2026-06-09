// Package memory provides a per-user append-only JSONL store for persistent
// facts and context snippets. Each entry is a single JSON line.
package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one memory record.
type Entry struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Store manages per-user memory files.
type Store struct {
	baseDir string // root dir; files are at baseDir/<userID>/memory.jsonl
}

// New creates a Store.
func New(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// Append adds a new entry to the user's memory file.
func (s *Store) Append(userID, content string) (*Entry, error) {
	if content == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}

	e := &Entry{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.ensureDir(userID); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(s.filePath(userID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open memory file: %w", err)
	}
	defer f.Close()

	data, _ := json.Marshal(e)
	_, err = fmt.Fprintf(f, "%s\n", data)
	return e, err
}

// List returns all memory entries for a user, oldest first.
func (s *Store) List(userID string) ([]*Entry, error) {
	f, err := os.Open(s.filePath(userID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open memory file: %w", err)
	}
	defer f.Close()

	var entries []*Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, &e)
	}
	return entries, scanner.Err()
}

// Delete removes the entry with the given ID.
func (s *Store) Delete(userID, entryID string) error {
	entries, err := s.List(userID)
	if err != nil {
		return err
	}

	path := s.filePath(userID)
	f, err := os.OpenFile(path, os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open memory file: %w", err)
	}
	defer f.Close()

	for _, e := range entries {
		if e.ID == entryID {
			continue
		}
		data, _ := json.Marshal(e)
		fmt.Fprintf(f, "%s\n", data)
	}
	return nil
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

func (s *Store) filePath(userID string) string {
	return filepath.Join(s.baseDir, userID, "memory.jsonl")
}

func (s *Store) ensureDir(userID string) error {
	return os.MkdirAll(filepath.Join(s.baseDir, userID), 0o750)
}
