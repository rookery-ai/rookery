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

	t.Setenv("SA_PUBLIC_URL", "")
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
	t.Setenv("SA_PUBLIC_URL", "")
	bad := func(string) (string, error) { return "localhost:8080", nil }
	got, src := Resolve(bad, "http://detected:8080")
	if got != "http://detected:8080" || src != SourceDetected {
		t.Fatalf("bad configured value must fall through: got %q src %v", got, src)
	}
}
