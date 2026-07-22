package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ilijad1/simple-agents/internal/convert"
)

// FilesDir is the vault folder holding preserved original uploads.
const FilesDir = "files"

// ImportInput describes a file entering the knowledge base.
type ImportInput struct {
	Data      []byte
	Filename  string // original name; used for the note name, title, and format detection
	SourceURL string // where it came from, when it came from the web
	DestDir   string // vault-relative folder for the note; defaults to notes/
	Title     string // overrides the derived title
}

// ImportResult reports where the import landed.
type ImportResult struct {
	NotePath     string
	OriginalPath string
	Kind         string
	Extractor    string
	Warnings     []string
}

// importMu serializes the reserve-then-write sequence in ImportFile across
// EVERY *Vault instance, not just one. The production wiring constructs more
// than one *vault.Vault backed by the same on-disk data (cmd/simple-agents
// wires one into the coder/runner/designer path, web.NewServer builds its own
// for the upload endpoint) — so a per-Vault mutex field would not close the
// gap between, say, the web upload door and the chat-attachment door. A
// package-level lock does, and imports are rare enough that global
// serialization costs nothing.
var importMu sync.Mutex

// ImportFile converts a file to markdown and files it in the knowledge base.
// It is the single save path shared by the save_to_kb tool, the CLI bridge, the
// web upload endpoint, and chat attachments — so a document enters the vault
// the same way regardless of which door it came through.
//
// On success, the original bytes are preserved alongside the note and linked
// from it: conversion is lossy by nature (a PDF's tables, a spreadsheet's
// formulas), and keeping the source means a later agent can re-extract instead
// of hitting a dead end. The original is written FIRST, so a failure before
// that point leaves nothing behind; if a later step then fails, ImportFile
// makes a best-effort attempt to remove that now-orphaned original before
// returning the error, so a caller retrying a failing import does not
// accumulate unreferenced x.csv, x-2.csv, x-3.csv, ... copies in files/.
func (v *Vault) ImportFile(workspaceID string, in ImportInput) (ImportResult, error) {
	if len(in.Data) == 0 {
		return ImportResult{}, fmt.Errorf("import: no file content")
	}

	destDir := strings.Trim(strings.TrimSpace(in.DestDir), "/")
	if destDir == "" {
		destDir = "notes"
	}
	// DestDir is otherwise only slash-trimmed, which would let an import land
	// inside a system-managed area (.kb, chats/, or another agent's own
	// agents/<id> dir) within the SAME workspace. Cross-tenant escape is
	// already blocked by Resolve; this check is about scope, not isolation.
	// Checked before anything is written so a rejected DestDir never even
	// creates the preserved original.
	if seg := topSegment(destDir); seg == InternalDir || seg == "chats" || seg == "agents" {
		return ImportResult{}, fmt.Errorf("import: dest dir %q targets a system-managed area", in.DestDir)
	}

	res, err := convert.ToMarkdown(in.Data, convert.Options{
		Filename:  in.Filename,
		SourceURL: in.SourceURL,
	})
	if err != nil {
		return ImportResult{}, err
	}

	base := sanitizeBaseName(in.Filename)
	if base == "" {
		base = "imported-" + time.Now().Format("20060102-150405")
	}

	// The lock spans the ENTIRE reserve-then-write sequence for both the
	// original and the note, not just each uniquePath lookup: uniquePath is
	// check-then-act (probe for a free path, write later), so the race is
	// between one goroutine's probe and another goroutine's write, not
	// between two probes. Locking only the lookup would still leave that gap
	// open. Imports are not a hot path, so a single coarse-grained critical
	// section costs nothing and is easy to reason about.
	importMu.Lock()
	defer importMu.Unlock()

	originalRel, err := uniquePath(v, workspaceID, path.Join(FilesDir, base+originalExt(in.Filename)))
	if err != nil {
		return ImportResult{}, fmt.Errorf("import: locate free path for original: %w", err)
	}
	// Preserve the original first: if this fails, nothing has been written.
	if err := v.WriteNote(workspaceID, originalRel, in.Data); err != nil {
		return ImportResult{}, fmt.Errorf("import: preserve original: %w", err)
	}

	noteRel, err := uniquePath(v, workspaceID, path.Join(destDir, base+".md"))
	if err != nil {
		return ImportResult{}, cleanupOrphan(v, workspaceID, originalRel, fmt.Errorf("import: locate free path for note: %w", err))
	}

	title := in.Title
	if title == "" {
		title = res.Title
	}
	if title == "" {
		title = base
	}

	note := renderImportedNote(title, in, res, originalRel)
	if err := v.WriteNote(workspaceID, noteRel, []byte(note)); err != nil {
		return ImportResult{}, cleanupOrphan(v, workspaceID, originalRel, fmt.Errorf("import: write note: %w", err))
	}

	return ImportResult{
		NotePath:     noteRel,
		OriginalPath: originalRel,
		Kind:         string(res.Kind),
		Extractor:    res.Extractor,
		Warnings:     res.Warnings,
	}, nil
}

// cleanupOrphan best-effort removes an original that was already written to
// files/ once a later step of the same import fails, so a retry doesn't pile
// up unreferenced copies indefinitely. If the removal itself fails, that
// failure is folded into the returned error rather than swallowed — silently
// eating a cleanup failure would recreate exactly the orphan problem this
// exists to prevent, just less predictably.
func cleanupOrphan(v *Vault, workspaceID, originalRel string, cause error) error {
	if delErr := v.Delete(workspaceID, originalRel); delErr != nil {
		return fmt.Errorf("%w (additionally, failed to remove orphaned original %q: %v)", cause, originalRel, delErr)
	}
	return cause
}

// renderImportedNote builds the note body. The frontmatter records how the note
// was produced — source, extractor, and any quality warnings — so a lossy
// conversion is never mistaken for a faithful one.
func renderImportedNote(title string, in ImportInput, res convert.Result, originalRel string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "title: %q\n", title)
	source := in.SourceURL
	if source == "" {
		source = in.Filename
	}
	fmt.Fprintf(&sb, "source: %q\n", source)
	fmt.Fprintf(&sb, "kind: %s\n", res.Kind)
	fmt.Fprintf(&sb, "extractor: %s\n", res.Extractor)
	fmt.Fprintf(&sb, "original_bytes: %d\n", len(in.Data))
	fmt.Fprintf(&sb, "original_file: %q\n", originalRel)
	fmt.Fprintf(&sb, "converted_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	if len(res.Warnings) > 0 {
		sb.WriteString("warnings:\n")
		for _, w := range res.Warnings {
			fmt.Fprintf(&sb, "  - %q\n", w)
		}
	}
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# %s\n\n", title)
	if len(res.Warnings) > 0 {
		sb.WriteString("> **Conversion notes:** " + strings.Join(res.Warnings, "; ") + "\n\n")
	}
	fmt.Fprintf(&sb, "_Converted from [%s](%s)._\n\n", path.Base(originalRel), originalRel)
	sb.WriteString(res.Markdown)
	if !strings.HasSuffix(res.Markdown, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

// sanitizeBaseName reduces an arbitrary (possibly hostile) filename to a safe
// note name: no directory components, no traversal, no control characters.
//
// It keeps Unicode letters and digits, not just ASCII, so a Cyrillic or CJK
// title survives recognizably instead of being destroyed and replaced with a
// bare timestamp fallback. This platform's operator works in Macedonian and
// English, so non-Latin filenames ("Отчет по продажам.csv", "売上報告書.csv")
// are the ordinary case here, not an edge case.
func sanitizeBaseName(filename string) string {
	base := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	base = strings.TrimSuffix(base, path.Ext(base))

	var sb strings.Builder
	for _, r := range base {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
		case r == '.' || r == '_' || r == ' ' || r == '-':
			sb.WriteRune(r)
		default:
			// Anything else (path separators, control chars, punctuation
			// that could confuse a path) becomes a separator, same as before.
			sb.WriteRune('-')
		}
	}
	base = sb.String()
	base = strings.Trim(strings.Join(strings.Fields(base), " "), "-. ")

	// Cap by rune count, not byte count: slicing a multi-byte UTF-8 title
	// (Cyrillic/CJK) at a raw byte offset can split a rune and produce
	// invalid UTF-8 in the note name.
	if runes := []rune(base); len(runes) > 80 {
		base = string(runes[:80])
	}
	if base == "" || base == "." || base == ".." {
		return ""
	}
	return base
}

// originalExt returns the original file's extension, or "" when it has none.
// Extensions are expected to be short ASCII tokens (.csv, .pdf, ...); anything
// else is dropped rather than risking an odd character in a path segment.
func originalExt(filename string) string {
	ext := path.Ext(path.Base(strings.ReplaceAll(filename, `\`, "/")))
	if len(ext) > 10 {
		return ""
	}
	for _, r := range strings.TrimPrefix(ext, ".") {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return ""
		}
	}
	return ext
}

// uniquePath appends -2, -3, … until the path is free, so an import never
// silently overwrites an existing note.
func uniquePath(v *Vault, workspaceID, rel string) (string, error) {
	free, err := pathIsFree(v, workspaceID, rel)
	if err != nil {
		return "", err
	}
	if free {
		return rel, nil
	}
	ext := path.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		free, err := pathIsFree(v, workspaceID, candidate)
		if err != nil {
			return "", err
		}
		if free {
			return candidate, nil
		}
	}
	return fmt.Sprintf("%s-%d%s", stem, time.Now().UnixNano(), ext), nil
}

// pathIsFree reports whether rel does not yet exist in the vault.
//
// Any error OTHER than "does not exist" — a vault-escape from Resolve, a
// permission failure from the filesystem — is propagated rather than treated
// as "free". Silently mapping every error to "free" would let a probe that
// failed for a real reason (escape, permission) steer an import into a path
// it was never actually cleared to use; WriteNote re-validates today, so
// nothing exploits this in practice, but that's incidental, not a guarantee.
func pathIsFree(v *Vault, workspaceID, rel string) (bool, error) {
	_, err := v.ReadNote(workspaceID, rel)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}
