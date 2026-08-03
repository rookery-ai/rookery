package connectors

import (
	"errors"
	"net/url"
	"strings"
)

// NormalizeBaseURL canonicalizes a user-supplied self-hosted service base URL so
// action templates can concatenate "{{conn.base_url}}/api/..." safely.
//
// A path PREFIX is allowed and preserved: https://host/nextcloud, and a Paperless-ngx
// behind a reverse proxy at /paperless, are mainstream deployments — rejecting them
// would refuse working installs at connect time. A query string or fragment is not
// allowed: neither can survive having a path concatenated onto it.
//
// Validation happens at CONNECT rather than at action time, because a 404 from a
// mistyped host reads as a broken connector rather than as a typo.
func NormalizeBaseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("base URL is required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("base URL is not a valid URL")
	}
	// A schemeless "host:8123" parses with the host as the SCHEME (scheme grammar
	// allows dots), so this check is what catches a missing scheme — not the host check.
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("base URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", errors.New("base URL must include a host, e.g. https://ha.example.com")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("base URL must not include a query string or #fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}
