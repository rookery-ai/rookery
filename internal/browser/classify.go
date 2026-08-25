package browser

import (
	"fmt"
	"sort"
	"strings"
)

// PageFacts is what the sandboxed helper reports about a rendered page. It is
// deliberately a flat, boring struct: everything interesting is decided HERE, in
// the host process, by pure functions a test can drive without a browser.
type PageFacts struct {
	Title            string `json:"title"`
	Text             string `json:"text"`
	FinalURL         string `json:"final_url"`
	Status           int    `json:"status"`
	HasPasswordField bool   `json:"has_password_field"`
	// FrameNames carries iframe titles/urls, which is where hcaptcha and
	// recaptcha actually live — the host page's own text often says nothing.
	FrameNames []string  `json:"frame_names"`
	Elements   []Element `json:"elements"`
	// Note carries a non-fatal caveat, e.g. a wait condition that timed out
	// while the page still rendered something worth returning.
	Note string `json:"note,omitempty"`
	// Error is set when the page could not be produced at all. It is carried in
	// the body rather than as an HTTP status because a navigation failure is
	// DATA the model should see and react to, not a transport fault.
	Error string `json:"error,omitempty"`
}

// blockedSignature is one bot-wall detector.
type blockedSignature struct {
	kind    string
	note    string
	phrases []string
}

// Detection is phrase-based and therefore approximate. That is acceptable
// because of what it is FOR: the result is reported to the model and the user as
// "this page is behind X", never used to trigger a workaround. A false positive
// costs one honest message; a false negative just degrades to "the page had no
// readable content", which is what would have happened anyway.
//
// Ordering matters: Cloudflare's interstitial also mentions captchas, so the
// more specific infrastructure signature is tested first.
var blockedSignatures = []blockedSignature{
	{
		kind: "cloudflare",
		note: "the site is behind a Cloudflare browser check",
		phrases: []string{
			"just a moment",
			"checking your browser before accessing",
			"enable javascript and cookies to continue",
			"cf-browser-verification",
			"attention required! | cloudflare",
			"performance & security by cloudflare",
			"ray id:",
		},
	},
	{
		kind: "captcha",
		note: "the page is asking for a captcha",
		phrases: []string{
			"recaptcha",
			"hcaptcha",
			"i'm not a robot",
			"verify you are human",
			"are you a robot",
			"complete the security check",
			"unusual traffic from your computer network",
		},
	},
}

// Classify decides whether a rendered page is a bot wall or a login wall.
//
// The two are separated because the remedies differ and conflating them misleads
// the user: a captcha or Cloudflare check is a hard stop this platform will not
// work around, while a login wall is something the owner can actually fix by
// storing credentials and granting the agent a session.
func Classify(f PageFacts) (kind, note string) {
	hay := strings.ToLower(f.Title + "\n" + f.Text + "\n" + strings.Join(f.FrameNames, "\n"))
	for _, sig := range blockedSignatures {
		for _, p := range sig.phrases {
			if strings.Contains(hay, p) {
				return sig.kind, sig.note
			}
		}
	}
	// A login wall is judged on STRUCTURE (a password input), not on prose.
	// Phrase-matching "sign in" would fire on the sign-in link every site has
	// in its header, which would mark most of the web as blocked.
	if f.HasPasswordField {
		return "login", "the page is behind a sign-in form"
	}
	// A 403/429 with almost no readable text is a bot block whose vendor we
	// could not name. Saying so beats reporting an empty page.
	if (f.Status == 403 || f.Status == 429) && len(strings.TrimSpace(f.Text)) < 200 {
		return "bot-check", fmt.Sprintf("the site refused the request (HTTP %d) and returned no readable content", f.Status)
	}
	return "", ""
}

// Page slices extracted text for one tool result, mirroring read_file's
// offset/limit contract so a model that has learned one has learned the other.
//
// Slicing is by RUNE, not by byte: cutting mid-rune yields U+FFFD in the
// model's context and, worse, an offset the next call cannot resume from
// cleanly.
func Page(text string, offset, limit int) (out string, truncated bool, next int) {
	runes := []rune(text)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(runes) {
		return "", false, 0
	}
	if limit <= 0 {
		limit = DefaultPageChars
	}
	end := offset + limit
	if end >= len(runes) {
		return string(runes[offset:]), false, 0
	}
	return string(runes[offset:end]), true, end
}

// DefaultPageChars bounds one page of extracted text. It sits below
// coder.maxToolResult (8 KiB) so the rendered text plus the header line and the
// "more remains" notice still fit inside one tool result — a page that exactly
// filled the cap would have its own continuation instructions truncated off.
const DefaultPageChars = 6000

// interactiveRoles are the ARIA roles worth offering as click/fill targets.
//
// A full "ai" aria snapshot measured 53,592 characters on one news homepage
// against an 8 KiB result cap, so returning it raw is not an option and
// truncating it cuts off the controls the model needs. Filtering to the roles
// that can actually be acted on is what makes the list fit.
var interactiveRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true,
	"checkbox": true, "radio": true, "combobox": true, "listbox": true,
	"menuitem": true, "menuitemcheckbox": true, "menuitemradio": true,
	"option": true, "slider": true, "spinbutton": true, "switch": true,
	"tab": true, "textarea": true,
}

// FilterInteractive keeps the elements a model can act on and drops the rest.
// An element with no accessible name is dropped even when its role qualifies:
// the model chooses by name, and "button (no name)" is not a choice it can make
// sensibly — it would be picking blind.
func FilterInteractive(in []Element) []Element {
	out := make([]Element, 0, len(in))
	for _, e := range in {
		if !interactiveRoles[strings.ToLower(e.Role)] {
			continue
		}
		if strings.TrimSpace(e.Name) == "" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// RenderElements formats the interactive list for a tool result, capping it and
// SAYING how many were withheld. A silent truncation would read as "this page
// has nine controls", which is the class of quiet lie this codebase keeps
// recording; the model would then conclude the button it needs does not exist.
func RenderElements(els []Element, max int) string {
	if len(els) == 0 {
		return "(no interactive elements found on this page)"
	}
	if max <= 0 {
		max = DefaultMaxElements
	}
	shown := els
	withheld := 0
	if len(shown) > max {
		withheld = len(shown) - max
		shown = shown[:max]
	}
	var b strings.Builder
	for _, e := range shown {
		fmt.Fprintf(&b, "%-6s %-10s %q", e.Ref, e.Role, e.Name)
		if e.Note != "" {
			fmt.Fprintf(&b, "  (%s)", e.Note)
		}
		b.WriteString("\n")
	}
	if withheld > 0 {
		fmt.Fprintf(&b, "\n… %d more interactive elements not shown. Narrow the page (scroll or navigate) if what you need is missing.\n", withheld)
	}
	return b.String()
}

// DefaultMaxElements bounds the rendered control list.
const DefaultMaxElements = 60

// SortElementsStable orders elements by their numeric ref so a page rendered
// twice presents its controls in the same order. Playwright assigns refs in
// document order, but they arrive as strings ("e2", "e10"), and a plain string
// sort would put e10 before e2 — which reads as the page having reshuffled
// between two calls and invites the model to re-scan instead of acting.
func SortElementsStable(els []Element) {
	sort.SliceStable(els, func(i, j int) bool {
		return refOrdinal(els[i].Ref) < refOrdinal(els[j].Ref)
	})
}

func refOrdinal(ref string) int {
	n := 0
	seen := false
	for _, r := range ref {
		if r >= '0' && r <= '9' {
			seen = true
			n = n*10 + int(r-'0')
			continue
		}
		if seen {
			break
		}
	}
	if !seen {
		return 1 << 30
	}
	return n
}
