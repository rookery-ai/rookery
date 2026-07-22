package vault

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

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

// ImportFile converts a file to markdown and files it in the knowledge base.
// It is the single save path shared by the save_to_kb tool, the CLI bridge, the
// web upload endpoint, and chat attachments — so a document enters the vault
// the same way regardless of which door it came through.
//
// The original bytes are preserved alongside the note. Conversion is lossy by
// nature (a PDF's tables, a spreadsheet's formulas), and keeping the source
// means a later agent can re-extract instead of hitting a dead end.
func (v *Vault) ImportFile(workspaceID string, in ImportInput) (ImportResult, error) {
	if len(in.Data) == 0 {
		return ImportResult{}, fmt.Errorf("import: no file content")
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

	// Preserve the original first: if this fails, nothing has been written.
	originalRel := uniquePath(v, workspaceID, path.Join(FilesDir, base+originalExt(in.Filename)))
	if err := v.WriteNote(workspaceID, originalRel, in.Data); err != nil {
		return ImportResult{}, fmt.Errorf("import: preserve original: %w", err)
	}

	destDir := strings.Trim(strings.TrimSpace(in.DestDir), "/")
	if destDir == "" {
		destDir = "notes"
	}
	noteRel := uniquePath(v, workspaceID, path.Join(destDir, base+".md"))

	title := in.Title
	if title == "" {
		title = res.Title
	}
	if title == "" {
		title = base
	}

	note := renderImportedNote(title, in, res, originalRel)
	if err := v.WriteNote(workspaceID, noteRel, []byte(note)); err != nil {
		return ImportResult{}, fmt.Errorf("import: write note: %w", err)
	}

	return ImportResult{
		NotePath:     noteRel,
		OriginalPath: originalRel,
		Kind:         string(res.Kind),
		Extractor:    res.Extractor,
		Warnings:     res.Warnings,
	}, nil
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

var unsafeNameChars = regexp.MustCompile(`[^a-zA-Z0-9._ -]+`)

// sanitizeBaseName reduces an arbitrary (possibly hostile) filename to a safe
// note name: no directory components, no traversal, no control characters.
func sanitizeBaseName(filename string) string {
	base := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	base = strings.TrimSuffix(base, path.Ext(base))
	base = unsafeNameChars.ReplaceAllString(base, "-")
	base = strings.Trim(strings.Join(strings.Fields(base), " "), "-. ")
	if len(base) > 80 {
		base = base[:80]
	}
	if base == "" || base == "." || base == ".." {
		return ""
	}
	return base
}

// originalExt returns the original file's extension, or "" when it has none.
func originalExt(filename string) string {
	ext := path.Ext(path.Base(strings.ReplaceAll(filename, `\`, "/")))
	if len(ext) > 10 || unsafeNameChars.MatchString(strings.TrimPrefix(ext, ".")) {
		return ""
	}
	return ext
}

// uniquePath appends -2, -3, … until the path is free, so an import never
// silently overwrites an existing note.
func uniquePath(v *Vault, workspaceID, rel string) string {
	if _, err := v.ReadNote(workspaceID, rel); err != nil {
		return rel
	}
	ext := path.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := v.ReadNote(workspaceID, candidate); err != nil {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d%s", stem, time.Now().UnixNano(), ext)
}
