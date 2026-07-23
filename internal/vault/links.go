package vault

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// wikilinkRE matches Obsidian-style links: [[Target]] or [[Target|Alias]].
// The target may be a bare note name ("Groceries") or a vault-relative path
// ("notes/Groceries"). An optional "#heading" suffix is tolerated and ignored
// for resolution.
var wikilinkRE = regexp.MustCompile(`\[\[([^\]\|]+?)(?:\|([^\]]+))?\]\]`)

// Wikilink is one parsed [[link]] occurrence.
type Wikilink struct {
	Target string // raw target text (note name or path), heading stripped
	Alias  string // display text if "|alias" was given, else ""
	Raw    string // the full original "[[...]]" text
}

// ParseWikilinks extracts every [[link]] from markdown content.
func ParseWikilinks(content string) []Wikilink {
	matches := wikilinkRE.FindAllStringSubmatch(content, -1)
	links := make([]Wikilink, 0, len(matches))
	for _, m := range matches {
		target := strings.TrimSpace(m[1])
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = strings.TrimSpace(target[:i])
		}
		links = append(links, Wikilink{Target: target, Alias: strings.TrimSpace(m[2]), Raw: m[0]})
	}
	return links
}

// agentLogRE matches an agent's run-log notes (agents/<id>/logs/…). These are
// machine transcripts, not knowledge, so they don't participate in the backlink
// graph as SOURCES (see linkSourceExcluded).
var agentLogRE = regexp.MustCompile(`^agents/[^/]+/logs/`)

// linkSourceExcluded reports whether a note should be ignored when collecting
// who-links-here. System-generated transcripts — inbox notifications, reflected
// chats, and agent run logs — mention other notes by name incidentally; letting
// them register as backlinks buries a user's real, meaningful references under
// machine spam. Targets are unaffected: a link TO any note still resolves.
func linkSourceExcluded(rel string) bool {
	return strings.HasPrefix(rel, "inbox/") ||
		strings.HasPrefix(rel, "chats/") ||
		agentLogRE.MatchString(rel)
}

// namePriority ranks candidate locations for resolving a bare [[Name]] link when
// several notes share a basename. User-authored knowledge (notes/, memory/, the
// root) wins over system-generated notes (agents/, chats/, inbox/, reminders/),
// so a link to "Foo" resolves to the user's notes/Foo.md rather than an agent's
// own Foo.md that merely sorts earlier in the walk. Higher wins; ties keep the
// first-seen (the walk is deterministic).
func namePriority(rel string) int {
	switch {
	case strings.HasPrefix(rel, "notes/"), strings.HasPrefix(rel, "memory/"):
		return 3
	case !strings.Contains(rel, "/"): // a root-level note
		return 3
	case strings.HasPrefix(rel, "agents/"), strings.HasPrefix(rel, "chats/"),
		strings.HasPrefix(rel, "inbox/"), strings.HasPrefix(rel, "reminders/"):
		return 1
	default: // skills/ and any other user-organizable folder
		return 2
	}
}

// LinkIndex maps note targets to vault-relative paths so [[wikilinks]] can be
// resolved for rendering and backlink discovery. It is rebuildable at any time by
// walking the vault; it is not authoritative state.
type LinkIndex struct {
	// byName maps a lowercased basename (without .md) to its vault-relative path.
	// On collision the higher-namePriority note wins (user content over system),
	// ties broken by first-seen in the deterministic sorted walk.
	byName map[string]string
	// byNamePrio tracks the namePriority of the path currently held in byName for
	// each basename, so a later, higher-priority candidate can displace it.
	byNamePrio map[string]int
	// byPath holds every note's vault-relative path (lowercased, .md trimmed) so
	// links written as paths ("notes/x") resolve too.
	byPath map[string]string
}

// BuildLinkIndex walks a user's vault and indexes every markdown note. The hidden
// .kb directory is skipped.
func (v *Vault) BuildLinkIndex(workspaceID string) (*LinkIndex, error) {
	root := v.Root(workspaceID)
	idx := &LinkIndex{byName: map[string]string{}, byNamePrio: map[string]int{}, byPath: map[string]string{}}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate unreadable entries
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
		rel, err := v.Rel(workspaceID, path)
		if err != nil {
			return nil
		}
		name := strings.ToLower(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())))
		prio := namePriority(rel)
		// Keep the highest-priority path for a basename (user content beats
		// system-generated notes); on a tie, first-seen wins.
		if _, exists := idx.byName[name]; !exists || prio > idx.byNamePrio[name] {
			idx.byName[name] = rel
			idx.byNamePrio[name] = prio
		}
		idx.byPath[strings.ToLower(strings.TrimSuffix(rel, ".md"))] = rel
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// Resolve returns the vault-relative path a [[target]] points at, or "" if no
// matching note exists (a dangling link).
func (idx *LinkIndex) Resolve(target string) string {
	key := strings.ToLower(strings.TrimSuffix(filepath.ToSlash(target), ".md"))
	if rel, ok := idx.byPath[key]; ok {
		return rel
	}
	base := key
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if rel, ok := idx.byName[base]; ok {
		return rel
	}
	return ""
}

// RenderHTMLLinks rewrites [[wikilinks]] in content into markdown links that point
// at the knowledge-base viewer. linkURL builds the href for a resolved relative
// path; resolved == "" means a dangling link, which is rendered as a span so it is
// visible but not clickable. The result is still markdown (HTML rendering happens
// downstream via goldmark).
func (idx *LinkIndex) RenderHTMLLinks(content string, linkURL func(relPath string) string) string {
	return wikilinkRE.ReplaceAllStringFunc(content, func(raw string) string {
		links := ParseWikilinks(raw)
		if len(links) == 0 {
			return raw
		}
		l := links[0]
		display := l.Alias
		if display == "" {
			display = l.Target
		}
		rel := idx.Resolve(l.Target)
		if rel == "" {
			return display + " <sup>(no note)</sup>"
		}
		return "[" + display + "](" + linkURL(rel) + ")"
	})
}

// Backlinks returns the vault-relative paths of every note that links to target.
func (v *Vault) Backlinks(workspaceID, targetRel string) ([]string, error) {
	root := v.Root(workspaceID)
	idx, err := v.BuildLinkIndex(workspaceID)
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && d.Name() == InternalDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, relErr := v.Rel(workspaceID, path)
		if relErr != nil {
			return nil
		}
		// Machine-generated transcripts (inbox, reflected chats, agent run logs)
		// don't count as backlink sources — see linkSourceExcluded.
		if linkSourceExcluded(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, l := range ParseWikilinks(string(data)) {
			if idx.Resolve(l.Target) == targetRel {
				out = append(out, rel)
				break
			}
		}
		return nil
	})
	return out, err
}
