package vault

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// SearchHit is one matching line within a note.
type SearchHit struct {
	Path    string // vault-relative slash path
	Line    int    // 1-based line number
	Snippet string // the matching line, trimmed
}

// Searcher runs keyword/full-text queries over a user's vault. The interface is
// kept deliberately small so a future embedding-based (semantic) implementation
// can be dropped in without touching callers.
type Searcher interface {
	Search(ctx context.Context, workspaceID, query string) ([]SearchHit, error)
}

// ripgrepSearcher shells out to ripgrep (rg) for fast full-text search, falling
// back to a pure-Go walk when rg is not installed.
type ripgrepSearcher struct {
	v *Vault
}

// NewSearcher returns the default keyword searcher for a vault.
func (v *Vault) NewSearcher() Searcher { return &ripgrepSearcher{v: v} }

func (s *ripgrepSearcher) Search(ctx context.Context, workspaceID, query string) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	root := s.v.Root(workspaceID)
	if _, err := exec.LookPath("rg"); err == nil {
		if hits, err := s.searchRipgrep(ctx, root, workspaceID, query); err == nil {
			return hits, nil
		}
		// fall through to the Go fallback on rg failure
	}
	return s.searchGo(workspaceID, query)
}

func (s *ripgrepSearcher) searchRipgrep(ctx context.Context, root, workspaceID, query string) ([]SearchHit, error) {
	// --json gives structured matches; -i case-insensitive; -F fixed strings so a
	// user's query is never interpreted as a regex; glob excludes the internal dir.
	cmd := exec.CommandContext(ctx, "rg", "--json", "-i", "-F",
		"--max-count", "5", "-g", "!"+InternalDir, "--", query, root)
	out, err := cmd.Output()
	if err != nil {
		// rg exits 1 when there are no matches — that is not an error for us.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	var hits []SearchHit
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				Lines struct {
					Text string `json:"text"`
				} `json:"lines"`
				LineNumber int `json:"line_number"`
			} `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.Type != "match" {
			continue
		}
		rel, err := s.v.Rel(workspaceID, ev.Data.Path.Text)
		if err != nil {
			continue
		}
		hits = append(hits, SearchHit{
			Path:    rel,
			Line:    ev.Data.LineNumber,
			Snippet: trimSnippet(ev.Data.Lines.Text),
		})
	}
	return hits, nil
}

// searchGo is the dependency-free fallback used when ripgrep is unavailable.
func (s *ripgrepSearcher) searchGo(workspaceID, query string) ([]SearchHit, error) {
	q := strings.ToLower(query)
	var hits []SearchHit
	nodes, err := s.v.walkNotes(workspaceID)
	if err != nil {
		return nil, err
	}
	for _, rel := range nodes {
		data, err := s.v.ReadNote(workspaceID, rel)
		if err != nil {
			continue
		}
		count := 0
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				hits = append(hits, SearchHit{Path: rel, Line: i + 1, Snippet: trimSnippet(line)})
				if count++; count >= 5 {
					break
				}
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
	return hits, nil
}

// walkNotes returns every markdown note's vault-relative path, skipping .kb.
func (v *Vault) walkNotes(workspaceID string) ([]string, error) {
	var out []string
	root := v.Root(workspaceID)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == InternalDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		if rel, err := v.Rel(workspaceID, path); err == nil {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func trimSnippet(s string) string {
	s = strings.TrimRight(s, "\r\n")
	s = strings.TrimSpace(s)
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
