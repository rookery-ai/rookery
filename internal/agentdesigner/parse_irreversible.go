package agentdesigner

import "strings"

// ParseIrreversibleLine reads AGENT.md's declaration of whether this agent's job
// involves something that cannot be undone — paying, ordering, transferring,
// deleting.
//
// It follows the same shape as the `# Skills:` and `# Connections:` headers: the
// implementation prompt asks for one line, and this reads it back. The value is
// a FINDING, not a permission — it decides whether the owner is shown a
// permission at all, so an agent that only reads is never asked to think about
// payments.
//
// Deliberately tolerant in one direction only. A missing or unparseable header
// reads as FALSE, because the alternative — defaulting to "this agent pays for
// things" — would put the warning on every agent built by a model that forgot
// the line, and a warning that appears everywhere is one nobody reads. The cost
// of a false negative is covered elsewhere: the first run that actually attempts
// an irreversible action is refused and records the finding itself, so the
// permission appears then instead.
func ParseIrreversibleLine(agentMD string) bool {
	for _, raw := range strings.Split(agentMD, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.HasPrefix(line, "#") {
			continue
		}
		head, value, ok := splitIrreversibleHeader(line)
		if !ok {
			continue
		}
		if !isIrreversibleHeading(head) {
			continue
		}
		return parseYes(value)
	}
	return false
}

// splitIrreversibleHeader splits "# Irreversible actions: yes" into its heading
// and value on the first separator.
func splitIrreversibleHeader(line string) (string, string, bool) {
	trimmed := strings.TrimLeft(line, "#")
	for _, sep := range []string{":", "-", "="} {
		if i := strings.Index(trimmed, sep); i > 0 {
			return strings.TrimSpace(trimmed[:i]), strings.TrimSpace(trimmed[i+1:]), true
		}
	}
	return "", "", false
}

// isIrreversibleHeading matches the wordings a model actually produces around
// the phrase we asked for, rather than only the exact string.
func isIrreversibleHeading(head string) bool {
	h := strings.ToLower(strings.TrimSpace(head))
	h = strings.TrimSuffix(h, "s")
	switch h {
	case "irreversible", "irreversible action", "irreversible step",
		"destructive", "destructive action", "spends money", "irreversible?":
		return true
	}
	return false
}

// parseYes reads the affirmative forms a model uses. Anything else — including
// an empty value — is false, per the one-directional tolerance above.
func parseYes(v string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(v), "`*_.\"'")) {
	case "yes", "true", "y", "1", "required", "needed":
		return true
	}
	return false
}
