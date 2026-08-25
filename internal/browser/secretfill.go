package browser

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// SecretResolver looks up one secret value for a workspace. The browser layer
// takes this as a narrow function rather than importing internal/secrets, for
// the same reason internal/connalert takes a SendToUser: it keeps the
// dependency pointing one way and lets a test drive it without a database.
type SecretResolver func(ctx context.Context, workspaceID, name string) (string, error)

var secretPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ResolveSecretValue substitutes ${NAME} placeholders in a value about to be
// typed into a page, and reports whether anything was substituted.
//
// It deliberately does NOT reuse secrets.Service.Proxy, despite sharing the
// syntax, for two reasons that matter here and not there:
//
//   - Proxy leaves an unresolvable placeholder AS-IS. In its own context
//     (prompt text) that is a sane degradation. Here it would type the literal
//     string "${CARD_NUMBER}" into a payment field — a silent, confusing
//     failure on exactly the flows where confusion is most expensive. This
//     fails closed instead, naming the missing secret.
//   - Proxy's doc comment restricts it to AgentRunner.Run(). Quietly widening
//     the set of callers to a "must not be logged" guarantee is how that
//     guarantee stops being true.
//
// The error names the SECRET, never its value: an error string is the one part
// of a tool result that routinely gets logged.
func ResolveSecretValue(ctx context.Context, resolve SecretResolver, workspaceID, value string) (string, bool, error) {
	matches := secretPlaceholder.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return value, false, nil
	}
	if resolve == nil {
		return "", false, fmt.Errorf("this run cannot use ${...} secrets in the browser")
	}
	out := value
	for _, m := range matches {
		name := m[1]
		v, err := resolve(ctx, workspaceID, name)
		if err != nil || v == "" {
			return "", false, fmt.Errorf("no secret named %s is available to this agent — "+
				"ask the user to add it, and do not type the placeholder or a guess into the page", name)
		}
		out = strings.ReplaceAll(out, m[0], v)
	}
	return out, true, nil
}

// MentionsSecret reports whether a value carries a placeholder, without
// resolving it. Used to decide whether a value may be echoed in a progress
// milestone: the placeholder is safe to show, the resolved value never is.
func MentionsSecret(value string) bool {
	return secretPlaceholder.MatchString(value)
}

// SafeForDisplay renders a fill value for a progress line or an audit entry.
// A value carrying a placeholder is shown with the placeholder intact; a plain
// value is shown as-is, because the model chose it and the user may need to see
// what was typed.
func SafeForDisplay(value string) string {
	if MentionsSecret(value) {
		return value
	}
	if len(value) > 60 {
		return value[:60] + "…"
	}
	return value
}
