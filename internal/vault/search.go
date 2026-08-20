package vault

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
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
	// A table hit needs its table's header, which means reading the file. Cached
	// per path for the life of this one search: ripgrep returns up to 5 matches
	// per file, so without it a busy note is read five times.
	fileCache := map[string]string{}
	contentOf := func(rel string) string {
		if c, ok := fileCache[rel]; ok {
			return c
		}
		data, err := s.v.ReadNote(workspaceID, rel)
		if err != nil {
			// Not fatal: snippetFor falls back to trimming the line alone.
			fileCache[rel] = ""
			return ""
		}
		fileCache[rel] = string(data)
		return fileCache[rel]
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
		snippet := snippetFor(contentOf(rel), ev.Data.LineNumber, ev.Data.Lines.Text)
		if snippet == "" {
			// The match was a bare structural wrapper with no text of its own.
			continue
		}
		hits = append(hits, SearchHit{
			Path:    rel,
			Line:    ev.Data.LineNumber,
			Snippet: snippet,
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
		content := string(data)
		count := 0
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				snippet := snippetFor(content, i+1, line)
				if snippet == "" {
					// A bare structural wrapper carries no text of its own.
					continue
				}
				hits = append(hits, SearchHit{Path: rel, Line: i + 1, Snippet: snippet})
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

const (
	// snippetMax is the budget for an ordinary prose hit. Deliberately
	// unchanged: raising it for every hit would spend the shared byte budget
	// (see kbsearch.go) on fewer results for no gain, since a prose line that
	// long is rare.
	snippetMax = 200

	// tableSnippetMax is the budget for a hit INSIDE a markdown table, which
	// also has to carry the table's header row. A converted-CSV row runs to
	// ~1774 characters on the note that prompted this, so 200 showed about a
	// tenth of one cell with no column names. Three labelled rows are worth
	// more than sixteen unlabelled fragments when the question is about a table.
	tableSnippetMax = 600
)

func trimSnippet(s string) string { return trimTo(s, snippetMax) }

// trimTo trims and caps, cutting on a RUNE boundary — this operator's notes are
// routinely Cyrillic, and a raw byte cut corrupts the final character rather
// than merely shortening the text.
func trimTo(s string, max int) string {
	s = strings.TrimRight(s, "\r\n")
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// snippetFor renders one search hit into the text a model reads.
//
// Two things it does beyond trimming, both aimed at the same failure — a hit
// that is technically correct and tells the reader nothing:
//
//   - A hit inside a markdown table carries that table's HEADER, without which
//     the row is uninterpretable. It uses the table the hit is actually in, not
//     the note's first: labelling a row with another table's columns is worse
//     than no header at all, because it reads as authoritative.
//   - A hit on one of the block constructs the KB editor produces (callout,
//     toggle, columns, alignment) is unwrapped to its readable text instead of
//     being returned as raw HTML. Images are dropped entirely, per the request:
//     an image path is not what someone searching their notes is looking for.
//
// content may be empty when the caller has no cheap access to the file; the
// table lookup is then skipped and the line is trimmed as before.
func snippetFor(content string, lineNo int, line string) string {
	line = unwrapConstructs(line)
	if line == "" {
		return ""
	}
	if header, ok := tableHeaderFor(content, lineNo); ok {
		// The hit may BE the header or the delimiter row, in which case the
		// header already contains it and appending would print it twice.
		if strings.Contains(header, strings.TrimSpace(line)) {
			return trimTo(header, tableSnippetMax)
		}
		return trimTo(header+"\n"+strings.TrimSpace(line), tableSnippetMax)
	}
	return trimTo(line, snippetMax)
}

// tableHeaderFor returns the header of the table containing lineNo, if any. It
// walks BACK to the start of the contiguous run of pipe-bearing lines and asks
// tableHeader whether that run opens a real table, so a note with several
// tables labels each row with its own.
func tableHeaderFor(content string, lineNo int) (string, bool) {
	if content == "" || lineNo < 1 {
		return "", false
	}
	lines := strings.Split(content, "\n")
	if lineNo > len(lines) {
		return "", false
	}
	idx := lineNo - 1
	if !strings.Contains(lines[idx], "|") {
		return "", false
	}
	start := idx
	for start > 0 && strings.Contains(lines[start-1], "|") {
		start--
	}
	return tableHeader(strings.Join(lines[start:], "\n"))
}

// unwrapConstructs turns the KB editor's block constructs into the text a
// reader would see, and returns "" for a construct that carries no text of its
// own — a bare wrapper is structure, and returning it as a search result is
// how "the search found something" and "the search found nothing useful" become
// indistinguishable.
func unwrapConstructs(s string) string {
	s = strings.TrimSpace(s)
	if divWrapperRE.MatchString(s) || s == "</div>" || s == "<details>" || s == "</details>" {
		return ""
	}
	s = imageRE.ReplaceAllString(s, "")
	s = summaryRE.ReplaceAllString(s, "$1")
	s = calloutRE.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var (
	// <div align="center"> and <div data-cols="2"> — the alignment and columns
	// nodes. Structure, never content.
	divWrapperRE = regexp.MustCompile(`^<div\s[^>]*>$`)
	summaryRE    = regexp.MustCompile(`<summary>(.*?)</summary>|</?summary>`)
	calloutRE    = regexp.MustCompile(`^>\s*\[!\w+\]\s*`)
	imageRE      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
)
