package render

import "testing"

func TestPassthroughReturnsInputUnchanged(t *testing.T) {
	got := Passthrough().Render("**bold** and `code`")
	if got != "**bold** and `code`" {
		t.Fatalf("passthrough altered input: %q", got)
	}
}

func TestForUnknownPlatformFallsBackToPassthrough(t *testing.T) {
	got := For("nonexistent-platform").Render("hello .world!")
	if got != "hello .world!" {
		t.Fatalf("For(unknown) should passthrough, got %q", got)
	}
}

func TestRegisterAndFor(t *testing.T) {
	Register("upper-test", RendererFunc(func(s string) string { return s + "!!" }))
	if got := For("upper-test").Render("x"); got != "x!!" {
		t.Fatalf("registered renderer not used, got %q", got)
	}
}
