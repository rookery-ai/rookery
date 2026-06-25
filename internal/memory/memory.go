// Package memory provides a per-user store for structured context files.
// Context lives at <vaultsBase>/<userID>/memory/ as named markdown files
// (USER.md, SOUL.md, GENERAL.md, etc.). Every .md file is browsable and
// editable via the Knowledge Base. ContextString assembles them all into a
// single block for LLM injection. Quick entries added via the Telegram
// /memory command land in GENERAL.md as bullet lines.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// jsonUnmarshal is a thin alias so the legacy importer reads naturally.
func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// Entry is one memory record, as returned by List.
type Entry struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Store manages per-user memory files.
type Store struct {
	baseDir string // vaults base; files live at baseDir/<userID>/memory/
}

// New creates a Store rooted at the vaults base directory.
func New(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// Append adds a bullet entry to memory/GENERAL.md.
// Used by the Telegram /memory add command.
func (s *Store) Append(userID, content string) (*Entry, error) {
	if content == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}
	dir := s.memDir(userID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	generalPath := filepath.Join(dir, "GENERAL.md")
	now := time.Now().UTC()

	existing, _ := os.ReadFile(generalPath)
	var sb strings.Builder
	if len(existing) == 0 {
		sb.WriteString("# General Memory\n\n")
	} else {
		sb.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString("- " + content + " <!--" + now.Format(time.RFC3339) + "-->\n")

	if err := writeFileAtomic(generalPath, []byte(sb.String()), 0o640); err != nil {
		return nil, fmt.Errorf("write GENERAL.md: %w", err)
	}

	bulletN := 0
	for _, l := range strings.Split(sb.String(), "\n") {
		if strings.HasPrefix(l, "- ") {
			bulletN++
		}
	}
	return &Entry{ID: fmt.Sprintf("general:%d", bulletN), Content: content, CreatedAt: now}, nil
}

// List returns bullet entries from memory/GENERAL.md.
// Used by the Telegram /memory list and /memory delete commands.
func (s *Store) List(userID string) ([]*Entry, error) {
	data, err := os.ReadFile(filepath.Join(s.memDir(userID), "GENERAL.md"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read GENERAL.md: %w", err)
	}
	var entries []*Entry
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		n++
		body := strings.TrimPrefix(line, "- ")
		var ts time.Time
		if i := strings.LastIndex(body, "<!--"); i >= 0 {
			if j := strings.Index(body[i:], "-->"); j >= 0 {
				stamp := strings.TrimSpace(body[i+4 : i+j])
				if t, parseErr := time.Parse(time.RFC3339, stamp); parseErr == nil {
					ts = t
				}
				body = strings.TrimSpace(body[:i])
			}
		}
		entries = append(entries, &Entry{
			ID:        fmt.Sprintf("general:%d", n),
			Content:   body,
			CreatedAt: ts,
		})
	}
	return entries, nil
}

// Delete removes a bullet entry from GENERAL.md by its "general:<n>" ID.
func (s *Store) Delete(userID, entryID string) error {
	_, numStr, found := strings.Cut(entryID, ":")
	if !found {
		// Legacy UUID path: try removing the old-format file (used during migration window).
		err := os.Remove(s.notePath(userID, entryID))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	n, err := strconv.Atoi(numStr)
	if err != nil || n < 1 {
		return fmt.Errorf("invalid entry ID: %s", entryID)
	}
	generalPath := filepath.Join(s.memDir(userID), "GENERAL.md")
	data, err := os.ReadFile(generalPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read GENERAL.md: %w", err)
	}
	var out []string
	bulletN := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "- ") {
			bulletN++
			if bulletN == n {
				continue // drop this bullet
			}
		}
		out = append(out, line)
	}
	return writeFileAtomic(generalPath, []byte(strings.Join(out, "\n")), 0o640)
}

// ImportJSONL converts a legacy memory.jsonl file (one {id,content,created_at}
// object per line) into per-entry UUID-named markdown notes under the user's vault.
// Existing notes are never overwritten. Returns the number of entries imported.
// These UUID-named notes are then consolidated by MigrateToStructuredFiles on the
// next startup.
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

// MigrateToStructuredFiles consolidates legacy UUID-keyed memory notes (those
// written by pre-v2 Append or ImportJSONL) into bullet lines in GENERAL.md, then
// deletes the UUID files. Idempotent: a second run finds no legacy files and returns
// immediately.
func (s *Store) MigrateToStructuredFiles(userID string) error {
	dir := s.memDir(userID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read memory dir: %w", err)
	}

	var legacyContent []string
	var legacyPaths []string
	for _, f := range files {
		if f.IsDir() || !strings.EqualFold(filepath.Ext(f.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !isLegacyNote(data) {
			continue
		}
		e := parseNote(strings.TrimSuffix(f.Name(), ".md"), data)
		if e != nil && e.Content != "" {
			legacyContent = append(legacyContent, e.Content)
		}
		legacyPaths = append(legacyPaths, path)
	}

	if len(legacyPaths) == 0 {
		return nil
	}

	generalPath := filepath.Join(dir, "GENERAL.md")
	existing, _ := os.ReadFile(generalPath)
	var sb strings.Builder
	if len(existing) == 0 {
		sb.WriteString("# General Memory\n\n")
	} else {
		sb.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			sb.WriteByte('\n')
		}
	}
	for _, c := range legacyContent {
		sb.WriteString("- " + c + "\n")
	}
	if err := writeFileAtomic(generalPath, []byte(sb.String()), 0o640); err != nil {
		return fmt.Errorf("write GENERAL.md: %w", err)
	}
	for _, path := range legacyPaths {
		_ = os.Remove(path)
	}
	return nil
}

// ContextString returns all non-empty memory files as a formatted string for LLM
// injection. Every .md file under memory/ contributes a section keyed by filename.
// Files whose effective body is blank (only headings and/or HTML comment placeholders)
// are skipped so scaffold templates don't pollute prompts before the user fills them.
func (s *Store) ContextString(userID string) (string, error) {
	dir := s.memDir(userID)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read memory dir: %w", err)
	}

	var sections []string
	for _, f := range files {
		if f.IsDir() || !strings.EqualFold(filepath.Ext(f.Name()), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		body := stripFrontmatter(string(data))
		if isEffectivelyEmpty(body) {
			continue
		}
		sections = append(sections, "## "+f.Name()+"\n"+strings.TrimSpace(body))
	}

	if len(sections) == 0 {
		return "", nil
	}
	return strings.Join(sections, "\n\n"), nil
}

func (s *Store) memDir(userID string) string {
	return filepath.Join(s.baseDir, userID, "memory")
}

func (s *Store) notePath(userID, id string) string {
	return filepath.Join(s.memDir(userID), id+".md")
}

// stripFrontmatter removes YAML frontmatter (---...---) from the top of a note body.
func stripFrontmatter(text string) string {
	if !strings.HasPrefix(text, "---") {
		return text
	}
	rest := text[3:]
	if end := strings.Index(rest, "\n---"); end >= 0 {
		return rest[end+4:]
	}
	return text
}

// isEffectivelyEmpty reports whether a note body carries no meaningful content —
// only headings and HTML comment blocks (used by scaffold templates as placeholder
// hints). Files that pass this check are excluded from ContextString.
func isEffectivelyEmpty(body string) bool {
	inComment := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if inComment {
			if strings.Contains(line, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") {
			if !strings.Contains(trimmed, "-->") {
				inComment = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		return false
	}
	return true
}

// isLegacyNote detects the old per-entry format: YAML frontmatter containing an "id:" key.
func isLegacyNote(data []byte) bool {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return false
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return false
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "id:") {
			return true
		}
	}
	return false
}

// renderNote serialises an entry to markdown with a minimal frontmatter header.
// Only used by ImportJSONL to create intermediate UUID-named files that
// MigrateToStructuredFiles then consolidates.
func renderNote(e *Entry) string {
	return fmt.Sprintf("---\nid: %s\ncreated_at: %s\n---\n\n%s\n",
		e.ID, e.CreatedAt.UTC().Format(time.RFC3339), strings.TrimRight(e.Content, "\n"))
}

// parseNote reconstructs an Entry from raw note bytes. fallbackID is the filename
// stem, used when frontmatter is absent or malformed.
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
