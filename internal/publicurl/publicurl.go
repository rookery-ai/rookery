package publicurl

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

// SettingKey is the system_settings row holding the configured instance URL.
// system_settings is instance-level and not tenant-scoped, which is correct: the
// public URL is a property of the deployment, not of a workspace.
const SettingKey = "public_url"

// Source records where the resolved URL came from, so the UI can tell the user
// whether they configured it or we guessed.
type Source int

const (
	SourceDetected Source = iota
	SourceEnv
	SourceConfigured
)

var errNotAbsolute = errors.New("public url must be an absolute http(s) URL with no path")

// Normalize validates a base URL and returns its canonical form: lowercased
// scheme and host, no trailing slash, no path, query or fragment.
//
// Rejecting a path is not pedantry — the callback route is appended to this
// value, so a stored "https://host/base" would produce a URI that no longer
// matches what the provider has registered.
func Normalize(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errNotAbsolute
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errNotAbsolute
	}
	if u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errNotAbsolute
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return "", errNotAbsolute
	}
	return scheme + "://" + strings.ToLower(u.Host), nil
}

// Resolve returns the instance's public base URL and where it came from.
// Precedence: the configured setting, then ROOKERY_PUBLIC_URL, then what the current
// request suggests. A stored value that fails Normalize is skipped rather than
// used, so a bad setting degrades to detection instead of breaking every
// connection silently.
func Resolve(get func(key string) (string, error), detected string) (string, Source) {
	if get != nil {
		if v, err := get(SettingKey); err == nil {
			if n, nerr := Normalize(v); nerr == nil {
				return n, SourceConfigured
			}
		}
	}
	if n, err := Normalize(os.Getenv("ROOKERY_PUBLIC_URL")); err == nil {
		return n, SourceEnv
	}
	if n, err := Normalize(detected); err == nil {
		return n, SourceDetected
	}
	return detected, SourceDetected
}
