package skilldesigner

import (
	"fmt"
	"strings"

	"github.com/ilijad1/rookery/internal/skilllibrary"
)

// validCategories is the closed set a skill may be filed under, matching what
// the bundled core skills ship. A generated value outside it is coerced rather
// than passed through: the UI groups on this field, and one hallucinated
// category creates a group of one that never goes away.
var validCategories = []string{
	"File Processing",
	"Agent Behaviour",
	"Web & Research",
	"Development",
	"Productivity",
	"Integrations",
	"Meta",
	"Other",
}

const (
	defaultVersion  = "1.0.0"
	defaultLicense  = "MIT-0"
	defaultCategory = "Other"
)

// canonicalCategory maps a model-written category onto the closed set,
// case-insensitively. Anything unrecognised becomes "Other".
func canonicalCategory(v string) string {
	v = strings.TrimSpace(v)
	for _, c := range validCategories {
		if strings.EqualFold(v, c) {
			return c
		}
	}
	return defaultCategory
}

// NormalizeFrontmatter guarantees a saved SKILL.md carries the same frontmatter
// a built-in skill does: name, description, version, license, category.
//
// It DEFAULTS rather than rejects. The generation prompt asks for every field,
// but a weak model omitting one must not fail a save at the end of a full design
// conversation — losing the conversation is far worse than a skill filed under
// "Other". Explicit values always win; only absent or unrecognised ones are
// replaced.
//
// name is the already-validated slug (the caller re-slugifies before saving), so
// the file's name field cannot disagree with its directory.
//
// ACCEPTED COST — this rewrites the frontmatter from the parsed model, so any
// key skilllibrary.ParseMeta does not know about is DROPPED. The fields it does
// model (name, description, version, license, category, topics, metadata.requires,
// metadata.install) all survive, and those are the only ones this platform reads.
// A third-party SKILL.md carrying, say, an `allowed-tools:` key from another
// runtime loses it on import. Preserving unknown keys would mean re-serialising a
// generic YAML tree, and the value of that is zero until something here reads one.
// The body is never touched, so nothing the model actually follows is lost.
// Note this applies to CREATE/IMPORT only — the KB editor's save path
// (skillstore.SaveContent) does not normalize.
func NormalizeFrontmatter(content, name string) string {
	meta, body := skilllibrary.ParseMeta(content)

	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		desc = "A user-created skill."
	}
	version := strings.TrimSpace(meta.Version)
	if version == "" {
		version = defaultVersion
	}
	license := strings.TrimSpace(meta.License)
	if license == "" {
		license = defaultLicense
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", name)
	fmt.Fprintf(&sb, "description: %s\n", yamlScalar(desc))
	fmt.Fprintf(&sb, "version: %s\n", version)
	fmt.Fprintf(&sb, "license: %s\n", license)
	fmt.Fprintf(&sb, "category: %s\n", canonicalCategory(meta.Category))
	if len(meta.Topics) > 0 {
		fmt.Fprintf(&sb, "topics: [%s]\n", strings.Join(meta.Topics, ", "))
	}
	sb.WriteString(metadataBlock(meta))
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(body))
	sb.WriteString("\n")
	return sb.String()
}

// yamlScalar quotes a description that would otherwise break the block — a
// leading indicator character, or an embedded ": " that YAML would read as a
// mapping. Descriptions are model-written prose and routinely contain both.
func yamlScalar(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(s[:1], "&*?|-<>=!%@`{}[]#,\"'") ||
		strings.Contains(s, ": ") || strings.HasSuffix(s, ":")
	if !needsQuote {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// metadataBlock re-emits the requires/install metadata the model declared.
//
// Rewriting the frontmatter must not silently drop a skill's tool requirements:
// agentrunner.resolveSkillBins reads them at run time, and a skill whose tools
// never resolve fails with a misleading "tool not installed" rather than at
// parse time.
func metadataBlock(m skilllibrary.SkillMeta) string {
	if len(m.RequiresBins) == 0 && len(m.AnyBins) == 0 &&
		len(m.RequiresEnv) == 0 && len(m.Install) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("metadata:\n")
	if len(m.RequiresBins) > 0 || len(m.AnyBins) > 0 || len(m.RequiresEnv) > 0 {
		sb.WriteString("  requires:\n")
		if len(m.RequiresBins) > 0 {
			fmt.Fprintf(&sb, "    bins: [%s]\n", strings.Join(m.RequiresBins, ", "))
		}
		if len(m.AnyBins) > 0 {
			fmt.Fprintf(&sb, "    anyBins: [%s]\n", strings.Join(m.AnyBins, ", "))
		}
		if len(m.RequiresEnv) > 0 {
			fmt.Fprintf(&sb, "    env: [%s]\n", strings.Join(m.RequiresEnv, ", "))
		}
	}
	if len(m.Install) > 0 {
		sb.WriteString("  install:\n")
		for _, sp := range m.Install {
			fmt.Fprintf(&sb, "    - kind: %s\n", sp.Kind)
			if sp.Bin != "" {
				fmt.Fprintf(&sb, "      bin: %s\n", sp.Bin)
			}
			if sp.Package != "" {
				fmt.Fprintf(&sb, "      package: %s\n", sp.Package)
			}
			if sp.URL != "" {
				fmt.Fprintf(&sb, "      url: %s\n", sp.URL)
			}
			if len(sp.Bins) > 0 {
				fmt.Fprintf(&sb, "      bins: [%s]\n", strings.Join(sp.Bins, ", "))
			}
			if sp.Strip != 0 {
				fmt.Fprintf(&sb, "      strip: %d\n", sp.Strip)
			}
		}
	}
	return sb.String()
}
