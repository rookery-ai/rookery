// Package skilllibrary embeds the core skill catalog — the adapted,
// standards-based defaults shipped with the binary and always-on for every user.
// Core skills are read from the embed at runtime: no DB rows, no disk seeding,
// no admin gate. Users create/import their own skills separately (see skillstore);
// core skills simply supplement the available-skills pool for every user.
//
//   - ParseMeta: parse a SKILL.md's Anthropic+ClawHub frontmatter (name,
//     description, version, license, category, metadata.openclaw.requires,
//     metadata.openclaw.install) using a real YAML parser.
//   - LoadBundled: enumerate the embedded core skills (metadata only).
//   - CoreSkillContent: the full SKILL.md body of a core skill (for agent
//     injection when an agent declares it).
//   - IsCoreSkill: whether a slug names a core skill.
//
// Skills follow the industry-standard folder layout: skills/<name>/SKILL.md plus
// optional scripts/, companion .md files, LICENSE.txt. The whole folder is the
// unit of distribution; only SKILL.md's body enters agent context (progressive
// disclosure).
package skilllibrary

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed all:skills
var skillsFS embed.FS

// InstallSpec is one entry of metadata.openclaw.install — how to provision a
// required tool. ClawHub kinds brew/node/go/uv are recognized; we add `binary`
// (static download) and `pip` for the simple-agents sandbox.
type InstallSpec struct {
	Kind    string   `yaml:"kind" json:"kind"`
	Bin     string   `yaml:"bin" json:"bin,omitempty"`
	Package string   `yaml:"package" json:"package,omitempty"`
	URL     string   `yaml:"url" json:"url,omitempty"`
	Strip   int      `yaml:"strip" json:"strip,omitempty"`
	Bins    []string `yaml:"bins" json:"bins,omitempty"`
}

// SkillMeta is the parsed frontmatter of a SKILL.md.
type SkillMeta struct {
	Name         string
	Description  string
	Category     string
	Version      string
	License      string
	Topics       []string
	RequiresBins []string // metadata.openclaw.requires.bins (all must be present)
	AnyBins      []string // metadata.openclaw.requires.anyBins (at least one)
	RequiresEnv  []string // metadata.openclaw.requires.env
	Install      []InstallSpec
}

type frontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Version     string   `yaml:"version"`
	License     string   `yaml:"license"`
	Category    string   `yaml:"category"`
	Topics      []string `yaml:"topics"`
	Metadata    struct {
		Openclaw struct {
			Requires struct {
				Bins    []string `yaml:"bins"`
				AnyBins []string `yaml:"anyBins"`
				Env     []string `yaml:"env"`
			} `yaml:"requires"`
			Install []InstallSpec `yaml:"install"`
		} `yaml:"openclaw"`
	} `yaml:"metadata"`
}

// ParseMeta splits a SKILL.md into frontmatter (parsed) and body. If there is no
// frontmatter block, it returns the whole content as body with an empty meta.
func ParseMeta(content string) (SkillMeta, string) {
	var meta SkillMeta
	text := strings.TrimSpace(content)
	if !strings.HasPrefix(text, "---") {
		return meta, content
	}
	rest := text[3:]
	// Find the closing "---" on its own line.
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return meta, content
	}
	front := rest[:endIdx]
	body := strings.TrimSpace(rest[endIdx+4:])

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		// Fall back: leave meta empty, return body so the skill is still usable.
		return meta, content
	}

	meta.Name = fm.Name
	meta.Description = fm.Description
	meta.Category = fm.Category
	meta.Version = fm.Version
	meta.License = fm.License
	meta.Topics = fm.Topics
	meta.RequiresBins = fm.Metadata.Openclaw.Requires.Bins
	meta.AnyBins = fm.Metadata.Openclaw.Requires.AnyBins
	meta.RequiresEnv = fm.Metadata.Openclaw.Requires.Env
	meta.Install = fm.Metadata.Openclaw.Install
	return meta, body
}

// bundledSkills enumerates every skill folder embedded under skills/, returning
// the slug → raw SKILL.md content pairs.
func bundledSkills() (map[string]string, error) {
	out := make(map[string]string)
	entries, err := fs.ReadDir(skillsFS, "skills")
	if err != nil {
		return nil, fmt.Errorf("read embedded skills dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		skillMD, err := fs.ReadFile(skillsFS, filepath.ToSlash(filepath.Join("skills", slug, "SKILL.md")))
		if err != nil {
			slog.Warn("skilllibrary: core skill missing SKILL.md", "slug", slug, "err", err)
			continue
		}
		out[slug] = string(skillMD)
	}
	return out, nil
}

// LoadBundled returns the metadata of every core skill, sorted by name. Core
// skills are always-on for every user; this feeds the available-skills pool used
// by the agent designer and the runner.
func LoadBundled() []SkillMeta {
	bySlug, err := bundledSkills()
	if err != nil {
		slog.Error("skilllibrary: failed to load core skills", "err", err)
		return nil
	}
	slugs := make([]string, 0, len(bySlug))
	for slug := range bySlug {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	out := make([]SkillMeta, 0, len(slugs))
	for _, slug := range slugs {
		meta, _ := ParseMeta(bySlug[slug])
		if meta.Name == "" {
			meta.Name = slug
		}
		if meta.Version == "" {
			meta.Version = "1.0.0"
		}
		out = append(out, meta)
	}
	return out
}

// IsCoreSkill reports whether slug names a core skill (an embedded folder).
// Folder names are lowercase, but callers pass slugs from varied sources
// (URL params typed by hand, LLM-parsed "# Skills:" headers, …) that don't
// always match case — normalize once here so every call site gets
// case-insensitive matching for free.
func IsCoreSkill(slug string) bool {
	if slug == "" {
		return false
	}
	slug = strings.ToLower(slug)
	_, err := fs.Stat(skillsFS, filepath.ToSlash(filepath.Join("skills", slug, "SKILL.md")))
	return err == nil
}

// CoreSkillContent returns the full SKILL.md body (frontmatter + body) of the
// core skill identified by slug, for injection into an agent's context when the
// agent declares the skill. ok is false if no such core skill exists. The slug
// may be either the folder name or the frontmatter `name` (they usually match).
func CoreSkillContent(slug string) (string, bool) {
	if content, ok := readCoreSkill(slug); ok {
		return content, true
	}
	// Fall back: try matching by frontmatter name.
	for slug2, meta := range coreMetaByName() {
		if meta == slug {
			if content, ok := readCoreSkill(slug2); ok {
				return content, true
			}
		}
	}
	return "", false
}

func readCoreSkill(slug string) (string, bool) {
	data, err := fs.ReadFile(skillsFS, filepath.ToSlash(filepath.Join("skills", slug, "SKILL.md")))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// coreMetaByName returns a slug → frontmatter-name map for fallback resolution.
func coreMetaByName() map[string]string {
	out := map[string]string{}
	bySlug, err := bundledSkills()
	if err != nil {
		return out
	}
	for slug, content := range bySlug {
		meta, _ := ParseMeta(content)
		if meta.Name != "" {
			out[slug] = meta.Name
		}
	}
	return out
}
