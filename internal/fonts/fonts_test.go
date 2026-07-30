package fonts

import "testing"

func TestInterVariableIsAWOFF2(t *testing.T) {
	if len(InterVariableWOFF2) == 0 {
		t.Fatal("embedded font is empty")
	}
	if got := string(InterVariableWOFF2[:4]); got != "wOF2" {
		t.Fatalf("not a woff2: magic bytes = %q", got)
	}
	// Guards against a truncated checkout or an LFS pointer landing here
	// instead of the real file — both would embed "successfully" and then
	// render nothing.
	if len(InterVariableWOFF2) < 20_000 {
		t.Fatalf("font suspiciously small: %d bytes", len(InterVariableWOFF2))
	}
}
