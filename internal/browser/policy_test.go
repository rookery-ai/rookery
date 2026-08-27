package browser

import (
	"errors"
	"strings"
	"testing"
)

func checkoutPage() PageContext {
	return PageContext{Title: "Checkout", URL: "https://shop.example.com/checkout", NameKnown: true}
}

func ordinaryPage() PageContext {
	return PageContext{Title: "Your invoices", URL: "https://billing-portal.example.com/invoices/2026", NameKnown: true}
}

// Ordinary interaction needs no permission at all. This is the change the whole
// rework is about: an agent asked to log in and read a bill must simply do it.
// The lower tier that used to gate this was removed after it was shown to gate
// nothing — the agent just used bash and curl instead.
func TestOrdinaryActingNeedsNoPermission(t *testing.T) {
	pol := Policy{}
	for _, a := range []Action{ActionClick, ActionFill, ActionPress} {
		if err := CheckAct(pol, a, "Sign in", ordinaryPage()); err != nil {
			t.Errorf("%s on an ordinary page was refused: %v", a, err)
		}
	}
}

func TestIrreversibleActionsNeedPermission(t *testing.T) {
	err := CheckAct(Policy{}, ActionClick, "Pay now", ordinaryPage())
	if !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("paying was allowed with no permission: %v", err)
	}
	if err := CheckAct(Policy{AllowIrreversible: true}, ActionClick, "Pay now", ordinaryPage()); err != nil {
		t.Fatalf("paying refused despite permission: %v", err)
	}
}

// The hole a name-only test leaves: submitting a focused payment form with Enter
// has no control to judge, so the page has to be judged instead.
func TestAKeypressOnAPaymentPageNeedsPermission(t *testing.T) {
	err := CheckAct(Policy{}, ActionPress, "", checkoutPage())
	if !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("Enter on a checkout page was allowed: %v", err)
	}
	// The same keypress on an ordinary page is fine — otherwise every form on
	// the web would need the permission.
	if err := CheckAct(Policy{}, ActionPress, "", ordinaryPage()); err != nil {
		t.Errorf("Enter on an ordinary page was refused: %v", err)
	}
}

// The second hole: a button with no accessible name, on a page that is clearly
// about money.
func TestAnUnidentifiableControlOnAPaymentPageNeedsPermission(t *testing.T) {
	page := checkoutPage()
	page.NameKnown = false
	if err := CheckAct(Policy{}, ActionClick, "", page); !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("an unnamed control on a checkout page was allowed: %v", err)
	}
}

// A checkout page makes EVERY mutating action need the permission, whatever the
// control happens to be called — "Continue" on the last step of a checkout is a
// payment.
func TestAnyActionOnACheckoutPageNeedsPermission(t *testing.T) {
	if err := CheckAct(Policy{}, ActionClick, "Continue", checkoutPage()); !errors.Is(err, ErrActingDisabled) {
		t.Fatalf(`"Continue" on a checkout page was allowed: %v`, err)
	}
}

// A test run may do everything up to the irreversible step, and must then
// DESCRIBE it rather than perform it. Refusing ordinary acting too would make a
// rehearsal unable to log in, which is most of what a rehearsal is for.
func TestABuildMayInteractButNotFinishAnIrreversibleStep(t *testing.T) {
	pol := Policy{BuildPhase: true, AllowIrreversible: true}
	if err := CheckAct(pol, ActionFill, "Password", ordinaryPage()); err != nil {
		t.Fatalf("a build was refused an ordinary fill: %v", err)
	}
	err := CheckAct(pol, ActionClick, "Pay now", ordinaryPage())
	if !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("a build performed an irreversible action: %v", err)
	}
	if !strings.Contains(err.Error(), "would have done") {
		t.Errorf("the build refusal does not ask the model to describe the step: %q", err)
	}
}

// The owner's grant must not license a rehearsal of an agent they have not yet
// approved to spend their money.
func TestABuildRefusesIrreversibleEvenWithPermission(t *testing.T) {
	err := CheckAct(Policy{BuildPhase: true, AllowIrreversible: true}, ActionClick, "Place order", ordinaryPage())
	if !errors.Is(err, ErrActingDisabled) {
		t.Fatalf("permission overrode the test-run rule: %v", err)
	}
}

func TestReadingAndWaitingAreNeverGated(t *testing.T) {
	for _, a := range []Action{ActionRead, ActionWait} {
		if err := CheckAct(Policy{BuildPhase: true}, a, "Pay now", checkoutPage()); err != nil {
			t.Errorf("%s was refused: %v", a, err)
		}
	}
}

// A guard that fires while merely browsing teaches the owner to switch it on
// permanently, which is worse than not having it.
func TestOrdinaryNavigationIsNotTreatedAsIrreversible(t *testing.T) {
	safe := []string{
		"Payment history", "Paypal settings", "Payments", "Buyer protection",
		"Deleted items", "Order history", "Transfers overview", "About paying",
	}
	for _, name := range safe {
		if LooksIrreversible(name) {
			t.Errorf("%q classified as irreversible", name)
		}
	}
}

func TestIrreversibleNamesAreCaught(t *testing.T) {
	risky := []string{
		"Pay", "Pay now", "Confirm payment", "Place order", "Submit order",
		"Buy now", "Delete", "Transfer", "Withdraw", "Cancel subscription",
	}
	for _, name := range risky {
		if !LooksIrreversible(name) {
			t.Errorf("%q not classified as irreversible", name)
		}
	}
}

// The owner of this platform is in Skopje. An English-only word list left the
// guard silently absent on exactly the sites he is most likely to use — and the
// old word splitter discarded non-Latin characters outright, so adding the words
// without fixing it would have changed nothing.
func TestNonEnglishPaymentButtonsAreCaught(t *testing.T) {
	for _, name := range []string{"Плати", "Плати сега", "Купи", "Bezahlen", "Jetzt kaufen", "Payer", "Pagar", "Acquista"} {
		if !LooksIrreversible(name) {
			t.Errorf("%q not classified as irreversible", name)
		}
	}
}

func TestPaymentPagesAreRecognisedByURL(t *testing.T) {
	for _, u := range []string{
		"https://shop.example.com/checkout",
		"https://shop.example.com/store/checkout?step=2",
		"https://example.com/account/billing",
		"https://example.com/settings/delete-account",
	} {
		if !pageLooksIrreversible(PageContext{URL: u}) {
			t.Errorf("%q not recognised as a payment/destructive page", u)
		}
	}
}

func TestOrdinaryPagesAreNotRecognisedAsPaymentPages(t *testing.T) {
	for _, u := range []string{
		"https://news.example.com/article/2026/vite-7",
		"https://example.com/docs/getting-started",
		"https://example.com/invoices/2026",
	} {
		if pageLooksIrreversible(PageContext{URL: u}) {
			t.Errorf("%q wrongly recognised as a payment page", u)
		}
	}
}
