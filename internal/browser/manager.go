package browser

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rookery-ai/rookery/internal/sandbox"
)

// Manager owns the browser helper process on behalf of the whole server.
//
// One helper serves every workspace. That is safe in this spec because each
// call gets a fresh incognito BrowserContext and nothing persists between them —
// the ephemeral context IS the tenancy boundary. Session-backed acting (which
// does persist) is keyed per workspace and per agent, and the key is minted
// here, never accepted from a caller.
type Manager struct {
	SelfExe string
	// Sandboxed applies Landlock confinement to the helper. Follows the
	// server's ROOKERY_SANDBOX setting for consistency with the coder, and is
	// verified to work with a real Chromium at ABI 8.
	Sandboxed bool
	// Guard keeps the browser out of private address space. Default true; an
	// owner who genuinely needs to read a self-hosted dashboard turns it off.
	Guard bool
	// IdleAfter stops the helper when unused. A browser is ~200 MB resident, and
	// most installs browse rarely, so holding it forever is a poor trade on the
	// laptops this platform targets.
	//
	// It MUST exceed sessionTTL, and that is not tidiness. The manager's timer
	// and the helper's per-session timer both measure idleness, so the shorter
	// one always wins — at 5 minutes here against a 10-minute session TTL the
	// session timer was unreachable, and worse, a logged-in flow that paused for
	// six minutes (a weak model on a slow provider between "read the page" and
	// "click the button") had its helper killed underneath it. The login went
	// with it, and the next call returned "no open page for this run" with no
	// way to recover, because cross-run persistence is deliberately not built.
	IdleAfter time.Duration

	mu       sync.Mutex
	cmd      *exec.Cmd
	baseURL  string
	token    string
	client   *http.Client
	lastUsed time.Time
	stopIdle context.CancelFunc
}

// idleAfterDefault is how long the helper survives with no calls at all. It is
// deliberately longer than sessionTTL (see IdleAfter), so the helper outlives
// the sessions it holds rather than taking a live login down with it.
const idleAfterDefault = 15 * time.Minute

// NewManager builds a manager with the project's defaults.
func NewManager(selfExe string, sandboxed, guard bool) *Manager {
	return &Manager{
		SelfExe:   selfExe,
		Sandboxed: sandboxed,
		Guard:     guard,
		IdleAfter: idleAfterDefault,
		client:    &http.Client{Timeout: 3 * time.Minute},
	}
}

// Available reports whether this manager can serve a request at all.
func (m *Manager) Available() Availability {
	if m == nil || m.SelfExe == "" {
		return Availability{Reason: "the browser subsystem is not wired on this server"}
	}
	return Probe()
}

// Render fetches and reads one page.
func (m *Manager) Render(ctx context.Context, req Request) (Result, error) {
	facts, err := m.renderFacts(ctx, req)
	if err != nil {
		return Result{}, err
	}
	return resultFrom(facts, req.Offset, req.Limit), nil
}

func (m *Manager) renderFacts(ctx context.Context, req Request) (PageFacts, error) {
	body := renderReq{
		URL:       req.URL,
		WaitFor:   req.WaitFor,
		TimeoutMS: req.TimeoutMS,
		Session:   req.Session,
		// The element list is computed only for a kept session, i.e. a flow
		// that can act on it. A plain read pays nothing for a listing it has no
		// tool to use.
		Elements: req.Session != "",
		HTML:     req.WantHTML,
	}
	return m.post(ctx, "/render", body)
}

// RenderHTML returns a page's rendered DOM. It exists for the search cascade,
// which must parse result anchors; nothing hands this to a model.
//
// It implements websearch.PageRenderer, declared there as a one-method
// interface so internal/websearch keeps no dependency on this package.
func (m *Manager) RenderHTML(ctx context.Context, url string) (string, error) {
	facts, err := m.renderFacts(ctx, Request{
		URL: url,
		// A results page is assembled by script after first paint, so waiting
		// only for domcontentloaded returns the shell.
		WaitFor:  "networkidle",
		WantHTML: true,
	})
	if err != nil {
		return "", err
	}
	return facts.HTML, nil
}

// Act performs one acting verb in an open session.
func (m *Manager) Act(ctx context.Context, req ActRequest) (Result, error) {
	facts, err := m.post(ctx, "/act", actReq{
		Session:   req.Session,
		Action:    req.Action,
		Ref:       req.Ref,
		Value:     req.Value,
		Key:       req.Key,
		WaitFor:   req.WaitFor,
		TimeoutMS: req.TimeoutMS,
		Elements:  true,
		Secret:    req.ValueIsSecret,
	})
	if err != nil {
		return Result{}, err
	}
	return resultFrom(facts, req.Offset, req.Limit), nil
}

// ActRequest is one acting call.
type ActRequest struct {
	Session   string
	Action    Action
	Ref       string
	Value     string
	Key       string
	WaitFor   string
	TimeoutMS int
	Offset    int
	Limit     int
	// ValueIsSecret tells the helper to redact Value out of every later result
	// from this session. Set by the caller that resolved the placeholder, since
	// the helper has no database and cannot recognise a secret on its own.
	ValueIsSecret bool
}

// ContextCount reports how many browser contexts the helper currently holds.
//
// It exists for the leak test and nothing else. A context that is never closed
// is invisible from outside the helper: it costs memory and changes no result,
// so a regression here would pass every behavioural test. Returns 0 when the
// helper is not running.
func (m *Manager) ContextCount(ctx context.Context) (int, error) {
	facts, err := m.post(ctx, "/ping", map[string]string{})
	if err != nil {
		return 0, err
	}
	return facts.Contexts, nil
}

// waitSegment is how long ONE wait call may block inside the helper.
//
// It must stay comfortably under the manager's HTTP client timeout (3 minutes),
// because a call that exceeds it fails at the transport and the error path stops
// the helper — taking the page, and any login on it, with it. That is the worst
// possible outcome for the case this exists to serve: an agent halfway through a
// payment, waiting for a bank push, losing its session because it waited too
// patiently.
const waitSegment = 20 * time.Second

// MaxWaitFor bounds a whole WaitFor, however long the caller asks for.
//
// Fifteen minutes is chosen against the real case rather than as a round number:
// a bank push notification lands on a phone that may be in another room at 03:00,
// and anything under a few minutes fails exactly when it matters. It is also
// under the browser helper's own idle reaper, which each segment keeps at bay by
// touching the session.
const MaxWaitFor = 15 * time.Minute

// WaitFor blocks until a page condition is met, polling in short segments.
//
// The segmenting is the point. A single long wait breaks three ways at once: it
// exceeds the manager's transport timeout and kills the helper; it lets the
// helper's idle reaper close the context underneath it, because lastUsed is
// stamped when a call STARTS; and a timeout comes back as a failed tool call,
// which counts against the engine's unproductive-turn budget. Polling turns one
// fragile long call into many cheap ones, each of which refreshes the session.
//
// Returns matched=false with a nil error when the deadline passes without the
// condition appearing. That is deliberately NOT an error: a wait that ends
// without the thing happening is information the model must act on, and shaping
// it as a failure would both trip the oscillation guard and read to the model as
// a broken tool rather than an answer.
func (m *Manager) WaitFor(ctx context.Context, session, condition string, total time.Duration) (Result, bool, error) {
	if total <= 0 || total > MaxWaitFor {
		total = MaxWaitFor
	}
	deadline := time.Now().Add(total)
	var last Result
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return last, false, nil
		}
		seg := waitSegment
		if remaining < seg {
			seg = remaining
		}
		res, err := m.Act(ctx, ActRequest{
			Session:   session,
			Action:    ActionWait,
			WaitFor:   condition,
			TimeoutMS: int(seg / time.Millisecond),
		})
		if err == nil {
			// The condition appeared within this segment.
			return res, true, nil
		}
		last = res
		// A segment that simply did not match is the expected case and is not a
		// failure — only a dead helper or a cancelled run is. Distinguishing them
		// matters: retrying forever against a helper that has gone would burn the
		// whole deadline achieving nothing.
		if ctx.Err() != nil {
			return last, false, ctx.Err()
		}
		if isFatalWaitErr(err) {
			return last, false, err
		}
	}
}

// isFatalWaitErr separates "the condition has not appeared yet" from "there is
// no longer a browser to ask". Playwright reports the former as a timeout, which
// is the one error this loop must swallow.
func isFatalWaitErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "timeout") || strings.Contains(s, "exceeded") {
		return false
	}
	return true
}

// CloseSession tears down a run's browser context.
func (m *Manager) CloseSession(ctx context.Context, session string) {
	if session == "" {
		return
	}
	if _, err := m.post(ctx, "/close", map[string]string{"session": session}); err != nil {
		slog.Debug("browser: close session failed", "err", err)
	}
}

func resultFrom(f PageFacts, offset, limit int) Result {
	kind, note := Classify(f)
	text, truncated, next := Page(f.Text, offset, limit)
	els := FilterInteractive(f.Elements)
	SortElementsStable(els)
	r := Result{
		Text:        text,
		Title:       f.Title,
		FinalURL:    f.FinalURL,
		Status:      f.Status,
		Truncated:   truncated,
		NextOffset:  next,
		Blocked:     kind,
		BlockedNote: note,
		Elements:    els,
	}
	if f.Note != "" && r.BlockedNote == "" {
		r.BlockedNote = f.Note
	}
	return r
}

// post sends one request to the helper, starting it if necessary.
func (m *Manager) post(ctx context.Context, path string, payload any) (PageFacts, error) {
	if av := m.Available(); !av.OK {
		return PageFacts{}, fmt.Errorf("%w: %s", ErrUnavailable, av.Reason)
	}
	base, token, err := m.ensure(ctx)
	if err != nil {
		return PageFacts{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return PageFacts{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return PageFacts{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		// The helper may have died (OOM, a Chromium crash). Drop it so the next
		// call starts a fresh one rather than retrying against a dead port
		// forever.
		m.stop()
		return PageFacts{}, fmt.Errorf("browser helper unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return PageFacts{}, fmt.Errorf("browser helper error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var facts PageFacts
	if err := json.NewDecoder(resp.Body).Decode(&facts); err != nil {
		return PageFacts{}, fmt.Errorf("browser helper response: %w", err)
	}
	if facts.Error != "" {
		return PageFacts{}, fmt.Errorf("%s", facts.Error)
	}
	return facts, nil
}

// ensure starts the helper if it is not already running.
func (m *Manager) ensure(ctx context.Context) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastUsed = time.Now()
	if m.cmd != nil && m.baseURL != "" {
		return m.baseURL, m.token, nil
	}

	token, err := randomToken()
	if err != nil {
		return "", "", err
	}
	scratch, err := os.MkdirTemp("", "rookery-browser")
	if err != nil {
		return "", "", fmt.Errorf("browser scratch dir: %w", err)
	}

	argv := []string{m.SelfExe, HostCommand}
	env := append(os.Environ(),
		EnvToken+"="+token,
		EnvGuard+"="+boolEnv(m.Guard),
		"TMPDIR="+scratch,
	)

	if m.Sandboxed && sandbox.Supported() {
		spec, err := m.sandboxSpec(argv, env, scratch)
		if err != nil {
			return "", "", err
		}
		argv, err = sandbox.Wrap(m.SelfExe, spec)
		if err != nil {
			return "", "", err
		}
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = scratch
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("start browser helper: %w", err)
	}

	base, err := readHostURL(stdout)
	if err != nil {
		_ = cmd.Process.Kill()
		return "", "", fmt.Errorf("browser helper did not start: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	m.cmd, m.baseURL, m.token = cmd, base, token
	idleCtx, cancel := context.WithCancel(context.Background())
	m.stopIdle = cancel
	go m.watchIdle(idleCtx)
	go func() {
		_ = cmd.Wait()
		m.stop()
	}()
	slog.Info("browser helper started", "sandboxed", m.Sandboxed && sandbox.Supported(), "guarded", m.Guard)
	return base, token, nil
}

// sandboxSpec is the confinement that makes running Chromium here acceptable.
//
// Verified against a real Chromium at Landlock ABI 8. Each grant is required:
// without the binary's own directory the helper cannot exec itself and fails
// with a bare "permission denied"; without the two playwright caches the driver
// and browser binaries are unreadable; without /dev/shm Chromium crashes on
// content-heavy pages. Nothing here grants the data directory, so the database,
// system.key, config.yaml and every vault stay unreadable.
func (m *Manager) sandboxSpec(argv, env []string, scratch string) (sandbox.Spec, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sandbox.Spec{}, fmt.Errorf("browser sandbox: resolve home: %w", err)
	}
	rw := []string{
		scratch,
		filepath.Join(home, ".cache", "ms-playwright"),
		filepath.Join(home, ".cache", "ms-playwright-go"),
		"/dev/shm",
	}
	if d := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); d != "" {
		rw = append(rw, d)
	}
	if d := os.Getenv("PLAYWRIGHT_DRIVER_PATH"); d != "" {
		rw = append(rw, d)
	}
	ro := append(sandbox.SystemReadOnlyPaths(), filepath.Dir(m.SelfExe))
	return sandbox.Spec{
		Command:        argv,
		Dir:            scratch,
		Env:            env,
		ReadWritePaths: rw,
		ReadOnlyPaths:  ro,
		ReadWriteFiles: sandbox.SystemReadWriteFiles(),
		// No RLIMIT_AS: V8 reserves a very large address space and dies under a
		// cap, the same reason the coder's spec leaves MemoryMB at zero.
	}, nil
}

// readHostURL waits for the helper to announce its listener.
func readHostURL(r io.Reader) (string, error) {
	type res struct {
		url string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if u, ok := strings.CutPrefix(line, "BROWSER_HOST_URL="); ok {
				ch <- res{url: u}
				// Keep draining so the helper never blocks on a full stdout
				// pipe once it starts logging.
				go func() { _, _ = io.Copy(io.Discard, r) }()
				return
			}
		}
		ch <- res{err: fmt.Errorf("helper exited before announcing its address")}
	}()
	select {
	case out := <-ch:
		return out.url, out.err
	case <-time.After(90 * time.Second):
		// Generous: the very first launch may be paging a ~400 MB Chromium off
		// a slow disk.
		return "", fmt.Errorf("timed out waiting for the browser helper")
	}
}

func (m *Manager) watchIdle(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			idle := m.IdleAfter > 0 && time.Since(m.lastUsed) > m.IdleAfter && m.cmd != nil
			m.mu.Unlock()
			if idle {
				slog.Debug("browser helper idle, stopping")
				m.stop()
				return
			}
		}
	}
}

// Stop tears the helper down. Safe to call repeatedly.
func (m *Manager) Stop() { m.stop() }

func (m *Manager) stop() {
	m.mu.Lock()
	cmd, cancel := m.cmd, m.stopIdle
	m.cmd, m.baseURL, m.token, m.stopIdle = nil, "", "", nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
