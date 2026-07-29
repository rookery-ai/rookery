# OAuth Redirect Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make OAuth connector setup completable by a non-technical user, and make every failure state name its own remedy instead of surfacing a raw provider error.

**Architecture:** A new `internal/publicurl` package owns the instance's public base URL and judges it against a per-provider redirect policy declared as YAML data. The resolved URI is displayed in the UI, checked before consent, and pinned into the signed OAuth state so consent and token exchange cannot disagree. Failures map to a taxonomy that carries a fix.

**Tech Stack:** Go 1.26, Echo v4, `golang.org/x/net/publicsuffix` (already a direct dependency), React 19 + Vite + Vitest, SQLite via `modernc.org/sqlite`.

## Global Constraints

- **Never commit to `main`.** All work on this branch; merge via squashed PR.
- **Conventional Commits** for every commit: `type(scope): summary`.
- The callback path `/dashboard/connectors/services/callback/:provider` is **FROZEN** — it is a registered external redirect URI. `web/spa_test.go:108` pins it.
- **No new Go module dependencies.** `golang.org/x/net` is already required at `go.mod:18`; `publicsuffix` is a subpackage of it.
- **Hard-blocking requires `verified: true`** on a provider's policy. A policy we have not confirmed against live documentation may only warn.
- `make ci` must pass before the PR (`ci-fmt`, `ci-vet`, `ci-test`, `ci-cross`, `ci-ui`).
- Go tests run with `-race`; the `web` package is slow under race (~343s) — budget for it.
- Every new `/api/v1` route must be added to the `want` table in `web/api_parity_test.go` or the parity test fails.

---

### Task 1: `publicurl` host classification and policy check

The pure core. No I/O, no DB, no HTTP. This is where correctness is locked in.

**Files:**
- Create: `internal/publicurl/policy.go`
- Test: `internal/publicurl/policy_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Severity int` (`SeveritySoft`, `SeverityHard`); `type Problem struct{Severity Severity; Code, Message, Fix string}`; `type Policy struct{Scheme, AllowRawIP string; RequirePublicHost, Verified bool}`; `func Check(base string, p Policy) []Problem`.

- [ ] **Step 1: Write the failing test**

Create `internal/publicurl/policy_test.go`:

```go
package publicurl

import "testing"

// googlePolicy mirrors the verified google provider policy from the spec.
var googlePolicy = Policy{
	Scheme:            "https_or_loopback",
	AllowRawIP:        "loopback_only",
	RequirePublicHost: true,
	Verified:          true,
}

func codes(ps []Problem) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func hasCode(ps []Problem, code string) *Problem {
	for i := range ps {
		if ps[i].Code == code {
			return &ps[i]
		}
	}
	return nil
}

func TestCheckGooglePolicy(t *testing.T) {
	cases := []struct {
		name      string
		base      string
		wantCode  string // "" means no problems
		wantSev   Severity
	}{
		{"localhost is exempt", "http://localhost:8080", "", 0},
		{"loopback ip is exempt", "http://127.0.0.1:8080", "", 0},
		{"ipv6 loopback is exempt", "http://[::1]:8080", "", 0},
		{"public https domain is clean", "https://agents.example.com", "", 0},
		{"multi-label suffix is clean", "https://agents.example.co.uk", "", 0},
		{"lan ip rejected", "http://192.168.1.194:8080", "raw_ip", SeverityHard},
		{"reserved tld rejected", "https://agents.rookie.lan", "non_public_host", SeverityHard},
		{"dot-local rejected", "https://box.local", "non_public_host", SeverityHard},
		{"dotless host rejected", "https://rookie", "non_public_host", SeverityHard},
		{"plain http public domain rejected", "http://agents.example.com", "scheme_not_https", SeverityHard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(tc.base, googlePolicy)
			if tc.wantCode == "" {
				if len(got) != 0 {
					t.Fatalf("want no problems, got %v", codes(got))
				}
				return
			}
			p := hasCode(got, tc.wantCode)
			if p == nil {
				t.Fatalf("want code %q, got %v", tc.wantCode, codes(got))
			}
			if p.Severity != tc.wantSev {
				t.Fatalf("code %q: want severity %v, got %v", tc.wantCode, tc.wantSev, p.Severity)
			}
			if p.Fix == "" {
				t.Fatalf("code %q: Fix must never be empty — it is the whole point", tc.wantCode)
			}
		})
	}
}

// A private-PSL entry like github.io is a REAL public domain. icann==false for
// both it and ".lan", so classifying on that flag alone would wrongly hard-block
// a domain that works. It must degrade to a soft warning instead.
func TestCheckPrivateSuffixIsNotHardBlocked(t *testing.T) {
	got := Check("https://agents.github.io", googlePolicy)
	if p := hasCode(got, "non_public_host"); p != nil {
		t.Fatalf("github.io must not be hard-blocked as a reserved host")
	}
	if p := hasCode(got, "unverified_host"); p == nil || p.Severity != SeveritySoft {
		t.Fatalf("github.io should produce a soft unverified_host warning, got %v", codes(got))
	}
}

// An unverified policy may never hard-block, no matter what it finds.
func TestCheckUnverifiedPolicyOnlyWarns(t *testing.T) {
	unverified := googlePolicy
	unverified.Verified = false
	got := Check("http://192.168.1.194:8080", unverified)
	p := hasCode(got, "raw_ip")
	if p == nil {
		t.Fatalf("want raw_ip problem, got %v", codes(got))
	}
	if p.Severity != SeveritySoft {
		t.Fatalf("unverified policy must only warn, got severity %v", p.Severity)
	}
}

// A malformed URL is OUR validation failing, not a provider guess — always hard.
func TestCheckMalformedIsAlwaysHard(t *testing.T) {
	for _, base := range []string{"localhost:8080", "", "not a url", "://x"} {
		got := Check(base, Policy{}) // zero policy: permissive, unverified
		p := hasCode(got, "malformed_url")
		if p == nil || p.Severity != SeverityHard {
			t.Fatalf("%q: want hard malformed_url, got %v", base, codes(got))
		}
	}
}

// The zero Policy is fully permissive: an absent YAML block must never block anyone.
func TestCheckZeroPolicyIsPermissive(t *testing.T) {
	for _, base := range []string{"http://192.168.1.194:8080", "https://agents.rookie.lan", "http://box"} {
		if got := Check(base, Policy{}); len(got) != 0 {
			t.Fatalf("%q: zero policy must be permissive, got %v", base, codes(got))
		}
	}
}

func TestCheckSlackRequiresHTTPSEvenOnLocalhost(t *testing.T) {
	slack := Policy{Scheme: "https", AllowRawIP: "no", Verified: true}
	got := Check("http://localhost:8080", slack)
	if p := hasCode(got, "scheme_not_https"); p == nil || p.Severity != SeverityHard {
		t.Fatalf("slack has no localhost exemption, got %v", codes(got))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/publicurl/ -run TestCheck -v`
Expected: FAIL — the package does not compile (`undefined: Policy`, `undefined: Check`).

- [ ] **Step 3: Write the implementation**

Create `internal/publicurl/policy.go`:

```go
// Package publicurl owns the instance's externally-reachable base URL and judges
// it against a provider's redirect-URI policy.
//
// The judgement (Check) is a pure function over (URL, Policy) with no I/O. That
// is deliberate: it is the one place OAuth-setup correctness is asserted, and a
// pure function is exhaustively table-testable in milliseconds where the real
// thing needs a browser, a provider account and a consent screen.
package publicurl

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Severity decides whether the UI blocks the Connect button or merely warns.
//
// SeveritySoft is the zero value on purpose: any code path that forgets to set a
// severity degrades to a warning rather than locking a user out of a provider
// that would have worked.
type Severity int

const (
	SeveritySoft Severity = iota
	SeverityHard
)

// Problem is one reason a base URL will not work with a provider. Fix is not
// optional — a problem the user cannot act on is noise.
type Problem struct {
	Severity Severity
	Code     string // malformed_url | scheme_not_https | raw_ip | non_public_host | unverified_host
	Message  string
	Fix      string
}

// Policy is a provider's redirect-URI rules, declared in its YAML.
//
// Every field's zero value is the permissive case, so a provider with no
// redirect_policy block is judged by a fully permissive, unverified policy and
// can never be hard-blocked. That is what makes rolling this out provider by
// provider safe.
type Policy struct {
	// Scheme: "" or "any" (default) | "https_or_loopback" | "https".
	Scheme string `yaml:"scheme"`
	// AllowRawIP: "" or "yes" (default) | "loopback_only" | "no".
	AllowRawIP string `yaml:"allow_raw_ip"`
	// RequirePublicHost rejects RFC-reserved names (.lan/.local/dotless).
	RequirePublicHost bool `yaml:"require_public_host"`
	// Verified records that a human confirmed this policy against live provider
	// documentation. Only a verified policy may hard-block.
	Verified bool `yaml:"verified"`
}

type hostClass int

const (
	classLoopback hostClass = iota
	classRawIP
	classReserved
	classPublic
	classUncertain
)

// reservedTLDs are reserved by RFC (1035/2606/6762/8375) and can never be
// registered, so a redirect URI using one is provably unusable with any provider
// that validates the domain. Deliberately short: this list is the ONLY basis for
// hard-blocking a hostname, so it contains only names we are certain about.
var reservedTLDs = map[string]bool{
	"local": true, "lan": true, "home": true, "internal": true,
	"test": true, "invalid": true, "example": true, "localdomain": true,
}

func classify(host string) hostClass {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return classLoopback
	}
	if ip := net.ParseIP(h); ip != nil {
		if ip.IsLoopback() {
			return classLoopback
		}
		return classRawIP
	}
	if h == "home.arpa" || strings.HasSuffix(h, ".home.arpa") {
		return classReserved
	}
	i := strings.LastIndex(h, ".")
	if i < 0 {
		return classReserved // a dotless host has no registrable domain at all
	}
	if reservedTLDs[h[i+1:]] {
		return classReserved
	}
	// icann==true means the suffix is an ICANN-managed TLD, which is the exact
	// rule Google enforces ("must use a valid top private domain"). We cannot
	// use icann==false as the inverse: a PSL *private* entry such as github.io
	// also reports false yet is a genuine public domain. Those land in
	// classUncertain and may only ever produce a soft warning.
	if _, icann := publicsuffix.PublicSuffix(h); icann {
		return classPublic
	}
	return classUncertain
}

// Check reports why base will not work as a redirect URI under p. An empty
// result means no known problem.
func Check(base string, p Policy) []Problem {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return []Problem{{
			Severity: SeverityHard,
			Code:     "malformed_url",
			Message:  "This is not a complete URL.",
			Fix:      "Enter a full URL including the scheme, for example https://agents.example.com or http://localhost:8080.",
		}}
	}

	// Only a policy a human verified against live provider docs may hard-block.
	sev := SeveritySoft
	if p.Verified {
		sev = SeverityHard
	}
	class := classify(u.Hostname())

	var out []Problem

	switch p.Scheme {
	case "https":
		if u.Scheme != "https" {
			out = append(out, Problem{sev, "scheme_not_https",
				"This provider requires an https address, with no exception for localhost.",
				"Put the app behind a reverse proxy that terminates HTTPS, then set the instance URL to the https address."})
		}
	case "https_or_loopback":
		if u.Scheme != "https" && class != classLoopback {
			out = append(out, Problem{sev, "scheme_not_https",
				"This provider requires an https address for anything other than localhost.",
				"Open the app at http://localhost:8080, or put it behind a reverse proxy that terminates HTTPS."})
		}
	}

	if class == classRawIP && (p.AllowRawIP == "no" || p.AllowRawIP == "loopback_only") {
		out = append(out, Problem{sev, "raw_ip",
			"This provider does not accept an IP address as the redirect host.",
			"Use a hostname instead — open the app at http://localhost:8080, or give it a domain name."})
	}

	if p.RequirePublicHost {
		switch class {
		case classReserved:
			out = append(out, Problem{sev, "non_public_host",
				"This provider requires a registrable public domain, and this name uses a reserved suffix that cannot be registered.",
				"Use a real domain you own (it can still resolve to a private IP on your own network), or open the app at http://localhost:8080."})
		case classUncertain:
			out = append(out, Problem{SeveritySoft, "unverified_host",
				"We could not confirm this hostname uses a registrable public suffix.",
				"If the provider rejects it, use a domain under a well-known suffix such as .com."})
		}
	}

	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/publicurl/ -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Verify formatting and vet**

Run: `gofmt -l internal/publicurl/ && go vet ./internal/publicurl/`
Expected: no output from either.

- [ ] **Step 6: Commit**

```bash
git add internal/publicurl/
git commit -m "feat(publicurl): redirect-URI policy check over host classification"
```

---

### Task 2: Normalize and Resolve the instance URL

**Files:**
- Create: `internal/publicurl/publicurl.go`
- Test: `internal/publicurl/publicurl_test.go`

**Interfaces:**
- Consumes: Task 1's package.
- Produces: `type Source int` (`SourceDetected`, `SourceEnv`, `SourceConfigured`); `func Normalize(raw string) (string, error)`; `func Resolve(get func(string) (string, error), detected string) (string, Source)`; `const SettingKey = "public_url"`.

`Resolve` takes a getter function rather than a `*db.DB` so the package stays free of a database dependency and the test needs no fixture.

- [ ] **Step 1: Write the failing test**

Create `internal/publicurl/publicurl_test.go`:

```go
package publicurl

import "testing"

func TestNormalize(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://agents.example.com", "https://agents.example.com"},
		{"https://agents.example.com/", "https://agents.example.com"},
		{"http://localhost:8080/", "http://localhost:8080"},
		{"  https://agents.example.com  ", "https://agents.example.com"},
		{"HTTPS://Agents.Example.COM", "https://agents.example.com"},
	}
	for _, tc := range ok {
		got, err := Normalize(tc.in)
		if err != nil {
			t.Fatalf("Normalize(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// A schemeless value is the exact bug this fixes: SA_PUBLIC_URL=localhost:8080
	// previously produced a silently broken redirect URI.
	bad := []string{"localhost:8080", "agents.example.com", "",
		"https://agents.example.com/base", "https://a.com?x=1", "https://a.com#f", "ftp://a.com"}
	for _, in := range bad {
		if _, err := Normalize(in); err == nil {
			t.Fatalf("Normalize(%q) should have failed", in)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	cfg := func(v string) func(string) (string, error) {
		return func(string) (string, error) { return v, nil }
	}
	none := func(string) (string, error) { return "", nil }

	got, src := Resolve(cfg("https://configured.example.com"), "http://detected:8080")
	if got != "https://configured.example.com" || src != SourceConfigured {
		t.Fatalf("configured must win: got %q src %v", got, src)
	}

	t.Setenv("SA_PUBLIC_URL", "https://env.example.com")
	got, src = Resolve(none, "http://detected:8080")
	if got != "https://env.example.com" || src != SourceEnv {
		t.Fatalf("env must beat detection: got %q src %v", got, src)
	}

	t.Setenv("SA_PUBLIC_URL", "")
	got, src = Resolve(none, "http://detected:8080")
	if got != "http://detected:8080" || src != SourceDetected {
		t.Fatalf("detection is the fallback: got %q src %v", got, src)
	}
}

// A stored or env value that does not normalize must not silently produce a
// broken redirect URI — fall through to detection instead.
func TestResolveIgnoresUnnormalizableValues(t *testing.T) {
	bad := func(string) (string, error) { return "localhost:8080", nil }
	got, src := Resolve(bad, "http://detected:8080")
	if got != "http://detected:8080" || src != SourceDetected {
		t.Fatalf("bad configured value must fall through: got %q src %v", got, src)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/publicurl/ -run "TestNormalize|TestResolve" -v`
Expected: FAIL — `undefined: Normalize`, `undefined: Resolve`.

- [ ] **Step 3: Write the implementation**

Create `internal/publicurl/publicurl.go`:

```go
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
// Precedence: the configured setting, then SA_PUBLIC_URL, then what the current
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
	if n, err := Normalize(os.Getenv("SA_PUBLIC_URL")); err == nil {
		return n, SourceEnv
	}
	if n, err := Normalize(detected); err == nil {
		return n, SourceDetected
	}
	return detected, SourceDetected
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/publicurl/ -v`
Expected: PASS — including Task 1's tests, still green.

- [ ] **Step 5: Commit**

```bash
git add internal/publicurl/
git commit -m "feat(publicurl): normalize and resolve the instance base URL"
```

---

### Task 3: Redirect policy on providers, with parent inheritance

**Files:**
- Modify: `internal/connectors/registry.go` (add field to `Provider`, add resolver)
- Modify: `internal/connectors/providers/google.yaml`, `github.yaml`, `notion.yaml`, `slack.yaml`
- Test: `internal/connectors/redirect_policy_test.go`

**Interfaces:**
- Consumes: `publicurl.Policy` from Task 1.
- Produces: `Provider.RedirectPolicy publicurl.Policy`; `func (r *Registry) RedirectPolicy(provider string) publicurl.Policy`.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/redirect_policy_test.go`:

```go
package connectors

import "testing"

func TestRedirectPolicyVerifiedProviders(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	cases := []struct {
		provider          string
		scheme            string
		allowRawIP        string
		requirePublicHost bool
	}{
		{"google", "https_or_loopback", "loopback_only", true},
		{"github", "", "", false},
		{"notion", "https_or_loopback", "", false},
		{"slack", "https", "no", false},
	}
	for _, tc := range cases {
		p := r.RedirectPolicy(tc.provider)
		if !p.Verified {
			t.Fatalf("%s: policy must be marked verified", tc.provider)
		}
		if p.Scheme != tc.scheme || p.AllowRawIP != tc.allowRawIP || p.RequirePublicHost != tc.requirePublicHost {
			t.Fatalf("%s: got %+v", tc.provider, p)
		}
	}
}

// A google-aliased child must inherit the parent's policy: the redirect URI is
// registered against the PARENT's OAuth app, so the parent's rules are the ones
// that apply.
func TestRedirectPolicyInheritsFromOAuthParent(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	parent := r.RedirectPolicy("google")
	for _, child := range []string{"google_drive", "google_sheets", "google_docs",
		"google_adsense", "google_analytics", "google_searchconsole", "youtube"} {
		if got := r.RedirectPolicy(child); got != parent {
			t.Fatalf("%s: got %+v, want parent policy %+v", child, got, parent)
		}
	}
}

// An unknown or unannotated provider yields the zero policy, which Check treats
// as fully permissive and unverified.
func TestRedirectPolicyDefaultsPermissive(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range []string{"dropbox", "no_such_provider"} {
		if p := r.RedirectPolicy(name); p.Verified {
			t.Fatalf("%s: an unannotated provider must not be verified: %+v", name, p)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestRedirectPolicy -v`
Expected: FAIL — `r.RedirectPolicy undefined`.

- [ ] **Step 3: Add the field and resolver**

In `internal/connectors/registry.go`, add the import `"github.com/ilijad1/simple-agents/internal/publicurl"` and add this field to the `Provider` struct, immediately after the `KeyExtra` field:

```go
	// RedirectPolicy declares what this provider accepts as a redirect URI, so
	// the connect UI can tell the user their instance URL will not work BEFORE
	// they create an OAuth app and click through consent. An absent block is the
	// zero Policy: fully permissive and unverified, which can only ever warn.
	RedirectPolicy publicurl.Policy `yaml:"redirect_policy"`
```

Then add the resolver at the end of the file:

```go
// RedirectPolicy returns the redirect-URI policy governing a provider.
//
// It resolves through OAuthProvider, so an aliased child (google_drive → google)
// inherits its parent's policy. That is required for correctness rather than
// convenience: the redirect URI is registered against the PARENT's OAuth app, so
// the parent's rules are the ones the provider will enforce.
func (r *Registry) RedirectPolicy(provider string) publicurl.Policy {
	if p, ok := r.OAuthProvider(provider); ok {
		return p.RedirectPolicy
	}
	if p, ok := r.ProviderByName(provider); ok {
		return p.RedirectPolicy
	}
	return publicurl.Policy{}
}
```

- [ ] **Step 4: Annotate the four verified providers**

Add to `internal/connectors/providers/google.yaml` at the top level (sibling of `label:`):

```yaml
# Verified 2026-07-29 against
# https://developers.google.com/identity/protocols/oauth2/web-server and
# https://support.google.com/cloud/answer/15549257 — "Redirect URIs must use the
# HTTPS scheme… Localhost URIs are exempt", "Hosts cannot be raw IP addresses",
# and the domain must be a valid top private domain.
redirect_policy:
  scheme: https_or_loopback
  allow_raw_ip: loopback_only
  require_public_host: true
  verified: true
```

Add to `internal/connectors/providers/github.yaml`:

```yaml
# Verified 2026-07-29 against
# https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
# — loopback redirect URIs over plain http are explicitly supported.
redirect_policy:
  verified: true
```

Add to `internal/connectors/providers/notion.yaml`:

```yaml
# Verified 2026-07-29 against
# https://developers.notion.com/guides/get-started/authorization — https is
# required in production; http://localhost is accepted for development.
redirect_policy:
  scheme: https_or_loopback
  verified: true
```

Add to `internal/connectors/providers/slack.yaml`:

```yaml
# Verified 2026-07-29 against
# https://docs.slack.dev/authentication/installing-with-oauth/ — "Redirect URLs
# and URIs must use HTTPS". There is NO localhost exemption, which is why this
# provider needs a reverse proxy on a plain-http install.
redirect_policy:
  scheme: https
  allow_raw_ip: "no"
  verified: true
```

> `allow_raw_ip: "no"` is quoted deliberately: unquoted `no` is a YAML 1.1 boolean and would fail to unmarshal into a string field.

- [ ] **Step 5: Fix the misleading setup-step wording**

Task 6 makes the redirect URI visible in the wizard, above these steps. Until then the wording promised something that did not exist. Verify each of these still reads correctly once the URI is displayed; no change is required if the step already says "shown above". Run:

```bash
grep -rn "shown above" internal/connectors/providers/*.yaml | wc -l
```

Expected: a non-zero count. These become accurate as of Task 6 — no edit needed now.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/connectors/ -run TestRedirectPolicy -v`
Expected: PASS.

- [ ] **Step 7: Run the full connectors suite for regressions**

Run: `go test ./internal/connectors/ -count=1`
Expected: PASS — the new YAML keys must not break manifest parsing.

- [ ] **Step 8: Commit**

```bash
git add internal/connectors/
git commit -m "feat(connectors): declare per-provider redirect-URI policy in YAML"
```

---

### Task 4: Pin the redirect URI into the signed OAuth state

**Files:**
- Modify: `web/handlers_services.go` (`publicBaseURL`, `buildConsentURL`, `handleOAuthCallback`)
- Test: `web/oauth_state_test.go`

**Interfaces:**
- Consumes: `publicurl.Resolve`, `publicurl.Normalize`.
- Produces: `func (s *Server) publicBaseURL(c echo.Context) string` (unchanged signature, new implementation); state payload gains a 6th `~`-separated field carrying the redirect URI.

- [ ] **Step 1: Write the failing test**

Create `web/oauth_state_test.go`:

```go
package web

import (
	"strings"
	"testing"
	"time"
)

// The state payload must round-trip 4-, 5- and 6-field shapes. The older shapes
// still arrive during the 10-minute TTL after a deploy.
func TestStatePayloadShapesRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now()
	for _, payload := range []string{
		"ws~google~default~nonce",
		"ws~google~default~nonce~aW5wdXRz",
		"ws~google~default~nonce~aW5wdXRz~https://agents.example.com/dashboard/connectors/services/callback/google",
	} {
		got, ok := verifyState(key, signState(key, payload, now), now)
		if !ok || got != payload {
			t.Fatalf("round-trip failed for %q (ok=%v got=%q)", payload, ok, got)
		}
		if n := len(strings.Split(got, "~")); n < 4 || n > 6 {
			t.Fatalf("unexpected field count %d", n)
		}
	}
}

func TestStateRejectsTamperedPayload(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now()
	tok := signState(key, "ws~google~default~nonce~~https://evil.example.com/cb", now)
	// Flip a byte in the encoded token.
	b := []byte(tok)
	b[len(b)/2] ^= 0x01
	if _, ok := verifyState(key, string(b), now); ok {
		t.Fatalf("tampered state must not verify")
	}
}

// redirectURIFromState is the accessor the callback uses; it must tolerate the
// older shapes by returning "" rather than panicking on a short slice.
func TestRedirectURIFromState(t *testing.T) {
	cases := []struct{ payload, want string }{
		{"ws~google~default~nonce", ""},
		{"ws~google~default~nonce~aW5w", ""},
		{"ws~google~default~nonce~aW5w~https://a.example.com/cb", "https://a.example.com/cb"},
		{"ws~google~default~nonce~~https://a.example.com/cb", "https://a.example.com/cb"},
	}
	for _, tc := range cases {
		if got := redirectURIFromState(strings.Split(tc.payload, "~")); got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.payload, got, tc.want)
		}
	}
}

// A pinned URI that disagrees with what we would compute now must still be USED,
// not rejected: the user has already granted consent, and the pinned string is
// the one the provider validated. Rejecting would bounce them into a loop.
func TestPinnedURIWinsOverCurrentComputation(t *testing.T) {
	pinned := "https://old.example.com/dashboard/connectors/services/callback/google"
	parts := strings.Split("ws~google~default~nonce~~"+pinned, "~")
	if got := redirectURIFromState(parts); got != pinned {
		t.Fatalf("pinned URI must be returned verbatim, got %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./web/ -run "TestState|TestRedirectURIFromState" -v`
Expected: FAIL — `undefined: redirectURIFromState`.

- [ ] **Step 3: Rewrite `publicBaseURL` and add the state accessor**

In `web/handlers_services.go`, replace the existing `publicBaseURL` (currently lines 87-93) with:

```go
// publicBaseURL is the instance's externally-reachable base URL: the configured
// setting, else SA_PUBLIC_URL, else what this request suggests.
//
// Detection is only the fallback. It is why the redirect URI used to change with
// however the operator happened to reach the page, which is the defect the
// configured setting exists to remove.
func (s *Server) publicBaseURL(c echo.Context) string {
	base, _ := s.resolvePublicURL(c)
	return base
}

// resolvePublicURL also reports where the value came from, for the settings UI.
func (s *Server) resolvePublicURL(c echo.Context) (string, publicurl.Source) {
	return publicurl.Resolve(s.db.GetSystemSetting, detectBaseURL(c))
}

// detectBaseURL infers a base URL from the current request. Note this reads the
// Host header directly and does NOT consult X-Forwarded-Host, so any reverse
// proxy that rewrites Host must have the instance URL configured explicitly.
func detectBaseURL(c echo.Context) string {
	return c.Scheme() + "://" + c.Request().Host
}

// redirectURIFromState reads the pinned redirect URI out of a split state
// payload, tolerating the 4- and 5-field shapes issued before it was pinned.
func redirectURIFromState(parts []string) string {
	if len(parts) < 6 {
		return ""
	}
	return parts[5]
}
```

Add `"github.com/ilijad1/simple-agents/internal/publicurl"` to the imports and remove the now-unused `"os"` import if nothing else in the file uses it (check with `go build ./web/`).

- [ ] **Step 4: Pin the URI when building the consent URL**

In `buildConsentURL`, replace the `payload := strings.Join(...)` line (currently line 146) with:

```go
	redirectURI := s.callbackURL(c, provider)
	// The URI is pinned into the signed state so the token exchange uses the
	// SAME string the consent request did. Recomputing it at callback time was a
	// real failure mode: any difference produces redirect_uri_mismatch AFTER the
	// user has already granted consent, which reads as a provider fault.
	payload := strings.Join([]string{w.ID, provider, label, nonce, encoded, redirectURI}, "~")
```

and change the final return to use the same variable:

```go
	return oauth.ConsentURL(clientID, redirectURI, state, child.DefaultScopes), nil
```

- [ ] **Step 5: Use the pinned URI at token exchange**

In `handleOAuthCallback`, widen the accepted field count and use the pinned value. Replace the length guard (currently line 218):

```go
	if len(parts) < 4 || len(parts) > 6 || parts[0] != w.ID || parts[1] != provider {
```

Then, immediately before the `oauth := connectors.OAuthClient{}` line, add:

```go
	// Use the URI pinned at consent time, unconditionally. It is by definition
	// the correct string: it is the one the provider itself saw and validated
	// when it issued this code, so the exchange must present the same one.
	//
	// Do NOT reject the callback when it diverges from what we would compute now.
	// The user has already granted consent at that point, and if the cause is
	// systematic — the operator changed the instance URL mid-flow, or a transient
	// GetSystemSetting error made Resolve fall through to detection — then
	// "start again" reproduces the divergence. That is a loop, not a recovery.
	// Log it and proceed.
	redirectURI := redirectURIFromState(parts)
	if redirectURI == "" {
		// A pre-pinning state, still inside the 10-minute TTL.
		redirectURI = s.callbackURL(c, provider)
	} else if current := s.callbackURL(c, provider); current != redirectURI {
		slog.Warn("oauth callback: instance URL changed mid-flow; using the pinned URI",
			"provider", provider, "pinned", redirectURI, "current", current)
	}
```

Add `"log/slog"` to the file's imports.

and change the exchange call to:

```go
	ts, err := oauth.ExchangeCode(ctx, authProv, clientID, clientSecret, code, redirectURI)
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./web/ -run "TestState|TestRedirectURIFromState" -v`
Expected: PASS.

- [ ] **Step 7: Run the existing services tests for regressions**

Run: `go test ./web/ -run "TestOAuth|TestService|TestConnect" -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/handlers_services.go web/oauth_state_test.go
git commit -m "fix(services): pin the redirect URI into the signed OAuth state"
```

---

### Task 5: Failure taxonomy

**Files:**
- Create: `web/oauth_errors.go`
- Test: `web/oauth_errors_test.go`
- Modify: `web/handlers_services.go` (the token-exchange error path)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func explainOAuthError(providerLabel, redirectURI string, err error) string`.

- [ ] **Step 1: Write the failing test**

Create `web/oauth_errors_test.go`:

```go
package web

import (
	"errors"
	"strings"
	"testing"
)

func TestExplainOAuthError(t *testing.T) {
	uri := "https://agents.example.com/dashboard/connectors/services/callback/google"
	cases := []struct {
		name     string
		err      error
		contains []string
	}{
		{
			"mismatch names the exact URI to register",
			errors.New(`oauth: token exchange failed: {"error":"redirect_uri_mismatch"}`),
			[]string{"Google", "does not match", uri},
		},
		{
			"invalid client points at credentials",
			errors.New(`{"error":"invalid_client","error_description":"bad secret"}`),
			[]string{"Client ID", "Client Secret"},
		},
		{
			"invalid scope points at enabled APIs",
			errors.New(`{"error":"invalid_scope"}`),
			[]string{"permissions"},
		},
		{
			"unknown error is preserved, not swallowed",
			errors.New("connection reset by peer"),
			[]string{"connection reset by peer"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := explainOAuthError("Google", uri, tc.err)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("message %q missing %q", got, want)
				}
			}
		})
	}
}

func TestExplainOAuthErrorNilIsEmpty(t *testing.T) {
	if got := explainOAuthError("Google", "https://x/cb", nil); got != "" {
		t.Fatalf("nil error should explain to empty string, got %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./web/ -run TestExplainOAuthError -v`
Expected: FAIL — `undefined: explainOAuthError`.

- [ ] **Step 3: Write the implementation**

Create `web/oauth_errors.go`:

```go
package web

import "strings"

// explainOAuthError turns a provider's token-exchange failure into a sentence
// that names the remedy.
//
// The raw error was previously concatenated straight into a query string, which
// told a non-technical user nothing actionable. Matching is on substrings of the
// provider's JSON body because the OAuth error codes are standardised even
// though the surrounding envelope is not.
func explainOAuthError(providerLabel, redirectURI string, err error) string {
	if err == nil {
		return ""
	}
	raw := err.Error()
	switch {
	case strings.Contains(raw, "redirect_uri_mismatch"):
		return "The redirect URI registered with " + providerLabel +
			" does not match this instance. Register exactly this URI in the " +
			providerLabel + " console, then try again: " + redirectURI
	case strings.Contains(raw, "invalid_client"), strings.Contains(raw, "unauthorized_client"):
		return "The Client ID or Client Secret for " + providerLabel +
			" is wrong, or belongs to a different app. Re-enter both from the " +
			providerLabel + " console."
	case strings.Contains(raw, "invalid_scope"), strings.Contains(raw, "insufficient_scope"):
		return providerLabel + " rejected the requested permissions. The app may not" +
			" have those APIs enabled — check them in the " + providerLabel + " console."
	case strings.Contains(raw, "invalid_grant"):
		return "The authorization expired before it could be used. Start the connection again."
	default:
		return providerLabel + " refused the connection: " + raw
	}
}
```

- [ ] **Step 4: Wire it into the callback**

In `web/handlers_services.go`, replace the token-exchange error branch:

```go
	if err != nil {
		return s.redirectWithError(c, "/connections", "Token exchange failed: "+err.Error())
	}
```

with:

```go
	if err != nil {
		label := authProv.Label
		if label == "" {
			label = provider
		}
		return s.redirectWithError(c, "/connections", explainOAuthError(label, redirectURI, err))
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./web/ -run TestExplainOAuthError -v && go build ./web/`
Expected: PASS, then a clean build.

- [ ] **Step 6: Commit**

```bash
git add web/oauth_errors.go web/oauth_errors_test.go web/handlers_services.go
git commit -m "feat(services): explain OAuth failures with the remedy"
```

---

### Task 6: API surface — redirect URI, preflight, instance URL, self-test

**Files:**
- Modify: `web/api_services.go` (DTO + `apiListServices`)
- Modify: `web/api_workspaces.go` (admin settings get/put + self-test trigger)
- Modify: `web/server.go` (unauthenticated `/healthz/echo`)
- Modify: `web/api_parity_test.go` (register new routes)
- Test: `web/api_services_preflight_test.go`

**Interfaces:**
- Consumes: `publicurl.Check`, `Registry.RedirectPolicy`, `s.resolvePublicURL`.
- Produces: `apiServiceProvider.RedirectURI string \`json:"redirect_uri"\``; `apiServiceProvider.Preflight []apiPreflightProblem`; `type apiPreflightProblem struct{Severity, Code, Message, Fix string}`.

- [ ] **Step 1: Write the failing test**

Create `web/api_services_preflight_test.go`. Follow the existing fixture style in `web/api_workspaces_test.go` for `newTestServer`/`doJSON`/cookies:

```go
package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Every OAuth provider must carry the exact redirect URI the user has to
// register. Its absence is what made OAuth unusable through the UI.
func TestListServicesCarriesRedirectURI(t *testing.T) {
	s, cookies := setupEnteredWorkspace(t)
	// The operator's own SA_PUBLIC_URL must not leak into the test's expectations.
	t.Setenv("SA_PUBLIC_URL", "")

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Providers []struct {
			Name        string `json:"name"`
			Kind        string `json:"kind"`
			RedirectURI string `json:"redirect_uri"`
			Preflight   []struct {
				Severity string `json:"severity"`
				Code     string `json:"code"`
				Fix      string `json:"fix"`
			} `json:"preflight"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var google, stripe *struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		RedirectURI string `json:"redirect_uri"`
		Preflight   []struct {
			Severity string `json:"severity"`
			Code     string `json:"code"`
			Fix      string `json:"fix"`
		} `json:"preflight"`
	}
	for i := range body.Providers {
		switch body.Providers[i].Name {
		case "google":
			google = &body.Providers[i]
		case "stripe":
			stripe = &body.Providers[i]
		}
	}
	if google == nil || stripe == nil {
		t.Fatalf("expected google and stripe in the provider list")
	}
	want := "/dashboard/connectors/services/callback/google"
	if len(google.RedirectURI) < len(want) || google.RedirectURI[len(google.RedirectURI)-len(want):] != want {
		t.Fatalf("google redirect_uri = %q, want it to end with %q", google.RedirectURI, want)
	}
	// An api_key provider has no redirect URI at all — emitting one would tell
	// the user to register something that is never used.
	if stripe.RedirectURI != "" {
		t.Fatalf("api_key provider must not carry a redirect_uri, got %q", stripe.RedirectURI)
	}
	// httptest requests arrive as http://example.com, a public domain over plain
	// http, which google's verified policy hard-blocks.
	if len(google.Preflight) == 0 {
		t.Fatalf("expected a preflight problem for google over plain http")
	}
	if google.Preflight[0].Severity != "hard" || google.Preflight[0].Fix == "" {
		t.Fatalf("got preflight %+v", google.Preflight[0])
	}
}
```

> If `setupEnteredWorkspace` does not already exist in the `web` test fixtures, reuse whatever helper `web/api_workspaces_test.go` uses to bootstrap an owner, create a workspace and enter it; do not invent a second bootstrap path.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./web/ -run TestListServicesCarriesRedirectURI -v`
Expected: FAIL — `redirect_uri` decodes empty.

- [ ] **Step 3: Extend the DTO**

In `web/api_services.go`, add the problem DTO next to the other DTOs:

```go
// apiPreflightProblem is a publicurl.Problem flattened for JSON. Severity is a
// string ("hard"/"soft") rather than the Go int so the SPA never depends on our
// enum ordering.
type apiPreflightProblem struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}
```

and add two fields to `apiServiceProvider`, after `ConnectInputs`:

```go
	RedirectURI string                `json:"redirect_uri"`
	Preflight   []apiPreflightProblem `json:"preflight"`
```

Add the converter at the bottom of the file:

```go
func toAPIPreflight(ps []publicurl.Problem) []apiPreflightProblem {
	out := make([]apiPreflightProblem, 0, len(ps))
	for _, p := range ps {
		sev := "soft"
		if p.Severity == publicurl.SeverityHard {
			sev = "hard"
		}
		out = append(out, apiPreflightProblem{sev, p.Code, p.Message, p.Fix})
	}
	return out
}
```

- [ ] **Step 4: Populate them in `apiListServices`**

Inside the provider loop in `apiListServices`, just before the `out = append(out, apiServiceProvider{...})` call, add:

```go
		// Only OAuth providers have a redirect URI; an api_key provider never
		// leaves the browser, so emitting one would be a false instruction.
		redirectURI, preflight := "", []apiPreflightProblem{}
		if kind == "oauth" {
			redirectURI = base + "/dashboard/connectors/services/callback/" + provider
			preflight = toAPIPreflight(publicurl.Check(base, s.connectors.RedirectPolicy(provider)))
			if len(preflight) == 0 {
				cleanProviders++
			}
			oauthProviders++
		}
```

Resolve the base URL **once, above the loop** — there are 45 providers and
`callbackURL` resolves again internally, so resolving per-iteration would mean
~90 `GetSystemSetting` reads on every page load:

```go
	base, _ := s.resolvePublicURL(c)
	oauthProviders, cleanProviders := 0, 0
```

and add the two fields to the struct literal:

```go
			RedirectURI:   redirectURI,
			Preflight:     preflight,
```

Add the `publicurl` import.

- [ ] **Step 4b: Emit the remedy tier**

Per-provider problems tell the user what is broken *here*; the remedy tier tells
them what the current URL costs them *overall*, while they are still choosing it.
Extend `apiServicesListResponse` in `web/api_services.go`:

```go
// apiPublicURLSummary answers "what does my current instance URL cost me?" in
// one line. Per-provider preflight cannot answer that — a user comparing URLs
// would otherwise have to open all 18 OAuth cards and tally them by hand.
type apiPublicURLSummary struct {
	BaseURL        string `json:"base_url"`
	OAuthProviders int    `json:"oauth_providers"`
	CleanProviders int    `json:"clean_providers"`
}
```

Add the field to the response struct and populate it from the counters:

```go
	Summary apiPublicURLSummary `json:"summary"`
```

```go
	return c.JSON(http.StatusOK, apiServicesListResponse{
		Providers: out,
		Summary: apiPublicURLSummary{
			BaseURL:        base,
			OAuthProviders: oauthProviders,
			CleanProviders: cleanProviders,
		},
	})
```

Add to `web/api_services_preflight_test.go`:

```go
func TestListServicesSummaryCountsCleanProviders(t *testing.T) {
	s, cookies := setupEnteredWorkspace(t)
	t.Setenv("SA_PUBLIC_URL", "")

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	var body struct {
		Summary struct {
			BaseURL        string `json:"base_url"`
			OAuthProviders int    `json:"oauth_providers"`
			CleanProviders int    `json:"clean_providers"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Summary.OAuthProviders < 10 {
		t.Fatalf("expected the bundled OAuth providers to be counted, got %d", body.Summary.OAuthProviders)
	}
	if body.Summary.CleanProviders > body.Summary.OAuthProviders {
		t.Fatalf("clean (%d) cannot exceed total (%d)",
			body.Summary.CleanProviders, body.Summary.OAuthProviders)
	}
	if body.Summary.BaseURL == "" {
		t.Fatalf("summary must carry the base URL it judged")
	}
}
```

- [ ] **Step 5: Add the instance URL to admin settings**

In `web/api_workspaces.go`, extend `apiAdminSettings` with:

```go
	PublicURL       string `json:"public_url"`        // the configured value, "" if unset
	PublicURLActual string `json:"public_url_actual"` // what is actually in use right now
	PublicURLSource string `json:"public_url_source"` // "configured" | "env" | "detected"
```

`apiLoadAdminSettings` takes no context today, so change it to accept one and populate the new fields:

```go
func (s *Server) apiLoadAdminSettings(c echo.Context) apiAdminSettings {
	d := s.loadAdminSettings()
	stored, _ := s.db.GetSystemSetting(publicurl.SettingKey)
	actual, src := s.resolvePublicURL(c)
	label := map[publicurl.Source]string{
		publicurl.SourceConfigured: "configured",
		publicurl.SourceEnv:        "env",
		publicurl.SourceDetected:   "detected",
	}[src]
	return apiAdminSettings{
		SandboxOn:       d.SandboxOn,
		LandlockReady:   d.LandlockReady,
		PublicURL:       stored,
		PublicURLActual: actual,
		PublicURLSource: label,
	}
}
```

Update `apiAdminGetSettings` to pass `c`. Then locate the admin-settings PUT handler — it is registered in `web/api_workspaces.go` near the `GET /admin/settings` line at `:26` and exercised by `web/api_workspaces_test.go:197`. Find it with:

```bash
grep -rn '"/admin/settings"' web/*.go
```

Extend that handler's request struct with `PublicURL string \`json:"public_url"\`` and add:

```go
	// An empty value clears the setting and returns the instance to detection.
	if req.PublicURL == "" {
		_ = s.db.SetSystemSetting(publicurl.SettingKey, "")
	} else {
		n, err := publicurl.Normalize(req.PublicURL)
		if err != nil {
			return jsonErr(c, http.StatusBadRequest, "invalid_public_url",
				"Enter a full URL including the scheme and no path, for example https://agents.example.com")
		}
		if err := s.db.SetSystemSetting(publicurl.SettingKey, n); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
	}
```

- [ ] **Step 6: Add the self-test endpoint and trigger**

In `web/server.go`, register the echo endpoint immediately after the existing `s.echo.GET("/healthz", s.apiHealthz)` line (line 176):

```go
	// Unauthenticated by necessity: the "Test this URL" check is a server-to-
	// server fetch that carries no session cookie, so an authenticated endpoint
	// would fail identically whether the URL was right or wrong — inverting the
	// signal the test exists to give. Safe because it is not an oracle: it echoes
	// only a nonce this process issued, once, within 30 seconds, and 404s
	// otherwise. It reveals no configuration.
	s.echo.GET("/healthz/echo", s.handleEchoNonce)
```

Add to `web/api_workspaces.go` (or a new `web/publicurl_selftest.go` if that file is already large):

```go
// echoNonces holds outstanding self-test nonces. Bounded by the 30s TTL and the
// fact that only the owner can mint one.
type echoNonce struct{ expires time.Time }

func (s *Server) handleEchoNonce(c echo.Context) error {
	tok := c.QueryParam("token")
	s.echoMu.Lock()
	n, ok := s.echoNonces[tok]
	delete(s.echoNonces, tok) // single use
	s.echoMu.Unlock()
	if !ok || time.Now().After(n.expires) {
		return c.NoContent(http.StatusNotFound)
	}
	return c.JSON(http.StatusOK, map[string]string{"token": tok})
}

// apiTestPublicURL fetches the candidate URL's echo endpoint and asserts the
// nonce comes back, proving the URL reaches THIS process — not merely that
// something answered. A typo, a wrong port, DNS pointing elsewhere and a proxy
// aimed at another instance all fail this check; a plain reachability probe
// would catch only the first two.
func (s *Server) apiTestPublicURL(c echo.Context) error {
	var req struct {
		URL string `json:"url"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	base, err := publicurl.Normalize(req.URL)
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_public_url",
			"Enter a full URL including the scheme, for example https://agents.example.com")
	}

	// crypto/rand, NOT math/rand — this nonce is the endpoint's only access
	// control. Import it as `crypto/rand`; a math/rand nonce reads as fine and is
	// a security bug.
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	tok := hex.EncodeToString(raw)
	s.echoMu.Lock()
	if s.echoNonces == nil {
		s.echoNonces = map[string]echoNonce{}
	}
	s.echoNonces[tok] = echoNonce{expires: time.Now().Add(30 * time.Second)}
	s.echoMu.Unlock()

	// DELIBERATE EXCEPTION: internal/nethttp.GuardedClient blocks loopback and
	// RFC1918 by design, and dialling ourselves is exactly the point of this
	// check. Do not "fix" this to use the guarded client — it would make the
	// self-test fail on every self-hosted install, which is all of them.
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 6*time.Second)
	defer cancel()
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz/echo?token="+tok, nil)
	resp, err := client.Do(hreq)
	if err != nil {
		// A certificate the SERVER does not trust is a third outcome, not a
		// failure. Verified empirically: against a Caddy internal-CA host this
		// succeeds when `caddy trust` has put the root in the system pool and
		// fails when it has not — so it is install-dependent, while the BROWSER
		// (the only party in an OAuth redirect that actually loads this URL) may
		// trust it either way. Reporting "unreachable" for a working setup is
		// worse than having no button at all.
		var uaErr x509.UnknownAuthorityError
		var hostErr x509.HostnameError
		if errors.As(err, &uaErr) || errors.As(err, &hostErr) {
			return c.JSON(http.StatusOK, map[string]any{"ok": true, "warning": true,
				"error": "Reached " + base + ", but this server does not trust its certificate. " +
					"That is fine for OAuth as long as your browser trusts it — the provider " +
					"never connects to this server, it only redirects your browser."})
		}
		return c.JSON(http.StatusOK, map[string]any{"ok": false,
			"error": "Could not reach " + base + " from the server: " + err.Error() +
				". If your network has no NAT hairpin, the server may be unable to reach its own " +
				"public name even though browsers can — check from a browser before changing anything."})
	}
	defer resp.Body.Close()
	var got struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&got)
	if resp.StatusCode != http.StatusOK || got.Token != tok {
		return c.JSON(http.StatusOK, map[string]any{"ok": false,
			"error": base + " answered, but it is not this instance. Check the address, the port, and any reverse proxy."})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}
```

Add the fields to the `Server` struct in `web/server.go`:

```go
	echoMu     sync.Mutex
	echoNonces map[string]echoNonce
```

Register the route in the owner-guarded group alongside the other admin routes:

```go
	g.POST("/admin/public-url/test", s.apiTestPublicURL)
```

- [ ] **Step 6b: Test the echo endpoint's security properties**

The endpoint is unauthenticated, so single-use and expiry ARE its access control
and must be pinned by tests. Add to `web/api_services_preflight_test.go`:

```go
func TestEchoNonceIsSingleUseAndScoped(t *testing.T) {
	s, _ := setupEnteredWorkspace(t)

	// An unissued token is never echoed.
	rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=bogus", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unissued token: status %d, want 404", rec.Code)
	}

	// An issued token works exactly once.
	s.echoMu.Lock()
	if s.echoNonces == nil {
		s.echoNonces = map[string]echoNonce{}
	}
	s.echoNonces["tok1"] = echoNonce{expires: time.Now().Add(30 * time.Second)}
	s.echoMu.Unlock()

	if rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=tok1", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("issued token: status %d, want 200", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=tok1", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("replayed token: status %d, want 404 — nonces must be single-use", rec.Code)
	}
}

func TestEchoNonceExpires(t *testing.T) {
	s, _ := setupEnteredWorkspace(t)
	s.echoMu.Lock()
	if s.echoNonces == nil {
		s.echoNonces = map[string]echoNonce{}
	}
	s.echoNonces["stale"] = echoNonce{expires: time.Now().Add(-time.Second)}
	s.echoMu.Unlock()

	if rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=stale", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expired token: status %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 7: Register the new route in the parity inventory**

In `web/api_parity_test.go`, add to the `want` table next to the other `admin` entries:

```go
		"POST /api/v1/admin/public-url/test",
```

- [ ] **Step 8: Run the tests**

Run: `go test ./web/ -run "TestListServicesCarriesRedirectURI|TestAPIParityInventory" -v`
Expected: PASS.

- [ ] **Step 9: Run the full web suite**

Run: `go test ./web/ -count=1 -timeout 900s`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add web/
git commit -m "feat(api): expose the redirect URI, preflight and instance-URL self-test"
```

---

### Task 7: SPA — show the URI, block on hard problems, configure the URL

**Files:**
- Modify: `web/ui/src/lib/connections.ts` (types)
- Modify: `web/ui/src/pages/connections/ServiceWizard.tsx`
- Modify: `web/ui/src/pages/settings/OwnerSections.tsx`
- Test: `web/ui/src/pages/connections/ServiceWizard.test.tsx`

**Interfaces:**
- Consumes: `redirect_uri` and `preflight` from Task 6's DTO.
- Produces: no new exports; UI behaviour only.

- [ ] **Step 1: Extend the types**

In `web/ui/src/lib/connections.ts`, add to the service-provider type (the one at line ~75 carrying `setup_steps`):

```ts
  redirect_uri: string;
  preflight: { severity: "hard" | "soft"; code: string; message: string; fix: string }[];
```

Update the existing test fixtures in `connections.test.ts`, `ServiceWizard.test.tsx` and `ProviderActions.test.tsx` that construct provider objects — add `redirect_uri: ""` and `preflight: []` to each so they still type-check.

- [ ] **Step 2: Write the failing test**

Add to `web/ui/src/pages/connections/ServiceWizard.test.tsx`:

```tsx
it("shows the redirect URI so the user can register it", async () => {
  renderWizard({ ...oauthProvider, redirect_uri: "https://agents.example.com/dashboard/connectors/services/callback/google" });
  expect(
    await screen.findByText("https://agents.example.com/dashboard/connectors/services/callback/google"),
  ).toBeInTheDocument();
});

it("disables Connect and explains when preflight finds a hard problem", async () => {
  renderWizard({
    ...oauthProvider,
    has_creds: true,
    redirect_uri: "http://192.168.1.194:8080/dashboard/connectors/services/callback/google",
    preflight: [{
      severity: "hard",
      code: "raw_ip",
      message: "This provider does not accept an IP address as the redirect host.",
      fix: "Use a hostname instead.",
    }],
  });
  expect(await screen.findByText(/does not accept an IP address/)).toBeInTheDocument();
  expect(await screen.findByText("Use a hostname instead.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /connect/i })).toBeDisabled();
});

it("warns but still allows Connect on a soft problem", async () => {
  renderWizard({
    ...oauthProvider,
    has_creds: true,
    preflight: [{ severity: "soft", code: "unverified_host", message: "Unconfirmed suffix.", fix: "Try it." }],
  });
  expect(await screen.findByText("Unconfirmed suffix.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /connect/i })).toBeEnabled();
});
```

> Match `renderWizard` and `oauthProvider` to the helpers already present in that test file; if they are named differently, use the existing names rather than adding new ones.

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`
Expected: FAIL — the URI is not rendered and Connect is not disabled.

- [ ] **Step 4: Render the URI and the preflight banner**

In `ServiceWizard.tsx`, inside the `view === "creds"` branch, immediately **above** the `setup_steps` list (so "the redirect URI shown above" is finally true), insert:

```tsx
{provider.redirect_uri && (
  <div className="space-y-1 rounded-lg border border-border bg-muted-surface p-3">
    <div className="text-xs font-semibold uppercase tracking-wide text-muted-2">
      Redirect URI to register
    </div>
    <div className="flex items-center gap-2">
      <code className="min-w-0 flex-1 break-all text-xs">{provider.redirect_uri}</code>
      <CopyButton value={provider.redirect_uri} />
    </div>
  </div>
)}

{provider.preflight.map((p) => (
  <div
    key={p.code}
    className={
      p.severity === "hard"
        ? "space-y-1 rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm"
        : "space-y-1 rounded-lg border border-border bg-muted-surface p-3 text-sm"
    }
  >
    <div className="font-medium">{p.message}</div>
    <div className="text-muted-2">{p.fix}</div>
  </div>
))}
```

Reuse the existing clipboard helper rather than writing a new one: `MessageMeta` in `components/chat/Bubbles.tsx` already handles the non-secure-context fallback (`navigator.clipboard` is `undefined` over plain HTTP on a LAN, which is exactly how this app is reached). Extract its `copy()` into a shared `CopyButton` component under `components/` and use it in both places.

- [ ] **Step 5: Disable Connect on a hard problem**

In the same file, derive the flag near the top of the component:

```tsx
const hardBlocked = provider.preflight.some((p) => p.severity === "hard");
```

and add `disabled={hardBlocked}` to the Connect button (combining with any existing disabled condition using `||`).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`
Expected: PASS.

- [ ] **Step 6b: Render the remedy tier on the connections page**

In `web/ui/src/pages/connections/ConnectionsPage.tsx`, render the summary once
above the provider grid, so the cost of the current URL is visible while the user
is choosing it rather than discoverable only card by card:

```tsx
{summary.oauth_providers > 0 && summary.clean_providers < summary.oauth_providers && (
  <div className="rounded-lg border border-border bg-muted-surface p-3 text-sm">
    <span className="font-medium">
      {summary.base_url} works with {summary.clean_providers} of{" "}
      {summary.oauth_providers} sign-in services.
    </span>{" "}
    <span className="text-muted-2">
      A public domain name over https unlocks the rest.{" "}
      <Link to="/settings" className="underline underline-offset-2">
        Change the instance URL
      </Link>
    </span>
  </div>
)}
```

Add the matching type to `web/ui/src/lib/connections.ts`:

```ts
export type PublicURLSummary = {
  base_url: string;
  oauth_providers: number;
  clean_providers: number;
};
```

and add `summary: PublicURLSummary` to the services-list response type. Update
any existing fixture in `connections.test.ts` that builds that response.

- [ ] **Step 7: Add the instance-URL field to owner settings**

In `web/ui/src/pages/settings/OwnerSections.tsx`, add a section with: a text input bound to `public_url`; helper text showing `public_url_actual` and `public_url_source` ("currently detected from your browser" vs "configured"); a Save button hitting the existing admin-settings PUT; and a "Test this URL" button hitting `POST /api/v1/admin/public-url/test`, rendering `{ok:true}` as a success chip and `{ok:false, error}` as the error text.

- [ ] **Step 8: Typecheck, lint and build**

Run: `cd web/ui && npx tsc -b && npx oxlint && npx vitest run && npm run build`
Expected: all clean.

- [ ] **Step 9: Commit**

```bash
git add web/ui/
git commit -m "feat(web/ui): show the redirect URI, preflight verdict and instance-URL setting"
```

---

### Task 8: Documentation and deployment guidance

**Files:**
- Modify: `Dockerfile`
- Modify: `packaging/systemd/simple-agents.service`
- Modify: `packaging/README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Document the variable in the Dockerfile**

In `Dockerfile`, immediately after the existing `ENV` block (line 68), add:

```dockerfile
# SA_PUBLIC_URL is REQUIRED behind any reverse proxy that rewrites Host: the app
# reads the Host header directly and does not consult X-Forwarded-Host, so
# without it every OAuth redirect URI points at the wrong address. Left unset,
# the instance URL is inferred from the browser's request.
#   -e SA_PUBLIC_URL=https://agents.example.com
```

- [ ] **Step 2: Document it in the systemd unit**

In `packaging/systemd/simple-agents.service`, after the existing `Environment=SA_DATA_DIR=…` line:

```ini
# Set this when the app sits behind a reverse proxy or is reached by a name
# other than the one the browser used, otherwise OAuth redirect URIs are wrong.
#Environment=SA_PUBLIC_URL=https://agents.example.com
```

- [ ] **Step 3: Add the deployment matrix to `packaging/README.md`**

Add a section:

```markdown
## OAuth and your instance URL

An OAuth provider never connects to this server — it redirects the user's
browser. So the server does not need to be reachable from the internet. What
gets validated is the **redirect URI string**, when you register it.

| How you reach the app | Redirect URI | Outcome |
|---|---|---|
| `http://localhost:8080` | `http://localhost:8080/…` | Google, GitHub, Notion work. Slack-class providers need HTTPS. |
| LAN server, plain HTTP on an IP | `http://192.168.1.194:8080/…` | Google rejects raw IP addresses. GitHub works. |
| LAN server, internal CA, `.lan` name | `https://agents.rookie.lan/…` | HTTPS satisfies Slack-class providers; Google rejects the reserved `.lan` suffix. |
| **Real domain, DNS-01 certificate, resolved on your LAN** | `https://agents.example.com/…` | **All providers work, with no inbound exposure.** |

The last row is the recommended setup for a self-hosted install: register a real
domain, obtain a certificate via a DNS-01 challenge (no inbound port needed),
and point the name at the server's private IP in your own DNS. Then set
`SA_PUBLIC_URL` — or the instance URL in Settings — to that address.

Settings → **Instance URL** shows what is currently in use and has a **Test this
URL** button that verifies the address actually reaches this server.
```

- [ ] **Step 4: Update `CLAUDE.md`**

Update the `SA_PUBLIC_URL` row in the environment-variable table to read:

```
| `SA_PUBLIC_URL` | — | externally reachable base URL for OAuth callbacks; validated at use (`internal/publicurl.Normalize`) and overridden by the instance URL in owner settings |
```

Add to the connector section, after the existing callback paragraph:

```markdown
**Redirect-URI reliability.** `internal/publicurl` owns the instance base URL
(`Resolve`: the `system_settings.public_url` row → `SA_PUBLIC_URL` → detection
from the request) and judges it against a provider's `redirect_policy` YAML block
(`Check`, a pure function). Only a policy marked `verified: true` may hard-block
the Connect button; an absent block is the zero `Policy`, which is fully
permissive, so rolling policies out provider by provider can never lock a user
out. Host classification hard-blocks only RFC-reserved suffixes — an ICANN
public suffix passes, and a PSL *private* entry such as `github.io` degrades to a
soft warning, because `publicsuffix.PublicSuffix` reports `icann=false` for both
it and `.lan`. The consent-time redirect URI is pinned into the signed OAuth
state (a 6th `~` field; 4- and 5-field states are still accepted for the 10-minute
TTL) so the token exchange cannot use a different string than consent did.
```

- [ ] **Step 5: Commit**

```bash
git add Dockerfile packaging/ CLAUDE.md
git commit -m "docs: document SA_PUBLIC_URL and the OAuth deployment matrix"
```

---

### Task 9: Full gate and pull request

- [ ] **Step 1: Run the complete CI gate locally**

Run: `make ci`
Expected: `ci-fmt`, `ci-vet`, `ci-test`, `ci-cross` and `ci-ui` all pass. Fix anything that fails before continuing; do not open the PR on a red gate.

- [ ] **Step 2: Verify the app still boots and serves**

Build, then start the server as a **background job** (this harness blocks a
foreground `sleep`, and a stray listener left on :8099 is exactly the setup for
killing the wrong process later):

```bash
go build -o "$CLAUDE_JOB_DIR/tmp/sa-verify" ./cmd/simple-agents
```

Start it with `run_in_background`, writing the PID to a file:

```bash
SA_DATA_DIR=$(mktemp -d) SA_PORT=8099 "$CLAUDE_JOB_DIR/tmp/sa-verify" serve \
  & echo $! > "$CLAUDE_JOB_DIR/tmp/sa-verify.pid"
```

Then probe and shut down **by captured PID only**:

```bash
curl -sS --retry 5 --retry-delay 1 --retry-connrefused \
  -o /dev/null -w 'healthz=%{http_code}\n' http://127.0.0.1:8099/healthz
curl -sS -o /dev/null -w 'echo=%{http_code}\n' 'http://127.0.0.1:8099/healthz/echo?token=bogus'
kill "$(cat "$CLAUDE_JOB_DIR/tmp/sa-verify.pid")"
```

Expected: `healthz=200` then `echo=404`.

Two rules here are not negotiable: use a **temporary** `SA_DATA_DIR`, never the
operator's live install; and kill **by captured PID**, never by name pattern — a
name-pattern kill has already taken down the live server once in this project.

- [ ] **Step 3: Push and open a draft PR**

```bash
git push -u origin worktree-oauth-redirect-reliability
gh pr create --draft \
  --title "feat(services): make OAuth redirect setup reliable and self-diagnosing" \
  --body "$(cat <<'EOF'
Implements docs/superpowers/specs/2026-07-29-oauth-redirect-reliability-design.md.

OAuth was unusable through the UI: 18 providers' setup steps tell the user to
register "the redirect URI shown above", and nothing showed it. The URI was also
derived per-request from the browser's Host header, recomputed independently at
consent and token exchange, and never checked against what the provider accepts.

- `internal/publicurl` resolves one instance base URL and judges it against a
  per-provider policy with a pure, table-tested `Check`.
- Redirect policy is YAML data; only `verified: true` policies may hard-block.
- The URI is pinned into the signed OAuth state so consent and exchange agree.
- Token-exchange failures map to a remedy instead of a raw error dump.
- The wizard shows the URI with a copy button and blocks provably-broken setups.
- Owner settings gain an instance URL with a self-test that proves the address
  reaches this process.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review notes

- **Spec coverage:** every spec section maps to a task — components 1/2 → Tasks 1-2, component 3 → Task 3, component 4 → Task 4, component 5 → Task 5, UX flows → Tasks 6-7, deployment guidance → Task 8, testing → distributed across all.
- **One spec deviation, deliberate:** the spec's YAML key `allow_reserved_host: false` became `require_public_host: true`. A Go bool defaults to `false`, so a field meaning "allow" would have defaulted to *deny* for every provider without a policy block — inverting the safety property the whole design rests on. The inverted name defaults correctly.
- **Task 6 depends on `authProv.Label`** being populated for the four verified providers; each provider YAML already carries `label:`, and `explainOAuthError` falls back to the raw provider name when it is empty.
- **The hard block is UI-only by design.** Do not add a server-side gate on the connect endpoint. Our policy data predicts a third party's validation rules rather than expressing an invariant we own, so a server-side gate would turn a stale YAML entry into a lockout with no override. The provider is the authoritative enforcement point.
- **Task 4 uses the pinned URI unconditionally.** An earlier draft rejected the callback when the pinned and freshly-computed URIs diverged; that bounces a user who has *already* granted consent, and loops if the cause is systematic. It logs and proceeds instead.
