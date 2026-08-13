package agentdesigner

import "fmt"

// Session ownership.
//
// A design session is a per-workspace singleton that BOTH the web UI and a chat
// adapter can reach. Before this type existed, neither the session nor the
// build-completion hook knew which of them the user was actually using — so a
// build started in the browser announced its dry-run result in Telegram and left
// the web surface blank, and a browser tab that merely ADOPTED a running chat
// session could cancel it.
//
// Origin is fixed when the session is created and never moves: the owning
// surface drives, the other one may read. Ownership is deliberately not
// persisted to the draft (that would need a column, and this change adds no
// migration), so a draft resumed after a restart is owned by whoever resumed it.
// That is correct rather than a compromise — after a restart there is no
// in-flight build and no surface holds a live view.

// Origin identifies the surface that owns a design session.
type Origin string

const (
	// OriginWeb is a session created from the SPA.
	OriginWeb Origin = "web"
	// OriginChat is a session created from a chat adapter (Telegram, Discord, Slack).
	OriginChat Origin = "chat"
)

// String is the wire form sent on /design/state and compared by the SPA.
func (o Origin) String() string { return string(o) }

// Label names the surface in a user-facing refusal.
//
// Deliberately generic for chat: a workspace may have several adapters linked
// and Origin does not record which one the message came from, so naming the
// wrong app would be worse than naming none.
func (o Origin) Label() string {
	switch o {
	case OriginWeb:
		return "the web app"
	case OriginChat:
		return "your chat app"
	default:
		return "another surface"
	}
}

// Owns reports whether a session with this origin may be driven from `from`.
//
// A zero origin on EITHER side fails open. A session built by a test, or one
// created by a build predating this field, would otherwise be drivable from
// nowhere — bricking it rather than merely leaving it unprotected. The cost of
// failing open is that such a session keeps today's behaviour; the cost of
// failing closed is a user who cannot touch their own session from any surface.
func (o Origin) Owns(from Origin) bool {
	return o == "" || from == "" || o == from
}

// errSessionActiveElsewhere is the refusal every creation entry point returns
// when a session already exists. It names the owning surface AND the way out:
// the old "you already have an active design session" told the user neither
// where to continue nor how to escape.
func errSessionActiveElsewhere(owner Origin) error {
	return fmt.Errorf(
		"you already have an active design session in %s; continue there, or send /agent cancel to discard it",
		owner.Label(),
	)
}

// nonOwnerRefusal is what a design turn from the wrong surface gets back.
//
// A plain response string, not an error: chat renders it verbatim, and an error
// would be reported to the user as a failure when nothing failed. It names
// /agent cancel because that is the ONLY action a non-owner may take — without
// it, a web-owned session whose browser is gone would lock chat out until the
// draft TTL expires a week later.
func nonOwnerRefusal(owner Origin) string {
	return fmt.Sprintf(
		"This design session is active in %s — please continue there.\n\n"+
			"If you'd rather start over here, send `/agent cancel` to discard it.",
		owner.Label(),
	)
}
