package buildinfo

import "testing"

// An unstamped binary (go build with no -ldflags, i.e. every developer build)
// must still produce something honest rather than an empty string.
func TestDefaultsAreHonest(t *testing.T) {
	if Version == "" {
		t.Error("Version must never be empty")
	}
	if Short() != Version {
		t.Errorf("Short() = %q, want %q", Short(), Version)
	}
	if got := String(); got == "" {
		t.Error("String() must never be empty")
	}
}

func TestStringIncludesAllFields(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	defer func() { Version, Commit, Date = oldV, oldC, oldD }()

	Version, Commit, Date = "0.1.0", "abc1234", "2026-07-28T00:00:00Z"
	want := "0.1.0 (abc1234, built 2026-07-28T00:00:00Z)"
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
