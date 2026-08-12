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
		name     string
		base     string
		wantCode string // "" means no problems
		wantSev  Severity
	}{
		{"localhost is exempt", "http://localhost:8080", "", 0},
		{"loopback ip is exempt", "http://127.0.0.1:8080", "", 0},
		{"ipv6 loopback is exempt", "http://[::1]:8080", "", 0},
		{"public https domain is clean", "https://agents.example.com", "", 0},
		{"multi-label suffix is clean", "https://agents.example.co.uk", "", 0},
		{"lan ip rejected", "http://192.168.1.50:8080", "raw_ip", SeverityHard},
		{"reserved tld rejected", "https://agents.example.lan", "non_public_host", SeverityHard},
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
	got := Check("http://192.168.1.50:8080", unverified)
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
	for _, base := range []string{"http://192.168.1.50:8080", "https://agents.example.lan", "http://box"} {
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
