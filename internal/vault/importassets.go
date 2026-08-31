package vault

import (
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/rookery-ai/rookery/internal/convert"
)

// Storing the images a converter extracted from inside a document.
//
// internal/convert must stay a pure function of its input — no vault, no
// filesystem — which is what lets it be tested against golden fixtures and
// behave identically on every host. So it returns embedded images in
// Result.Assets and references them as "rookery-asset:<index>", and the
// rewriting happens here, in ImportFile: the single choke point the web upload,
// the chat attachment, the save_to_kb tool and the CLI bridge all funnel
// through, so every door stores media the same way.
//
// The images land in the SAME uploads/ folder as the preserved original and as
// anything pasted into the editor, and are referenced by vault-relative path.
// That is the shape the editor's image picker and the export inliner already
// consume, so an extracted image renders in the note, survives an HTML or PDF
// export, and can be re-inserted elsewhere — with no new storage location and no
// new serving route.

// writeImportAssets stores each asset and returns index -> vault-relative path.
// A failure to write one asset drops that image and reports it, rather than
// failing the whole import: a note with most of its pictures is worth more than
// no note at all, and the original is preserved beside it either way.
func (v *Vault) writeImportAssets(workspaceID string, assets []convert.Asset) (map[int]string, []string) {
	if len(assets) == 0 {
		return nil, nil
	}
	out := make(map[int]string, len(assets))
	var failed int
	for i, a := range assets {
		name := assetFileName(a, i)
		rel, err := uniquePath(v, workspaceID, path.Join(FilesDir, name))
		if err != nil {
			failed++
			slog.Warn("vault: could not place an extracted image", "workspace_id", workspaceID, "err", err)
			continue
		}
		if err := v.WriteNote(workspaceID, rel, a.Data); err != nil {
			failed++
			slog.Warn("vault: could not write an extracted image", "workspace_id", workspaceID, "path", rel, "err", err)
			continue
		}
		out[i] = rel
	}
	var warnings []string
	if failed > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d embedded image(s) could not be saved and are missing from this note", failed))
	}
	return out, warnings
}

// assetFileName derives a safe file name for an extracted image.
//
// The name comes from inside an uploaded archive, so it is attacker-controlled
// and gets the same treatment as any uploaded file name: sanitizeBaseName strips
// directory components and traversal. The index is appended because names inside
// a document collide constantly (every .docx calls its first picture image1.png),
// and uniquePath would otherwise have to disambiguate every single one.
func assetFileName(a convert.Asset, i int) string {
	base := sanitizeBaseName(a.Name)
	ext := path.Ext(a.Name)
	if base == "" {
		base = "image"
	}
	if ext == "" {
		ext = extensionForContentType(a.ContentType)
	}
	return fmt.Sprintf("%s-%d%s", base, i+1, ext)
}

// extensionForContentType covers the image types a document can embed. Sniffed
// content type rather than the archive's own metadata, which is often absent.
func extensionForContentType(ct string) string {
	switch {
	case strings.HasPrefix(ct, "image/png"):
		return ".png"
	case strings.HasPrefix(ct, "image/jpeg"):
		return ".jpg"
	case strings.HasPrefix(ct, "image/gif"):
		return ".gif"
	case strings.HasPrefix(ct, "image/webp"):
		return ".webp"
	case strings.HasPrefix(ct, "image/bmp"):
		return ".bmp"
	default:
		return ".img"
	}
}

// rewriteAssetRefs replaces every "rookery-asset:<index>" destination with the
// vault-relative path the asset was written to.
//
// An index with no stored path — the write failed, or a converter emitted a
// reference it never backed with bytes — has its whole image reference REMOVED
// rather than left pointing at an unresolvable scheme. A dangling
// "![](rookery-asset:3)" renders as a broken image and tells the reader nothing;
// removing it leaves the surrounding prose intact, and the accompanying warning
// says images are missing.
func rewriteAssetRefs(md string, paths map[int]string) string {
	if !strings.Contains(md, convert.AssetRefScheme) {
		return md
	}
	var sb strings.Builder
	rest := md
	for {
		// Locate the whole "![...](rookery-asset:N)" construct, working from the
		// scheme outward: the destination is what identifies it, and the label
		// may legitimately be empty.
		i := strings.Index(rest, "]("+convert.AssetRefScheme)
		if i < 0 {
			sb.WriteString(rest)
			break
		}
		close := strings.IndexByte(rest[i:], ')')
		if close < 0 {
			sb.WriteString(rest)
			break
		}
		close += i
		// Walk back to the "![" that opens this image.
		start := strings.LastIndex(rest[:i], "![")
		if start < 0 {
			// A link rather than an image, or malformed. Leave it untouched and
			// continue past it so the scan cannot loop.
			sb.WriteString(rest[:close+1])
			rest = rest[close+1:]
			continue
		}
		label := rest[start+2 : i]
		idxStr := rest[i+2+len(convert.AssetRefScheme) : close]
		sb.WriteString(rest[:start])
		if p, err := strconv.Atoi(idxStr); err == nil {
			if stored, ok := paths[p]; ok {
				sb.WriteString("![" + label + "](" + escapeDestination(stored) + ")")
			}
			// else: drop the reference entirely (see the doc comment).
		}
		rest = rest[close+1:]
	}
	return sb.String()
}

// escapeDestination mirrors internal/convert's rule for the URL half of an
// image: a space would end the destination and turn the construct back into
// literal text, and an unescaped paren would truncate the path. Duplicated
// rather than exported because the two packages must not depend on each other's
// internals, and it is four lines.
func escapeDestination(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`, ` `, "%20")
	return r.Replace(s)
}
