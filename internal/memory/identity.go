package memory

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Identity is the full set of values the identity memory files are rendered
// from. Every field is optional; an empty field omits its line or section.
//
// It is a plain struct rather than a reference to db.Workspace or
// profile.Profile so this package keeps importing neither: the caller reads
// from whichever store owns each value and hands the result over.
type Identity struct {
	WorkspaceName  string
	WorkspaceAbout string
	DisplayName    string
	Email          string
	Location       string
	Notes          string
	Tone           string
	Language       string
}

// The two identity files. Named constants because several packages reference
// them (renderer, migration, README template, prompts).
const (
	AboutFile = "ABOUT.md"
	StyleFile = "STYLE.md"

	// The files these replaced. Kept here, next to their successors, so the
	// rename cannot drift apart from the names it maps to.
	legacyAboutFile = "USER.md"
	legacyStyleFile = "SOUL.md"
)

// toneGuidance expands a curated tone value into an instruction. The picker's
// label ("Concise — brief but complete") is a UI affordance; a model needs the
// behaviour spelled out. The keys match TONE_OPTIONS in
// web/ui/src/components/profile/options.ts.
//
// An unrecognised value (a tone typed by hand, or a future option) falls
// through to the raw string rather than being dropped: a user's own words are
// better guidance than silence.
func toneGuidance(tone string) string {
	switch strings.ToLower(strings.TrimSpace(tone)) {
	case "":
		return ""
	case "direct":
		return "**direct** — lead with the answer, no filler, no preamble"
	case "friendly":
		return "**friendly** — warm and conversational, but still get to the point"
	case "concise":
		return "**concise** — brief but complete; prefer bullets over paragraphs"
	case "detailed":
		return "**detailed** — explain the reasoning and cover the edge cases"
	case "formal":
		return "**formal** — professional register, no slang"
	case "casual":
		return "**casual** — relaxed and informal"
	case "encouraging":
		return "**encouraging** — supportive and positive in tone"
	case "neutral":
		return "**neutral** — plain and matter-of-fact, no editorialising"
	default:
		return "**" + strings.TrimSpace(tone) + "**"
	}
}

// RenderAbout renders memory/ABOUT.md. It returns "" when there is nothing
// worth saying — a heading-only file would be classified as content by
// isEffectivelyEmpty and would then block the backfill forever.
//
// A workspace NAME alone does not count as content: it is already visible
// everywhere in the UI, and a file whose only claim is "this workspace is
// called X" costs prompt budget for nothing.
func RenderAbout(id Identity) string {
	hasPerson := id.DisplayName != "" || id.Email != "" || id.Location != ""
	if id.WorkspaceAbout == "" && !hasPerson && id.Notes == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# About This Workspace\n")

	if id.WorkspaceAbout != "" || id.WorkspaceName != "" {
		sb.WriteString("\n## Workspace\n")
		switch {
		case id.WorkspaceName != "" && id.WorkspaceAbout != "":
			fmt.Fprintf(&sb, "This workspace is called **%s**. It is for: %s\n",
				id.WorkspaceName, strings.TrimSpace(id.WorkspaceAbout))
		case id.WorkspaceName != "":
			fmt.Fprintf(&sb, "This workspace is called **%s**.\n", id.WorkspaceName)
		default:
			fmt.Fprintf(&sb, "This workspace is for: %s\n", strings.TrimSpace(id.WorkspaceAbout))
		}
	}

	if hasPerson {
		sb.WriteString("\n## Who I am\n")
		if id.DisplayName != "" {
			fmt.Fprintf(&sb, "- Name: %s\n", id.DisplayName)
		}
		if id.Email != "" {
			fmt.Fprintf(&sb, "- Email: %s\n", id.Email)
		}
		if id.Location != "" {
			fmt.Fprintf(&sb, "- Based in: %s\n", id.Location)
		}
	}

	if id.Notes != "" {
		sb.WriteString("\n## Background\n")
		sb.WriteString(strings.TrimSpace(id.Notes))
		sb.WriteString("\n")
	}

	return sb.String()
}

// RenderStyle renders memory/STYLE.md. Returns "" when neither tone nor
// language is set.
func RenderStyle(id Identity) string {
	guidance := toneGuidance(id.Tone)
	if guidance == "" && id.Language == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Communication Style\n\n")
	if id.Language != "" {
		fmt.Fprintf(&sb, "- Reply in **%s**.\n", id.Language)
	}
	if guidance != "" {
		fmt.Fprintf(&sb, "- Tone: %s.\n", guidance)
	}
	return sb.String()
}

// SeedIdentity writes ABOUT.md and STYLE.md from id.
//
// Each file is written only when it is absent or "effectively empty" — the
// predicate ContextString already uses to decide whether a file is worth
// injecting. Reusing that one function for both "is this worth injecting" and
// "is this safe to overwrite" is deliberate: two predicates could disagree, and
// the disagreement would either strand an install with an empty identity or
// overwrite a file someone had written in.
//
// An empty render writes nothing at all, so a workspace that skipped both the
// basics text and the profile step is left with no file rather than a
// heading-only one.
func (s *Store) SeedIdentity(workspaceID string, id Identity) error {
	return s.seedIdentity(workspaceID, id, nil)
}

// seedIdentity is SeedIdentity with an explicit skip list, used by the migration
// to leave a file alone when its legacy predecessor is still on disk.
func (s *Store) seedIdentity(workspaceID string, id Identity, skip []string) error {
	dir := s.memDir(workspaceID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("memory dir: %w", err)
	}
	for _, f := range []struct {
		name    string
		content string
	}{
		{AboutFile, RenderAbout(id)},
		{StyleFile, RenderStyle(id)},
	} {
		if f.content == "" || slices.Contains(skip, f.name) {
			continue
		}
		path := filepath.Join(dir, f.name)
		existing, err := os.ReadFile(path)
		switch {
		case err == nil:
			if !isEffectivelyEmpty(stripFrontmatter(string(existing))) {
				continue // the user has written here; never touch it
			}
		case os.IsNotExist(err):
			// nothing there yet — write it
		default:
			return fmt.Errorf("read %s: %w", f.name, err)
		}
		if err := writeFileAtomic(path, []byte(f.content), 0o640); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	return nil
}

// MigrateIdentityFiles brings a workspace's memory/ up to the current identity
// layout: USER.md → ABOUT.md, SOUL.md → STYLE.md, then a backfill of whatever
// is still empty.
//
// The rename is a MOVE, not a copy. ContextString globs memory/*.md and
// sections by filename, so a leftover USER.md would be injected alongside
// ABOUT.md and the install would carry two identity documents — the exact
// duplication this design exists to remove.
//
// When BOTH names already exist, neither is touched and the collision is
// logged. That state means someone (or an interrupted earlier run) produced
// two files, and there is no safe way to pick a winner automatically.
//
// Idempotent: a workspace already on the new layout with non-empty files is a
// no-op on every subsequent call.
func (s *Store) MigrateIdentityFiles(workspaceID string, id Identity) error {
	dir := s.memDir(workspaceID)

	// Files left unresolved by the rename phase. Seeding must skip them: the
	// legacy file is still present and still injected, so writing DB values into
	// its successor would produce two identity documents in one prompt — exactly
	// the duplication the rename exists to prevent. Reachable in practice, since
	// the README now tells the owner to edit ABOUT.md and they may create it by
	// hand while USER.md is still there.
	var blocked []string

	for _, r := range []struct{ from, to string }{
		{legacyAboutFile, AboutFile},
		{legacyStyleFile, StyleFile},
	} {
		src := filepath.Join(dir, r.from)
		if _, err := os.Stat(src); err != nil {
			continue // nothing to rename
		}
		dst := filepath.Join(dir, r.to)
		if _, err := os.Stat(dst); err == nil {
			slog.Warn("memory: identity rename skipped, both names present",
				"workspace", workspaceID, "legacy", r.from, "current", r.to)
			blocked = append(blocked, r.to)
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			slog.Warn("memory: identity rename failed",
				"workspace", workspaceID, "from", r.from, "to", r.to, "err", err)
			blocked = append(blocked, r.to)
		}
	}

	return s.seedIdentity(workspaceID, id, blocked)
}
