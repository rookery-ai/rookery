package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// HostCommand is the hidden CLI subcommand that runs the browser. cmd/rookery
// registers it and routes it to RunHost.
//
// It exists as a separate PROCESS, launched through sandbox.Wrap, because the
// host process holds the database, the system key and every decrypted secret.
// Chromium renders untrusted third-party content; it must not share that
// address space or that filesystem view. Process separation alone would buy
// nothing here — same uid, same files — so it is the Landlock confinement that
// makes the split worth its complexity, and that confinement was verified
// against a real Chromium before this was written.
const HostCommand = "__browser-host"

// Environment variables the parent sets when spawning the helper. These are
// INTERNAL (written into a subprocess env, never configured by an operator), so
// they belong in scripts/docs-sync-internal-env.txt rather than README's
// configuration table.
const (
	EnvToken = "ROOKERY_BROWSER_TOKEN"
	EnvGuard = "ROOKERY_BROWSER_GUARD"
)

// realisticUserAgent replaces Chromium's default headless UA.
//
// Playwright's own default advertises "HeadlessChrome", which a large number of
// sites refuse outright — so leaving it would make the feature fail on exactly
// the pages a user reaches for a browser to read. This is not an attempt to
// defeat bot protection (see the Blocked classification, which reports rather
// than evades); it is removing a banner that says "automated" on requests the
// owner is making of their own accord.
const realisticUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// sessionTTL bounds how long an idle browser context is kept alive. A run holds
// its context across calls so a multi-step flow can proceed; nothing should hold
// one after the run that opened it has gone.
const sessionTTL = 10 * time.Minute

type hostSession struct {
	ctx      playwright.BrowserContext
	page     playwright.Page
	lastUsed time.Time
	// secretValues are the resolved secret strings typed into this session's
	// pages. They are held ONLY to redact them back out of results — see
	// redact. They are never logged and never returned.
	secretValues []string
}

type browserHost struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	proxy   *guardedProxy
	token   string

	mu   sync.Mutex
	sess map[string]*hostSession
}

// RunHost is the helper entrypoint. It starts the guarded proxy, launches
// Chromium behind it, and serves a loopback API until ctx is cancelled. The
// bound URL is printed on stdout as the first line so the parent can find it
// without a fixed port.
func RunHost(ctx context.Context) error {
	token := os.Getenv(EnvToken)
	if token == "" {
		return fmt.Errorf("browser host: %s is required", EnvToken)
	}
	guard := os.Getenv(EnvGuard) != "0"

	proxy, proxyAddr, err := startGuardedProxy(guard)
	if err != nil {
		return err
	}
	defer proxy.close()

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("browser host: start playwright driver: %w", err)
	}
	defer func() { _ = pw.Stop() }()

	br, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Proxy:    &playwright.Proxy{Server: "http://" + proxyAddr},
		Args: []string{
			// Force loopback through the proxy too. Chromium bypasses the proxy
			// for localhost by DEFAULT, which would let a page reach this
			// install's own connector/KB/MCP bridges and their bearer tokens.
			// Playwright already compensates for this today — measured — so the
			// flag is currently redundant. It is set anyway because relying on
			// an undocumented default for a security property is how that
			// property disappears in a dependency bump; the accompanying test
			// asserts the BEHAVIOUR (loopback refused), not the flag.
			"--proxy-bypass-list=<-loopback>",
			// Chromium's /dev/shm usage exceeds the default size in many
			// containers; without this it crashes on content-heavy pages.
			"--disable-dev-shm-usage",
		},
	})
	if err != nil {
		return fmt.Errorf("browser host: launch chromium: %w", err)
	}
	defer func() { _ = br.Close() }()

	h := &browserHost{pw: pw, browser: br, proxy: proxy, token: token, sess: map[string]*hostSession{}}
	go h.reapIdle(ctx)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("browser host: listen: %w", err)
	}
	// The parent blocks on this line to learn the port, so it must reach it
	// before the helper starts serving.
	fmt.Println("BROWSER_HOST_URL=http://" + ln.Addr().String())
	_ = os.Stdout.Sync()

	srv := &http.Server{Handler: h.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func (h *browserHost) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", h.auth(func(w http.ResponseWriter, _ *http.Request) {
		// contexts is how the leak test observes what it is testing. A context
		// that is never closed is invisible from outside the helper — it costs
		// memory and nothing else — so without a count exposed here the fix
		// above could regress with every test still passing.
		writeJSON(w, map[string]any{"ok": true, "contexts": len(h.browser.Contexts())})
	}))
	mux.HandleFunc("/render", h.auth(h.handleRender))
	mux.HandleFunc("/act", h.auth(h.handleAct))
	mux.HandleFunc("/close", h.auth(h.handleClose))
	return mux
}

// auth gates every route on the per-spawn bearer token. The listener is on
// loopback, which is not by itself an authorisation boundary — anything running
// as any user on this host can reach it.
func (h *browserHost) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != h.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

type renderReq struct {
	URL       string `json:"url"`
	WaitFor   string `json:"wait_for"`
	TimeoutMS int    `json:"timeout_ms"`
	Session   string `json:"session"`
	// Elements asks for the interactive-element list. A plain read does not
	// need it and computing it costs an extra round trip into the page.
	Elements bool `json:"elements"`
	// HTML asks for the rendered DOM. Only the search provider sets it.
	HTML bool `json:"html"`
}

func (h *browserHost) handleRender(w http.ResponseWriter, r *http.Request) {
	var req renderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sess, err := h.session(req.Session)
	if err != nil {
		writeErr(w, err)
		return
	}
	// An ephemeral context (no session key) belongs to this call alone and must
	// be closed here. It is never stored in h.sess, so reapIdle cannot see it —
	// without this every browser_read leaks a Chromium context for as long as the
	// helper lives, which on the always-on read path is every chat turn that
	// opens a page. The deferred close covers the early returns below too.
	if req.Session == "" {
		defer func() { _ = sess.ctx.Close() }()
	}
	timeout := float64(req.TimeoutMS)
	if timeout <= 0 {
		timeout = 30000
	}

	resp, err := sess.page.Goto(req.URL, playwright.PageGotoOptions{
		WaitUntil: waitUntil(req.WaitFor),
		Timeout:   playwright.Float(timeout),
	})
	if err != nil {
		writeErr(w, fmt.Errorf("navigate: %s", sess.redact(err.Error())))
		return
	}
	if err := applyExtraWait(sess.page, req.WaitFor, timeout); err != nil {
		// A wait that times out is NOT fatal: the page may still hold what the
		// caller needs, and returning nothing would be strictly worse than
		// returning what rendered. The condition is reported in the note.
		writeFacts(w, h.collect(sess, resp, collectOpts{elements: req.Elements, html: req.HTML, note: "wait condition not met: " + sess.redact(err.Error())}))
		return
	}
	writeFacts(w, h.collect(sess, resp, collectOpts{elements: req.Elements, html: req.HTML}))
}

func (h *browserHost) handleClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	h.mu.Lock()
	s := h.sess[req.Session]
	delete(h.sess, req.Session)
	h.mu.Unlock()
	if s != nil {
		_ = s.ctx.Close()
	}
	writeJSON(w, map[string]any{"ok": true})
}

// session returns the context for a key, creating it if needed. An empty key
// gets a fresh ephemeral context that the caller is expected to close — that is
// the read-only path, where nothing should persist between calls.
func (h *browserHost) session(key string) (*hostSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if key != "" {
		if s, ok := h.sess[key]; ok {
			s.lastUsed = time.Now()
			return s, nil
		}
	}
	bctx, err := h.browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(realisticUserAgent),
		// A desktop viewport: a mobile-sized default changes which controls a
		// responsive site renders, so the element list would not match what the
		// user sees.
		Viewport: &playwright.Size{Width: 1440, Height: 900},
	})
	if err != nil {
		return nil, fmt.Errorf("new context: %w", err)
	}
	page, err := bctx.NewPage()
	if err != nil {
		_ = bctx.Close()
		return nil, fmt.Errorf("new page: %w", err)
	}
	s := &hostSession{ctx: bctx, page: page, lastUsed: time.Now()}
	if key != "" {
		h.sess[key] = s
	}
	return s, nil
}

func (h *browserHost) reapIdle(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.mu.Lock()
			for k, s := range h.sess {
				if time.Since(s.lastUsed) > sessionTTL {
					_ = s.ctx.Close()
					delete(h.sess, k)
				}
			}
			h.mu.Unlock()
		}
	}
}

// collect reads everything the host process needs to classify and render the
// page. Note what is gathered and what is not: no screenshot, ever. Redaction
// cannot touch pixels, so an image taken after a secret was typed would carry
// that secret into the model's context with nothing able to strip it.
// collectOpts says what to gather. A struct rather than positional booleans:
// there are two of them now and they are both bools, which is exactly the
// signature where a call site silently swaps them.
type collectOpts struct {
	elements bool
	html     bool
	note     string
}

func (h *browserHost) collect(s *hostSession, resp playwright.Response, o collectOpts) PageFacts {
	wantElements, note := o.elements, o.note
	f := PageFacts{Note: note}
	if resp != nil {
		f.Status = resp.Status()
	}
	if t, err := s.page.Title(); err == nil {
		f.Title = t
	}
	f.FinalURL = s.page.URL()
	if txt, err := s.page.InnerText("body"); err == nil {
		f.Text = txt
	}

	// Structural, not textual: a password INPUT is what distinguishes a sign-in
	// wall from the sign-in link every site has in its header.
	if el, err := s.page.QuerySelector("input[type=password]"); err == nil && el != nil {
		f.HasPasswordField = true
	}

	// Captchas live in iframes whose parent page often says nothing at all, so
	// frame URLs are collected for the classifier to look at.
	for _, fr := range s.page.Frames() {
		if u := fr.URL(); u != "" {
			f.FrameNames = append(f.FrameNames, u)
		}
	}

	if wantElements {
		if snap, err := s.page.AriaSnapshot(playwright.PageAriaSnapshotOptions{
			Mode: playwright.AriaSnapshotModeAi,
		}); err == nil {
			f.Elements = ParseAriaSnapshot(snap)
		}
	}

	if o.html {
		if content, err := s.page.Content(); err == nil {
			f.HTML = content
		}
	}

	f.Title = s.redact(f.Title)
	f.Text = s.redact(f.Text)
	f.FinalURL = s.redact(f.FinalURL)
	for i := range f.Elements {
		f.Elements[i].Name = s.redact(f.Elements[i].Name)
		f.Elements[i].Note = s.redact(f.Elements[i].Note)
	}
	return f
}

// redact removes any secret value this session typed from text on its way back
// to the model.
//
// Four channels echo a filled value and all four pass through here: the page's
// own text, a field's rendered value in the element list, the final URL (a GET
// form puts it in the query string) and Playwright's error messages, several of
// which quote the value that failed to match.
func (s *hostSession) redact(in string) string {
	if in == "" {
		return in
	}
	out := in
	for _, v := range s.secretValues {
		if len(v) < 3 {
			// Refuse to redact a very short value: it would match unrelated
			// substrings across the whole page and hand back mangled text that
			// reads as a rendering fault.
			continue
		}
		out = strings.ReplaceAll(out, v, "[redacted]")
	}
	return out
}

func (s *hostSession) rememberSecret(v string) {
	if v == "" {
		return
	}
	for _, existing := range s.secretValues {
		if existing == v {
			return
		}
	}
	s.secretValues = append(s.secretValues, v)
}

func waitUntil(waitFor string) *playwright.WaitUntilState {
	switch {
	case waitFor == "load":
		return playwright.WaitUntilStateLoad
	case waitFor == "networkidle":
		return playwright.WaitUntilStateNetworkidle
	case waitFor == "commit":
		return playwright.WaitUntilStateCommit
	default:
		return playwright.WaitUntilStateDomcontentloaded
	}
}

// applyExtraWait handles the two wait forms that are not navigation states.
func applyExtraWait(page playwright.Page, waitFor string, timeout float64) error {
	switch {
	case strings.HasPrefix(waitFor, "selector:"):
		_, err := page.WaitForSelector(strings.TrimPrefix(waitFor, "selector:"),
			playwright.PageWaitForSelectorOptions{Timeout: playwright.Float(timeout)})
		return err
	case strings.HasPrefix(waitFor, "text:"):
		return page.GetByText(strings.TrimPrefix(waitFor, "text:")).First().
			WaitFor(playwright.LocatorWaitForOptions{Timeout: playwright.Float(timeout)})
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeFacts(w http.ResponseWriter, f PageFacts) { writeJSON(w, f) }

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // a page-level failure is data, not a transport error
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}
