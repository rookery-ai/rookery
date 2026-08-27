package coder

import (
	"context"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/browser"
)

type fakeBrowser struct {
	available bool
	lastReq   browser.Request
	lastAct   browser.ActRequest
	result    browser.Result
	err       error
}

func (f *fakeBrowser) Available() browser.Availability {
	return browser.Availability{OK: f.available, Reason: "not installed"}
}

func (f *fakeBrowser) Render(_ context.Context, r browser.Request) (browser.Result, error) {
	f.lastReq = r
	return f.result, f.err
}

func (f *fakeBrowser) Act(_ context.Context, r browser.ActRequest) (browser.Result, error) {
	f.lastAct = r
	return f.result, f.err
}

func (f *fakeBrowser) CloseSession(context.Context, string) {}

func browserToolNames(h *hostToolSet) []string {
	var out []string
	for _, t := range h.tools() {
		out = append(out, t.Name)
	}
	return out
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// An install with no browser runtime must not be offered browser tools. A tool
// the host cannot execute is worse than a missing one: the model spends turns
// on it and then reports a platform fault to the user.
func TestNoBrowserToolsWhenTheRuntimeIsAbsent(t *testing.T) {
	h := &hostToolSet{browser: &fakeBrowser{available: false}, includeExecTools: true}
	for _, n := range browserToolNames(h) {
		if strings.HasPrefix(n, "browser_") {
			t.Fatalf("offered %s with no runtime installed", n)
		}
	}
}

// Chat gets reading and NOTHING else. This is the same line run_script sits on:
// chat is a human typing in real time with no approval gate, so a chat that
// could click "Pay" would hold the user against themselves.
func TestChatGetsReadingButNeverActing(t *testing.T) {
	h := &hostToolSet{
		browser:          &fakeBrowser{available: true},
		includeExecTools: false, // chat
		browserPolicy:    browser.Policy{AllowIrreversible: true},
	}
	names := browserToolNames(h)
	if !has(names, "browser_read") {
		t.Error("chat was not offered browser_read")
	}
	for _, n := range names {
		if strings.HasPrefix(n, "browser_") && n != "browser_read" {
			t.Errorf("chat was offered the acting tool %s", n)
		}
	}
}

// Every agent gets the acting tools. There is no longer a grant for clicking:
// it gated one route to something the agent could already do with bash and curl,
// so it cost the owner a decision and bought no safety. What needs permission is
// judged per action, in browser.CheckAct.
func TestEveryAgentGetsTheActingTools(t *testing.T) {
	h := &hostToolSet{
		browser:          &fakeBrowser{available: true},
		includeExecTools: true,
		browserPolicy:    browser.Policy{},
	}
	names := browserToolNames(h)
	for _, want := range []string{"browser_read", "browser_open", "browser_click", "browser_fill", "browser_press", "browser_wait", "browser_page"} {
		if !has(names, want) {
			t.Errorf("granted agent missing %s", want)
		}
	}
}

// A refusal must not carry the "error:" prefix. The API engine's oscillation
// guard counts that prefix as a failing call and will short-circuit a repeat,
// but a refusal is a settled outcome the model should REPORT — the same
// distinction internal/mcp draws between a tool's own error and a protocol one.
func TestARefusalIsNotShapedLikeAFailingCall(t *testing.T) {
	h := &hostToolSet{
		browser:          &fakeBrowser{available: true},
		includeExecTools: true,
		browserPolicy:    browser.Policy{BuildPhase: true},
	}
	out := h.execBrowserAct(context.Background(), browser.ActRequest{Action: browser.ActionClick}, "Pay now", browser.PageContext{NameKnown: true})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("refusal shaped as a failing call: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "test run") {
		t.Errorf("refusal does not say why: %q", out)
	}
}

// The session key must be derived from the workspace and working directory, not
// supplied by the model: a browser context accumulates logged-in cookies, so a
// caller-chosen key would let one agent attach to another's authenticated
// session.
func TestSessionKeyIsDerivedFromTheAgentNotTheModel(t *testing.T) {
	a := &hostToolSet{workspaceID: "ws1", workDir: "/vaults/ws1/agents/a"}
	b := &hostToolSet{workspaceID: "ws1", workDir: "/vaults/ws1/agents/b"}
	c := &hostToolSet{workspaceID: "ws2", workDir: "/vaults/ws2/agents/a"}
	if a.browserSessionKey() == b.browserSessionKey() {
		t.Error("two agents in one workspace share a browser session")
	}
	if a.browserSessionKey() == c.browserSessionKey() {
		t.Error("two workspaces share a browser session")
	}
}

// The whole point of a separate tool is that the model knows when to reach for
// it. web_fetch names browser_read at the moment it comes back empty, because a
// routing rule stated thousands of tokens earlier in a prompt is one a weak
// model does not apply.
func TestWebFetchNamesTheBrowserOnAnEmptyShell(t *testing.T) {
	h := &hostToolSet{browser: &fakeBrowser{available: true}}
	shell := "<html><head><script src=x></script></head><body><div id=root></div></body></html>"
	hint := h.jsShellHint("text/html", shell)
	if !strings.Contains(hint, "browser_read") {
		t.Fatalf("no handoff to the browser: %q", hint)
	}
}

func TestWebFetchStaysQuietOnARealPage(t *testing.T) {
	h := &hostToolSet{browser: &fakeBrowser{available: true}}
	article := strings.Repeat("this is a real sentence of article prose. ", 20)
	if hint := h.jsShellHint("text/html", article); hint != "" {
		t.Fatalf("hinted on a page that rendered fine: %q", hint)
	}
}

// Never advertise a tool this host does not have.
func TestWebFetchDoesNotNameTheBrowserWhenItIsAbsent(t *testing.T) {
	h := &hostToolSet{browser: &fakeBrowser{available: false}}
	if hint := h.jsShellHint("text/html", "<html><body></body></html>"); hint != "" {
		t.Fatalf("advertised a missing browser: %q", hint)
	}
}

// A JSON API response is not an unrendered page, however short it is.
func TestWebFetchDoesNotHintOnNonHTML(t *testing.T) {
	h := &hostToolSet{browser: &fakeBrowser{available: true}}
	if hint := h.jsShellHint("application/json", `{"t":21}`); hint != "" {
		t.Fatalf("hinted on a JSON response: %q", hint)
	}
}

// A blocked page must tell the model to stop rather than to retry. A model told
// only "no content" will try again; told it is behind a Cloudflare check, it
// reports and moves on.
func TestBlockedPageTellsTheModelToStop(t *testing.T) {
	out := renderBrowserResult(browser.Result{
		FinalURL:    "https://example.com",
		Blocked:     "cloudflare",
		BlockedNote: "the site is behind a Cloudflare browser check",
	}, false)
	low := strings.ToLower(out)
	if !strings.Contains(low, "cloudflare") {
		t.Error("does not name the obstacle")
	}
	if !strings.Contains(low, "report") {
		t.Error("does not tell the model to report it")
	}
}

// A refusal must be REMEMBERED, not just returned. This is the loop that makes
// the permission appear on the agent's page by itself: without it the owner
// meets an agent that stopped and a refusal buried in a run log, and has to work
// out on their own that a switch exists somewhere.
func TestARefusedIrreversibleActionIsRecorded(t *testing.T) {
	h := &hostToolSet{
		browser:          &fakeBrowser{available: true},
		includeExecTools: true,
		browserPolicy:    browser.Policy{}, // no permission
	}
	if h.browserWantedIrreversible {
		t.Fatal("flag set before anything happened")
	}
	h.execBrowserAct(context.Background(), browser.ActRequest{Action: browser.ActionClick},
		"Pay now", browser.PageContext{NameKnown: true})
	if !h.browserWantedIrreversible {
		t.Error("a refused payment was not recorded, so the owner is never shown the permission")
	}
}

// An action that was ALLOWED must not set the flag — otherwise every agent that
// legitimately pays (with permission) would keep re-announcing a need already
// met, and an ordinary click would raise a payment warning.
func TestAnAllowedActionIsNotRecordedAsNeedingPermission(t *testing.T) {
	h := &hostToolSet{
		browser:          &fakeBrowser{available: true},
		includeExecTools: true,
		browserPolicy:    browser.Policy{AllowIrreversible: true},
	}
	h.execBrowserAct(context.Background(), browser.ActRequest{Action: browser.ActionClick},
		"Pay now", browser.PageContext{NameKnown: true})
	if h.browserWantedIrreversible {
		t.Error("an allowed action was recorded as needing permission")
	}

	h2 := &hostToolSet{browser: &fakeBrowser{available: true}, includeExecTools: true}
	h2.execBrowserAct(context.Background(), browser.ActRequest{Action: browser.ActionClick},
		"Next page", browser.PageContext{NameKnown: true, URL: "https://example.com/docs"})
	if h2.browserWantedIrreversible {
		t.Error("an ordinary click was recorded as needing payment permission")
	}
}
