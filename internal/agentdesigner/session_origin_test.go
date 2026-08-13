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

// Both directions of the delivery decision, which is the whole bug. A chat-owned
// build MUST reach chat; a web-owned build must NOT — pushing it anyway is what
// put a dry-run in Telegram while the user watched a blank browser.
//
// An unset origin does not deliver: strict by design, licensed by
// TestEveryCreationPathStampsOrigin proving no real path leaves it unset.
func TestDeliverToChat(t *testing.T) {
	cases := []struct {
		origin Origin
		want   bool
	}{
		{OriginChat, true},
		{OriginWeb, false},
		{Origin(""), false},
	}
	for _, c := range cases {
		if got := DeliverToChat(c.origin); got != c.want {
			t.Errorf("DeliverToChat(%q) = %v, want %v", c.origin, got, c.want)
		}
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
