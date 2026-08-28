package browser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

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

// irreversibleHints are control names that mean an action cannot be undone.
//
// This list is now the FIRST half of the only browser permission there is (the
// second half is the page test below), which raises the standard it has to meet.
// It was written as a second layer, where a miss cost nothing the lower tier was
// not already gating; that tier is gone, so a miss here is a real click on a
// real payment button.
//
// Non-English entries are not decoration. This platform's own owner is in
// Skopje, and a checkout button reading "Плати" or "Bezahlen" matched nothing at
// all in the English-only version — so the guard was silently absent on exactly
// the sites its owner is most likely to use. The list cannot be exhaustive
// across every language, which is why pageLooksIrreversible exists: it catches
// what the name test misses, without needing to know the word.
var irreversibleHints = []string{
	// English
	"pay", "pay now", "purchase", "buy", "buy now", "checkout", "check out",
	"place order", "confirm order", "submit order", "complete order",
	"confirm payment", "confirm and pay", "transfer", "send money", "withdraw",
	"delete", "delete account", "remove account", "close account",
	"cancel subscription", "unsubscribe", "confirm booking", "book now",
	// Macedonian / Serbian / Bulgarian (Cyrillic)
	"плати", "плаќање", "купи", "нарачај", "порачај", "потврди", "избриши",
	"откажи", "испрати",
	// German
	"bezahlen", "kaufen", "jetzt kaufen", "bestellen", "löschen", "kündigen",
	// French
	"payer", "acheter", "commander", "supprimer", "résilier",
	// Spanish / Portuguese
	"pagar", "comprar", "pedido", "realizar pedido", "eliminar", "borrar",
	// Italian
	"paga", "acquista", "ordina", "elimina",
	// Dutch / Nordic
	"betalen", "kopen", "betal", "kjøp", "köp", "slet", "slett",
}

// LooksIrreversible reports whether a control's accessible name suggests an
// action that cannot be undone.
//
// Matching is on word boundaries for the short entries: "pay" as a bare
// substring fires on "Payment history" and "Paypal settings", which are ordinary
// navigation — and a guard that fires on browsing would train an owner to switch
// it on permanently, which is worse than not having it at all.
func LooksIrreversible(name string) bool {
	return matchesHint(name, irreversibleHints)
}

// pageHints are page titles and URL fragments that mean "whatever you click
// here probably spends money or destroys something".
//
// The URL half is the more reliable of the two, because a checkout path is a
// convention every commerce platform follows and is not translated: /checkout,
// /payment and /billing look the same in Skopje as in Seattle.
var pageHints = []string{
	"checkout", "check-out", "payment", "payments", "billing",
	"place-order", "placeorder", "order-confirm", "confirm-order",
	"purchase", "subscribe", "cancel-subscription", "close-account",
	"delete-account", "transfer", "withdraw",
	"плаќање", "нарачка", "kasse", "bezahlung", "paiement", "pagamento", "pago",
}

// pageLooksIrreversible judges the PAGE rather than the control.
//
// This is what makes the guard work on a button with no accessible name, and on
// browser_press, which has no control at all — the two ways a name-only test is
// walked past on a form that is one Enter away from a payment.
func pageLooksIrreversible(page PageContext) bool {
	if matchesHint(page.Title, pageHints) {
		return true
	}
	// Only the PATH and query are matched, never the host.
	//
	// Matching the whole URL looked equivalent and is not: a company whose
	// domain is billing-portal.example.com, or a shop hosted at
	// payments.example.com, would have EVERY action on EVERY page treated as a
	// payment. That is the failure mode this guard most has to avoid — one that
	// fires while merely browsing teaches the owner to switch it on permanently,
	// which is worse than not having it. A checkout PATH is a real signal; a
	// company's choice of hostname is not.
	//
	// Within the path it is a plain substring match rather than a word-boundary
	// one, because "/store/checkout?step=2" carries the signal inside a token.
	u, err := url.Parse(page.URL)
	if err != nil {
		return false
	}
	path := strings.ToLower(u.EscapedPath() + "?" + u.RawQuery)
	for _, h := range pageHints {
		if strings.Contains(path, h) {
			return true
		}
	}
	return false
}

// matchesHint tests a phrase against a hint list on WORD boundaries.
func matchesHint(s string, hints []string) bool {
	n := strings.ToLower(strings.TrimSpace(s))
	if n == "" {
		return false
	}
	// Split on anything that is not a letter or digit in ANY script. The
	// previous version restricted itself to a-z, which silently discarded every
	// Cyrillic and accented word — so the non-English entries above would have
	// been unmatchable even once added.
	words := strings.FieldsFunc(n, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	joined := " " + strings.Join(words, " ") + " "
	for _, hint := range hints {
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
