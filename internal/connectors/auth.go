package connectors

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/rookery-ai/rookery/internal/awssig"
)

// applyAuth injects the connection credential into req per the provider's auth block.
// Default (no block / kind=="oauth2") is the legacy Authorization: Bearer <token>.
// connExtra resolves any {{conn.<key>}} references in a templated Basic username
// (e.g. Zendesk's "{{conn.email}}/token").
//
// body is the rendered request body, needed only by kind=="sigv4": AWS signs a
// hash of the payload, so unlike every other scheme this one cannot be applied
// from the headers alone. It is called LAST in Execute for the same reason —
// see the note at that call site.
//
// It returns an error only for a scheme that can fail (sigv4's missing or
// malformed credentials); the header-and-query schemes cannot.
func applyAuth(req *http.Request, prov Provider, credential string, connExtra map[string]string, body []byte) error {
	a := prov.Auth
	// Keyless providers have no credential: leave the request exactly as rendered.
	// The default branch below would set "Authorization: Bearer " with an empty
	// value, which is worse than no header at all.
	if a.Kind == "none" {
		return nil
	}
	if a.Kind == "sigv4" {
		return applySigV4(req, a, credential, connExtra, body)
	}
	if a.Kind != "api_key" {
		req.Header.Set("Authorization", "Bearer "+credential)
		return nil
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
	return nil
}

// applySigV4 signs the request with AWS Signature Version 4.
//
// The credential here is the SECRET access key, which is why it travels in
// encrypted_access_token like every other connector credential. The access key
// ID and the region are NOT secrets — the key id appears in the Authorization
// header of every signed request — so they come from the connection's `extra`,
// which is plaintext JSON by design.
//
// Service and region are per-CONNECTION rather than per-provider because one
// AWS connection reaches many services and every region is a different signing
// scope. The service name for a given action comes from the action's own
// connect-input default; the region is the user's.
func applySigV4(req *http.Request, a AuthConfig, secretKey string, connExtra map[string]string, body []byte) error {
	region := connExtra[argOr(a.RegionArg, "region")]
	service := connExtra[argOr(a.ServiceArg, "service")]
	sum := sha256.Sum256(body)
	return awssig.Sign(req,
		awssig.Credentials{
			AccessKey: connExtra[argOr(a.AccessKeyArg, "access_key_id")],
			SecretKey: secretKey,
		},
		region, service, hex.EncodeToString(sum[:]), time.Now())
}

func argOr(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}
