package agentdesigner

import "testing"

func TestOriginLabels(t *testing.T) {
	cases := []struct {
		origin Origin
		label  string
	}{
		{OriginWeb, "the web app"},
		{OriginChat, "your chat app"},
		{Origin(""), "another surface"},
	}
	for _, c := range cases {
		if got := c.origin.Label(); got != c.label {
			t.Errorf("Origin(%q).Label() = %q, want %q", c.origin, got, c.label)
		}
	}
}

// The wire form is what /design/state sends and what the SPA compares against,
// so it must be the bare word, not a Go-ish rendering.
func TestOriginStringIsTheWireForm(t *testing.T) {
	if OriginWeb.String() != "web" || OriginChat.String() != "chat" {
		t.Errorf("String() = %q/%q, want web/chat", OriginWeb, OriginChat)
	}
}

// Owns is the ownership predicate the Step gate and the web cancel handler both
// read. A zero origin on either side must fail OPEN: a session built by a test,
// or one created before this field existed, has to stay drivable.
func TestOriginOwns(t *testing.T) {
	cases := []struct {
		owner, from Origin
		want        bool
	}{
		{OriginWeb, OriginWeb, true},
		{OriginChat, OriginChat, true},
		{OriginWeb, OriginChat, false},
		{OriginChat, OriginWeb, false},
		{Origin(""), OriginChat, true},
		{OriginWeb, Origin(""), true},
	}
	for _, c := range cases {
		if got := c.owner.Owns(c.from); got != c.want {
			t.Errorf("Origin(%q).Owns(%q) = %v, want %v", c.owner, c.from, got, c.want)
		}
	}
}
