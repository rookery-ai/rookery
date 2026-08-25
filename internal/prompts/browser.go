package prompts

// browserToolsBlock explains the browser to the model, in ONE place.
//
// The routing rule is the entire point of this block, and it is the part a weak
// model gets wrong in both directions: reaching for a browser to read a JSON API
// (slow, and the result is worse), or giving up on a JavaScript app because
// web_fetch returned an empty shell. Both failures are cheap to prevent with one
// sentence and expensive to diagnose afterwards.
//
// It is single-sourced across the CLI and API wordings for the reason
// connectedToolsBlock was factored out: two copies of a routing rule drift, and
// the drift shows up as a capability that appears to exist on one coder kind and
// not the other.
func browserToolsBlock(backendType string, acting bool) string {
	if backendType == BackendBasicModel {
		// A basic model has no tool calls at all, so advertising the browser
		// would describe something it cannot reach.
		return ""
	}

	read := "- browser_read(url): open a page in a REAL browser, let its JavaScript run, and read the\n" +
		"  resulting text."
	if backendType == BackendFullCoder {
		read = "- `" + browserBinPlaceholder + " browser read <url>`: open a page in a REAL browser, let its\n" +
			"  JavaScript run, and read the resulting text."
	}

	b := `<browser>
Some pages have no content until JavaScript builds them — dashboards, single-page
apps, anything that shows a spinner first. A plain fetch of one of those returns an
empty shell, which is why you have a browser.

` + read + `

WHEN TO USE WHICH. Reach for the ordinary web fetch FIRST: it is much faster, and for
an API, a JSON/RSS feed or an ordinary article it gives you a better result. Use the
browser when the fetch came back with almost no text and said the page renders with
JavaScript, or when you already know the target is an app rather than a document.
Do not use the browser to read an API.

The browser cannot carry secrets into a plain read, and it will not get you past a
captcha or a Cloudflare check. When a page is behind one of those, the result says so —
report that to the user and stop. Do not retry it and do not go looking for another
route in; there isn't one.
`
	if acting {
		b += `
ACTING ON A PAGE. browser_open keeps a page open so you can work in it, and returns its
controls as ` + "`ref role \"name\"`" + ` lines. Act by REF — browser_click(ref), browser_fill(ref,
value) — never by writing a CSS selector. Refs change every time the page re-renders, so
always use one from the most recent listing, and call browser_page after a click to see
where you ended up. browser_wait is how you let a slow page settle before reading it.

To type a password, a card number or any other stored credential, pass the SECRET NAME
in ${...} form — for example ${ELECTRIC_BILL_PASSWORD}. The value is substituted into
the page for you. You will never see it, and you must never guess one or type a
placeholder: if the secret does not exist, say so and stop.

You may be refused. A test run may not click anything at all, and the user grants
permission to act — and separately, permission for anything that looks irreversible like
paying or deleting — per agent. A refusal is final: report what you were about to do and
why it needs the user, and do not look for a way around it.
`
	}
	return b + `</browser>

`
}

// browserBinPlaceholder is replaced with the rookery binary path by the caller
// that knows it, mirroring how connectedToolsBlock renders `connector exec`.
const browserBinPlaceholder = "{{rookery}}"
