package browser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

// Action names the acting verbs. The set is deliberately small and closed: a
// weak model chooses badly from a large menu, and every verb here is one the
// host can describe, gate and audit precisely.
type Action string

const (
	ActionClick Action = "click"
	ActionFill  Action = "fill"
	ActionPress Action = "press"
	ActionWait  Action = "wait"
	ActionRead  Action = "read"
)

// IsMutating reports whether an action changes state on the far side.
//
// This is the predicate the build-phase refusal keys on. `read` and `wait`
// observe; everything else acts. Getting this wrong in the permissive direction
// means a rehearsal of an unapproved agent clicks a real "Pay" button, which is
// precisely the failure this package exists to prevent.
func IsMutating(a Action) bool {
	switch a {
	case ActionRead, ActionWait:
		return false
	default:
		return true
	}
}

// irreversibleHints are accessible-name fragments that suggest an action cannot
// be undone.
//
// This is a HEURISTIC and is treated as one. It is the second of two tiers, not
// the protection: acting is refused outright unless the owner has granted this
// agent acting rights on this session, and irreversible actions need a second,
// separate grant on top. A heuristic that misses therefore costs nothing the
// first tier was not already gating — which is the only reason a word list is
// acceptable here at all.
var irreversibleHints = []string{
	"pay", "purchase", "buy", "checkout", "place order", "confirm order",
	"submit order", "transfer", "send money", "withdraw", "delete",
	"remove account", "cancel subscription", "unsubscribe", "confirm payment",
}

// LooksIrreversible reports whether a control's accessible name suggests an
// irreversible action. Matching is on word boundaries for the short entries:
// "pay" as a bare substring fires on "Payment history" and "Paypal settings",
// which are ordinary navigation and would train the owner to grant the
// irreversible tier just to browse.
func LooksIrreversible(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	words := strings.FieldsFunc(n, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	joined := " " + strings.Join(words, " ") + " "
	for _, hint := range irreversibleHints {
		if strings.Contains(hint, " ") {
			if strings.Contains(joined, " "+hint+" ") {
				return true
			}
			continue
		}
		if strings.Contains(joined, " "+hint+" ") {
			return true
		}
	}
	return false
}

type actReq struct {
	Session   string `json:"session"`
	Action    Action `json:"action"`
	Ref       string `json:"ref"`
	Value     string `json:"value"`
	Key       string `json:"key"`
	WaitFor   string `json:"wait_for"`
	TimeoutMS int    `json:"timeout_ms"`
	Elements  bool   `json:"elements"`
	// Secret marks Value as a resolved secret so the session can redact it back
	// out of every later result. The helper is told this explicitly rather than
	// guessing, because it has no database and cannot know what a secret looks
	// like.
	Secret bool `json:"secret"`
}

func (h *browserHost) handleAct(w http.ResponseWriter, r *http.Request) {
	var req actReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	sess := h.sess[req.Session]
	h.mu.Unlock()
	if sess == nil {
		writeErr(w, fmt.Errorf("no open page for this run — call browser_open first"))
		return
	}

	timeout := float64(req.TimeoutMS)
	if timeout <= 0 {
		timeout = 15000
	}
	if req.Secret {
		sess.rememberSecret(req.Value)
	}

	if err := h.perform(sess, req, timeout); err != nil {
		writeErr(w, fmt.Errorf("%s failed: %s", req.Action, sess.redact(err.Error())))
		return
	}
	writeFacts(w, h.collect(sess, nil, collectOpts{elements: req.Elements}))
}

func (h *browserHost) perform(sess *hostSession, req actReq, timeout float64) error {
	switch req.Action {
	case ActionRead:
		return nil
	case ActionWait:
		return applyExtraWait(sess.page, req.WaitFor, timeout)
	case ActionPress:
		return sess.page.Keyboard().Press(req.Key)
	case ActionClick:
		loc, err := h.locate(sess, req.Ref)
		if err != nil {
			return err
		}
		return loc.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(timeout)})
	case ActionFill:
		loc, err := h.locate(sess, req.Ref)
		if err != nil {
			return err
		}
		return loc.Fill(req.Value, playwright.LocatorFillOptions{Timeout: playwright.Float(timeout)})
	}
	return fmt.Errorf("unknown action %q", req.Action)
}

// locate resolves a [ref=eN] handle through Playwright's aria-ref selector
// engine. This is the whole reason the element list is built from an aria
// snapshot: the model addresses controls by a handle the browser already
// assigned, instead of composing a CSS selector it has no reliable way to get
// right.
func (h *browserHost) locate(sess *hostSession, ref string) (playwright.Locator, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("a ref is required — call browser_open (or read) first and use one of the listed refs")
	}
	// A ref goes stale the moment the page navigates or re-renders. Saying so
	// explicitly matters: the alternative message is Playwright's own "strict
	// mode violation / element not found", which reads as "the button is gone"
	// and sends a weak model hunting for a different control instead of simply
	// re-reading the page.
	return sess.page.Locator("aria-ref=" + ref), nil
}
