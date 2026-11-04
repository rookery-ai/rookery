//go:build browser

package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These pages are invented. Each reproduces a common way forms are built.

var (
	binOnce sync.Once
	binPath string
)

// sharedBinary builds the rookery binary once for this file's tests; each build
// takes most of a minute. It lives outside any one test's TempDir, which is
// removed when that test ends.
func sharedBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rookery-form-test-")
		if err != nil {
			t.Fatal(err)
		}
		binPath = filepath.Join(dir, "rookery")
		if out, err := exec.Command("go", "build", "-o", binPath, "../../cmd/rookery").CombinedOutput(); err != nil {
			t.Fatalf("build rookery: %v\n%s", err, out)
		}
	})
	return binPath
}

func servePage(t *testing.T, html string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body>" + html + "</body></html>"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type formSession struct {
	t   *testing.T
	m   *Manager
	ctx context.Context
	key string
}

func openForm(t *testing.T, url string) (*formSession, Result) {
	t.Helper()
	m := NewManager(sharedBinary(t), true, false) // loopback test server: guard off, as the other browser tests do
	t.Cleanup(m.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	s := &formSession{t: t, m: m, ctx: ctx, key: "test:" + t.Name()}
	res, err := m.Render(ctx, Request{URL: url, Session: s.key})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s, res
}

func (s *formSession) read() Result {
	s.t.Helper()
	res, err := s.m.Act(s.ctx, ActRequest{Session: s.key, Action: ActionRead})
	if err != nil {
		s.t.Fatalf("read: %v", err)
	}
	return res
}

func (s *formSession) ref(name string) string {
	s.t.Helper()
	for _, e := range s.read().Elements {
		if e.Name == name {
			return e.Ref
		}
	}
	s.t.Fatalf("no element named %q in %+v", name, s.read().Elements)
	return ""
}

func (s *formSession) act(req ActRequest) (Result, error) {
	req.Session = s.key
	return s.m.Act(s.ctx, req)
}

func (s *formSession) mustAct(req ActRequest) Result {
	s.t.Helper()
	res, err := s.act(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", req.Action, req.Ref, err)
	}
	return res
}

// A card-expiry box that writes its hidden month/year inputs only from an
// onkeyup handler. Locator.Fill fires no key events, so the visible box read
// back correctly while the form submitted empty month and year.
func TestKeyupOnlyExpiryFillsHiddenFields(t *testing.T) {
	srv := servePage(t, `
<label>Expiry <input id="d" maxlength="5" placeholder="MM/YY" onkeyup="fmt(this)"></label>
<input type="hidden" id="month"><input type="hidden" id="year">
<button onclick="document.getElementById('out').textContent='month='+month.value+' year='+year.value">Show</button>
<p id="out"></p>
<script>function fmt(el){var v=el.value.replace(/[^0-9]/g,'');month.value=v.slice(0,2);year.value=v.slice(2,4);}</script>`)
	s, _ := openForm(t, srv.URL)
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Expiry"), Value: "12/30"})
	if !strings.Contains(res.ActNote, "matched") {
		t.Fatalf("act note = %q", res.ActNote)
	}
	res = s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Show")})
	if !strings.Contains(res.Text, "month=12 year=30") {
		t.Fatalf("hidden fields not written by the page's own key handler: %q", res.Text)
	}
}

// A hosted card field: a separate site in an iframe that reformats digits into
// groups and echoes a masked tail. The number must not reach the result in any
// form, and the field must be listed by its placeholder.
func TestHostedCardFieldNeverLeaks(t *testing.T) {
	frame := servePage(t, `
<input id="pan" aria-label="Card number" inputmode="numeric">
<span id="echo"></span>
<script>pan.addEventListener('input',()=>{const d=pan.value.replace(/\D/g,'').slice(0,16);pan.value=d.replace(/(\d{4})(?=\d)/g,'$1 ');echo.textContent=d.length>=4?'•••• '+d.slice(-4):'';});</script>`)
	frameURL := strings.Replace(frame.URL, "127.0.0.1", "localhost", 1) // a different site
	outer := servePage(t, `<h1>Pay</h1><iframe src="`+frameURL+`" width="400" height="120"></iframe>`)
	s, _ := openForm(t, outer.URL)

	const card = "4111111111114242"
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Card number"), Value: card, ValueIsSecret: true, SecretLabel: "${CARD}"})
	if !strings.Contains(res.ActNote, "matched") {
		t.Fatalf("act note = %q", res.ActNote)
	}
	for _, r := range []Result{res, s.read()} {
		raw, _ := json.Marshal(r)
		for _, leak := range []string{"4111", "1111 1111", "4242"} {
			if strings.Contains(string(raw), leak) {
				t.Fatalf("result leaks %q: %s", leak, raw)
			}
		}
		found := false
		for _, e := range r.Elements {
			if e.Name == "Card number" && strings.Contains(e.Note, "filled from ${CARD}") {
				found = true
			}
		}
		if !found {
			t.Fatalf("card field not labelled by its placeholder: %+v", r.Elements)
		}
	}
}

// A field with a placeholder reported "fill: matched" and was then listed as
// "empty", which reads as the page rejecting the value. And a SECRET field with a placeholder now exposes its value
// line, which must be relabelled rather than shown.
func TestFilledPlaceholderFieldIsListedWithItsValue(t *testing.T) {
	srv := servePage(t, `<label>Card holder <input placeholder="Name on card"></label>
<label>Account <input placeholder="Account number"></label>`)
	s, _ := openForm(t, srv.URL)
	s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Card holder"), Value: "TEST HOLDER"})
	s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Account"), Value: "acct-445566", ValueIsSecret: true, SecretLabel: "${ACCT}"})
	res := s.read()
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "445566") {
		t.Fatalf("secret leaked: %s", raw)
	}
	for _, e := range res.Elements {
		switch e.Name {
		case "Card holder":
			if !strings.Contains(e.Note, "value: TEST HOLDER") {
				t.Errorf("filled field listed as %q", e.Note)
			}
		case "Account":
			if !strings.Contains(e.Note, "filled from ${ACCT}") {
				t.Errorf("secret field listed as %q", e.Note)
			}
		}
	}
}

// A field the page rebuilds (a fresh node with a fresh ref) must stay labelled.
func TestSecretFieldStaysLabelledAfterRerender(t *testing.T) {
	srv := servePage(t, `
<div id="w"><input id="f" aria-label="Account"></div>
<button onclick="const o=document.getElementById('f');o.replaceWith(o.cloneNode(true));">Rebuild</button>`)
	s, _ := openForm(t, srv.URL)
	before := s.ref("Account")
	s.mustAct(ActRequest{Action: ActionFill, Ref: before, Value: "acct-778899", ValueIsSecret: true, SecretLabel: "${ACCT}"})
	res := s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Rebuild")})
	for _, e := range res.Elements {
		if e.Name == "Account" {
			if e.Ref == before {
				t.Skip("the page kept the same ref; nothing to test")
			}
			if !strings.Contains(e.Note, "filled from ${ACCT}") {
				t.Fatalf("rebuilt field lost its label: %+v", e)
			}
			return
		}
	}
	t.Fatalf("field not listed: %+v", res.Elements)
}

// A fill aimed at a button must be refused, even when the button is the only
// element carrying a label's wording.
func TestFillRefusesNonField(t *testing.T) {
	srv := servePage(t, `<button onclick="document.getElementById('o').textContent='CLICKED'">Continue</button><p id="o"></p>`)
	s, _ := openForm(t, srv.URL)
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Continue"), Value: "user@example.com"})
	if !strings.Contains(res.ActNote, "not a field") {
		t.Fatalf("act note = %q", res.ActNote)
	}
	if strings.Contains(res.Text, "CLICKED") {
		t.Fatal("a refused fill must not activate the button")
	}
}

func TestMaxlengthPartialHint(t *testing.T) {
	srv := servePage(t, `<label>Expiry <input maxlength="5"></label>`)
	s, _ := openForm(t, srv.URL)
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Expiry"), Value: "07/2030"})
	if !strings.Contains(res.ActNote, "partial") || !strings.Contains(res.ActNote, "${NAME[-2:]}") {
		t.Fatalf("act note = %q", res.ActNote)
	}
}

// --- scripted dropdowns ---------------------------------------------------

const dropdownPage = `
<div class="dropdown"><button id="m" type="button" data-toggle="dropdown">Month</button>
<ul role="menu" id="menu" style="display:none">` + months + `</ul></div>
<input type="hidden" id="month">
<button id="c" onclick="document.getElementById('o').textContent='CONFIRMED'">Confirm</button>
<p id="o"></p><p id="o2"></p>
<script>
m.onclick=()=>{menu.style.display=menu.style.display==='none'?'block':'none'};
menu.querySelectorAll('a').forEach(a=>a.onclick=e=>{e.preventDefault();REGISTER;menu.style.display='none';});
</script>`

const months = `<li><a href="#">01</a></li><li><a href="#">02</a></li><li><a href="#">03</a></li><li><a href="#">04</a></li><li><a href="#">05</a></li><li><a href="#">06</a></li><li><a href="#">07</a></li><li><a href="#">08</a></li><li><a href="#">09</a></li><li><a href="#">10</a></li><li><a href="#">11</a></li><li><a href="#">12</a></li>`

func dropdownServer(t *testing.T, register string) *httptest.Server {
	return servePage(t, strings.Replace(dropdownPage, "REGISTER", register, 1))
}

const registers = `m.textContent=a.textContent;month.value=a.textContent;o2.textContent='month='+month.value`

func TestDropdownChoosesByText(t *testing.T) {
	s, _ := openForm(t, dropdownServer(t, registers).URL)
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Month"), Value: "12"})
	if !strings.Contains(res.ActNote, `chose "12"`) {
		t.Fatalf("act note = %q", res.ActNote)
	}
	if !strings.Contains(res.Text, "month=12") {
		t.Fatalf("choice not registered: %q", res.Text)
	}
}

func TestDropdownNeverClicksOutsidePopup(t *testing.T) {
	s, _ := openForm(t, dropdownServer(t, registers).URL)
	_, err := s.act(ActRequest{Action: ActionFill, Ref: s.ref("Month"), Value: "Confirm"})
	if err == nil || !strings.Contains(err.Error(), `"01"`) {
		t.Fatalf("want an error listing the options, got %v", err)
	}
	if strings.Contains(s.read().Text, "CONFIRMED") {
		t.Fatal("an option match landed on a control outside the popup")
	}
}

func TestDropdownSecretChoiceIsNotEchoed(t *testing.T) {
	s, _ := openForm(t, dropdownServer(t, `m.textContent=a.textContent;month.value=a.textContent`).URL)
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Month"), Value: "07", ValueIsSecret: true, SecretLabel: "${MONTH}"})
	if !strings.Contains(res.ActNote, "${MONTH}") || strings.Contains(res.ActNote, "07") {
		t.Fatalf("act note = %q", res.ActNote)
	}
	for _, e := range res.Elements {
		if e.Name == "07" {
			t.Fatalf("the secret choice is listed as the trigger's name: %+v", e)
		}
	}
}

func TestDropdownThatDoesNotRegister(t *testing.T) {
	s, _ := openForm(t, dropdownServer(t, `void 0`).URL)
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Month"), Value: "03"})
	if !strings.Contains(res.ActNote, "did not register") {
		t.Fatalf("act note = %q", res.ActNote)
	}
}

// A ref remembered as "filled from a secret" must never relabel a DIFFERENT
// element that reuses the ref on the next page — that would report an empty
// required field as filled, or list a Pay button under the wrong name, which is
// the name the irreversibility check judges.
func TestSecretLabelDoesNotFollowARefToAnotherPage(t *testing.T) {
	b := servePage(t, `<label>Account <input></label><a href="#">Next</a>`)
	a := servePage(t, `<label>Account <input></label><a href="`+b.URL+`/b">Next</a>`)
	s, _ := openForm(t, a.URL)
	ref := s.ref("Account")
	s.mustAct(ActRequest{Action: ActionFill, Ref: ref, Value: "acct-778899", ValueIsSecret: true, SecretLabel: "${ACCT}"})
	res := s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Next")})
	if !strings.HasSuffix(res.FinalURL, "/b") {
		t.Fatalf("did not reach page B: %s", res.FinalURL)
	}
	for _, e := range s.read().Elements {
		if e.Name == "Account" && strings.Contains(e.Note, "filled from") {
			t.Fatalf("a fresh, empty field on the next page is reported as filled: %+v", e)
		}
	}
}

// browser_fill on anything that toggles (an accordion, a nav menu) must not
// click a revealed option that cannot be undone: that click would bypass the
// irreversibility check, which judges only the trigger.
func TestDropdownRefusesAnIrreversibleOption(t *testing.T) {
	srv := servePage(t, `<button aria-expanded="false" onclick="p.style.display='block';this.setAttribute('aria-expanded','true')">Account options</button>
<div id="p" style="display:none"><a href="#" onclick="document.getElementById('o').textContent='DELETED'">Delete account</a><a href="#">Settings</a></div><p id="o"></p>`)
	s, _ := openForm(t, srv.URL)
	res := s.mustAct(ActRequest{Action: ActionFill, Ref: s.ref("Account options"), Value: "Delete account"})
	if strings.Contains(res.Text, "DELETED") {
		t.Fatal("an irreversible option was clicked through browser_fill")
	}
	if !strings.Contains(res.ActNote, "browser_click") {
		t.Fatalf("act note = %q", res.ActNote)
	}
}

// A confirmation page counting down must not end a change-wait; the page
// moving on (a new control appearing, as when an outside approval lands) must.
func TestWaitForChangeIgnoresACountdown(t *testing.T) {
	srv := servePage(t, `<p>Approve in your app. Time left: <span id="t">300</span></p>
<script>let n=300;setInterval(()=>{document.getElementById('t').textContent=--n},200);
setTimeout(()=>{const b=document.createElement('button');b.textContent='Back to merchant';document.body.appendChild(b)},5000);</script>`)
	s, _ := openForm(t, srv.URL)
	start := time.Now()
	res, matched, err := s.m.WaitFor(s.ctx, s.key, WaitChange, 30*time.Second)
	if err != nil || !matched {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
	if time.Since(start) < 4*time.Second {
		t.Fatalf("the countdown ended the wait after %v", time.Since(start))
	}
	found := false
	for _, e := range res.Elements {
		found = found || e.Name == "Back to merchant"
	}
	if !found {
		t.Fatalf("returned page lacks the new control: %+v", res.Elements)
	}
}

// --- obstructions -----------------------------------------------------------

func TestCoveredButtonIsDescribed(t *testing.T) {
	srv := servePage(t, `
<button onclick="document.getElementById('o').textContent='CLICKED'">Month</button><p id="o"></p>
<div style="position:fixed;inset:0;background:#fff;z-index:9">We use cookies <button>Accept</button><button>Reject</button></div>`)
	s, _ := openForm(t, srv.URL)
	res := s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Month")})
	if !strings.Contains(res.ActNote, "covered") || !strings.Contains(res.ActNote, `"Accept"`) {
		t.Fatalf("act note = %q", res.ActNote)
	}
	if strings.Contains(res.Text, "CLICKED") {
		t.Fatal("clicked through the overlay")
	}
}

func TestOwnLabelIsNotAnOverlay(t *testing.T) {
	srv := servePage(t, `
<div style="position:relative"><input type="checkbox" id="c" style="position:absolute;left:0;top:0;opacity:0;z-index:-1">
<label for="c" style="display:inline-block;padding-left:24px">Agree to terms</label></div>`)
	s, _ := openForm(t, srv.URL)
	res := s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Agree to terms")})
	if strings.Contains(res.ActNote, "covered") {
		t.Fatalf("own label reported as an overlay: %q", res.ActNote)
	}
	for _, e := range res.Elements {
		if e.Name == "Agree to terms" && strings.Contains(e.Note, "checked") {
			return
		}
	}
	t.Fatalf("checkbox not checked: %+v", res.Elements)
}

func TestDisabledIsReportedImmediately(t *testing.T) {
	srv := servePage(t, `<button disabled>Pay</button>`)
	s, _ := openForm(t, srv.URL)
	start := time.Now()
	res := s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Pay")})
	if time.Since(start) > 8*time.Second {
		t.Fatalf("took %v — waited out the timeout instead of reporting", time.Since(start))
	}
	if !strings.Contains(res.ActNote, "disabled right now") {
		t.Fatalf("act note = %q", res.ActNote)
	}
}

// A key press reports where focus landed, by label — never by value.
func TestPressReportsFocusWithoutValue(t *testing.T) {
	srv := servePage(t, `<label>First <input value="alpha-secret-ish"></label><label>Second <input value="beta"></label>`)
	s, _ := openForm(t, srv.URL)
	s.mustAct(ActRequest{Action: ActionPress, Key: "Tab"})
	res := s.mustAct(ActRequest{Action: ActionPress, Key: "Tab"})
	if !strings.Contains(res.ActNote, `focus: textbox "Second"`) {
		t.Fatalf("act note = %q", res.ActNote)
	}
	if strings.Contains(res.ActNote, "beta") {
		t.Fatalf("focus note carries the field's value: %q", res.ActNote)
	}
}

// A notice pop-up that declares no role at all: an empty full-screen backdrop
// with the panel above it. It hides the names of every control behind it, and
// the model must be told why.
func TestRolelessModalIsReportedAndNamedWhenItCovers(t *testing.T) {
	srv := servePage(t, `<button onclick="document.getElementById('o').textContent='CLICKED'">Submit order</button><p id="o"></p>
<div style="position:fixed;inset:0;background:rgba(0,0,0,.4);z-index:10"></div>
<div style="position:fixed;left:30%;top:30%;width:40%;height:30%;background:#fff;z-index:11"><b>Important notice</b><p>We changed how payments work.</p><button>OK</button></div>`)
	s, res := openForm(t, srv.URL)
	if !strings.Contains(res.BlockedNote, `a dialog is open: "Important notice`) || !strings.Contains(res.BlockedNote, `"OK"`) {
		t.Fatalf("note = %q", res.BlockedNote)
	}
	res = s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Submit order")})
	if !strings.Contains(res.ActNote, "Important notice") || !strings.Contains(res.ActNote, `"OK"`) {
		t.Fatalf("cover not described by the panel above the backdrop: %q", res.ActNote)
	}
	if strings.Contains(res.Text, "CLICKED") {
		t.Fatal("clicked through the modal")
	}
}

// A slide-in panel over a backdrop, with a fixed bottom action bar
// at a HIGHER z-index. The note must name the panel, not the bar.
func TestSlideInPanelBeatsFixedBottomBar(t *testing.T) {
	srv := servePage(t, `<p>Orders</p>
<div style="position:fixed;inset:0;background:rgba(0,0,0,.4);z-index:10"></div>
<div style="position:fixed;top:0;right:0;width:45%;height:100%;background:#fff;z-index:11"><h2>Payment confirmation</h2><input aria-label="Email"><button>Continue to payment</button></div>
<div style="position:fixed;left:0;right:0;bottom:0;height:60px;background:#c06;z-index:50">Selected items: 1 <button>Submit order</button></div>`)
	_, res := openForm(t, srv.URL)
	if !strings.Contains(res.BlockedNote, `"Payment confirmation"`) {
		t.Fatalf("note = %q", res.BlockedNote)
	}
}

// A sticky header with links must NOT be reported as an open dialog on every
// listing — a note that is always there is a note nobody reads.
func TestStickyHeaderIsNotADialog(t *testing.T) {
	srv := servePage(t, `<div style="position:fixed;top:0;left:0;right:0;height:60px;background:#eee">Shop <a href="#">Home</a> <button>Cart</button></div>
<div style="height:2000px;padding-top:80px">Content</div>`)
	_, res := openForm(t, srv.URL)
	if strings.Contains(res.BlockedNote, "dialog is open") {
		t.Fatalf("sticky header reported as a dialog: %q", res.BlockedNote)
	}
}

func TestOpenModalIsReported(t *testing.T) {
	srv := servePage(t, `<button>Behind</button>
<div role="dialog" aria-modal="true" style="position:fixed;inset:20%;background:#eee"><h2>Important notice</h2><button>OK</button></div>`)
	_, res := openForm(t, srv.URL)
	if !strings.Contains(res.BlockedNote, `a dialog is open: "Important notice"`) {
		t.Fatalf("note = %q", res.BlockedNote)
	}
}

func TestNavigationFailureIsReported(t *testing.T) {
	srv := servePage(t, `<button onclick="location='http://127.0.0.1:1/pay'">Continue</button>`)
	s, _ := openForm(t, srv.URL)
	res := s.mustAct(ActRequest{Action: ActionClick, Ref: s.ref("Continue")})
	if !strings.Contains(res.BlockedNote, "failed to load http://127.0.0.1:1/pay") {
		res = s.read()
		if !strings.Contains(res.BlockedNote, "failed") {
			t.Fatalf("note = %q url=%q", res.BlockedNote, res.FinalURL)
		}
	}
}
