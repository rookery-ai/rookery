package connectors

import "net/http"

// applyAuth injects the connection credential into req per the provider's auth block.
// Default (no block / kind=="oauth2") is the legacy Authorization: Bearer <token>.
// connExtra resolves any {{conn.<key>}} references in a templated Basic username
// (e.g. Zendesk's "{{conn.email}}/token").
func applyAuth(req *http.Request, prov Provider, credential string, connExtra map[string]string) {
	a := prov.Auth
	// Keyless providers have no credential: leave the request exactly as rendered.
	// The default branch below would set "Authorization: Bearer " with an empty
	// value, which is worse than no header at all.
	if a.Kind == "none" {
		return
	}
	if a.Kind != "api_key" {
		req.Header.Set("Authorization", "Bearer "+credential)
		return
	}
	switch a.Placement {
	case "query":
		q := req.URL.Query()
		q.Set(a.ParamName, credential)
		req.URL.RawQuery = q.Encode()
	case "basic":
		switch {
		case a.BasicUserTemplate != "":
			user := subst(a.BasicUserTemplate, nil, connExtra)
			req.SetBasicAuth(user, credential)
		case a.BasicPassLiteral != "":
			// The inverse arrangement: the credential is the USERNAME and the password
			// is a fixed constant. Toggl Track wants literally "api_token" there.
			req.SetBasicAuth(credential, a.BasicPassLiteral)
		default:
			req.SetBasicAuth(credential, "")
		}
	default: // "header"
		name := a.HeaderName
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, a.ValuePrefix+credential)
	}
}
