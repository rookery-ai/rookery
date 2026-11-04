package browser

import "testing"

func TestFillVerdict(t *testing.T) {
	cases := []struct{ typed, got, want string }{
		{"4111111111111111", "4111 1111 1111 1111", "matched"},
		{"12/30", "12/30", "matched"},
		{"Ana Petrova", "ANA PETROVA", "matched"},
		{"07/2030", "07/20", "partial: 4 of 6 characters"},
		{"abc", "", "did not take the value"},
		{"abc", "xyz", "did not take the value"},
	}
	for _, c := range cases {
		if got := fillVerdict(c.typed, c.got); got != c.want {
			t.Errorf("fillVerdict(%q, %q) = %q, want %q", c.typed, c.got, got, c.want)
		}
	}
}

// Playwright's own errors can quote the text being typed, so redacting them is
// not enough: a secret fill's failure is REPLACED by a fixed sentence.
func TestSecretFillErrorIsFixed(t *testing.T) {
	got := secretFillError("${CARD}", "f2e2").Error()
	want := "typing ${CARD} into ref f2e2 failed — re-read the page before trying again"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestMaxlengthHint(t *testing.T) {
	n := fillNote("partial: 4 of 6 characters", 5, 7)
	for _, want := range []string{"partial", "at most 5", "${NAME[-2:]}"} {
		if !contains(n, want) {
			t.Errorf("note %q lacks %q", n, want)
		}
	}
	if n := fillNote("matched", 5, 5); n != "fill: matched" {
		t.Errorf("matched note = %q", n)
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
