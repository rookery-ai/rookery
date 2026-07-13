package agentdesigner

import (
	"regexp"
	"strings"

	"github.com/ilijad1/simple-agents/internal/db"
)

var connHeaderRE = regexp.MustCompile(`(?im)^#{1,6}\s*connections\s*[:\-=]\s*(.+)$`)

// Note: '/' is NOT a separator here (unlike skills) because "provider/label" uses it.
var connSplitRE = regexp.MustCompile(`[,;|+&\n]`)

// parseConnectionsLine reads the "# Connections:" header the designer emits in AGENT.md
// and returns the connection IDs the agent declared, matched (case-insensitively) by
// "provider/label", bare label, account identity, or bare provider name (which binds
// ALL of that provider's connections). Mirrors parseSkillsLine's tolerance.
//
// Contract: returns nil ONLY when no "# Connections:" header exists at all (caller
// treats as "declared none"); a present-but-empty/"none" header returns a non-nil
// empty slice.
func parseConnectionsLine(agentMD string, available []db.ServiceConnection) []string {
	m := connHeaderRE.FindStringSubmatch(agentMD)
	if m == nil {
		return nil
	}
	rest := strings.TrimSpace(m[1])
	if rest == "" || strings.EqualFold(rest, "none") {
		return []string{}
	}
	var out []string
	seen := map[string]bool{}
	for _, tok := range connSplitRE.Split(rest, -1) {
		t := strings.Trim(strings.TrimSpace(tok), "`'\"")
		if t == "" {
			continue
		}
		for _, conn := range available {
			if !seen[conn.ID] && matchesConn(t, conn) {
				out = append(out, conn.ID)
				seen[conn.ID] = true
			}
		}
	}
	return out
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
