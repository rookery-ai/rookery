package agentdesigner

import (
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/db"
)

// connHeaderRE matches the "# Connections:" heading and captures the (possibly empty)
// inline value. An empty inline value means the accounts are listed as bullets on the
// following lines (a form weak models like to use).
var connHeaderRE = regexp.MustCompile(`(?im)^#{1,6}\s*connections\s*[:\-=]?\s*(.*)$`)

// Note: '/' is NOT a token separator here (unlike skills) because "provider/label" uses it.
var connSplitRE = regexp.MustCompile(`[,;|+&\n]`)

// parseConnectionsLine reads the "# Connections:" header the designer emits in AGENT.md
// and returns the connection IDs the agent declared. It handles BOTH forms:
//   - inline:  "# Connections: google/personal, github/work"
//   - bullets: "# Connections:\n# - google account \"personal\" — me@x.com"
//
// Matching is tolerant: a connection binds if the header region contains its account
// identity or "provider/label", or a token equals its provider, label, provider/label,
// or identity (a bare provider name binds ALL of that provider's connections).
//
// Contract: returns nil ONLY when no "# Connections:" header exists at all; a present-but
// -empty / "none" header returns a non-nil empty slice.
func parseConnectionsLine(agentMD string, available []db.ServiceConnection) []string {
	loc := connHeaderRE.FindStringSubmatchIndex(agentMD)
	if loc == nil {
		return nil
	}
	inline := strings.TrimSpace(agentMD[loc[2]:loc[3]])

	// Region = inline value + following bullet/comment list lines (the form weak models
	// use). parts[0] is the empty boundary right after the header line; start at parts[1]
	// and collect only list items (-, *, •, #) so prose body lines are never swallowed.
	region := inline
	parts := strings.Split(agentMD[loc[1]:], "\n")
	for i := 1; i < len(parts); i++ {
		t := strings.TrimSpace(parts[i])
		if t == "" || !(strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "•") || strings.HasPrefix(t, "#")) {
			break
		}
		region += "\n" + t
	}
	if rt := strings.TrimSpace(region); rt == "" || strings.EqualFold(rt, "none") {
		return []string{}
	}

	low := strings.ToLower(region)
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}

	// An identity may only be used for the loose contains-match below when it is an
	// email AND belongs to exactly one connection. Both halves are load-bearing, and
	// both were learned from a real over-grant.
	//
	// A binding hands an agent live credentials, so a wrong one is access, not
	// untidiness. The old rule substring-matched EVERY identity against the whole
	// header region, which broke twice over on an ordinary workspace:
	//
	//   - Short, generic identities are shared. "test" belonged to adguard, mailchimp
	//     AND stripe, so the header "# Connections: adguard/test, google_sheets/…"
	//     contained the substring "test" and granted a DNS watchdog the owner's
	//     payment and mailing-list credentials.
	//   - Family identities are shared by design. Every google_* child carries the
	//     same address, so one email in a header bound Drive and Docs alongside the
	//     Sheets account that was actually named.
	//
	// Requiring an "@" keeps the case this match exists for — the bullet form a weak
	// model writes, "google account \"personal\" — me@x.com" — while refusing to treat
	// a bare word like "test" or "personal" as an identifier. Requiring uniqueness
	// refuses an address that cannot single anything out. Everything excluded here is
	// still reachable through provider/label or exact-token matching below, so the
	// cost is at worst under-binding, which the user fixes with a checkbox; the cost
	// of the alternative is credentials granted to an agent that never asked for them.
	identityCount := map[string]int{}
	for _, c := range available {
		if id := strings.ToLower(strings.TrimSpace(c.AccountIdentity)); id != "" {
			identityCount[id]++
		}
	}

	// 1. Robust contains-match: a distinctive account identity, or "provider/label",
	//    appears verbatim somewhere in the region (handles the bullet form).
	for _, c := range available {
		ident := strings.ToLower(strings.TrimSpace(c.AccountIdentity))
		distinctive := ident != "" && strings.Contains(ident, "@") && identityCount[ident] == 1
		if distinctive && strings.Contains(low, ident) {
			add(c.ID)
		} else if strings.Contains(low, strings.ToLower(c.Provider+"/"+c.AccountLabel)) {
			add(c.ID)
		}
	}
	// 2. Token match for the inline comma/pipe list form (provider, label, provider/label).
	for _, tok := range connSplitRE.Split(region, -1) {
		t := strings.Trim(strings.TrimSpace(tok), "`'\"#-*• ")
		if t == "" || strings.EqualFold(t, "none") {
			continue
		}
		for _, c := range available {
			if matchesConn(t, c) {
				add(c.ID)
			}
		}
	}
	return out
}

// AutoBindTargets decides which connection IDs to bind to an agent after a build.
//   - If AGENT.md has a `# Connections:` header (even empty/"none"), it WINS — return its parse.
//   - Otherwise (no header): if the agent already has bindings, leave them untouched (return
//     apply=false); else auto-bind the connections the build actually used, filtered to the
//     workspace's available connections. Never binds all; never clobbers existing.
func AutoBindTargets(agentMD string, available, existingBound []db.ServiceConnection, usedFromBuild []string) (ids []string, apply bool) {
	if parsed := parseConnectionsLine(agentMD, available); parsed != nil {
		return parsed, true // explicit header wins (incl. empty / "none")
	}
	if len(existingBound) > 0 {
		return nil, false // no header + already bound → don't touch
	}
	valid := map[string]bool{}
	for _, c := range available {
		valid[c.ID] = true
	}
	var used []string
	seen := map[string]bool{}
	for _, id := range usedFromBuild {
		if valid[id] && !seen[id] {
			used = append(used, id)
			seen[id] = true
		}
	}
	if len(used) == 0 {
		return nil, false
	}
	return used, true
}

func matchesConn(tok string, c db.ServiceConnection) bool {
	tok = strings.ToLower(tok)
	cands := []string{
		strings.ToLower(c.Provider + "/" + c.AccountLabel),
		strings.ToLower(c.AccountLabel),
		strings.ToLower(c.AccountIdentity),
	}
	for _, cnd := range cands {
		if cnd != "" && cnd == tok {
			return true
		}
	}
	return tok == strings.ToLower(c.Provider) // bare provider → all its connections
}
