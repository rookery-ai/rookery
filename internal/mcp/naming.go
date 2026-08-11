package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// MaxToolNameLen is the longest tool name an LLM provider will accept. Both OpenAI
// and Anthropic enforce ^[a-zA-Z0-9_-]{1,64}$, and a provider rejects the WHOLE tool
// list when one name violates it — so a single badly-named MCP tool would take out
// every other tool the agent has, connector tools included.
const MaxToolNameLen = 64

// ToolNamePrefix namespaces every MCP tool.
//
// Unlike connectors there is no bare-name case. MCP tool names are arbitrary and
// collide both with connector actions (`search`, `list_files`) and between servers —
// the spec itself notes that aggregating clients will hit this and should prefix with
// a server identifier.
const ToolNamePrefix = "mcp"

// hashSuffixLen is the hex length appended when a name must be truncated. Eight hex
// characters of SHA-256 over the full identity make an accidental collision between
// two truncated names of the same server vanishingly unlikely, while keeping the name
// readable.
const hashSuffixLen = 8

// slugSegment reduces free text to the LLM tool-name charset.
//
// This is not cosmetic. MCP tool names legally contain DOTS (`admin.tools.list`) and
// run to 128 characters — neither of which a provider accepts — so a server exposing
// an entirely spec-compliant name would otherwise break the agent's whole tool list.
func slugSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

// SlugFor derives a server's namespace slug from its display name, lowercased.
// Uniqueness within the workspace is enforced by the caller against the DB, since a
// name can legitimately repeat.
func SlugFor(name string) string {
	return strings.ToLower(slugSegment(strings.TrimSpace(name)))
}

// ExposedToolName builds the name the model sees: mcp__<slug>__<tool>.
//
// It is DETERMINISTIC for a given (slug, tool) pair — the result is persisted in
// mcp_tools.tool_name and must be reproducible across syncs, or a re-sync would
// rename tools the model has already been taught within a conversation.
//
// `taken` guards against two distinct tools of one server truncating onto the same
// name; it is consulted only after the deterministic form is computed, so the common
// case never depends on iteration order.
func ExposedToolName(slug, tool string, taken map[string]bool) string {
	base := ToolNamePrefix + "__" + slugSegment(slug) + "__" + slugSegment(tool)
	if len(base) <= MaxToolNameLen && !taken[base] {
		return base
	}

	// Hash the UNSLUGGED identity: two different upstream names that slug to the
	// same text (`a.b` and `a_b`) must still produce different exposed names.
	sum := sha256.Sum256([]byte(slug + "\x00" + tool))
	suffix := "_" + hex.EncodeToString(sum[:])[:hashSuffixLen]

	keep := MaxToolNameLen - len(suffix)
	if keep < 1 {
		keep = 1
	}
	if len(base) > keep {
		base = base[:keep]
	}
	name := base + suffix
	if !taken[name] {
		return name
	}
	// Unreachable in practice — (server_id, name) is unique and the hash covers the
	// full identity — but a silently duplicated tool name would be worse than an
	// ugly one, so disambiguate rather than return a known collision.
	for i := 2; ; i++ {
		alt := name[:len(name)-2] + string(rune('a'+(i%26))) + string(rune('a'+((i/26)%26)))
		if !taken[alt] {
			return alt
		}
	}
}
