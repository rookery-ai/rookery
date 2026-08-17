package prompts

import (
	"strings"
	"testing"
)

// Nothing in these prompts mentioned the inbox, so a user asking for "notify me
// in the inbox, not on Telegram" met a model that had never heard of it. It
// could neither confirm the inbox exists nor explain that the channels cannot be
// chosen between, and proposed a SILENT agent instead — close to the opposite of
// the request.
func TestPlatformContextDescribesTheInbox(t *testing.T) {
	surfaces := []Surface{SurfaceAgent, SurfaceChat}
	for _, surface := range surfaces {
		p := platformContextBlock(surface, nil, "/vault")
		if !strings.Contains(p, "## Inbox") {
			t.Fatalf("surface %v: no inbox section", surface)
		}
		// The load-bearing fact: the inbox is automatic, so an agent must not go
		// looking for a way to write to it.
		if !strings.Contains(p, "automatically") {
			t.Errorf("surface %v: does not say inbox delivery is automatic", surface)
		}
	}
}

// The constraint is the whole point. A model that knows delivery is
// all-or-nothing can offer the two real options; one that does not will either
// agree to something impossible or quietly pick silent.
func TestPlatformContextStatesTheChannelsCannotBeSplit(t *testing.T) {
	p := platformContextBlock(SurfaceAgent, nil, "/vault")
	if !strings.Contains(p, "NOT supported") {
		t.Error("does not state that choosing one channel is unsupported")
	}
	for _, want := range []string{"[SILENT]", "inbox AND every connected chat app"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q — the two real options must both be named", want)
		}
	}
}

// "Inbox" is ambiguous in a product that also connects Gmail and Outlook, and
// the two mean completely different work: one is automatic, the other is sending
// mail through a connector.
func TestPlatformContextDisambiguatesTheInboxFromEmail(t *testing.T) {
	p := platformContextBlock(SurfaceAgent, nil, "/vault")
	for _, want := range []string{"Gmail", "Outlook", "SENDING AN EMAIL"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q — an email inbox must not be confused with this one", want)
		}
	}
}

// Guard against the fix drifting into a capability claim. Nothing here should
// suggest an agent can pick a channel, because internal/agentrunner pairs
// recordInbox with SendOutput at every delivery site — the split does not exist.
func TestInboxBlockPromisesNoChannelSelection(t *testing.T) {
	p := platformContextBlock(SurfaceAgent, nil, "/vault")
	for _, forbidden := range []string{
		"inbox-only mode",
		"set the delivery channel",
		"choose which channel",
	} {
		if strings.Contains(strings.ToLower(p), strings.ToLower(forbidden)) {
			t.Errorf("prompt implies channel selection exists: %q", forbidden)
		}
	}
}
