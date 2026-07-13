package connectors

import (
	"encoding/json"
	"strings"
)

// ToolDef is one connector action exposed to a coder as a callable tool. It is the
// coder-agnostic definition both the API engine (→ llm.Tool) and the CLI bridge
// (→ subcommand + prompt listing) build from, so tool names/params/mutating are defined
// exactly once. Conn+Action identify what the name resolves to.
type ToolDef struct {
	Name        string          // gmail_send_email, or gmail_send_email__work (multi-account)
	Description string          // action description, account-prefixed when multi-account
	Params      json.RawMessage // JSON-schema for the args
	Mutating    bool
	Conn        BoundConn
	Action      string // the bare action name (no account suffix)
}

// slugLabel reduces a free-text account label to the character set allowed in an LLM
// tool/function name (^[a-zA-Z0-9_-]+). Without this a label like "My Work" would produce
// an invalid tool name and a provider would reject the whole tool list.
func slugLabel(s string) string {
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
		return "acct"
	}
	return b.String()
}

func providerCounts(bound []BoundConn) map[string]int {
	m := map[string]int{}
	for _, b := range bound {
		m[b.Provider]++
	}
	return m
}

// ToolDefs returns the tool definitions for every action of every bound connection.
// Single account of a provider → bare action name; multiple accounts of one provider →
// each action suffixed __<slug(label)> so the model targets an account by tool name.
func (r *Registry) ToolDefs(bound []BoundConn) []ToolDef {
	if r == nil || len(bound) == 0 {
		return nil
	}
	counts := providerCounts(bound)
	var out []ToolDef
	for _, b := range bound {
		multi := counts[b.Provider] > 1
		for _, a := range r.Actions(b.Provider) {
			name := a.Name
			desc := a.Description
			if multi {
				name = a.Name + "__" + slugLabel(b.AccountLabel)
				desc = "[" + b.AccountLabel + " / " + b.AccountIdentity + "] " + desc
			}
			out = append(out, ToolDef{
				Name: name, Description: desc, Params: a.Params,
				Mutating: a.Mutating, Conn: b, Action: a.Name,
			})
		}
	}
	return out
}

// ResolveTool maps a tool name back to (connection, bare action). It reverses the
// __<slug> suffix used for multi-account providers.
func (r *Registry) ResolveTool(bound []BoundConn, name string) (BoundConn, string, bool) {
	if r == nil {
		return BoundConn{}, "", false
	}
	counts := providerCounts(bound)
	base, label := name, ""
	if i := strings.LastIndex(name, "__"); i >= 0 {
		base, label = name[:i], name[i+2:]
	}
	for _, b := range bound {
		if _, ok := r.Action(b.Provider, base); !ok {
			continue
		}
		if counts[b.Provider] > 1 {
			if slugLabel(b.AccountLabel) == label {
				return b, base, true
			}
			continue
		}
		if label == "" {
			return b, base, true
		}
	}
	return BoundConn{}, "", false
}
