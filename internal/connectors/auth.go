package connectors

import "net/http"

// applyAuth injects the connection credential into req per the provider's auth block.
// Default (no block / kind=="oauth2") is the legacy Authorization: Bearer <token>.
func applyAuth(req *http.Request, prov Provider, credential string) {
	a := prov.Auth
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
		req.SetBasicAuth(credential, "")
	default: // "header"
		name := a.HeaderName
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, a.ValuePrefix+credential)
	}
}
