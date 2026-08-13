package agentdesigner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/coder"
)

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

// The lifecycle log records a CLASS, never the error text — a provider error can
// echo back the request that produced it, and CodeQL traced that to the
// workspace's API-key secret. The class must still discriminate the cases an
// operator actually triages on, or the redaction has cost the log its value.
func TestBuildErrClass(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "canceled"},
		{coder.ErrUsageLimit, "usage_limit"},
		{coder.ErrRateLimited, "rate_limited"},
		{coder.ErrAPIAuth, "auth"},
		{coder.ErrMaxTurns, "max_turns"},
		{coder.ErrLocalCoderDisabled, "local_coder_disabled"},
		{errors.New("something else"), "other"},
		// Wrapped sentinels must still classify — the coder wraps on the way out.
		{fmt.Errorf("generate: %w", coder.ErrUsageLimit), "usage_limit"},
	}
	for _, c := range cases {
		if got := buildErrClass(c.err); got != c.want {
			t.Errorf("buildErrClass(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// The class must never carry the underlying message, which is the entire point.
//
// The canary is deliberately NOT shaped like a real credential. An earlier draft
// used a provider-style live-key literal, which reads better and fails the
// build: gitleaks scans the repository for that shape and cannot tell a test
// fixture from the real thing. The assertion holds for ANY string, so the
// credential shape was decorative — if the message were echoed into the class,
// this marker would appear in it just the same.
//
// Per .gitleaks.toml, the fix is to change the fixture, never to allowlist it:
// an exception has to be spelled out in a file that other scanners also read,
// which is how a previous allowlist entry made Trivy flag the allowlist itself.
func TestBuildErrClassNeverEchoesTheMessage(t *testing.T) {
	canary := "CANARY-must-not-appear-in-the-class"
	got := buildErrClass(fmt.Errorf("provider rejected credential %s", canary))
	if strings.Contains(got, canary) || got != "other" {
		t.Errorf("buildErrClass leaked the message: %q", got)
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
