package coder

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/llm"
)

// browserTools offers the browser as native function tools.
//
// browser_read is READ-ONLY and sits outside the exec gate, so chat gets it —
// reading a JavaScript-rendered page carries no more authority than web_fetch,
// which is already always-on. The acting tools ARE exec-gated (agent builds and
// runs only, never chat) for the same reason run_script is: chat is a human
// typing in real time with no approval gate at all, so a chat that can click
// "Pay" holds the user against themselves.
//
// Nothing is offered when the runtime is absent. A tool the host cannot execute
// is worse than a missing one — the model spends turns on it and reports a
// platform fault to the user.
func (h *hostToolSet) browserTools() []llm.Tool {
	if h.browser == nil || !h.browser.Available().OK {
		return nil
	}
	tools := []llm.Tool{{
		Name: "browser_read",
		Description: "Open a URL in a REAL browser (JavaScript runs) and return the page's visible text. " +
			"Use this ONLY when web_fetch is not enough: when web_fetch reported that the page rendered no content, " +
			"or when you already know the target is an app that builds its page in the browser (a dashboard, a single-page app). " +
			"web_fetch is faster and is still the right first choice for an API, a feed, or an ordinary article. " +
			"Optional: wait_for — \"networkidle\" to let late-loading data settle, \"selector:<css>\" or \"text:<words>\" " +
			"to wait for something specific to appear. Large pages are paged: pass offset to continue where the last call stopped. " +
			"It cannot log in or fill anything in, and it cannot carry secrets.",
		Parameters: rawSchema(`{"type":"object","properties":{"url":{"type":"string","description":"the http/https URL to open"},"wait_for":{"type":"string","description":"networkidle | selector:<css> | text:<substring>"},"offset":{"type":"integer","description":"character offset to resume reading from"}},"required":["url"]}`),
	}}

	// Acting tools are offered to every agent build and run. There is no longer
	// a grant for "may click at all": it gated one route to actions the agent
	// could already take with bash and curl, so it cost the owner a decision and
	// bought nothing. What still needs permission is judged per call, on the
	// action itself — see browser.CheckAct.
	if !h.includeExecTools {
		return tools
	}
	return append(tools,
		llm.Tool{
			Name: "browser_open",
			Description: "Open a URL in the browser and KEEP IT OPEN so you can act on it. " +
				"Returns the page's interactive controls as `ref role \"name\"` lines. " +
				"Use the ref (e.g. e12) with browser_click and browser_fill — never a CSS selector. " +
				"Every later browser_* call acts on this same page, so navigation, cookies and your login persist between calls.",
			Parameters: rawSchema(`{"type":"object","properties":{"url":{"type":"string"},"wait_for":{"type":"string","description":"networkidle | selector:<css> | text:<substring>"}},"required":["url"]}`),
		},
		llm.Tool{
			Name: "browser_click",
			Description: "Click one control on the open page, by the ref from the last listing. " +
				"Returns the page's controls again afterwards, so you can see what changed. " +
				"Refs change whenever the page re-renders — always use one from the MOST RECENT listing.",
			Parameters: rawSchema(`{"type":"object","properties":{"ref":{"type":"string","description":"element ref, e.g. e12"}},"required":["ref"]}`),
		},
		llm.Tool{
			Name: "browser_fill",
			Description: "Type into one field on the open page, by ref. " +
				"To enter a password, card number or any other stored credential, pass the SECRET NAME in ${...} form — " +
				`e.g. value "${ELECTRIC_BILL_PASSWORD}". The server substitutes the real value directly into the page; ` +
				"you never see it and must never guess or invent one. If the secret does not exist, say so and stop.",
			Parameters: rawSchema(`{"type":"object","properties":{"ref":{"type":"string"},"value":{"type":"string","description":"text to type, or ${SECRET_NAME}"}},"required":["ref","value"]}`),
		},
		llm.Tool{
			Name:        "browser_press",
			Description: "Press a key on the open page, e.g. \"Enter\" to submit a focused form, \"Escape\" to close a dialog.",
			Parameters:  rawSchema(`{"type":"object","properties":{"key":{"type":"string","description":"e.g. Enter, Tab, Escape"}},"required":["key"]}`),
		},
		llm.Tool{
			Name: "browser_wait",
			Description: "Wait for the open page to finish changing before you read it again — after a click that loads data, " +
				"or while a spinner is showing. Pass \"networkidle\", \"selector:<css>\", or \"text:<words>\".",
			Parameters: rawSchema(`{"type":"object","properties":{"wait_for":{"type":"string"},"timeout_ms":{"type":"integer"}},"required":["wait_for"]}`),
		},
		llm.Tool{
			Name:        "browser_page",
			Description: "Re-read the open page: its visible text and its current interactive controls. Use this after a click or a wait to see where you are.",
			Parameters:  rawSchema(`{"type":"object","properties":{"offset":{"type":"integer"}},"required":[]}`),
		},
	)
}

// browserSessionKey is the browser context this toolset owns.
//
// It is derived from the workspace and the working directory rather than
// accepted from the model, because the context accumulates logged-in cookies:
// a model-supplied key would let one agent attach to another agent's
// authenticated session, which is the entire credential store this feature
// creates.
func (h *hostToolSet) browserSessionKey() string {
	return "ws:" + h.workspaceID + "|dir:" + h.workDir
}

func (h *hostToolSet) execBrowserRead(ctx context.Context, url, waitFor string, offset int) string {
	if h.browser == nil {
		return "error: the browser is not available on this server"
	}
	res, err := h.browser.Render(ctx, browser.Request{
		URL:     url,
		WaitFor: waitFor,
		Offset:  offset,
	})
	if err != nil {
		return browserErrorResult(err)
	}
	return renderBrowserResult(res, false)
}

func (h *hostToolSet) execBrowserAct(ctx context.Context, req browser.ActRequest, elementName string, page browser.PageContext) string {
	if h.browser == nil {
		return "error: the browser is not available on this server"
	}
	if err := browser.CheckAct(h.browserPolicy, req.Action, elementName, page); err != nil {
		// Record that this agent wanted to do something irreversible, so the
		// owner is shown the permission it needs instead of having to guess from
		// a refusal buried in a run log.
		h.browserWantedIrreversible = true
		// Deliberately NOT prefixed with "error:". A refusal is a settled
		// outcome the model must report to the user, not a failing call worth
		// retrying — and the API engine's oscillation guard counts an "error:"
		// prefix as a failure. This is the same distinction internal/mcp draws
		// between a tool's own error and a protocol failure.
		return err.Error()
	}
	req.Session = h.browserSessionKey()
	res, err := h.browser.Act(ctx, req)
	if err != nil {
		return browserErrorResult(err)
	}
	return renderBrowserResult(res, true)
}

// browserErrorResult keeps the "runtime missing" case distinguishable from an
// ordinary page failure, because the remedies are completely different: one is
// an install command for the operator, the other is something the model can
// react to.
func browserErrorResult(err error) string {
	if errors.Is(err, browser.ErrUnavailable) {
		return "error: " + err.Error() + " (tell the user; do not retry)"
	}
	return "error: " + err.Error()
}

// renderBrowserResult formats a page for the model.
func renderBrowserResult(res browser.Result, withElements bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[browser %s]\n", res.FinalURL)
	if res.Title != "" {
		fmt.Fprintf(&b, "title: %s\n", res.Title)
	}
	if res.Blocked != "" {
		// Stated plainly and once. A model told only "no content" will retry;
		// told the page is behind a Cloudflare check, it reports and stops.
		fmt.Fprintf(&b, "\nBLOCKED (%s): %s.\nThis cannot be worked around — report it to the user rather than retrying or looking for another route.\n",
			res.Blocked, res.BlockedNote)
	} else if res.BlockedNote != "" {
		fmt.Fprintf(&b, "note: %s\n", res.BlockedNote)
	}
	if withElements {
		b.WriteString("\ninteractive controls:\n")
		b.WriteString(browser.RenderElements(res.Elements, browser.DefaultMaxElements))
	}
	if txt := strings.TrimSpace(res.Text); txt != "" {
		b.WriteString("\n" + txt + "\n")
	} else if res.Blocked == "" {
		b.WriteString("\n(the page rendered no readable text)\n")
	}
	if res.Truncated {
		fmt.Fprintf(&b, "\n… more text remains. Call again with offset %d to continue.\n", res.NextOffset)
	}
	return b.String()
}

// browserCallArgs is the subset of tool arguments the browser verbs use. It is
// a named type because execute()'s own argument struct is anonymous and cannot
// cross a function boundary.
type browserCallArgs struct {
	URL       string
	WaitFor   string
	Ref       string
	Value     string
	Key       string
	Offset    int
	TimeoutMS int
}

// dispatchBrowserAct maps one browser tool name onto the acting choke point.
func (h *hostToolSet) dispatchBrowserAct(ctx context.Context, name string, a browserCallArgs) string {
	if h.browser == nil {
		return "error: the browser is not available on this server"
	}
	session := h.browserSessionKey()

	switch name {
	case "browser_open":
		// Opening a page is navigation, not acting, so it is not gated by
		// CheckAct — a page the agent may read is a page it may open. What
		// distinguishes this from browser_read is only that the context is KEPT.
		res, err := h.browser.Render(ctx, browser.Request{
			URL: a.URL, WaitFor: a.WaitFor, TimeoutMS: a.TimeoutMS, Session: session,
		})
		if err != nil {
			return browserErrorResult(err)
		}
		return renderBrowserResult(res, true)

	case "browser_page":
		return h.execBrowserAct(ctx, browser.ActRequest{Action: browser.ActionRead, Offset: a.Offset}, "", browser.PageContext{})

	case "browser_wait":
		return h.execBrowserAct(ctx, browser.ActRequest{
			Action: browser.ActionWait, WaitFor: a.WaitFor, TimeoutMS: a.TimeoutMS,
		}, "", browser.PageContext{})

	case "browser_press":
		// A keypress carries no ref, but the PAGE still has to be judged — this
		// is the call that would otherwise submit a focused payment form with
		// Enter and never meet a check at all.
		_, page, _ := h.browserTarget(ctx, session, "")
		return h.execBrowserAct(ctx, browser.ActRequest{Action: browser.ActionPress, Key: a.Key}, "", page)

	case "browser_click":
		name, page, ok := h.browserTarget(ctx, session, a.Ref)
		if !ok {
			return "error: ref " + a.Ref + " is not on the page any more — call browser_page and use a ref from the new listing"
		}
		return h.execBrowserAct(ctx, browser.ActRequest{Action: browser.ActionClick, Ref: a.Ref}, name, page)

	case "browser_fill":
		elName, page, ok := h.browserTarget(ctx, session, a.Ref)
		if !ok {
			return "error: ref " + a.Ref + " is not on the page any more — call browser_page and use a ref from the new listing"
		}
		value, isSecret, err := browser.ResolveSecretValue(ctx, h.browserSecretResolver(), h.workspaceID, a.Value)
		if err != nil {
			// Not an "error:" prefix: a missing secret is a settled fact the
			// model must report, not a call to retry with different arguments.
			return err.Error()
		}
		return h.execBrowserAct(ctx, browser.ActRequest{
			Action: browser.ActionFill, Ref: a.Ref, Value: value, ValueIsSecret: isSecret,
		}, elName, page)
	}
	return "error: unknown browser action " + name
}

// browserTarget resolves what a ref currently points at AND what page it is on,
// by re-reading the live page.
//
// It must not trust a name from an earlier listing: the irreversibility check is
// only as good as the name it judges, and a page that re-rendered may have
// turned the "Next" the model saw into a "Pay now". The page identity is read at
// the same time because a keypress has no control to name — and that is the case
// most in need of judging.
func (h *hostToolSet) browserTarget(ctx context.Context, session, ref string) (string, browser.PageContext, bool) {
	res, err := h.browser.Act(ctx, browser.ActRequest{Session: session, Action: browser.ActionRead})
	if err != nil {
		return "", browser.PageContext{}, false
	}
	page := browser.PageContext{Title: res.Title, URL: res.FinalURL}
	if strings.TrimSpace(ref) == "" {
		return "", page, true
	}
	for _, e := range res.Elements {
		if e.Ref == ref {
			page.NameKnown = e.Name != ""
			return e.Name, page, true
		}
	}
	return "", page, false
}

// browserSecretResolver resolves ${NAME} against the secrets already injected
// into this run's environment.
//
// The API engine is handed the workspace's decrypted secrets as subprocessEnv
// so run_script and bash can use them; reading from that same map keeps the
// browser consistent with those tools by construction, and avoids giving this
// package a database dependency it otherwise does not need.
func (h *hostToolSet) browserSecretResolver() browser.SecretResolver {
	return func(_ context.Context, _ string, name string) (string, error) {
		if v, ok := h.subprocessEnv[name]; ok && v != "" {
			return v, nil
		}
		return "", fmt.Errorf("no such secret")
	}
}
