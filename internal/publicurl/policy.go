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
