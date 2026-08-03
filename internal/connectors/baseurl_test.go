package connectors

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain host", "https://ha.example.com", "https://ha.example.com"},
		{"trailing slash stripped", "https://ha.example.com/", "https://ha.example.com"},
		{"several trailing slashes stripped", "https://ha.example.com///", "https://ha.example.com"},
		{"surrounding whitespace trimmed", "  https://ha.example.com  ", "https://ha.example.com"},
		{"http allowed for a LAN box", "http://192.168.1.10:8123", "http://192.168.1.10:8123"},
		{"port preserved", "https://ha.example.com:8123", "https://ha.example.com:8123"},
		// A path PREFIX is mainstream, not an error: Nextcloud at /nextcloud and a
		// reverse-proxied Paperless at /paperless are both normal deployments.
		{"path prefix preserved", "https://example.com/nextcloud", "https://example.com/nextcloud"},
		{"path prefix trailing slash stripped", "https://example.com/paperless/", "https://example.com/paperless"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeBaseURL(tc.in)
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q) errored: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeBaseURLRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"whitespace only", "   "},
		// url.Parse reads "homeassistant.local" as a scheme here, so the scheme check
		// is what catches it — not a host check.
		{"no scheme with port", "homeassistant.local:8123"},
		{"no scheme bare host", "ha.example.com"},
		{"unsupported scheme", "ftp://ha.example.com"},
		{"scheme but no host", "https://"},
		{"query string", "https://ha.example.com?token=abc"},
		{"fragment", "https://ha.example.com#section"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := NormalizeBaseURL(tc.in); err == nil {
				t.Errorf("NormalizeBaseURL(%q) = %q, want an error", tc.in, got)
			}
		})
	}
}
