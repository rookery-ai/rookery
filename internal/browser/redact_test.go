package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func newRedactor(vals ...string) *redactor {
	r := &redactor{}
	for _, v := range vals {
		r.remember(v)
	}
	return r
}

func TestRedactExact(t *testing.T) {
	if got := newRedactor("hunter22").apply("pw hunter22"); got != "pw [redacted]" {
		t.Fatalf("got %q", got)
	}
}

// A field that reformats a card number echoes a string that is not the secret
// byte-for-byte. Exact matching let exactly that reach the model.
func TestRedactSeparatorTolerant(t *testing.T) {
	r := newRedactor("4111111111111111")
	for _, in := range []string{"4111 1111 1111 1111", "4111-1111-1111-1111", "4111.1111.1111.1111", "value: 4111 1111 1111 1111 ok"} {
		got := r.apply(in)
		if strings.Contains(got, "1111") || !strings.Contains(got, "[redacted]") {
			t.Errorf("apply(%q) = %q", in, got)
		}
	}
}

// An email typed from a secret can come back as a later page's pre-filled
// value, and it escaped redaction because the separator class allowed "." but not "@".
// A secret's own punctuation must always be allowed between its characters.
func TestRedactSecretWithItsOwnPunctuation(t *testing.T) {
	r := newRedactor("first.last123@example.org")
	for _, in := range []string{"value: first.last123@example.org", "FIRST.LAST123@EXAMPLE.ORG", "first.last123 @ example.org"} {
		if got := r.apply(in); strings.Contains(strings.ToLower(got), "last123") {
			t.Errorf("apply(%q) = %q", in, got)
		}
	}
	r = newRedactor("p@ss_w0rd!")
	if got := r.apply("typed p@ss_w0rd! here"); strings.Contains(got, "w0rd") {
		t.Errorf("got %q", got)
	}
	// Characters that are special inside a character class must not break
	// compilation (an unescaped "-" makes a range; a bad range panics).
	for _, v := range []string{"a-z@0^9]x[y\\q", "~·-@zz12", "ab]c-d^e"} {
		r = newRedactor(v)
		if got := r.apply("x " + v + " y"); strings.Contains(got, v) {
			t.Errorf("%q not redacted: %q", v, got)
		}
	}
}

func TestRedactCaseInsensitive(t *testing.T) {
	if got := newRedactor("AbCdEf").apply("abcdef"); got != "[redacted]" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactMaskedTail(t *testing.T) {
	r := newRedactor("4111111111114242")
	for _, in := range []string{"card •••• 4242", "**** **** **** 4242", "411111******4242", "xxxx-4242"} {
		got := r.apply(in)
		if strings.Contains(got, "4242") || strings.Contains(got, "411111") {
			t.Errorf("apply(%q) = %q", in, got)
		}
	}
}

// Redacting every last-4 run would wipe ordinary amounts and years off a page.
func TestRedactLeavesNonSecretsAlone(t *testing.T) {
	r := newRedactor("4111111111114242")
	in := "Total 1750.00 — due 2031 — ref 4242"
	if got := r.apply(in); got != in {
		t.Fatalf("got %q", got)
	}
}

func TestRedactShortSecrets(t *testing.T) {
	if got := newRedactor("123").apply("cvv 123"); got != "cvv [redacted]" {
		t.Fatalf("3-char secret: got %q", got)
	}
	if got := newRedactor("12").apply("12/30"); got != "12/30" {
		t.Fatalf("2-char secret must not mangle text: got %q", got)
	}
}

func TestRedactNilAndEmpty(t *testing.T) {
	var r *redactor
	if r.apply("x") != "x" {
		t.Fatal("nil redactor must pass through")
	}
	if newRedactor("secret1").apply("") != "" {
		t.Fatal("empty in, empty out")
	}
}

// The CLI bridge is the host-side layer for CLI coders: every channel of a
// result is redacted before the subprocess sees it.
func TestBridgeSessionRedactsEveryChannel(t *testing.T) {
	s := &bridgeSession{}
	s.red.remember("4111111111114242")
	r := s.redactResult(Result{
		Text: "4111 1111 1111 4242", Title: "•••• 4242", FinalURL: "https://x.example/?p=4111111111114242",
		BlockedNote: "4111-1111-1111-4242", ActNote: "x 4111111111114242",
		Elements: []Element{{Name: "4111111111114242", Note: "value: 4111 1111 1111 4242"}},
	})
	raw := strings.Join([]string{r.Text, r.Title, r.FinalURL, r.BlockedNote, r.ActNote, r.Elements[0].Name, r.Elements[0].Note}, "|")
	if strings.Contains(raw, "4242") || strings.Contains(raw, "4111") {
		t.Fatalf("leaked: %s", raw)
	}
}

func TestResolveSecretSlice(t *testing.T) {
	vals := map[string]string{"YEAR": "2030", "MONTH": "07"}
	res := func(_ context.Context, _, name string) (string, error) {
		if v, ok := vals[name]; ok {
			return v, nil
		}
		return "", errors.New("missing")
	}
	cases := map[string]string{
		"${YEAR[-2:]}":          "30",
		"${YEAR[0:2]}":          "20",
		"${YEAR[:2]}":           "20",
		"${MONTH}/${YEAR[-2:]}": "07/30",
		"${YEAR}":               "2030",
		"${YEAR[1:]}":           "030",
		"${YEAR[-10:]}":         "2030",
	}
	for in, want := range cases {
		got, secret, err := ResolveSecretValue(context.Background(), res, "ws", in)
		if err != nil || got != want || !secret {
			t.Errorf("%s → %q secret=%v err=%v, want %q", in, got, secret, err, want)
		}
	}
	if _, _, err := ResolveSecretValue(context.Background(), res, "ws", "${YEAR[x:]}"); err == nil || !strings.Contains(err.Error(), "slice") {
		t.Errorf("malformed slice: err=%v", err)
	}
	if !MentionsSecret("${YEAR[-2:]}") {
		t.Error("a slice placeholder must count as a secret mention")
	}
}
