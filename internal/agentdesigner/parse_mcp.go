package agentdesigner

import (
	"regexp"
	"strings"

	"github.com/ilijad1/rookery/internal/db"
)

// mcpHeaderRE matches an "# MCP:" / "# MCP servers:" heading and captures the
// (possibly empty) inline value. An empty inline value means the servers are listed
// as bullets on the following lines — the form weak models reach for.
// Horizontal whitespace only ([^\S\r\n]) rather than \s: \s matches a newline, so a
// greedy \s* before the capture group would step onto the NEXT line and read the
// first bullet as the inline value — silently dropping it, since the bullet's own "#"
// is not stripped from an inline value.
var mcpHeaderRE = regexp.MustCompile(`(?im)^#{1,6}[^\S\r\n]*mcp(?:[^\S\r\n]+servers?)?[^\S\r\n]*[:\-=]?[^\S\r\n]*(.*)$`)

// mcpSplitRE deliberately omits '/' as a separator: a server name can contain one,
// and splitting on it would turn "Home/Lab" into two names that match nothing.
var mcpSplitRE = regexp.MustCompile(`[,;|+&\n]`)

// parseMCPLine reads the "# MCP:" header from AGENT.md and returns the server IDs the
// agent declared, mirroring parseConnectionsLine.
//
// Contract, and it is load-bearing: returns nil ONLY when no header exists at all. A
// present-but-empty or "none" header returns a NON-NIL empty slice, which is how
// AutoBindMCPTargets tells "the model said none" from "the model forgot" — the first
// must be honoured, the second must fall back to what the build actually used.
func parseMCPLine(agentMD string, available []*db.MCPServer) []string {
	loc := mcpHeaderRE.FindStringSubmatchIndex(agentMD)
	if loc == nil {
		return nil
	}
	inline := strings.TrimSpace(agentMD[loc[2]:loc[3]])

	// Region = the inline value plus any following bullet/comment lines, so both
	// shapes parse without the caller having to know which one the model chose.
	region := inline
	// The header match ends at the end of its own line, so the remainder opens with
	// the newline that terminates it. Trimming exactly that one prevents the scan
	// below from seeing an empty first line and stopping before the bullets start.
	rest := strings.TrimPrefix(agentMD[loc[1]:], "\n")
	for _, line := range strings.Split(rest, "\n") {
		t := strings.TrimSpace(line)
		t = strings.TrimPrefix(t, "#")
		t = strings.TrimSpace(t)
		if t == "" {
			break
		}
		if !strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "*") {
			break
		}
		region += "\n" + strings.TrimSpace(strings.TrimLeft(t, "-* "))
	}

	out := []string{}
	seen := map[string]bool{}
	for _, tok := range mcpSplitRE.Split(region, -1) {
		tok = strings.TrimSpace(strings.Trim(tok, "`'\"."))
		if tok == "" || strings.EqualFold(tok, "none") {
			continue
		}
		for _, s := range available {
			if seen[s.ID] {
				continue
			}
			if matchesMCPServer(tok, s) {
				out = append(out, s.ID)
				seen[s.ID] = true
			}
		}
	}
	return out
}

func matchesMCPServer(tok string, s *db.MCPServer) bool {
	tok = strings.ToLower(tok)
	for _, cand := range []string{strings.ToLower(s.Name), strings.ToLower(s.Slug)} {
		if cand != "" && cand == tok {
			return true
		}
	}
	// A trailing prose fragment ("Home Assistant (for the lights)") is common enough
	// that a prefix match earns its keep here.
	name := strings.ToLower(s.Name)
	return name != "" && strings.HasPrefix(tok, name)
}

// AutoBindMCPTargets decides an agent's MCP bindings after a build, mirroring
// AutoBindTargets for connections.
//
// Priority: an explicit header always wins, including an explicit "none". Otherwise,
// if the agent is already bound to something, leave it alone — the owner may have set
// it by hand on the agent page, and a rebuild must not silently discard that. Only a
// genuinely unbound agent falls back to the servers the build actually called.
//
// apply=false means "write nothing", which is different from apply=true with an empty
// slice ("the model said none, unbind everything").
func AutoBindMCPTargets(agentMD string, available []*db.MCPServer, existingBound []string, usedFromBuild []string) (ids []string, apply bool) {
	if parsed := parseMCPLine(agentMD, available); parsed != nil {
		return parsed, true
	}
	if len(existingBound) > 0 {
		return nil, false
	}
	valid := map[string]bool{}
	for _, s := range available {
		valid[s.ID] = true
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
