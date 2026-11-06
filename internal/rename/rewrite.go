// Package rename renames an agent or a user skill.
//
// The rule it implements: STRUCTURAL references are rewritten and prose is left
// alone. A structural reference is one the platform generated or resolves by name,
// so leaving it stale would break something or show a name that no longer exists. A
// sentence that happens to mention the old name is the owner's writing, and blind
// find-and-replace over a knowledge base is how a skill called "notes" ends up
// rewriting every notes/ path in the vault.
package rename

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidName reports a name that cannot be used. Its wrapped message is safe to
// show the owner.
var ErrInvalidName = errors.New("invalid name")

// maxAgentNameRunes bounds an agent name; the point of many renames is a shorter one.
const maxAgentNameRunes = 120

// ValidateAgentName trims a proposed agent name and refuses what would break the
// constructs a name is written into: [ ] and | end a wikilink or a [CALL: …] line, and
// # starts a wikilink's heading.
func ValidateAgentName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: a name is required", ErrInvalidName)
	}
	if utf8.RuneCountInString(name) > maxAgentNameRunes {
		return "", fmt.Errorf("%w: keep it to %d characters", ErrInvalidName, maxAgentNameRunes)
	}
	if strings.ContainsAny(name, "[]|#") {
		return "", fmt.Errorf("%w: a name cannot contain [ ] | or #", ErrInvalidName)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: a name must be a single line of text", ErrInvalidName)
		}
	}
	return name, nil
}

// RewriteAgentCalls rewrites [CALL: old]. It is case-sensitive because the runner
// resolves a call with an exact GetAgentByName: a differently-cased call is already
// "agent not found", and rewriting it would change what that agent does.
func RewriteAgentCalls(text, oldName, newName string) string {
	return strings.ReplaceAll(text, "[CALL: "+oldName+"]", "[CALL: "+newName+"]")
}

// RewriteAgentTitle rewrites the first line that is exactly "# old". That line is a
// generated title; any other mention in AGENT.md is an instruction to the agent.
func RewriteAgentTitle(text, oldName, newName string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " \t\r") == "# "+oldName {
			lines[i] = strings.Replace(line, oldName, newName, 1)
			break
		}
	}
	return strings.Join(lines, "\n")
}

// RewriteStateHeading rewrites state.md's first line when it is the template's heading.
// It deliberately splices that one line rather than going through agentstate, whose
// writers re-render the file: a rename has no business normalising an agent's memory.
func RewriteStateHeading(text, oldName, newName string) string {
	first, rest, hadNewline := strings.Cut(text, "\n")
	if strings.TrimRight(first, " \t\r") != "# State — "+oldName {
		return text
	}
	first = strings.Replace(first, oldName, newName, 1)
	if !hadNewline {
		return first
	}
	return first + "\n" + rest
}

// RewriteRunHeading rewrites the "# Run of [[old]]" heading of a run note and nothing
// else: everything below it is what the run actually said at the time.
func RewriteRunHeading(text, oldName, newName string) string {
	prefix := "# Run of [[" + oldName + "]]"
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = "# Run of [[" + newName + "]]" + strings.TrimPrefix(line, prefix)
			break
		}
	}
	return strings.Join(lines, "\n")
}

// isTokenChar reports a byte that continues a name rather than ending it. Multi-byte
// runes count as boundaries, which is right for the ASCII-only skill slugs and harmless
// for an agent name, where ContainsToken only drives a report.
func isTokenChar(b byte) bool {
	return b == '_' || b == '-' ||
		(b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// replaceToken replaces whole-token occurrences of old. A scan rather than a regexp
// with boundary groups, which would consume the separator and miss "a a".
func replaceToken(text, oldTok, newTok string) string {
	if oldTok == "" {
		return text
	}
	var b strings.Builder
	rest := 0
	for from := 0; ; {
		i := strings.Index(text[from:], oldTok)
		if i < 0 {
			break
		}
		start := from + i
		end := start + len(oldTok)
		if (start == 0 || !isTokenChar(text[start-1])) && (end == len(text) || !isTokenChar(text[end])) {
			b.WriteString(text[rest:start])
			b.WriteString(newTok)
			rest = end
			from = end
			continue
		}
		from = start + 1
	}
	if rest == 0 {
		return text
	}
	b.WriteString(text[rest:])
	return b.String()
}

// ContainsToken reports a whole-token occurrence of name.
func ContainsToken(text, name string) bool {
	return replaceToken(text, name, "\x00") != text
}

var (
	// skillsHeadingRE matches an AGENT.md "# Skills:" header. The # is required here,
	// unlike the designer's more forgiving parser: that one reads a declaration, while
	// this one edits text, and "Skills: my notes" in a sentence is not a declaration.
	skillsHeadingRE = regexp.MustCompile(`(?i)^(\s*#{1,6}\s*(?:required\s+|needed\s+|uses?\s+)?skills?\s*(?:[:\-=]\s*|$))(.*)$`)
	skillBulletRE   = regexp.MustCompile(`^\s*(?:#\s*)?(?:[-*+]|\d+\.)\s+`)
)

// RewriteSkillRefs rewrites references to a skill in text the platform or an agent acts
// on. A hyphenated name is distinctive, so every whole token is a reference. A one-word
// name may be an ordinary word, so only forms that cannot be prose are touched.
func RewriteSkillRefs(text, oldName, newName string) string {
	if strings.Contains(oldName, "-") {
		return replaceToken(text, oldName, newName)
	}
	for _, form := range []string{"skills/%s", "envs/%s", `env="%s"`, "env='%s'", "env=%s", "--env %s", "--env=%s"} {
		text = replaceToken(text, fmt.Sprintf(form, oldName), fmt.Sprintf(form, newName))
	}
	lines := strings.Split(text, "\n")
	inList := false
	for i, line := range lines {
		if m := skillsHeadingRE.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + replaceToken(m[2], oldName, newName)
			inList = strings.TrimSpace(m[2]) == ""
			continue
		}
		if !inList {
			if t := strings.TrimRight(line, " \t\r"); t == "name: "+oldName || t == `name: "`+oldName+`"` || t == "name: '"+oldName+"'" {
				lines[i] = strings.Replace(line, oldName, newName, 1)
			}
			continue
		}
		if skillBulletRE.MatchString(line) {
			lines[i] = replaceToken(line, oldName, newName)
			continue
		}
		inList = false
	}
	return strings.Join(lines, "\n")
}

// wikilinkRE mirrors vault.wikilinkRE, with the alias's | captured so it can be kept.
var wikilinkRE = regexp.MustCompile(`\[\[([^\]\|]+?)(\|[^\]]+)?\]\]`)

// rewriteWikilinkTargets applies fn to each link's target, keeping a #heading and an
// |alias as written.
func rewriteWikilinkTargets(text string, fn func(target string) (string, bool)) string {
	return wikilinkRE.ReplaceAllStringFunc(text, func(raw string) string {
		m := wikilinkRE.FindStringSubmatch(raw)
		target, heading := m[1], ""
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target, heading = target[:i], target[i:]
		}
		next, ok := fn(strings.TrimSpace(target))
		if !ok {
			return raw
		}
		return "[[" + next + heading + m[2] + "]]"
	})
}

// RewriteWikilinks rewrites [[old]] links, case-insensitively as the link index resolves
// them. The caller must not call it when old resolves to a real note: such a link points
// at that note, not at the agent or skill that shares its name.
func RewriteWikilinks(text, oldName, newName string) string {
	return rewriteWikilinkTargets(text, func(target string) (string, bool) {
		return newName, strings.EqualFold(target, oldName)
	})
}

// RewriteSkillLinks rewrites links that address a file inside the skill's folder, which
// has moved: [[skills/old/…]] and markdown destinations under skills/old. A bare mention
// of the path in a sentence is prose and stays.
func RewriteSkillLinks(text, oldName, newName string) string {
	oldPrefix, newPrefix := "skills/"+oldName, "skills/"+newName
	text = rewriteWikilinkTargets(text, func(target string) (string, bool) {
		if strings.EqualFold(target, oldPrefix) {
			return newPrefix, true
		}
		if len(target) > len(oldPrefix) && strings.EqualFold(target[:len(oldPrefix)+1], oldPrefix+"/") {
			return newPrefix + target[len(oldPrefix):], true
		}
		return "", false
	})
	dest := regexp.MustCompile(`\]\(((?:\.{1,2}/)*)` + regexp.QuoteMeta(oldPrefix) + `([/)])`)
	return dest.ReplaceAllStringFunc(text, func(raw string) string {
		m := dest.FindStringSubmatch(raw)
		return "](" + m[1] + newPrefix + m[2]
	})
}
