package vault

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// A crafted path must not be able to forge a log entry — by ANY route.
//
// The first fix sanitised the `path` field and left `error` alone, which let the
// value straight back in: Vault.Resolve returns
// `fmt.Errorf("%w: %q", ErrEscapes, relPath)`, so the error text quotes the very
// path the field beside it had just cleaned. CodeQL caught that; this test is so
// the next reader does not have to.
//
// Asserted against a handler that does NOT quote, deliberately. Go's default
// slog handler escapes attribute values, which would make this pass whether or
// not the sanitising is there — the point is that safety should not depend on a
// handler choice made in another file.
func TestScopedSearchCannotForgeALogEntry(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(newRawHandler(&buf)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	v := New(t.TempDir())
	const ws = "u1"
	mustWrite(t, v, ws, "notes/real.md", "# Real\n\ndentist\n")

	// A path that does NOT escape the vault, so Vault.Resolve accepts it and the
	// failure comes from the file read instead. That distinction is the whole
	// test: Resolve formats with %q and escapes the newline itself, so an
	// escaping path proves nothing. An os error quotes the path RAW.
	evil := "notes/a\nFORGED 2026-08-21 INFO the server was compromised.md"
	SearchKBIn(context.Background(), v, v.NewSearcher(), ws, "dentist", evil, 4096)

	out := buf.String()
	if out == "" {
		t.Skip("no log line emitted; nothing to assert")
	}
	if strings.Count(out, "\n") > 1 {
		t.Errorf("a crafted path produced more than one log line:\n%q", out)
	}
	if strings.Contains(out, "FORGED 2026-08-21 INFO") && strings.Contains(out, "\nFORGED") {
		t.Errorf("the forged entry starts its own line:\n%q", out)
	}
}

// rawHandler writes attributes without quoting or escaping, so the test
// measures the sanitising rather than the handler's incidental protection.
type rawHandler struct{ w *bytes.Buffer }

func newRawHandler(w *bytes.Buffer) slog.Handler { return &rawHandler{w: w} }

func (h *rawHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *rawHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *rawHandler) WithGroup(string) slog.Handler            { return h }
func (h *rawHandler) Handle(_ context.Context, r slog.Record) error {
	h.w.WriteString(r.Level.String() + " " + r.Message)
	r.Attrs(func(a slog.Attr) bool {
		h.w.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	h.w.WriteString("\n")
	return nil
}
